package socket

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/taigrr/blastd/internal/db"
	syncpkg "github.com/taigrr/blastd/internal/sync"
)

type Request struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Response struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Message  string `json:"message,omitempty"`
	Total    *int64 `json:"total,omitempty"`
	Unsynced *int64 `json:"unsynced,omitempty"`
}

type ActivityData struct {
	Project          string  `json:"project"`
	GitRemote        string  `json:"git_remote"`
	StartedAt        string  `json:"started_at"`
	EndedAt          string  `json:"ended_at"`
	Filename         string  `json:"filename"`
	Filetype         string  `json:"filetype"`
	LinesAdded       int     `json:"lines_added"`
	LinesRemoved     int     `json:"lines_removed"`
	GitBranch        string  `json:"git_branch"`
	ActionsPerMinute float64 `json:"actions_per_minute"`
	WordsPerMinute   float64 `json:"words_per_minute"`
	Editor           string  `json:"editor"`
}

type SyncFunc func() error

type syncRecord struct {
	id int64
	at time.Time
}

type Server struct {
	path     string
	db       *db.DB
	machine  string
	syncFunc SyncFunc
	listener net.Listener
	done     chan struct{}
	stopOnce sync.Once

	rateMu       sync.Mutex
	syncRequests []syncRecord
	syncSeq      int64

	// connMu guards the set of live connections and the handler WaitGroup so
	// Stop can unblock and wait for in-flight handlers before the DB closes.
	connMu   sync.Mutex
	conns    map[net.Conn]struct{}
	handlers sync.WaitGroup
}

const (
	syncRateLimit   = 10
	syncRateWindow  = 10 * time.Minute
	maxRequestBytes = 1 << 20 // 1 MiB per request line
	shutdownGrace   = 5 * time.Second
)

func NewServer(path string, database *db.DB, machine string) *Server {
	return &Server{
		path:    path,
		db:      database,
		machine: machine,
		done:    make(chan struct{}),
		conns:   make(map[net.Conn]struct{}),
	}
}

func (s *Server) SetSyncFunc(fn SyncFunc) {
	s.syncFunc = fn
}

func (s *Server) Start() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}

	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	s.listener = listener

	if err := os.Chmod(s.path, 0o600); err != nil {
		if closeErr := listener.Close(); closeErr != nil {
			return fmt.Errorf("chmod socket: %w (close listener: %v)", err, closeErr)
		}
		return err
	}

	go s.accept()
	return nil
}

func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
	})
	if s.listener != nil {
		if err := s.listener.Close(); err != nil {
			log.Printf("close listener: %v", err)
		}
	}

	// Unblock any handlers parked in scanner.Scan by setting a past read
	// deadline, then wait (bounded) for them to finish so the DB isn't closed
	// out from under an in-flight InsertActivity/SyncNow.
	s.connMu.Lock()
	for c := range s.conns {
		if err := c.SetReadDeadline(time.Now()); err != nil {
			log.Printf("set read deadline: %v", err)
		}
	}
	s.connMu.Unlock()

	waited := make(chan struct{})
	go func() {
		s.handlers.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(shutdownGrace):
		log.Printf("socket: timed out waiting for connection handlers")
	}

	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		log.Printf("remove socket: %v", err)
	}
}

func (s *Server) accept() {
	for {
		select {
		case <-s.done:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.done:
					return
				default:
					log.Printf("accept error: %v", err)
					continue
				}
			}

			// Register the connection and increment the handler WaitGroup
			// under connMu, gated on done, so Stop's sweep+Wait always
			// observes every live handler and no Add can race a completed
			// Wait. If Stop already fired, drop the connection.
			s.connMu.Lock()
			select {
			case <-s.done:
				s.connMu.Unlock()
				if err := conn.Close(); err != nil {
					log.Printf("close connection: %v", err)
				}
				return
			default:
			}
			s.conns[conn] = struct{}{}
			s.handlers.Add(1)
			s.connMu.Unlock()

			go s.handle(conn)
		}
	}
}

func (s *Server) handle(conn net.Conn) {
	defer s.handlers.Done()
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("close connection: %v", err)
		}
	}()
	defer func() {
		s.connMu.Lock()
		delete(s.conns, conn)
		s.connMu.Unlock()
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestBytes)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if encodeErr := encoder.Encode(Response{OK: false, Error: "invalid json"}); encodeErr != nil {
				log.Printf("encode response: %v", encodeErr)
				return
			}
			continue
		}

		switch req.Type {
		case "activity":
			s.handleActivity(req.Data, encoder)
		case "sync":
			s.handleSync(encoder)
		case "status":
			s.handleStatus(encoder)
		case "ping":
			if err := encoder.Encode(Response{OK: true}); err != nil {
				log.Printf("encode response: %v", err)
				return
			}
		default:
			if err := encoder.Encode(Response{OK: false, Error: "unknown request type"}); err != nil {
				log.Printf("encode response: %v", err)
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// A read deadline set by Stop unblocks Scan with a timeout error on
		// clean shutdown; don't misreport that as a client error.
		select {
		case <-s.done:
		default:
			if encodeErr := encoder.Encode(Response{OK: false, Error: "read error: " + err.Error()}); encodeErr != nil {
				log.Printf("encode response: %v", encodeErr)
			}
			log.Printf("connection read error: %v", err)
		}
	}
}

