package query_test

import (
	"testing"

	"github.com/jaedle/mqtt-archive-sink/internal/query"
	"github.com/stretchr/testify/assert"
)

func TestMatchTopic(t *testing.T) {
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"#", "a", true},
		{"#", "a/b/c", true},
		{"a/b", "a/b", true},
		{"a/b", "a/c", false},
		{"a/b", "a/b/c", false},
		{"a/b/c", "a/b", false},
		{"a/+", "a/b", true},
		{"a/+", "a/b/c", false},
		{"a/+/c", "a/b/c", true},
		{"+", "a/b", false},
		{"a/#", "a", true}, // '#' also matches the parent level (MQTT spec)
		{"a/#", "a/b/c", true},
		{"a/#", "b", false},
	}

	for _, c := range cases {
		assert.Equalf(t, c.want, query.MatchTopic(c.filter, c.topic), "filter %q topic %q", c.filter, c.topic)
	}
}
