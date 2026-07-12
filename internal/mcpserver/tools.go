package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/jaedle/mqtt-archive-sink/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultLimit = 100
	maxLimit     = 1000
)

type listDaysOutput struct {
	Days []query.DayInfo `json:"days"`
}

type queryInput struct {
	From   string `json:"from,omitempty" jsonschema:"start of the time range, RFC3339 (optional)"`
	To     string `json:"to,omitempty" jsonschema:"end of the time range, RFC3339 (optional)"`
	Topic  string `json:"topic,omitempty" jsonschema:"MQTT topic filter with + and # wildcards (default #)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max records per call, 1-1000 (default 100)"`
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque continuation cursor from a previous response"`
}

type tailInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"cursor from the previous tail response; omit to start at the current end of the archive"`
	Topic  string `json:"topic,omitempty" jsonschema:"MQTT topic filter with + and # wildcards (default #)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max records per call, 1-1000 (default 100)"`
}

type scanOutput struct {
	Records      []query.Record `json:"records"`
	NextCursor   string         `json:"next_cursor"`
	HasMore      bool           `json:"has_more"`
	InvalidLines int            `json:"invalid_lines"`
}

func newMCPServer(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mqtt-archive", Version: "1"}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_days",
		Description: "List every archived day with its state (active/pending/untrusted/final) and on-disk size.",
	}, listDaysTool(cfg))
	mcp.AddTool(s, &mcp.Tool{
		Name: "query",
		Description: "Query archived MQTT messages by time range and topic filter, oldest first. " +
			"Pass next_cursor from the previous response to continue; repeat the other parameters unchanged on continuation calls.",
	}, queryTool(cfg))
	mcp.AddTool(s, &mcp.Tool{
		Name: "tail",
		Description: "Poll for newly archived MQTT messages. The first call (without cursor) returns no records and a " +
			"cursor at the current end of the archive; keep calling with the returned cursor to receive new messages.",
	}, tailTool(cfg))
	return s
}

func listDaysTool(cfg Config) mcp.ToolHandlerFor[struct{}, listDaysOutput] {
	return func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, listDaysOutput, error) {
		days, err := query.ListDays(cfg.ArchiveDir, cfg.Now)
		if err != nil {
			return nil, listDaysOutput{}, err
		}
		if days == nil {
			days = []query.DayInfo{}
		}
		return nil, listDaysOutput{Days: days}, nil
	}
}

func queryTool(cfg Config) mcp.ToolHandlerFor[queryInput, scanOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, scanOutput, error) {
		req := query.ScanRequest{Topic: in.Topic, Limit: clampLimit(in.Limit)}
		var err error
		if req.From, err = parseTime("from", in.From); err != nil {
			return nil, scanOutput{}, err
		}
		if req.To, err = parseTime("to", in.To); err != nil {
			return nil, scanOutput{}, err
		}
		if in.Cursor != "" {
			if req.Cursor, err = query.DecodeCursor(in.Cursor); err != nil {
				return nil, scanOutput{}, err
			}
		}
		res, err := query.Scan(cfg.ArchiveDir, req, cfg.Now)
		if err != nil {
			return nil, scanOutput{}, err
		}
		return nil, toScanOutput(res), nil
	}
}

func tailTool(cfg Config) mcp.ToolHandlerFor[tailInput, scanOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in tailInput) (*mcp.CallToolResult, scanOutput, error) {
		if in.Cursor == "" {
			start, err := query.TailStart(cfg.ArchiveDir, cfg.Now)
			if err != nil {
				return nil, scanOutput{}, err
			}
			return nil, scanOutput{Records: []query.Record{}, NextCursor: query.EncodeCursor(start)}, nil
		}
		cursor, err := query.DecodeCursor(in.Cursor)
		if err != nil {
			return nil, scanOutput{}, err
		}
		res, err := query.Scan(cfg.ArchiveDir, query.ScanRequest{
			Topic:  in.Topic,
			Cursor: cursor,
			Limit:  clampLimit(in.Limit),
		}, cfg.Now)
		if err != nil {
			return nil, scanOutput{}, err
		}
		return nil, toScanOutput(res), nil
	}
}

func toScanOutput(res query.ScanResult) scanOutput {
	records := res.Records
	if records == nil {
		records = []query.Record{}
	}
	return scanOutput{
		Records:      records,
		NextCursor:   query.EncodeCursor(res.Next),
		HasMore:      res.HasMore,
		InvalidLines: res.InvalidLines,
	}
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultLimit
	case limit > maxLimit:
		return maxLimit
	default:
		return limit
	}
}

func parseTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: %w", field, err)
	}
	return t, nil
}
