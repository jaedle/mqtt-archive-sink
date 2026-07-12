package app_test

import (
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/stretchr/testify/require"
)

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
	server := mochi.New(&mochi.Options{
		InlineClient: true,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, server.AddHook(new(auth.AllowHook), nil))
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
	case <-time.After(10 * time.Second):
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
