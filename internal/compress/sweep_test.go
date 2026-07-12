package compress

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const today = "2026-03-05"

func newTestSweeper(dir string) *Sweeper {
	return NewSweeper(dir, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func writePlain(t *testing.T, dir, date, content string) string {
	t.Helper()
	path := filepath.Join(dir, date+".ndjson")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func decode(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	dec, err := zstd.NewReader(f)
	require.NoError(t, err)
	defer dec.Close()
	data, err := io.ReadAll(dec)
	require.NoError(t, err)
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// zstWrongContent writes a valid zstd file whose content does not match src,
// simulating corruption between write and verify.
func zstWrongContent(s *Sweeper) func(src, dst string) error {
	real := s.compressFile
	return func(src, dst string) error {
		if err := real(src, dst); err != nil {
			return err
		}
		f, err := os.Create(dst)
		if err != nil {
			return err
		}
		enc, err := zstd.NewWriter(f)
		if err != nil {
			return err
		}
		if _, err := enc.Write([]byte("tampered")); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		return f.Close()
	}
}

func TestMismatchNeverDeletesPlainAndSweepContinues(t *testing.T) {
	dir := t.TempDir()
	bad := writePlain(t, dir, "2026-03-01", "precious line\n")
	good := writePlain(t, dir, "2026-03-02", "other line\n")

	s := newTestSweeper(dir)
	realCompress := s.compressFile
	tampered := zstWrongContent(s)
	s.compress = func(src, dst string) error {
		if strings.Contains(src, "2026-03-01") {
			return tampered(src, dst)
		}
		return realCompress(src, dst)
	}

	s.Sweep(today)

	assert.True(t, exists(bad), "plain file must survive a verify mismatch")
	assert.False(t, exists(good), "sweep must continue with the next file")
	assert.Equal(t, "other line\n", decode(t, good+".zst"))
}

func TestLeftoverZstIsRedoneWhilePlainExists(t *testing.T) {
	dir := t.TempDir()
	plain := writePlain(t, dir, "2026-03-01", "real content\n")
	require.NoError(t, os.WriteFile(plain+".zst", []byte("garbage, not zstd"), 0o644))

	newTestSweeper(dir).Sweep(today)

	assert.False(t, exists(plain), "plain deleted after successful redo")
	assert.Equal(t, "real content\n", decode(t, plain+".zst"))
}

func TestFinalZstAloneIsLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	plain := writePlain(t, dir, "2026-03-01", "done\n")
	s := newTestSweeper(dir)
	s.Sweep(today)
	require.False(t, exists(plain))

	s.Sweep(today)

	assert.False(t, exists(plain), "no plain file must reappear")
	assert.Equal(t, "done\n", decode(t, plain+".zst"))
}

func TestUnreadablePlainIsKeptAndSweepContinues(t *testing.T) {
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "2026-03-01.ndjson")
	require.NoError(t, os.Mkdir(unreadable, 0o755))
	good := writePlain(t, dir, "2026-03-02", "fine\n")

	newTestSweeper(dir).Sweep(today)

	assert.True(t, exists(unreadable), "unprocessable entry must be left alone")
	assert.False(t, exists(good), "sweep must continue with the next file")
	assert.Equal(t, "fine\n", decode(t, good+".zst"))
}

func TestTodayAndNonDateFilesAreNotSwept(t *testing.T) {
	dir := t.TempDir()
	todayPlain := writePlain(t, dir, today, "in progress\n")
	odd := filepath.Join(dir, "notes.ndjson")
	require.NoError(t, os.WriteFile(odd, []byte("not a daily file\n"), 0o644))

	newTestSweeper(dir).Sweep(today)

	assert.True(t, exists(todayPlain), "today's file must never be touched")
	assert.False(t, exists(todayPlain+".zst"))
	assert.True(t, exists(odd))
	assert.False(t, exists(odd+".zst"))
}
