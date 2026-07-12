package app

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
	"github.com/jaedle/mqtt-archive-sink/internal/compress"
	"github.com/jaedle/mqtt-archive-sink/internal/mqtt"
)

// MaxRecordSize is the serialized-record guard (SPEC.md behavior 3).
const MaxRecordSize = 16 << 20

const defaultTick = 10 * time.Second

type Config struct {
	Broker   string
	Topic    string
	ClientID string

	ArchiveDir    string
	FlushInterval time.Duration
	FsyncInterval time.Duration
	HeartbeatFile string
	ZstdLevel     int
	BufferSize    int

	Now    func() time.Time
	Logger *slog.Logger
}

type stats struct {
	lines      atomic.Int64
	bytes      atomic.Int64
	skipped    atomic.Int64
	reconnects atomic.Int64
	connected  atomic.Bool
}

// Run archives messages until ctx is cancelled (graceful shutdown, returns
// nil) or a hot-path disk error occurs (returns the error).
func Run(ctx context.Context, cfg Config) error {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	if err := os.MkdirAll(cfg.ArchiveDir, 0o755); err != nil {
		return err
	}

	var st stats
	lines := make(chan []byte, cfg.BufferSize)

	client := mqtt.Connect(mqtt.Config{
		Broker:   cfg.Broker,
		Topic:    cfg.Topic,
		ClientID: cfg.ClientID,
		Logger:   cfg.Logger,
		OnConnectionUp: func() {
			st.connected.Store(true)
			cfg.Logger.Info("connected", "broker", cfg.Broker)
		},
		OnConnectionLost: func(err error) {
			st.connected.Store(false)
			st.reconnects.Add(1)
			cfg.Logger.Warn("connection lost, reconnecting", "error", err)
		},
		OnMessage: func(topic string, payload []byte) {
			line, err := mqtt.EncodeRecord(cfg.Now(), topic, payload)
			if err != nil {
				cfg.Logger.Error("encode failed, message dropped", "topic", topic, "error", err)
				return
			}
			if len(line)+1 > MaxRecordSize {
				st.skipped.Add(1)
				cfg.Logger.Warn("record exceeds size limit, skipped", "topic", topic, "size", len(line))
				return
			}
			select {
			case lines <- line:
				st.lines.Add(1)
				st.bytes.Add(int64(len(line) + 1))
			case <-ctx.Done():
			}
		},
	})

	writer := archive.NewWriter(cfg.ArchiveDir, cfg.Now, cfg.FlushInterval > 0)
	sweeper := compress.NewSweeper(cfg.ArchiveDir, cfg.ZstdLevel, cfg.Logger)

	sweepReq := make(chan struct{}, 1)
	var sweepWG sync.WaitGroup
	sweepWG.Add(1)
	go func() {
		defer sweepWG.Done()
		for range sweepReq {
			sweeper.Sweep(cfg.Now().UTC().Format(archive.DateFormat))
		}
	}()
	triggerSweep := func() {
		select {
		case sweepReq <- struct{}{}:
		default:
		}
	}
	triggerSweep()

	flushEvery := cfg.FlushInterval
	if flushEvery <= 0 {
		flushEvery = defaultTick
	}
	fsyncEvery := cfg.FsyncInterval
	if fsyncEvery <= 0 {
		fsyncEvery = time.Minute
	}
	flushTicker := time.NewTicker(flushEvery)
	defer flushTicker.Stop()
	fsyncTicker := time.NewTicker(fsyncEvery)
	defer fsyncTicker.Stop()

	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case line := <-lines:
			rotated, err := writer.Append(line)
			if err != nil {
				runErr = err
				break loop
			}
			if rotated {
				triggerSweep()
			}
		case <-flushTicker.C:
			rotated, err := writer.RotateIfDue()
			if err != nil {
				runErr = err
				break loop
			}
			if rotated {
				triggerSweep()
			}
			if err := writer.Flush(); err != nil {
				runErr = err
				break loop
			}
			if err := touch(cfg.HeartbeatFile, cfg.Now()); err != nil {
				runErr = err
				break loop
			}
			cfg.Logger.Info("stats",
				"lines", st.lines.Load(),
				"bytes", st.bytes.Load(),
				"skipped", st.skipped.Load(),
				"repaired", writer.Repaired(),
				"buffered", len(lines),
				"connected", st.connected.Load(),
				"reconnects", st.reconnects.Load(),
			)
		case <-fsyncTicker.C:
			if err := writer.Sync(); err != nil {
				runErr = err
				break loop
			}
		}
	}

	client.Disconnect()
drain:
	for {
		select {
		case line := <-lines:
			if _, err := writer.Append(line); err != nil {
				if runErr == nil {
					runErr = err
				}
				break drain
			}
		default:
			break drain
		}
	}
	if err := writer.Close(); err != nil && runErr == nil {
		runErr = err
	}
	close(sweepReq)
	sweepWG.Wait()
	return runErr
}

func touch(path string, ts time.Time) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, ts, ts)
}
