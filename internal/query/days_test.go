package query_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListDaysClassifiesDirectoryStates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "2026-07-08.ndjson.zst", "zst")
	writeFile(t, dir, "2026-07-09.ndjson", "old\n")
	writeFile(t, dir, "2026-07-10.ndjson", "plain\n")
	writeFile(t, dir, "2026-07-10.ndjson.zst", "zst")
	writeFile(t, dir, "2026-07-11.ndjson", "today\n")
	writeFile(t, dir, "heartbeat", "")

	days, err := query.ListDays(dir, clockAt("2026-07-11"))

	require.NoError(t, err)
	assert.Equal(t, []query.DayInfo{
		{Date: "2026-07-08", State: query.StateFinal, SizeBytes: 3, Compressed: true},
		{Date: "2026-07-09", State: query.StatePending, SizeBytes: 4},
		{Date: "2026-07-10", State: query.StateUntrusted, SizeBytes: 6},
		{Date: "2026-07-11", State: query.StateActive, SizeBytes: 6},
	}, days)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// clockAt returns an injectable now() fixed to noon UTC of the given day.
func clockAt(date string) func() time.Time {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return day.Add(12 * time.Hour) }
}
