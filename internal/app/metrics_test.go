package app_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/jaedle/mqtt-archive-sink/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsEndpointServesAppMetrics(t *testing.T) {
	dir := t.TempDir()
	broker := startBroker(t, freeAddr(t))
	metricsAddr := freeAddr(t)
	sink := startSink(t, dir, broker.addr, func(c *app.Config) { c.MetricsListenAddr = metricsAddr })
	broker.waitSubscribed()

	broker.publish("sensors/temp", []byte("21.5"))
	broker.publish("sensors/hum", []byte("60"))
	waitForLineCount(t, todayFile(dir), 2)
	body := waitForMetricLine(t, metricsAddr, "mqtt_archive_sink_lines_total 2")

	assert.Contains(t, body, "mqtt_archive_sink_connected 1\n")
	assert.Contains(t, body, "mqtt_archive_sink_bytes_total")
	assert.Contains(t, body, "mqtt_archive_sink_skipped_total 0\n")
	assert.Contains(t, body, "mqtt_archive_sink_repaired_total 0\n")
	assert.Contains(t, body, "mqtt_archive_sink_reconnects_total 0\n")
	assert.Contains(t, body, "mqtt_archive_sink_buffered_messages")
	assert.NotContains(t, body, "go_goroutines", "must expose app metrics only")
	assert.NotContains(t, body, "process_", "must expose app metrics only")
	sink.stop()
}

func TestMetricsBadAddrFailsStartup(t *testing.T) {
	cfg := app.Config{
		Broker:            "tcp://127.0.0.1:1",
		ArchiveDir:        t.TempDir(),
		MetricsListenAddr: "definitely-not-an-address",
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := app.Run(context.Background(), cfg)

	require.ErrorContains(t, err, "metrics listener")
}

// waitForMetricLine polls the metrics endpoint until the body contains the
// exact metric line, then returns the body for further assertions.
func waitForMetricLine(t *testing.T, addr string, line string) string {
	t.Helper()
	var body string
	require.Eventuallyf(t, func() bool {
		b, ok := fetchMetrics(addr)
		if !ok {
			return false
		}
		body = b
		return strings.Contains(body, line+"\n")
	}, testTimeout, pollInterval, "metrics endpoint at %s never served %q", addr, line)
	return body
}

func fetchMetrics(addr string) (string, bool) {
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", false
	}
	return string(data), true
}
