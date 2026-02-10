package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/taigrr/blastd/internal/config"
	"github.com/taigrr/blastd/internal/daemon"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	d, err := daemon.New(cfg)
	if err != nil {
		log.Fatalf("failed to create daemon: %v", err)
	}

	// Handle shutdown gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("shutting down...")
		d.Stop()
	}()

	if err := d.Run(); err != nil {
		log.Fatalf("daemon error: %v", err)
	}
}
