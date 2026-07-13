package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/app"
	"github.com/jaedle/mqtt-archive-sink/internal/verify"
)

const heartbeatMaxAge = 5 * time.Minute

// version is stamped via -ldflags at release build time.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	var err error
	args := os.Args[1:]
	switch {
	case len(args) == 0:
		err = runSink(logger)
	case args[0] == "verify":
		dir := archiveDir()
		if len(args) > 1 {
			dir = args[1]
		}
		err = verify.Dir(dir, time.Now(), logger)
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

func runSink(logger *slog.Logger) error {
	logger.Info("starting", "version", version)

	broker := os.Getenv("MQTT_BROKER")
	if broker == "" {
		return fmt.Errorf("MQTT_BROKER is required")
	}
	flushInterval, err := envDuration("FLUSH_INTERVAL", 10*time.Second)
	if err != nil {
		return err
	}
	fsyncInterval, err := envDuration("FSYNC_INTERVAL", time.Minute)
	if err != nil {
		return err
	}
	zstdLevel, err := envInt("ZSTD_LEVEL", 19)
	if err != nil {
		return err
	}
	bufferSize, err := envInt("BUFFER_SIZE", 10000)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	return app.Run(ctx, app.Config{
		Broker:        broker,
		Topic:         envOr("MQTT_TOPIC", "#"),
		ClientID:      envOr("MQTT_CLIENT_ID", "archiver"),
		ArchiveDir:    archiveDir(),
		FlushInterval: flushInterval,
		FsyncInterval: fsyncInterval,
		HeartbeatFile: heartbeatFile(),
		ZstdLevel:     zstdLevel,
		BufferSize:    bufferSize,
		Logger:        logger,
	})
}

func runHealth() error {
	fi, err := os.Stat(heartbeatFile())
	if err != nil {
		return err
	}
	if age := time.Since(fi.ModTime()); age > heartbeatMaxAge {
		return fmt.Errorf("heartbeat is %s old (max %s)", age.Round(time.Second), heartbeatMaxAge)
	}
	return nil
}

func archiveDir() string {
	return envOr("ARCHIVE_DIR", "/var/lib/mqtt-archive")
}

func heartbeatFile() string {
	return envOr("HEARTBEAT_FILE", filepath.Join(archiveDir(), "heartbeat"))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	if v == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}
