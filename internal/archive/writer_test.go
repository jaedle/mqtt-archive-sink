package archive_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrashTruncatedLineIsRepairedOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-03-01.ndjson")
	require.NoError(t, os.WriteFile(path, []byte("{\"complete\":1}\n{\"partial"), 0o644))

	clock := func() time.Time { return time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC) }
	w := archive.NewWriter(dir, clock, true)
	_, err := w.Append([]byte(`{"next":2}`))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "{\"complete\":1}\n{\"partial\n{\"next\":2}\n", string(data),
		"partial line must be terminated, not truncated; next line intact")
	assert.EqualValues(t, 1, w.Repaired())
}
