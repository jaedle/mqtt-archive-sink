package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/mcpserver"
)

const shutdownGrace = 10 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	var err error
	args := os.Args[1:]
	switch {
	case len(args) == 0:
		err = runServer(logger)
	case args[0] == "health":
		err = runHealth()
	default:
		err = fmt.Errorf("unknown subcommand %q", args[0])
	}
	if err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func runServer(logger *slog.Logger) error {
	handler, err := mcpserver.NewHandler(mcpserver.Config{
		ArchiveDir: envOr("ARCHIVE_DIR", "/var/lib/mqtt-archive"),
		Token:      os.Getenv("AUTH_TOKEN"),
		Now:        time.Now,
		Logger:     logger,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	server := &http.Server{Addr: listenAddr(), Handler: handler}
	errs := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", server.Addr)
		errs <- server.ListenAndServe()
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

// runHealth probes the local /healthz endpoint; used as the container
// healthcheck (scratch image, no shell or curl).
func runHealth() error {
	_, port, err := net.SplitHostPort(listenAddr())
	if err != nil {
		return fmt.Errorf("LISTEN_ADDR: %w", err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}

func listenAddr() string {
	return envOr("LISTEN_ADDR", ":8080")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
