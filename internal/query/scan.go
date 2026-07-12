package query

import (
	"encoding/json"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
)

// Record mirrors the archived record (docs/spec/archival.md): exactly one of
// Payload/PayloadB64 is set.
type Record struct {
	TS         string  `json:"ts"`
	Topic      string  `json:"topic"`
	Payload    *string `json:"payload,omitempty"`
	PayloadB64 *string `json:"payload_b64,omitempty"`
}

type ScanRequest struct {
	From, To time.Time // zero value = unbounded
	Topic    string    // MQTT topic filter; "" = "#"
	Cursor   Cursor    // zero value = start at From's day (or the first archived day)
	Limit    int       // max records returned; <= 0 = unlimited
}

type ScanResult struct {
	Records []Record
	// Next continues the scan; with HasMore=false it points at the current
	// end of the scanned data, so polling it acts as a tail.
	Next Cursor
	// HasMore reports that another call with Next makes progress: more
	// records in the day, or the cursor rolled to a newer day.
	HasMore      bool
	InvalidLines int
}

// Scan reads at most one day file per call (docs/spec/mcp.md, bounded work):
// the cursor's day — or From's day, or the first archived day — filtered by
// [From, To] and the topic filter. Once the day is exhausted, Next rolls to
// the next existing day within the range with HasMore set.
func Scan(dir string, req ScanRequest, now func() time.Time) (ScanResult, error) {
	days, err := ListDays(dir, now)
	if err != nil {
		return ScanResult{}, err
	}
	filter := req.Topic
	if filter == "" {
		filter = "#"
	}

	res := ScanResult{Next: req.Cursor}
	if res.Next.Date == "" {
		res.Next = Cursor{Date: startDate(req, days, now)}
	}

	done, err := scanDay(dir, res.Next, filter, req, &res)
	if err != nil {
		return ScanResult{}, err
	}
	if done {
		res.HasMore = true
		return res, nil
	}

	for _, day := range days {
		if day.Date > res.Next.Date && !afterDay(req.To, day.Date) {
			res.Next = Cursor{Date: day.Date}
			res.HasMore = true
			break
		}
	}
	return res, nil
}

func startDate(req ScanRequest, days []DayInfo, now func() time.Time) string {
	if !req.From.IsZero() {
		return req.From.UTC().Format(archive.DateFormat)
	}
	if len(days) > 0 {
		return days[0].Date
	}
	return today(now)
}

// scanDay consumes one day from at; done reports that the limit was hit.
func scanDay(dir string, at Cursor, filter string, req ScanRequest, res *ScanResult) (bool, error) {
	r, err := openDayFrom(dir, at)
	if err != nil {
		return false, err
	}
	if r == nil {
		return false, nil // a day without messages has no file
	}
	defer r.close()

	for {
		line, ok, err := r.next()
		if err != nil {
			return false, err
		}
		if !ok {
			res.Next = r.pos
			return false, nil
		}
		res.Next = r.pos
		rec, ts, valid := parseRecord(line)
		if !valid {
			res.InvalidLines++
			continue
		}
		if (!req.From.IsZero() && ts.Before(req.From)) || (!req.To.IsZero() && ts.After(req.To)) {
			continue
		}
		if !MatchTopic(filter, rec.Topic) {
			continue
		}
		res.Records = append(res.Records, rec)
		if req.Limit > 0 && len(res.Records) == req.Limit {
			return true, nil
		}
	}
}

// TailStart returns a cursor at the current end of the archive, so a
// subsequent Scan returns only lines appended afterwards.
func TailStart(dir string, now func() time.Time) (Cursor, error) {
	days, err := ListDays(dir, now)
	if err != nil {
		return Cursor{}, err
	}
	if len(days) == 0 {
		return Cursor{Date: today(now)}, nil
	}

	last := Cursor{Date: days[len(days)-1].Date}
	r, err := openDayFrom(dir, last)
	if err != nil {
		return Cursor{}, err
	}
	if r == nil {
		return last, nil
	}
	defer r.close()
	for {
		_, ok, err := r.next()
		if err != nil {
			return Cursor{}, err
		}
		if !ok {
			return r.pos, nil
		}
	}
}

func parseRecord(line []byte) (Record, time.Time, bool) {
	var r Record
	if json.Unmarshal(line, &r) != nil || r.Topic == "" {
		return Record{}, time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, r.TS)
	if err != nil {
		return Record{}, time.Time{}, false
	}
	return r, ts, true
}

func afterDay(to time.Time, date string) bool {
	return !to.IsZero() && date > to.UTC().Format(archive.DateFormat)
}
