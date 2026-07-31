package app_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/app"
	"github.com/klauspost/compress/zstd"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// testTimeout bounds every "eventually" wait and the sink shutdown.
	testTimeout = 10 * time.Second
	// pollInterval is how often waits re-check their condition.
	pollInterval = 10 * time.Millisecond
	// fastFlush makes the sink flush almost immediately so tests observe writes promptly.
	fastFlush = 20 * time.Millisecond
	// maxRecordScan sits above the 16 MiB SPEC record limit so no line is ever split.
	maxRecordScan = 32 << 20
	// clockSkewTolerance bounds the drift between wall-clock now and a record's timestamp.
	clockSkewTolerance = time.Minute
	// testUsername and testPassword are the credentials the authenticating broker demands.
	testUsername = "sink"
	testPassword = "s3cret"
)

type record struct {
	TS         string  `json:"ts"`
	Topic      string  `json:"topic"`
	Payload    *string `json:"payload"`
	PayloadB64 *string `json:"payload_b64"`
}

// --- embedded broker ---------------------------------------------------------

type subscribedHook struct {
	mochi.HookBase
	ch chan struct{}
}

func (h *subscribedHook) ID() string { return "subscribed-signal" }

func (h *subscribedHook) Provides(b byte) bool { return b == mochi.OnSubscribed }

func (h *subscribedHook) OnSubscribed(_ *mochi.Client, _ packets.Packet, _ []byte) {
	select {
	case h.ch <- struct{}{}:
	default:
	}
}

type testBroker struct {
	t          *testing.T
	server     *mochi.Server
	addr       string
	subscribed chan struct{}
	closeOnce  sync.Once
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func startBroker(t *testing.T, addr string) *testBroker {
	t.Helper()
	return startBrokerWithAuth(t, addr, new(auth.AllowHook), nil)
}

// startAuthBroker refuses every client that does not present testUsername and
// testPassword, so reaching SUBSCRIBE proves the sink authenticated.
func startAuthBroker(t *testing.T, addr string) *testBroker {
	t.Helper()
	ledger := &auth.Ledger{
		Auth: auth.AuthRules{{Username: testUsername, Password: testPassword, Allow: true}},
		// A rule with no filters allows every topic; with an empty ACL the
		// broker would deny the sink's subscribe.
		ACL: auth.ACLRules{{}},
	}
	return startBrokerWithAuth(t, addr, new(auth.Hook), &auth.Options{Ledger: ledger})
}

func startBrokerWithAuth(t *testing.T, addr string, authHook mochi.Hook, authConfig any) *testBroker {
	t.Helper()
	server := mochi.New(&mochi.Options{
		InlineClient: true,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, server.AddHook(authHook, authConfig))
	hook := &subscribedHook{ch: make(chan struct{}, 1)}
	require.NoError(t, server.AddHook(hook, nil))
	require.NoError(t, server.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: addr})))
	go func() { _ = server.Serve() }()
	b := &testBroker{t: t, server: server, addr: addr, subscribed: hook.ch}
	t.Cleanup(b.stop)
	return b
}

func (b *testBroker) waitSubscribed() {
	b.t.Helper()
	select {
	case <-b.subscribed:
	case <-time.After(testTimeout):
		b.t.Fatal("timed out waiting for sink to subscribe")
	}
}

func (b *testBroker) publish(topic string, payload []byte) {
	b.t.Helper()
	require.NoError(b.t, b.server.Publish(topic, payload, false, 1))
}

func (b *testBroker) stop() {
	b.closeOnce.Do(func() { _ = b.server.Close() })
}

// --- sink under test ---------------------------------------------------------

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
		FlushInterval: fastFlush,
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

func withCredentials(username, password string) func(*app.Config) {
	return func(c *app.Config) {
		c.Username = username
		c.Password = password
	}
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

// --- injectable clock & log capture -----------------------------------------

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

type lockedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// --- reading archives --------------------------------------------------------

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
	scanner.Buffer(nil, maxRecordScan)
	for scanner.Scan() {
		var r record
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &r), "line: %q", scanner.Text())
		records = append(records, r)
	}
	require.NoError(t, scanner.Err())
	return records
}

// countLines returns 0 for a not-yet-created file so waiters can poll from zero.
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

func topicsOf(records []record) []string {
	topics := make([]string, len(records))
	for i, r := range records {
		topics[i] = r.Topic
	}
	return topics
}

func decodeBase64(t *testing.T, s string) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(s)
	require.NoError(t, err)
	return data
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

// --- waiters (replace inline Eventually closures) ----------------------------

func waitForLineCount(t *testing.T, path string, want int) {
	t.Helper()
	require.Eventuallyf(t, func() bool { return countLines(path) == want },
		testTimeout, pollInterval, "expected exactly %d archived lines in %s", want, path)
}

func waitForAtLeastLines(t *testing.T, path string, want int) {
	t.Helper()
	require.Eventuallyf(t, func() bool { return countLines(path) >= want },
		testTimeout, pollInterval, "expected at least %d archived lines in %s", want, path)
}

func waitForCompressedDay(t *testing.T, plainPath string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		_, zstErr := os.Stat(plainPath + ".zst")
		_, plainErr := os.Stat(plainPath)
		return zstErr == nil && os.IsNotExist(plainErr)
	}, testTimeout, pollInterval, "closed day must be compressed and plain removed: %s", plainPath)
}

func waitForFreshHeartbeat(t *testing.T, path string) {
	t.Helper()
	require.Eventuallyf(t, func() bool {
		fi, err := os.Stat(path)
		return err == nil && time.Since(fi.ModTime()) < clockSkewTolerance
	}, testTimeout, pollInterval, "heartbeat must be touched even with zero traffic: %s", path)
}

func assertTimestampsAreRecent(t *testing.T, records []record) {
	t.Helper()
	for _, r := range records {
		ts, err := time.Parse(time.RFC3339Nano, r.TS)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), ts, clockSkewTolerance)
	}
}
