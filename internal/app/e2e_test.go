package app_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testTimeout = 10 * time.Second

type record struct {
	TS         string  `json:"ts"`
	Topic      string  `json:"topic"`
	Payload    *string `json:"payload"`
	PayloadB64 *string `json:"payload_b64"`
}

type sink struct {
	t       *testing.T
	dir     string
	cancel  context.CancelFunc
	done    chan error
	stopped bool
}

func startSink(t *testing.T, dir string, broker string, mutate ...func(*app.Config)) *sink {
	t.Helper()
	cfg := app.Config{
		Broker:        "tcp://" + broker,
		Topic:         "#",
		ClientID:      "archiver-test",
		ArchiveDir:    dir,
		FlushInterval: 20 * time.Millisecond,
		FsyncInterval: time.Second,
		HeartbeatFile: filepath.Join(dir, "heartbeat"),
		ZstdLevel:     3,
		BufferSize:    100,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, m := range mutate {
		m(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg) }()
	s := &sink{t: t, dir: dir, cancel: cancel, done: done}
	t.Cleanup(s.stopIgnoreError)
	return s
}

func (s *sink) stop() {
	s.t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		s.stopped = true
		require.NoError(s.t, err)
	case <-time.After(testTimeout):
		s.t.Fatal("timed out waiting for sink to shut down")
	}
}

func (s *sink) stopIgnoreError() {
	if s.stopped {
		return
	}
	s.cancel()
	select {
	case <-s.done:
		s.stopped = true
	case <-time.After(testTimeout):
	}
}

func todayFile(dir string) string {
	return filepath.Join(dir, time.Now().UTC().Format("2006-01-02")+".ndjson")
}

func readRecords(t *testing.T, path string) []record {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var records []record
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 32*1024*1024)
	for scanner.Scan() {
		var r record
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &r), "line: %q", scanner.Text())
		records = append(records, r)
	}
	require.NoError(t, scanner.Err())
	return records
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

func TestArchivesPublishedMessages(t *testing.T) {
	dir := t.TempDir()
	broker := startBroker(t, freeAddr(t))
	s := startSink(t, dir, broker.addr)
	broker.waitSubscribed()

	binary := []byte{0xff, 0xfe, 0x01}
	broker.publish("sensors/temp", []byte("21.5"))
	broker.publish("sensors/bin", binary)
	broker.publish("sensors/hum", []byte(`{"value":60}`))

	require.Eventually(t, func() bool { return countLines(todayFile(dir)) == 3 },
		testTimeout, 10*time.Millisecond)
	s.stop()

	records := readRecords(t, todayFile(dir))
	require.Len(t, records, 3)

	require.NotNil(t, records[0].Payload)
	assert.Equal(t, "sensors/temp", records[0].Topic)
	assert.Equal(t, "21.5", *records[0].Payload)

	assert.Equal(t, "sensors/bin", records[1].Topic)
	require.Nil(t, records[1].Payload, "non-UTF-8 payload must use payload_b64")
	require.NotNil(t, records[1].PayloadB64)
	decoded, err := base64.StdEncoding.DecodeString(*records[1].PayloadB64)
	require.NoError(t, err)
	assert.Equal(t, binary, decoded)

	require.NotNil(t, records[2].Payload)
	assert.Equal(t, `{"value":60}`, *records[2].Payload)

	for _, r := range records {
		ts, err := time.Parse(time.RFC3339Nano, r.TS)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), ts, time.Minute)
	}
}

func TestHeartbeatTouchedWithoutTraffic(t *testing.T) {
	dir := t.TempDir()
	broker := startBroker(t, freeAddr(t))
	s := startSink(t, dir, broker.addr)
	broker.waitSubscribed()

	heartbeat := filepath.Join(dir, "heartbeat")
	require.Eventually(t, func() bool {
		fi, err := os.Stat(heartbeat)
		return err == nil && time.Since(fi.ModTime()) < time.Minute
	}, testTimeout, 10*time.Millisecond, "heartbeat must be touched even with zero messages")
	s.stop()
}

func TestReconnectsAfterBrokerRestart(t *testing.T) {
	dir := t.TempDir()
	addr := freeAddr(t)
	broker := startBroker(t, addr)
	s := startSink(t, dir, addr)
	broker.waitSubscribed()

	broker.publish("t/1", []byte("before"))
	require.Eventually(t, func() bool { return countLines(todayFile(dir)) == 1 },
		testTimeout, 10*time.Millisecond)

	broker.stop()
	broker = startBroker(t, addr)
	broker.waitSubscribed()

	broker.publish("t/2", []byte("after"))
	require.Eventually(t, func() bool { return countLines(todayFile(dir)) >= 2 },
		testTimeout, 10*time.Millisecond, "message after broker restart must be archived")
	s.stop()

	records := readRecords(t, todayFile(dir))
	var topics []string
	for _, r := range records {
		topics = append(topics, r.Topic)
	}
	assert.Contains(t, topics, "t/1")
	assert.Contains(t, topics, "t/2")
}
