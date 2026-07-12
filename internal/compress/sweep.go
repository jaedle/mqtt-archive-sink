package compress

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
	"github.com/klauspost/compress/zstd"
)

// Sweeper turns plain daily files into verified .zst archives. The sweep is
// idempotent and safe: the plain file is only deleted after the written .zst
// decodes byte-identical to it (SPEC.md behavior 7).
type Sweeper struct {
	dir    string
	level  int
	logger *slog.Logger

	// compress is swappable for fault-injection tests only.
	compress func(src, dst string) error
}

func NewSweeper(dir string, level int, logger *slog.Logger) *Sweeper {
	s := &Sweeper{dir: dir, level: level, logger: logger}
	s.compress = s.compressFile
	return s
}

// Sweep processes every plain daily file dated before today. Failures are
// logged and never delete the plain file; the next sweep retries.
func (s *Sweeper) Sweep(today string) {
	matches, err := filepath.Glob(filepath.Join(s.dir, "*.ndjson"))
	if err != nil {
		s.logger.Error("sweep: listing failed", "error", err)
		return
	}
	sort.Strings(matches)
	for _, src := range matches {
		date := strings.TrimSuffix(filepath.Base(src), ".ndjson")
		if _, err := time.Parse(archive.DateFormat, date); err != nil || date >= today {
			continue
		}
		if err := s.sweepOne(src); err != nil {
			s.logger.Error("sweep: keeping plain file", "file", src, "error", err)
			continue
		}
		s.logger.Info("archived", "file", src+".zst")
	}
}

func (s *Sweeper) sweepOne(src string) error {
	dst := src + ".zst"
	if err := s.compress(src, dst); err != nil {
		return fmt.Errorf("compress: %w", err)
	}
	if err := verifyIdentical(src, dst); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return fmt.Errorf("sync dir: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove plain: %w", err)
	}
	return nil
}

// compressFile writes dst as a single zstd frame with content checksum,
// overwriting any untrusted leftover, and fsyncs it.
func (s *Sweeper) compressFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	enc, err := zstd.NewWriter(out,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(s.level)),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		_ = out.Close()
		return err
	}
	if _, err := io.Copy(enc, in); err != nil {
		_ = enc.Close()
		_ = out.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// verifyIdentical decodes zst and byte-compares it against plain.
func verifyIdentical(plain, zst string) error {
	pf, err := os.Open(plain)
	if err != nil {
		return err
	}
	defer func() { _ = pf.Close() }()

	zf, err := os.Open(zst)
	if err != nil {
		return err
	}
	defer func() { _ = zf.Close() }()

	dec, err := zstd.NewReader(zf)
	if err != nil {
		return err
	}
	defer dec.Close()

	if err := equalStreams(pf, dec); err != nil {
		return err
	}
	return nil
}

func equalStreams(a, b io.Reader) error {
	const chunk = 1 << 20
	bufA := make([]byte, chunk)
	bufB := make([]byte, chunk)
	for {
		nA, errA := io.ReadFull(a, bufA)
		nB, errB := io.ReadFull(b, bufB)
		if nA != nB || !bytes.Equal(bufA[:nA], bufB[:nB]) {
			return fmt.Errorf("content mismatch")
		}
		aDone := errA == io.EOF || errA == io.ErrUnexpectedEOF
		bDone := errB == io.EOF || errB == io.ErrUnexpectedEOF
		switch {
		case aDone && bDone:
			return nil
		case errA != nil && !aDone:
			return errA
		case errB != nil && !bDone:
			return errB
		case aDone != bDone:
			return fmt.Errorf("content mismatch")
		}
	}
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
