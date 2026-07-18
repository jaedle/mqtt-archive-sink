package app

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsShutdownGrace = 10 * time.Second

// metricsHandler serves the sink's counters at /metrics in Prometheus
// format. A fresh registry keeps the output to application metrics only —
// no Go runtime or process collectors.
func metricsHandler(st *stats, repaired func() int64, buffered func() int) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "mqtt_archive_sink_lines_total",
			Help: "NDJSON lines accepted into the write buffer.",
		}, func() float64 { return float64(st.lines.Load()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "mqtt_archive_sink_bytes_total",
			Help: "Bytes of accepted lines including the trailing newline.",
		}, func() float64 { return float64(st.bytes.Load()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "mqtt_archive_sink_skipped_total",
			Help: "Records skipped for exceeding the record size limit.",
		}, func() float64 { return float64(st.skipped.Load()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "mqtt_archive_sink_repaired_total",
			Help: "Crash-truncated lines terminated on file open.",
		}, func() float64 { return float64(repaired()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "mqtt_archive_sink_reconnects_total",
			Help: "Broker connection losses.",
		}, func() float64 { return float64(st.reconnects.Load()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "mqtt_archive_sink_connected",
			Help: "1 while connected to the broker, else 0.",
		}, func() float64 {
			if st.connected.Load() {
				return 1
			}
			return 0
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "mqtt_archive_sink_buffered_messages",
			Help: "Messages waiting in the bounded receive buffer.",
		}, func() float64 { return float64(buffered()) }),
	)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
}
