// Package query is the read engine over the archive directory: topic
// matching, cursors, day listing, and cursor-resumable scanning. It knows
// nothing about MCP or HTTP (docs/spec/mcp.md).
package query

import "strings"

// MatchTopic reports whether an MQTT topic filter matches topic: '+' matches
// exactly one level, a trailing '#' matches any remaining levels including
// the parent level itself.
func MatchTopic(filter, topic string) bool {
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	for i := 0; ; i++ {
		switch {
		case i == len(f):
			return i == len(t)
		case f[i] == "#":
			return true
		case i == len(t):
			return false
		case f[i] != "+" && f[i] != t[i]:
			return false
		}
	}
}