func (s *Server) handleSync(encoder *json.Encoder) {
	if s.syncFunc == nil {
		if err := encoder.Encode(Response{OK: false, Error: "sync not available"}); err != nil {
			log.Printf("encode response: %v", err)
		}
		return
	}

	if token, err := s.reserveSyncSlot(); err != nil {
		if encodeErr := encoder.Encode(Response{OK: false, Error: err.Error()}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	} else if err := s.syncFunc(); err != nil {
		// Only return the reserved slot for the in-progress no-op, which issued
		// no request. Real attempts (including failures against a down server)
		// keep their slot so the rate limit still bounds outbound load.
		if errors.Is(err, syncpkg.ErrSyncInProgress) {
			s.releaseSyncSlot(token)
		}
		if encodeErr := encoder.Encode(Response{OK: false, Error: err.Error()}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	}

	if err := encoder.Encode(Response{OK: true, Message: "sync complete"}); err != nil {
		log.Printf("encode response: %v", err)
	}
}

// reserveSyncSlot checks the rate limit and records the request atomically
// under a single lock hold to avoid a check-then-record race. It returns a
// token identifying the reserved slot for use with releaseSyncSlot.
func (s *Server) reserveSyncSlot() (int64, error) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	cutoff := time.Now().Add(-syncRateWindow)
	recent := s.syncRequests[:0]
	for _, r := range s.syncRequests {
		if r.at.After(cutoff) {
			recent = append(recent, r)
		}
	}
	s.syncRequests = recent

	if len(s.syncRequests) >= syncRateLimit {
		oldest := s.syncRequests[0].at
		waitUntil := oldest.Add(syncRateWindow)
		remaining := time.Until(waitUntil).Round(time.Second)
		return 0, fmt.Errorf("rate limited: try again in %s", remaining)
	}

	s.syncSeq++
	token := s.syncSeq
	s.syncRequests = append(s.syncRequests, syncRecord{id: token, at: time.Now()})
	return token, nil
}

// releaseSyncSlot removes the reservation with the given token, used when a
// reserved sync ultimately did no useful work.
func (s *Server) releaseSyncSlot(token int64) {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	for i, r := range s.syncRequests {
		if r.id == token {
			s.syncRequests = append(s.syncRequests[:i], s.syncRequests[i+1:]...)
			return
		}
	}
}

func (s *Server) handleStatus(encoder *json.Encoder) {
	stats, err := s.db.GetStats()
	if err != nil {
		if encodeErr := encoder.Encode(Response{OK: false, Error: err.Error()}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	}
	if err := encoder.Encode(Response{OK: true, Total: &stats.Total, Unsynced: &stats.Unsynced}); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func (s *Server) handleActivity(data json.RawMessage, encoder *json.Encoder) {
	var ad ActivityData
	if err := json.Unmarshal(data, &ad); err != nil {
		if encodeErr := encoder.Encode(Response{OK: false, Error: "invalid activity data"}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	}

	startedAt, err := time.Parse(time.RFC3339, ad.StartedAt)
	if err != nil {
		if encodeErr := encoder.Encode(Response{OK: false, Error: "invalid started_at"}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	}

	endedAt, err := time.Parse(time.RFC3339, ad.EndedAt)
	if err != nil {
		if encodeErr := encoder.Encode(Response{OK: false, Error: "invalid ended_at"}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	}

	editor := ad.Editor
	if editor == "" {
		editor = "neovim"
	}

	activity := &db.Activity{
		Project:          ad.Project,
		GitRemote:        ad.GitRemote,
		StartedAt:        startedAt,
		EndedAt:          endedAt,
		Filename:         ad.Filename,
		Filetype:         ad.Filetype,
		LinesAdded:       ad.LinesAdded,
		LinesRemoved:     ad.LinesRemoved,
		GitBranch:        ad.GitBranch,
		ActionsPerMinute: ad.ActionsPerMinute,
		WordsPerMinute:   ad.WordsPerMinute,
		Editor:           editor,
		Machine:          s.machine,
	}

	if err := s.db.InsertActivity(activity); err != nil {
		if encodeErr := encoder.Encode(Response{OK: false, Error: err.Error()}); encodeErr != nil {
			log.Printf("encode response: %v", encodeErr)
		}
		return
	}

	if err := encoder.Encode(Response{OK: true}); err != nil {
		log.Printf("encode response: %v", err)
	}
}
