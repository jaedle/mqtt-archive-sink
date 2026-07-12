package query

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/archive"
)

// Cursor addresses the next unread line as a day plus the count of complete
// lines already consumed in that day. Line indexes stay valid across append
// (files never truncate), rotation (a new day starts at 0), and compression
// (the .zst decodes byte-identical to the plain file). Off optionally carries
// the byte offset after those lines in the plain file — a read optimization,
// discarded whenever it no longer fits the file on disk.
type Cursor struct {
	Date string `json:"d"`
	Line int64  `json:"l"`
	Off  int64  `json:"o,omitempty"`
}

const cursorVersion = 1

type cursorEnvelope struct {
	Version int `json:"v"`
	Cursor
}

// EncodeCursor renders c opaque for clients (versioned base64url JSON).
func EncodeCursor(c Cursor) string {
	b, err := json.Marshal(cursorEnvelope{Version: cursorVersion, Cursor: c})
	if err != nil {
		panic(err) // a plain struct of strings and ints cannot fail to marshal
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor: %w", err)
	}
	var e cursorEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return Cursor{}, fmt.Errorf("cursor: %w", err)
	}
	if e.Version != cursorVersion {
		return Cursor{}, fmt.Errorf("cursor: unsupported version %d", e.Version)
	}
	if _, err := time.Parse(archive.DateFormat, e.Date); err != nil {
		return Cursor{}, fmt.Errorf("cursor: %w", err)
	}
	if e.Line < 0 || e.Off < 0 {
		return Cursor{}, fmt.Errorf("cursor: negative position")
	}
	return e.Cursor, nil
}
