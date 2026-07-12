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
	const (
		completeLine = `{"complete":1}`
		partialLine  = `{"partial` // crash left this line unterminated
		nextLine     = `{"next":2}`
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-03-01.ndjson")
	require.NoError(t, os.WriteFile(path, []byte(completeLine+"\n"+partialLine), 0o644))

	clock := func() time.Time { return time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC) }
	writer := archive.NewWriter(dir, clock, true)
	_, err := writer.Append([]byte(nextLine))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	repaired := completeLine + "\n" + partialLine + "\n" + nextLine + "\n"
	assert.Equal(t, repaired, string(data), "partial line terminated, not truncated; next line intact")
	assert.EqualValues(t, 1, writer.Repaired())
}
