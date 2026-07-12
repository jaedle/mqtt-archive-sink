package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/compress"
	"github.com/jaedle/mqtt-archive-sink/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testToken = "test-token"

func TestNewHandlerRefusesEmptyToken(t *testing.T) {
	_, err := mcpserver.NewHandler(mcpserver.Config{ArchiveDir: t.TempDir()})

	require.Error(t, err)
}

func TestRequestsWithoutValidTokenAreRejected(t *testing.T) {
	srv := newServer(t, t.TempDir())

	for name, header := range map[string]string{"missing": "", "wrong": "Bearer nope"} {
		for _, path := range []string{"/mcp", "/days/2026-07-10.ndjson"} {
			req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
			require.NoError(t, err)
			if header != "" {
				req.Header.Set("Authorization", header)
			}

			resp, err := http.DefaultClient.Do(req)

			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equalf(t, http.StatusUnauthorized, resp.StatusCode, "%s token on %s", name, path)
		}
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	srv := newServer(t, t.TempDir())

	resp, err := http.Get(srv.URL + "/healthz")

	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestListsAllTools(t *testing.T) {
	session := connect(t, newServer(t, t.TempDir()))

	tools, err := session.ListTools(t.Context(), nil)

	require.NoError(t, err)
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{"list_days", "query", "tail"}, names)
}

func TestListDaysToolReportsStates(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10", recordLine("2026-07-10", "t", "x"))
	session := connect(t, newServer(t, dir))

	out := callTool(t, session, "list_days", map[string]any{})

	days := out["days"].([]any)
	require.Len(t, days, 1)
	day := days[0].(map[string]any)
	assert.Equal(t, "2026-07-10", day["date"])
	assert.Equal(t, "active", day["state"], "plain file of the injected today")
}

func TestQueryToolPaginates(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10",
		recordLine("2026-07-10", "t", "one"),
		recordLine("2026-07-10", "t", "two"),
		recordLine("2026-07-10", "t", "three"),
	)
	session := connect(t, newServer(t, dir))

	first := callTool(t, session, "query", map[string]any{"limit": 2})
	rest := callTool(t, session, "query", map[string]any{"limit": 2, "cursor": first["next_cursor"]})

	assert.Equal(t, []string{"one", "two"}, payloadsOf(t, first))
	assert.Equal(t, true, first["has_more"])
	assert.Equal(t, []string{"three"}, payloadsOf(t, rest))
	assert.Equal(t, false, rest["has_more"])
}

func TestTailFirstCallStartsAtCurrentEnd(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10", recordLine("2026-07-10", "t", "already archived"))
	session := connect(t, newServer(t, dir))

	start := callTool(t, session, "tail", map[string]any{})
	appendLine(t, dir, "2026-07-10", recordLine("2026-07-10", "t", "fresh"))
	poll := callTool(t, session, "tail", map[string]any{"cursor": start["next_cursor"]})

	assert.Empty(t, payloadsOf(t, start), "first call must not replay history")
	assert.Equal(t, []string{"fresh"}, payloadsOf(t, poll))
}

func TestQueryToolRejectsBadInput(t *testing.T) {
	session := connect(t, newServer(t, t.TempDir()))

	for name, args := range map[string]map[string]any{
		"bad from":   {"from": "yesterday-ish"},
		"bad cursor": {"cursor": "garbage"},
	} {
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "query", Arguments: args})

		require.NoError(t, err, name)
		assert.Truef(t, res.IsError, "%s must surface as tool error", name)
	}
}

func TestDownloadStreamsUncompressedNDJSON(t *testing.T) {
	dir := t.TempDir()
	content := recordLine("2026-07-10", "t", "compressed away") + "\n"
	writeDay(t, dir, "2026-07-10", recordLine("2026-07-10", "t", "compressed away"))
	compressDay(t, dir, "2026-07-11")
	srv := newServer(t, dir)

	body, resp := download(t, srv, "/days/2026-07-10.ndjson")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))
	assert.Equal(t, content, body, "served uncompressed although stored as .zst")
}

func TestDownloadRejectsUnknownAndMalformedDays(t *testing.T) {
	srv := newServer(t, t.TempDir())

	_, missing := download(t, srv, "/days/2026-07-10.ndjson")
	_, malformed := download(t, srv, "/days/not-a-date.ndjson")

	assert.Equal(t, http.StatusNotFound, missing.StatusCode)
	assert.Equal(t, http.StatusBadRequest, malformed.StatusCode)
}

func newServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	handler, err := mcpserver.NewHandler(mcpserver.Config{
		ArchiveDir: dir,
		Token:      testToken,
		Now:        func() time.Time { return time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC) },
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func connect(t *testing.T, srv *httptest.Server) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// bearerTransport authenticates every MCP client request in tests.
type bearerTransport struct{}

func (bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+testToken)
	return http.DefaultTransport.RoundTrip(r)
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Falsef(t, res.IsError, "tool %s failed: %+v", name, res.Content)
	out, ok := res.StructuredContent.(map[string]any)
	require.Truef(t, ok, "tool %s returned no structured output", name)
	return out
}

func payloadsOf(t *testing.T, out map[string]any) []string {
	t.Helper()
	raw, err := json.Marshal(out["records"])
	require.NoError(t, err)
	var records []struct {
		Payload string `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(raw, &records))
	var payloads []string
	for _, r := range records {
		payloads = append(payloads, r.Payload)
	}
	return payloads
}

func download(t *testing.T, srv *httptest.Server, path string) (string, *http.Response) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return string(body), resp
}

func recordLine(date, topic, payload string) string {
	return fmt.Sprintf(`{"ts":%q,"topic":%q,"payload":%q}`, date+"T12:00:00Z", topic, payload)
}

func writeDay(t *testing.T, dir, date string, lines ...string) {
	t.Helper()
	var content string
	for _, l := range lines {
		content += l + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, date+".ndjson"), []byte(content), 0o644))
}

func appendLine(t *testing.T, dir, date, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, date+".ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(line + "\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// compressDay runs the real sweeper so the fixture matches a production .zst.
func compressDay(t *testing.T, dir, today string) {
	t.Helper()
	compress.NewSweeper(dir, 3, slog.New(slog.NewTextHandler(io.Discard, nil))).Sweep(today)
	plain, err := filepath.Glob(filepath.Join(dir, "*.ndjson"))
	require.NoError(t, err)
	require.Empty(t, plain, "sweep must have compressed and removed the plain file")
}
