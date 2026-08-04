package mqtt_test

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/mqtt"
	"github.com/stretchr/testify/require"
)

func TestLogsConnectFailures(t *testing.T) {
	broker := unreachableTLSBroker(t)
	logs := newSyncBuffer()

	client, err := mqtt.Connect(mqtt.Config{
		Broker:   broker,
		Topic:    "#",
		ClientID: "test",
		Logger:   slog.New(slog.NewTextHandler(logs, nil)),
	})
	require.NoError(t, err)
	defer client.Disconnect()

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), "level=ERROR")
	}, 5*time.Second, 50*time.Millisecond, "connect failure was not logged")
}

func TestRejectsCredentialsInBrokerURL(t *testing.T) {
	cfg := configWith(func(c *mqtt.Config) { c.Broker = "tcp://user:pass@localhost:1883" })

	_, err := mqtt.Connect(cfg)

	require.ErrorContains(t, err, "MQTT_BROKER must not contain credentials")
}

func TestRejectsPasswordWithoutUsername(t *testing.T) {
	cfg := configWith(func(c *mqtt.Config) { c.Password = "s3cret" })

	_, err := mqtt.Connect(cfg)

	require.ErrorContains(t, err, "MQTT_PASSWORD requires MQTT_USERNAME")
}

// configWith builds an otherwise-valid config so each test varies exactly the
// one field it is about. The broker is never dialled — validation rejects first.
func configWith(mutate func(*mqtt.Config)) mqtt.Config {
	cfg := mqtt.Config{
		Broker:   "tcp://localhost:1883",
		Topic:    "#",
		ClientID: "test",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mutate(&cfg)
	return cfg
}

// unreachableTLSBroker returns an mqtts:// URL of a listener that closes every
// connection immediately, failing the TLS handshake.
func unreachableTLSBroker(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return "mqtts://" + l.Addr().String()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
