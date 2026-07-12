package mqtt

import (
	"encoding/base64"
	"encoding/json"
	"time"
	"unicode/utf8"
)

// EncodeRecord serializes one archived message as a single NDJSON line
// (without trailing newline). See SPEC.md "File formats / Record".
func EncodeRecord(ts time.Time, topic string, payload []byte) ([]byte, error) {
	r := struct {
		TS         string  `json:"ts"`
		Topic      string  `json:"topic"`
		Payload    *string `json:"payload,omitempty"`
		PayloadB64 *string `json:"payload_b64,omitempty"`
	}{
		TS:    ts.UTC().Format(time.RFC3339Nano),
		Topic: topic,
	}
	if utf8.Valid(payload) {
		p := string(payload)
		r.Payload = &p
	} else {
		p := base64.StdEncoding.EncodeToString(payload)
		r.PayloadB64 = &p
	}
	return json.Marshal(r)
}
