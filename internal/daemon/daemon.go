package daemon

import (
	"log"
	gosync "sync"

	"github.com/taigrr/blastd/internal/config"
	"github.com/taigrr/blastd/internal/db"
	"github.com/taigrr/blastd/internal/socket"
	"github.com/taigrr/blastd/internal/sync"
)

type Daemon struct {
	cfg      *config.Config
	db       *db.DB
	socket   *socket.Server
	syncer   *sync.Syncer
	stopOnce gosync.Once
}

func New(cfg *config.Config) (*Daemon, error) {
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	socketServer := socket.NewServer(cfg.SocketPath, database, cfg.Machine)
	syncer := sync.NewSyncer(database, cfg.ServerURL, cfg.APIToken, cfg.SyncIntervalMinutes, cfg.SyncBatchSize, cfg.MetricsOnly)
	socketServer.SetSyncFunc(syncer.SyncNow)

	return &Daemon{
		cfg:    cfg,
		db:     database,
		socket: socketServer,
		syncer: syncer,
	}, nil
}

func (d *Daemon) Run() error {
	log.Printf("starting blastd daemon")
	log.Printf("  socket: %s", d.cfg.SocketPath)
	log.Printf("  database: %s", d.cfg.DBPath)
	log.Printf("  server: %s", d.cfg.ServerURL)
	log.Printf("  sync interval: %d minutes", d.cfg.SyncIntervalMinutes)

	if err := d.socket.Start(); err != nil {
		return err
	}

	// Run syncer (blocks until stopped)
	d.syncer.Start()

	return nil
}

// Stop shuts the daemon down in order (syncer flush, socket handlers, DB) and
// is safe to call multiple times; concurrent callers block until the first
// completes.
func (d *Daemon) Stop() {
	d.stopOnce.Do(func() {
		log.Println("stopping daemon...")
		d.syncer.Stop()
		d.socket.Stop()
		if err := d.db.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	})
}
