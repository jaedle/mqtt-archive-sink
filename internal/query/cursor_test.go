package query_test

import (
	"encoding/base64"
	"testing"

	"github.com/jaedle/mqtt-archive-sink/internal/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	in := query.Cursor{Date: "2026-07-11", Line: 12345, Off: 987654}

	out, err := query.DecodeCursor(query.EncodeCursor(in))

	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestDecodeCursorRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":               "",
		"not base64":          "%%%",
		"not json":            b64("nope"),
		"unsupported version": b64(`{"v":2,"d":"2026-07-11"}`),
		"bad date":            b64(`{"v":1,"d":"nope"}`),
		"negative line":       b64(`{"v":1,"d":"2026-07-11","l":-1}`),
	}

	for name, cursor := range cases {
		_, err := query.DecodeCursor(cursor)

		assert.Errorf(t, err, "case %q (input %q)", name, cursor)
	}
}

func b64(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
