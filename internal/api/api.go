// Package api holds the data shapes of the daemon's HTTP API v1 (contracts/api-v1.md): the objects the task engine
// returns, the requests it takes and the events of a task's log. The HTTP server, the MCP server and the dashboard
// (through the generated web/src/api/types.gen.ts) share them, so field names here are the wire names.
//
// Times are RFC 3339 UTC strings with milliseconds (FormatTime); optional scalars are omitted when empty, optional
// objects are null, and lists are never null.
//
// After changing a shape, run `go generate ./internal/api` (internal/api/tsgen); a test fails while the committed
// TypeScript file is stale.
package api

//go:generate go run ./tsgen

import "time"

// timeLayout is RFC 3339 in UTC with milliseconds, as the contract requires.
const timeLayout = "2006-01-02T15:04:05.000Z"

// FormatTime returns t in the API's time format, or "" for the zero time.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

// ParseTime reads a time written by FormatTime (or any RFC 3339 time).
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// NonNil returns list, or an empty list when it is nil, so it marshals as [] (lists are never null).
func NonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
