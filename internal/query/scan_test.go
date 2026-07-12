package query_test

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/compress"
	"github.com/jaedle/mqtt-archive-sink/internal/query"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanReturnsRecordsInArchiveOrderAndCountsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10",
		recordLine("2026-07-10", "t/a", "one"),
		"not json",
		recordLine("2026-07-10", "t/b", "two"),
	)

	res, err := query.Scan(dir, query.ScanRequest{}, clockAt("2026-07-10"))

	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, payloadsOf(res.Records))
	assert.Equal(t, 1, res.InvalidLines)
	assert.False(t, res.HasMore)
}

func TestScanPaginatesWithCursor(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "one", "two", "three", "four", "five")...)
	clock := clockAt("2026-07-10")

	var got []string
	req := query.ScanRequest{Limit: 2}
	for page := 0; page < 3; page++ {
		res, err := query.Scan(dir, req, clock)
		require.NoError(t, err)
		got = append(got, payloadsOf(res.Records)...)
		req.Cursor = res.Next
	}

	assert.Equal(t, []string{"one", "two", "three", "four", "five"}, got)
}

func TestScanFiltersByTopicAndTimeRange(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10",
		record("2026-07-10T05:00:00Z", "home/kitchen/temp", "too early"),
		record("2026-07-10T10:00:00Z", "home/kitchen/temp", "match"),
		record("2026-07-10T11:00:00Z", "home/kitchen/humidity", "wrong topic"),
		record("2026-07-10T20:00:00Z", "home/kitchen/temp", "too late"),
	)

	res, err := query.Scan(dir, query.ScanRequest{
		From:  time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC),
		To:    time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		Topic: "home/+/temp",
	}, clockAt("2026-07-10"))

	require.NoError(t, err)
	assert.Equal(t, []string{"match"}, payloadsOf(res.Records))
}

// TestCursorSurvivesCompression asserts the core cursor contract: a cursor
// taken while a day is plain stays valid after the sweep replaces the file
// with its byte-identical .zst.
func TestCursorSurvivesCompression(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "one", "two", "three", "four")...)
	first, err := query.Scan(dir, query.ScanRequest{Limit: 2}, clockAt("2026-07-10"))
	require.NoError(t, err)

	compressDay(t, dir, "2026-07-11")
	rest, err := query.Scan(dir, query.ScanRequest{Cursor: first.Next}, clockAt("2026-07-11"))

	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, payloadsOf(first.Records))
	assert.Equal(t, []string{"three", "four"}, payloadsOf(rest.Records))
}

// TestTailAcrossRotation asserts a poll loop keeps working while the archive
// rotates to a new day and the polled day gets compressed away: the exhausted
// day rolls the cursor forward with HasMore, the next poll delivers.
func TestTailAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "before tail")...)
	cursor, err := query.TailStart(dir, clockAt("2026-07-10"))
	require.NoError(t, err)

	appendDay(t, dir, "2026-07-10", recordLines("2026-07-10", "live one")...)
	poll1, err := query.Scan(dir, query.ScanRequest{Cursor: cursor}, clockAt("2026-07-10"))
	require.NoError(t, err)

	writeDay(t, dir, "2026-07-11", recordLines("2026-07-11", "next day")...)
	compressDay(t, dir, "2026-07-11")
	rolled, err := query.Scan(dir, query.ScanRequest{Cursor: poll1.Next}, clockAt("2026-07-11"))
	require.NoError(t, err)
	poll2, err := query.Scan(dir, query.ScanRequest{Cursor: rolled.Next}, clockAt("2026-07-11"))
	require.NoError(t, err)

	assert.Equal(t, []string{"live one"}, payloadsOf(poll1.Records))
	assert.Empty(t, rolled.Records, "exhausted day only rolls the cursor")
	assert.True(t, rolled.HasMore, "roll signals another poll makes progress")
	assert.Equal(t, "2026-07-11", rolled.Next.Date, "cursor rolled to the new day")
	assert.Equal(t, []string{"next day"}, payloadsOf(poll2.Records))
}

// TestScanReadsAtMostOneDayPerCall asserts the bounded-work contract: a call
// never decodes more than one day file; later days are reached via HasMore.
func TestScanReadsAtMostOneDayPerCall(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-09", recordLines("2026-07-09", "day one")...)
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "day two")...)
	clock := clockAt("2026-07-10")

	first, err := query.Scan(dir, query.ScanRequest{}, clock)
	require.NoError(t, err)
	second, err := query.Scan(dir, query.ScanRequest{Cursor: first.Next}, clock)
	require.NoError(t, err)

	assert.Equal(t, []string{"day one"}, payloadsOf(first.Records))
	assert.True(t, first.HasMore)
	assert.Equal(t, query.Cursor{Date: "2026-07-10"}, first.Next)
	assert.Equal(t, []string{"day two"}, payloadsOf(second.Records))
	assert.False(t, second.HasMore)
}

