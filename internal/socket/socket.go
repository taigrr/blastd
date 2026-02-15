package socket

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/taigrr/blastd/internal/db"
)

type Request struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
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

type Server struct {
	path     string
	db       *db.DB
	machine  string
	syncFunc SyncFunc
	listener net.Listener
	done     chan struct{}

	rateMu       sync.Mutex
	syncRequests []time.Time
}

const (
	syncRateLimit  = 10
	syncRateWindow = 10 * time.Minute
)

func NewServer(path string, database *db.DB, machine string) *Server {
	return &Server{
		path:    path,
		db:      database,
		machine: machine,
		done:    make(chan struct{}),
	}
}

func (s *Server) SetSyncFunc(fn SyncFunc) {
	s.syncFunc = fn
}

func (s *Server) Start() error {
	os.Remove(s.path)

	listener, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	s.listener = listener

	os.Chmod(s.path, 0600)

	go s.accept()
	return nil
}

func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.path)
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
			go s.handle(conn)
		}
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			encoder.Encode(Response{OK: false, Error: "invalid json"})
			continue
		}

		switch req.Type {
		case "activity":
			s.handleActivity(req.Data, encoder)
		case "sync":
			s.handleSync(encoder)
		case "ping":
			encoder.Encode(Response{OK: true})
		default:
			encoder.Encode(Response{OK: false, Error: "unknown request type"})
		}
	}
}

func (s *Server) handleSync(encoder *json.Encoder) {
	if s.syncFunc == nil {
		encoder.Encode(Response{OK: false, Error: "sync not available"})
		return
	}

	if err := s.checkSyncRateLimit(); err != nil {
		encoder.Encode(Response{OK: false, Error: err.Error()})
		return
	}

	s.recordSyncRequest()

	if err := s.syncFunc(); err != nil {
		encoder.Encode(Response{OK: false, Error: err.Error()})
		return
	}

	encoder.Encode(Response{OK: true, Message: "sync complete"})
}

func (s *Server) checkSyncRateLimit() error {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()

	cutoff := time.Now().Add(-syncRateWindow)
	recent := s.syncRequests[:0]
	for _, t := range s.syncRequests {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.syncRequests = recent

	if len(s.syncRequests) >= syncRateLimit {
		oldest := s.syncRequests[0]
		waitUntil := oldest.Add(syncRateWindow)
		remaining := time.Until(waitUntil).Round(time.Second)
		return fmt.Errorf("rate limited: try again in %s", remaining)
	}

	return nil
}

func (s *Server) recordSyncRequest() {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	s.syncRequests = append(s.syncRequests, time.Now())
}

func (s *Server) handleActivity(data json.RawMessage, encoder *json.Encoder) {
	var ad ActivityData
	if err := json.Unmarshal(data, &ad); err != nil {
		encoder.Encode(Response{OK: false, Error: "invalid activity data"})
		return
	}

	startedAt, err := time.Parse(time.RFC3339, ad.StartedAt)
	if err != nil {
		encoder.Encode(Response{OK: false, Error: "invalid started_at"})
		return
	}

	endedAt, err := time.Parse(time.RFC3339, ad.EndedAt)
	if err != nil {
		encoder.Encode(Response{OK: false, Error: "invalid ended_at"})
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
		encoder.Encode(Response{OK: false, Error: err.Error()})
		return
	}

	encoder.Encode(Response{OK: true})
}
