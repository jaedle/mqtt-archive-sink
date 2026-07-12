package app_test

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/app"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func decodeZst(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	dec, err := zstd.NewReader(f)
	require.NoError(t, err)
	defer dec.Close()
	data, err := io.ReadAll(dec)
	require.NoError(t, err)
	return data
}

func TestRotationCompressesClosedDay(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)

	clock := &fakeClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	s := startSink(t, dir, addr, func(c *app.Config) { c.Now = clock.now })
	broker.waitSubscribed()

	day1 := filepath.Join(dir, "2026-03-01.ndjson")
	day2 := filepath.Join(dir, "2026-03-02.ndjson")

	broker.publish("rot/1", []byte("first day"))
	require.Eventually(t, func() bool { return countLines(day1) == 1 },
		testTimeout, 10*time.Millisecond)
	day1Content, err := os.ReadFile(day1)
	require.NoError(t, err)

	clock.set(time.Date(2026, 3, 2, 0, 0, 1, 0, time.UTC))
	broker.publish("rot/2", []byte("second day"))

	require.Eventually(t, func() bool { return countLines(day2) == 1 },
		testTimeout, 10*time.Millisecond, "new day's message must land in the new file")
	require.Eventually(t, func() bool {
		_, zstErr := os.Stat(day1 + ".zst")
		_, plainErr := os.Stat(day1)
		return zstErr == nil && os.IsNotExist(plainErr)
	}, testTimeout, 10*time.Millisecond, "closed day must be compressed and plain removed")
	s.stop()

	assert.Equal(t, day1Content, decodeZst(t, day1+".zst"),
		"archive must decode byte-identical to the plain file it replaced")

	records := readRecords(t, day2)
	require.Len(t, records, 1)
	assert.Equal(t, "rot/2", records[0].Topic)
}

func TestRestartAppendsToSameDailyFile(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)

	s1 := startSink(t, dir, addr)
	broker.waitSubscribed()
	broker.publish("run/1", []byte("first run"))
	require.Eventually(t, func() bool { return countLines(todayFile(dir)) == 1 },
		testTimeout, 10*time.Millisecond)
	s1.stop()

	s2 := startSink(t, dir, addr)
	broker.waitSubscribed()
	broker.publish("run/2", []byte("second run"))
	require.Eventually(t, func() bool { return countLines(todayFile(dir)) == 2 },
		testTimeout, 10*time.Millisecond, "second run must append to the same daily file")
	s2.stop()

	records := readRecords(t, todayFile(dir))
	require.Len(t, records, 2)
	assert.Equal(t, "run/1", records[0].Topic)
	assert.Equal(t, "run/2", records[1].Topic)
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestOversizedRecordIsSkipped(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)

	logs := &lockedBuffer{}
	s := startSink(t, dir, addr, func(c *app.Config) {
		c.Logger = slog.New(slog.NewJSONHandler(logs, nil))
	})
	broker.waitSubscribed()

	oversized := bytes.Repeat([]byte("x"), app.MaxRecordSize)
	broker.publish("big/1", oversized)
	broker.publish("small/1", []byte("fits"))

	require.Eventually(t, func() bool { return countLines(todayFile(dir)) == 1 },
		testTimeout, 10*time.Millisecond, "the small record must still be archived")
	s.stop()

	records := readRecords(t, todayFile(dir))
	require.Len(t, records, 1, "oversized record must not be archived")
	assert.Equal(t, "small/1", records[0].Topic)
	assert.True(t, strings.Contains(logs.String(), "skipped"),
		"skip must be logged/counted")
}
