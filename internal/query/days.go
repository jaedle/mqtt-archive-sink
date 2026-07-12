package query

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
)

// Directory states per the sweep contract (docs/spec/compression.md), plus
// "active" for today's plain file.
const (
	StateActive    = "active"
	StatePending   = "pending"
	StateUntrusted = "untrusted"
	StateFinal     = "final"
)

// DayInfo describes one archived day. SizeBytes is the on-disk size of the
// file reads would come from (the plain file whenever it exists).
type DayInfo struct {
	Date       string `json:"date"`
	State      string `json:"state"`
	SizeBytes  int64  `json:"size_bytes"`
	Compressed bool   `json:"compressed"`
}

// ListDays returns every archived day in date order, classified by state.
func ListDays(dir string, now func() time.Time) ([]DayInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	plain := map[string]int64{}
	zst := map[string]int64{}
	for _, e := range entries {
		name := e.Name()
		var date string
		var sizes map[string]int64
		switch {
		case strings.HasSuffix(name, ".ndjson"):
			date, sizes = strings.TrimSuffix(name, ".ndjson"), plain
		case strings.HasSuffix(name, ".ndjson.zst"):
			date, sizes = strings.TrimSuffix(name, ".ndjson.zst"), zst
		default:
			continue
		}
		if _, err := time.Parse(archive.DateFormat, date); err != nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		sizes[date] = fi.Size()
	}

	today := today(now)
	var days []DayInfo
	for date, size := range plain {
		day := DayInfo{Date: date, State: StatePending, SizeBytes: size}
		if _, hasZst := zst[date]; hasZst {
			day.State = StateUntrusted
		} else if date == today {
			day.State = StateActive
		}
		days = append(days, day)
	}
	for date, size := range zst {
		if _, ok := plain[date]; ok {
			continue
		}
		days = append(days, DayInfo{Date: date, State: StateFinal, SizeBytes: size, Compressed: true})
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return days, nil
}

func today(now func() time.Time) string {
	return now().UTC().Format(archive.DateFormat)
}
