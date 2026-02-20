package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"github.com/taigrr/blastd/internal/config"
	"github.com/taigrr/blastd/internal/daemon"
)

// version is set at build time via ldflags by GoReleaser.
// When empty (local builds), falls back to VCS info.
var version string

func init() {
	if version != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		version = "dev"
		return
	}

	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
		return
	}

	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}

	if revision == "" {
		version = "dev"
		return
	}

	if len(revision) > 7 {
		revision = revision[:7]
	}

	version = revision + modified
}

func main() {
	cmd := &cobra.Command{
		Use:   "blastd",
		Short: "Local daemon for Blast activity tracking",
		Long:  "blastd receives editor activity events over a Unix socket, caches them locally, and syncs to a remote Blast server.",
		RunE:  run,
	}

	if err := fang.Execute(
		context.Background(),
		cmd,
		fang.WithVersion(version),
	); err != nil {
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	d, err := daemon.New(cfg)
	if err != nil {
		log.Fatalf("failed to create daemon: %v", err)
	}

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

	return nil
}
