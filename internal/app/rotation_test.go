package app_test

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRotationCompressesClosedDay(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)
	clock := &fakeClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	sink := startSink(t, dir, addr, func(c *app.Config) { c.Now = clock.now })
	broker.waitSubscribed()
	day1 := filepath.Join(dir, "2026-03-01.ndjson")
	day2 := filepath.Join(dir, "2026-03-02.ndjson")

	broker.publish("rot/1", []byte("first day"))
	waitForLineCount(t, day1, 1)
	day1Content, err := os.ReadFile(day1)
	require.NoError(t, err)
	clock.set(time.Date(2026, 3, 2, 0, 0, 1, 0, time.UTC))
	broker.publish("rot/2", []byte("second day"))
	waitForLineCount(t, day2, 1)
	waitForCompressedDay(t, day1)
	sink.stop()

	assert.Equal(t, day1Content, decodeZst(t, day1+".zst"),
		"archive must decode byte-identical to the plain file it replaced")
	assert.Equal(t, []string{"rot/2"}, topicsOf(readRecords(t, day2)))
}

func TestRestartAppendsToSameDailyFile(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)

	first := startSink(t, dir, addr)
	broker.waitSubscribed()
	broker.publish("run/1", []byte("first run"))
	waitForLineCount(t, todayFile(dir), 1)
	first.stop()
	second := startSink(t, dir, addr)
	broker.waitSubscribed()
	broker.publish("run/2", []byte("second run"))
	waitForLineCount(t, todayFile(dir), 2)
	second.stop()

	assert.Equal(t, []string{"run/1", "run/2"}, topicsOf(readRecords(t, todayFile(dir))))
}

func TestOversizedRecordIsSkipped(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)
	logs := &lockedBuffer{}
	sink := startSink(t, dir, addr, func(c *app.Config) {
		c.Logger = slog.New(slog.NewJSONHandler(logs, nil))
	})
	broker.waitSubscribed()

	broker.publish("big/1", bytes.Repeat([]byte("x"), app.MaxRecordSize))
	broker.publish("small/1", []byte("fits"))
	waitForLineCount(t, todayFile(dir), 1)
	sink.stop()

	assert.Equal(t, []string{"small/1"}, topicsOf(readRecords(t, todayFile(dir))),
		"oversized record must not be archived, small one must survive")
	assert.True(t, strings.Contains(logs.String(), "skipped"), "skip must be logged/counted")
}