func TestTornLineIsWithheldUntilTerminated(t *testing.T) {
	dir := t.TempDir()
	full := record("2026-07-10T10:00:00Z", "t", "torn")
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "complete")...)
	appendRaw(t, dir, "2026-07-10", full[:len(full)/2])

	withheld, err := query.Scan(dir, query.ScanRequest{}, clockAt("2026-07-10"))
	require.NoError(t, err)
	appendRaw(t, dir, "2026-07-10", full[len(full)/2:]+"\n")
	completed, err := query.Scan(dir, query.ScanRequest{Cursor: withheld.Next}, clockAt("2026-07-10"))
	require.NoError(t, err)

	assert.Equal(t, []string{"complete"}, payloadsOf(withheld.Records))
	assert.Equal(t, []string{"torn"}, payloadsOf(completed.Records), "torn line emitted exactly once, after termination")
}

func TestScanPrefersPlainOverUntrustedZst(t *testing.T) {
	dir := t.TempDir()
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "from plain")...)
	writeZst(t, dir, "2026-07-10", recordLine("2026-07-10", "t", "from untrusted zst")+"\n")

	res, err := query.Scan(dir, query.ScanRequest{}, clockAt("2026-07-11"))

	require.NoError(t, err)
	assert.Equal(t, []string{"from plain"}, payloadsOf(res.Records))
}

func TestTailStartOnEmptyArchiveSeesFirstData(t *testing.T) {
	dir := t.TempDir()
	clock := clockAt("2026-07-10")

	cursor, err := query.TailStart(dir, clock)
	require.NoError(t, err)
	empty, err := query.Scan(dir, query.ScanRequest{Cursor: cursor}, clock)
	require.NoError(t, err)
	writeDay(t, dir, "2026-07-10", recordLines("2026-07-10", "first")...)
	first, err := query.Scan(dir, query.ScanRequest{Cursor: empty.Next}, clock)
	require.NoError(t, err)

	assert.Empty(t, empty.Records)
	assert.Equal(t, []string{"first"}, payloadsOf(first.Records))
}

func record(ts, topic, payload string) string {
	return fmt.Sprintf(`{"ts":%q,"topic":%q,"payload":%q}`, ts, topic, payload)
}

// recordLine builds a record at noon UTC of the given day.
func recordLine(date, topic, payload string) string {
	return record(date+"T12:00:00Z", topic, payload)
}

// recordLines builds records on the given day, one second apart, topic "t".
func recordLines(date string, payloads ...string) []string {
	lines := make([]string, len(payloads))
	for i, p := range payloads {
		lines[i] = record(fmt.Sprintf("%sT12:00:%02dZ", date, i), "t", p)
	}
	return lines
}

func writeDay(t *testing.T, dir, date string, lines ...string) {
	t.Helper()
	writeFile(t, dir, date+".ndjson", joinLines(lines))
}

func appendDay(t *testing.T, dir, date string, lines ...string) {
	t.Helper()
	appendRaw(t, dir, date, joinLines(lines))
}

func appendRaw(t *testing.T, dir, date, raw string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, date+".ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(raw)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func joinLines(lines []string) string {
	var s string
	for _, l := range lines {
		s += l + "\n"
	}
	return s
}

// compressDay runs the real sweeper so fixtures match production archives.
func compressDay(t *testing.T, dir, today string) {
	t.Helper()
	compress.NewSweeper(dir, 3, slog.New(slog.NewTextHandler(io.Discard, nil))).Sweep(today)
	plain, err := filepath.Glob(filepath.Join(dir, "*.ndjson"))
	require.NoError(t, err)
	for _, p := range plain {
		require.GreaterOrEqual(t, filepath.Base(p), today, "sweep must have compressed and removed closed days")
	}
}

// writeZst plants a divergent .zst next to a plain file (untrusted state).
func writeZst(t *testing.T, dir, date, content string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, date+".ndjson.zst"))
	require.NoError(t, err)
	enc, err := zstd.NewWriter(f)
	require.NoError(t, err)
	_, err = enc.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	require.NoError(t, f.Close())
}

func payloadsOf(records []query.Record) []string {
	var out []string
	for _, r := range records {
		if r.Payload == nil {
			out = append(out, "<no payload>")
			continue
		}
		out = append(out, *r.Payload)
	}
	return out
}
