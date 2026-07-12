// Package mcpserver wires the query engine to the outside: bearer-guarded
// MCP tools over streamable HTTP, a whole-day download, and a healthcheck
// endpoint (docs/spec/mcp.md).
package mcpserver

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
	"github.com/jaedle/mqtt-archive-sink/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Config struct {
	ArchiveDir string
	Token      string
	Now        func() time.Time
	Logger     *slog.Logger
}

// NewHandler builds the full HTTP surface. Everything except /healthz
// requires the bearer token; an empty token is refused so the archive can
// never be exposed unauthenticated by accident.
func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.Token == "" {
		return nil, errors.New("auth token must not be empty")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	server := newMCPServer(cfg)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", requireBearer(cfg.Token, mcpHandler))
	mux.Handle("GET /days/{file}", requireBearer(cfg.Token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveDay(cfg, w, r)
	})))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux, nil
}

func requireBearer(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveDay streams one whole day as uncompressed NDJSON regardless of how it
// is stored (docs/spec/mcp.md).
func serveDay(cfg Config, w http.ResponseWriter, r *http.Request) {
	date, ok := strings.CutSuffix(r.PathValue("file"), ".ndjson")
	if !ok {
		http.Error(w, "expected /days/YYYY-MM-DD.ndjson", http.StatusBadRequest)
		return
	}
	if _, err := time.Parse(archive.DateFormat, date); err != nil {
		http.Error(w, fmt.Sprintf("malformed date %q", date), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	err := query.WriteDay(cfg.ArchiveDir, date, w)
	switch {
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, fmt.Sprintf("no archive for %s", date), http.StatusNotFound)
	case err != nil:
		// The stream may already be half-written; all we can do is drop it.
		cfg.Logger.Error("day download failed", "date", date, "error", err)
	}
}
