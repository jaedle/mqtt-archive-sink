// Package verify implements the offline archive check (SPEC.md
// "Subcommands"): decode-check every closed .zst and flag plain daily files
// whose compression is overdue.
package verify

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
	"github.com/klauspost/compress/zstd"
)

func Dir(dir string, now time.Time, logger *slog.Logger) error {
	today := now.UTC().Format(archive.DateFormat)
	yesterday := now.UTC().AddDate(0, 0, -1).Format(archive.DateFormat)
	failures := 0

	archives, err := filepath.Glob(filepath.Join(dir, "*.ndjson.zst"))
	if err != nil {
		return err
	}
	for _, path := range archives {
		date := strings.TrimSuffix(filepath.Base(path), ".ndjson.zst")
		if date == today {
			logger.Info("skipped (current date)", "file", path)
			continue
		}
		if err := decodeCheck(path); err != nil {
			failures++
			logger.Error("corrupt archive", "file", path, "error", err)
			continue
		}
		logger.Info("ok", "file", path)
	}

	plains, err := filepath.Glob(filepath.Join(dir, "*.ndjson"))
	if err != nil {
		return err
	}
	for _, path := range plains {
		date := strings.TrimSuffix(filepath.Base(path), ".ndjson")
		if _, err := time.Parse(archive.DateFormat, date); err != nil {
			continue
		}
		if date < yesterday {
			failures++
			logger.Error("stale plain file, compression stuck", "file", path)
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d file(s) failed verification", failures)
	}
	return nil
}

func decodeCheck(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	dec, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer dec.Close()
	_, err = io.Copy(io.Discard, dec)
	return err
}
