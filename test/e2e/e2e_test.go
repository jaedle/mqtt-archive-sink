//go:build e2e

// Package e2e drives the real dockerized stack (mosquitto broker + the shipped
// sink and mcp images) via docker compose. Runs only under `-tags e2e` and
// needs Docker.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// connectTimeout bounds the first-time image build + broker connect.
	connectTimeout = 90 * time.Second
	// archivedTimeout is generous — "wait long enough" for buffer + flush.
	archivedTimeout = 60 * time.Second
	// pollInterval is how often logs / the archive are re-checked.
	pollInterval = 500 * time.Millisecond
	// mcpToken is the static bearer token wired into the mcp container.
	mcpToken = "e2e-token"
)

type record struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload"`
}

type stack struct {
	t          *testing.T
	project    string
	archiveDir string
}

// startStack brings the stack up, isolated per run so many can run at once on one
// machine: a unique compose project name (no shared containers/networks) and a
// per-run archive dir. No host ports are published — see docker-compose.yaml.
func startStack(t *testing.T) *stack {
	t.Helper()
	requireDocker(t)
	s := &stack{
		t:          t,
		project:    fmt.Sprintf("mas-e2e-%d", os.Getpid()),
		archiveDir: t.TempDir(),
	}
	t.Cleanup(func() { _, _ = s.run("down", "-v") })
	_, err := s.run("up", "-d", "--build")
	require.NoError(t, err)
	return s
}

func (s *stack) waitConnected() {
	s.t.Helper()
	require.Eventually(s.t, func() bool {
		out, _ := s.run("logs", "sink")
		return strings.Contains(out, "connected")
	}, connectTimeout, pollInterval, "sink never connected to the broker")
}

func (s *stack) publish(topic, payload string) {
	s.t.Helper()
	_, err := s.run("exec", "-T", "broker", "mosquitto_pub", "-r", "-t", topic, "-m", payload)
	require.NoError(s.t, err)
}

func (s *stack) requireArchived(topic, payload string) {
	s.t.Helper()
	var got *record
	require.Eventually(s.t, func() bool {
		got = s.findByPayload(payload)
		return got != nil
	}, archivedTimeout, pollInterval, "no archived line contained payload %q", payload)

	assert.Equal(s.t, topic, got.Topic)
	assert.Equal(s.t, payload, got.Payload)
}

// run invokes docker compose scoped to this run's project and archive dir.
func (s *stack) run(args ...string) (string, error) {
	base := []string{"compose", "-p", s.project, "-f", "docker-compose.yaml"}
	cmd := exec.Command("docker", append(base, args...)...)
	cmd.Env = append(os.Environ(), "ARCHIVE_HOST_DIR="+s.archiveDir, "MCP_AUTH_TOKEN="+mcpToken)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker compose %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

func (s *stack) findByPayload(payload string) *record {
	path := filepath.Join(s.archiveDir, time.Now().UTC().Format("2006-01-02")+".ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var r record
		if json.Unmarshal(line, &r) == nil && r.Payload == payload {
			return &r
		}
	}
	return nil
}

func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
}

func TestArchivesPublishedMessageEndToEnd(t *testing.T) {
	payload := fmt.Sprintf("hello-e2e-%d", os.Getpid())
	stack := startStack(t)
	stack.waitConnected()

	stack.publish("sensors/e2e", payload)

	stack.requireArchived("sensors/e2e", payload)
}

// TestServesArchivedMessagesOverMCP proves the shipped mcp image end-to-end:
// entrypoint, bearer auth, read-only volume, query tool, and day download.
func TestServesArchivedMessagesOverMCP(t *testing.T) {
	payload := fmt.Sprintf("hello-mcp-%d", os.Getpid())
	stack := startStack(t)
	stack.waitConnected()
	stack.publish("sensors/mcp", payload)
	stack.requireArchived("sensors/mcp", payload)

	queried := stack.queryOverMCP("sensors/mcp")
	day := stack.downloadToday()

	assert.Contains(t, queried, payload)
	assert.Contains(t, day, payload)
}

// mcpBaseURL resolves the mcp container's ephemeral host port.
func (s *stack) mcpBaseURL() string {
	s.t.Helper()
	out, err := s.run("port", "mcp", "8080")
	require.NoError(s.t, err)
	addr, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	require.NotEmpty(s.t, addr, "mcp container publishes no port")
	return "http://" + strings.TrimSpace(addr)
}

// queryOverMCP calls the query tool through a real MCP session and returns
// the payloads of all records for the topic.
func (s *stack) queryOverMCP(topic string) []string {
	s.t.Helper()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   s.mcpBaseURL() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{}},
	}, nil)
	require.NoError(s.t, err)
	defer func() { _ = session.Close() }()

	from := time.Now().UTC().Format("2006-01-02") + "T00:00:00Z" // query scans one UTC day
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "query", Arguments: map[string]any{"topic": topic, "from": from}})
	require.NoError(s.t, err)
	require.Falsef(s.t, res.IsError, "query tool failed: %+v", res.Content)
	return payloadsOf(s.t, res.StructuredContent)
}

// downloadToday fetches today's uncompressed NDJSON via the download route.
func (s *stack) downloadToday() string {
	s.t.Helper()
	day := time.Now().UTC().Format("2006-01-02")
	req, err := http.NewRequest(http.MethodGet, s.mcpBaseURL()+"/days/"+day+".ndjson", nil)
	require.NoError(s.t, err)
	req.Header.Set("Authorization", "Bearer "+mcpToken)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(s.t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)
	require.NoError(s.t, resp.Body.Close())
	require.Equal(s.t, http.StatusOK, resp.StatusCode)
	return string(body)
}

type bearerTransport struct{}

func (bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+mcpToken)
	return http.DefaultTransport.RoundTrip(r)
}

func payloadsOf(t *testing.T, structured any) []string {
	t.Helper()
	raw, err := json.Marshal(structured)
	require.NoError(t, err)
	var out struct {
		Records []record `json:"records"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	var payloads []string
	for _, r := range out.Records {
		payloads = append(payloads, r.Payload)
	}
	return payloads
}
