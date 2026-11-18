package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PorticoMediaServer/portico/server-next/internal/mcp"
)

// mcpMoreTools are the rest of what outside-tools rule 34 gives an assistant: start a scan,
// list unmatched titles, read the guide and the recordings, and with Administer the storage and
// the recent messages. Each calls the ordinary route as the key, so the key's scopes and its
// person's access decide, and each answers that route's own reply.
func (s *Server) mcpMoreTools(r *http.Request, c Caller, get func(string, url.Values, any) error, args func(json.RawMessage, any) error) []mcp.Tool {
	read := func(path string, query url.Values) (any, error) {
		var out json.RawMessage
		err := get(path, query, &out)
		return out, toolError(err)
	}
	library := func(raw json.RawMessage) (string, error) {
		var a struct{ Library string }
		if err := args(raw, &a); err != nil || a.Library == "" {
			return "", errors.New("give the library's id, from list_libraries")
		}
		return url.PathEscape(a.Library), nil
	}
	tools := []mcp.Tool{
		{Name: "start_scan", Description: "Scan a library for new, changed and removed files, as Scan now does. Needs a key with Library.",
			InputSchema: mcp.Schema([]string{"library"}, map[string]string{"library": "The library's id, from list_libraries."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				id, err := library(raw)
				if err != nil {
					return nil, err
				}
				var job Job
				err = s.callAsWith(r, c, "POST", "/v1/admin/libraries/"+id+"/scan", nil, ScanRequest{}, &job)
				return map[string]any{"library": id, "job": job.ID, "state": job.State}, toolError(err)
			}},
		{Name: "list_unmatched", Description: "The titles of a library no provider has matched, with why and the closest matches.",
			InputSchema: mcp.Schema([]string{"library"}, map[string]string{"library": "The library's id, from list_libraries.", "cursor": "From the previous answer."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				id, err := library(raw)
				if err != nil {
					return nil, err
				}
				var a struct{ Cursor string }
				_ = args(raw, &a)
				q := url.Values{"limit": {"20"}}
				if a.Cursor != "" {
					q.Set("cursor", a.Cursor)
				}
				return read("/v1/admin/libraries/"+id+"/unmatched", q)
			}},
		{Name: "tv_guide", Description: "What is on the Live TV channels this key's profile may watch, from now for three hours, 20 channels at a time.",
			InputSchema: mcp.Schema(nil, map[string]string{"cursor": "From the previous answer, for the next channels."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Cursor string }
				_ = args(raw, &a)
				q := url.Values{}
				if a.Cursor != "" {
					q.Set("cursor", a.Cursor)
				}
				return read("/v1/live/guide", q)
			}},
		{Name: "list_recordings", Description: "Recordings coming up (clashes first) and those made, for this key's profile.",
			InputSchema: mcp.Schema(nil, nil),
			Run:         func(context.Context, json.RawMessage) (any, error) { return read("/v1/dvr/recordings", nil) }},
	}
	if compatSeesEveryone(c) {
		tools = append(tools,
			mcp.Tool{Name: "server_storage", Description: "The data folder's disk: its size, free space and what uses it. Owner only.",
				InputSchema: mcp.Schema(nil, nil),
				Run:         func(context.Context, json.RawMessage) (any, error) { return read("/v1/admin/storage", nil) }},
			mcp.Tool{Name: "recent_messages", Description: "The server's recent messages (its log), newest first.",
				InputSchema: mcp.Schema(nil, nil),
				Run: func(context.Context, json.RawMessage) (any, error) {
					return read("/v1/admin/logs", url.Values{"limit": {"50"}})
				}})
	}
	return tools
}
