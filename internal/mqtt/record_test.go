package mqtt_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/mqtt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ts = time.Date(2026, 3, 1, 12, 30, 45, 123456789, time.UTC)

func TestPayloadWithNewlinesStaysASingleLine(t *testing.T) {
	line, err := mqtt.EncodeRecord(ts, "t", []byte("multi\nline\r\npayload"))
	require.NoError(t, err)

	assert.NotContains(t, string(line), "\n", "record must never break the NDJSON framing")

	var decoded struct {
		Payload string `json:"payload"`
	}
	require.NoError(t, json.Unmarshal(line, &decoded))
	assert.Equal(t, "multi\nline\r\npayload", decoded.Payload, "round-trip must be lossless")
}

func TestEmptyPayloadIsPresentAsUTF8(t *testing.T) {
	line, err := mqtt.EncodeRecord(ts, "t", nil)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(line, &decoded))
	assert.Contains(t, decoded, "payload")
	assert.NotContains(t, decoded, "payload_b64", "exactly one payload field")
	assert.Equal(t, "", decoded["payload"])
}

func TestTimestampIsUTCRFC3339Nano(t *testing.T) {
	line, err := mqtt.EncodeRecord(ts.In(time.FixedZone("CET", 3600)), "t", []byte("x"))
	require.NoError(t, err)

	var decoded struct {
		TS string `json:"ts"`
	}
	require.NoError(t, json.Unmarshal(line, &decoded))
	assert.Equal(t, "2026-03-01T12:30:45.123456789Z", decoded.TS)
	assert.False(t, bytes.Contains(line, []byte("+01:00")), "timestamps are normalized to UTC")
}
