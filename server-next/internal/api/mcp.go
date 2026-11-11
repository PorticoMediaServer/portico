package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mcp"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// The assistant (MCP) address. An assistant connects with a key, while the owner has
// "Assistants" on, and gets a short list of tools: find and read titles, see what is playing,
// mark watched, and, with an administrator key, read the server's status. Every tool is an
// ordinary API call made as the key, so it sees only what the key's profile may see. There is
// no tool that deletes, changes settings, people, keys or webhooks, stops someone's stream or
// touches backups, whatever the key may do on the ordinary API: an assistant that misreads a
// request must not be able to remove or reconfigure anything.

// MCPReply is the JSON-RPC answer, or nothing (202) for a notification.
type MCPReply struct{ raw json.RawMessage }

func (m MCPReply) MarshalJSON() ([]byte, error) {
	if m.raw == nil {
		return []byte("{}"), nil
	}
	return m.raw, nil
}

func (m MCPReply) reply(http.Header) int {
	if m.raw == nil {
		return http.StatusAccepted
	}
	return 0
}

func (s *Server) mcpRoutes() {
	Handle(s, "POST /v1/mcp", SignedIn, s.serveMCP).Scope = ScopeRead
}

func (s *Server) serveMCP(r *http.Request, body JSON) (MCPReply, error) {
	c := CallerFrom(r.Context())
	if !s.integrations.get().Assistants {
		return MCPReply{}, ErrNotFound()
	}
	if !c.Key {
		return MCPReply{}, refusal(http.StatusForbidden, "key_required", "Give the assistant an API key or a personal token.", false)
	}
	return MCPReply{mcp.Serve(r.Context(), body, s.serverName(), s.mcpTools(r, c))}, nil
}

// mcpTools are the tools this caller may use.
func (s *Server) mcpTools(r *http.Request, c Caller) []mcp.Tool {
	get := func(path string, query url.Values, out any) error { return s.callAs(r, c, "GET", path, query, out) }
	args := func(raw json.RawMessage, out any) error {
		if err := json.Unmarshal(raw, out); err != nil {
			return errors.New("the arguments do not fit this tool")
		}
		return nil
	}
	tools := []mcp.Tool{
		{Name: "list_libraries", Description: "List the libraries this key can see, with their ids and kinds.",
			InputSchema: mcp.Schema(nil, nil),
			Run: func(context.Context, json.RawMessage) (any, error) {
				var libs Libraries
				err := get("/v1/libraries", nil, &libs)
				out := []map[string]string{}
				for _, l := range libs.Items {
					out = append(out, map[string]string{"id": l.ID, "name": l.Name, "kind": l.Kind})
				}
				return map[string]any{"libraries": out}, toolError(err)
			}},
		{Name: "list_titles", Description: "List the titles of a library, 50 at a time, newest added first.",
			InputSchema: mcp.Schema([]string{"library"}, map[string]string{"library": "The library's id, from list_libraries.",
				"kind": "Only this kind: movie, show, episode, artist, album, track, book or audiobook.", "cursor": "From the previous answer, for the next 50.",
				"unwatched": "(boolean) Only titles not yet watched."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Library, Kind, Cursor string
					Unwatched             bool
				}
				if err := args(raw, &a); err != nil {
					return nil, err
				}
				q := url.Values{"library": {a.Library}, "limit": {"50"}, "sort": {"added"}, "order": {"desc"}}
				return s.mcpList(get, q, a.Kind, a.Cursor, a.Unwatched)
			}},
		{Name: "list_children", Description: "List what is inside a title: a show's seasons, a season's episodes, an album's tracks.",
			InputSchema: mcp.Schema([]string{"id"}, map[string]string{"id": "The title's id.", "cursor": "From the previous answer, for the next 50."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a struct{ ID, Cursor string }
				if err := args(raw, &a); err != nil {
					return nil, err
				}
				return s.mcpList(get, url.Values{"parent": {a.ID}, "limit": {"50"}}, "", a.Cursor, false)
			}},
		{Name: "search_titles", Description: "Find titles by name, as the server's own search does: films, shows, episodes, artists, albums, tracks, books and audiobooks, best match first.",
			InputSchema: mcp.Schema([]string{"query"}, map[string]string{"query": "What to look for.",
				"kind":   "Only this kind: movie, show, episode, artist, album, track, book or audiobook. With a kind, 20 at a time.",
				"cursor": "From the previous answer, with the same query and kind, for the next 20."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Query, Kind, Cursor string }
				if err := args(raw, &a); err != nil {
					return nil, err
				}
				q := url.Values{"q": {a.Query}, "limit": {"20"}}
				if a.Kind != "" {
					q.Set("kind", a.Kind)
				}
				if a.Cursor != "" {
					q.Set("cursor", a.Cursor)
				}
				var found SearchResults
				if err := get("/v1/search", q, &found); err != nil {
					return nil, toolError(err)
				}
				out, next := []map[string]any{}, ""
				for _, g := range found.Groups {
					for _, it := range g.Items {
						title := mcpTitle(it)
						if it.Parent != nil {
							title["in"] = it.Parent.Title
						}
						if it.Root != nil {
							title["show"] = it.Root.Title
						}
						out = append(out, title)
					}
					if a.Kind != "" {
						next = g.NextCursor
					}
				}
				return map[string]any{"titles": out, "nextCursor": next}, nil
			}},
		{Name: "get_title", Description: "Read one title: its summary, year, length, where it sits and whether it was watched.",
			InputSchema: mcp.Schema([]string{"id"}, map[string]string{"id": "The title's id."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a struct{ ID string }
				if err := args(raw, &a); err != nil {
					return nil, err
				}
				var it Item
				if err := get("/v1/items/"+url.PathEscape(a.ID), nil, &it); err != nil {
					return nil, toolError(err)
				}
				out := mcpTitle(it)
				out["summary"], out["externalIds"] = it.Summary, it.ExternalIDs
				if it.Parent != nil {
					out["in"] = it.Parent.Title
				}
				if it.Root != nil {
					out["show"] = it.Root.Title
				}
				return out, nil
			}},
		{Name: "now_playing", Description: "What is playing now on this account; with an administrator key, on the whole server.",
			InputSchema: mcp.Schema(nil, nil),
			Run: func(context.Context, json.RawMessage) (any, error) {
				sessions, err := s.streamsSeenBy(r.Context(), db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, compatSeesEveryone(c))
				out := []map[string]any{}
				for _, p := range sessions {
					if p.State == playback.StateEnded {
						continue
					}
					stream := map[string]any{"device": p.Device.Name, "state": p.State, "positionMs": p.PositionMs}
					// A title the key's profile may not see is left out; the stream is still listed.
					if p.Visible {
						stream["title"], stream["titleId"], stream["durationMs"] = p.Title, p.Item.PublicID, p.Item.DurationMS
					}
					out = append(out, stream)
				}
				return map[string]any{"playing": out}, err
			}},
		{Name: "mark_watched", Description: "Mark a title watched or not watched for this key's profile.",
			InputSchema: mcp.Schema([]string{"id", "watched"}, map[string]string{"id": "The title's id.", "watched": "(boolean) true for watched, false for not watched."}),
			Run: func(_ context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					ID      string
					Watched *bool
				}
				if err := args(raw, &a); err != nil || a.Watched == nil {
					return nil, errors.New("give the title's id and watched true or false")
				}
				err := s.callAsWith(r, c, "PUT", "/v1/items/"+url.PathEscape(a.ID)+"/state", nil, map[string]bool{"watched": *a.Watched}, nil)
				return map[string]any{"id": a.ID, "watched": *a.Watched}, toolError(err)
			}},
	}
	if compatSeesEveryone(c) {
		tools = append(tools, mcp.Tool{Name: "server_status", Description: "The server's name, version and how many streams are playing.",
			InputSchema: mcp.Schema(nil, nil),
			Run: func(context.Context, json.RawMessage) (any, error) {
				var sys System
				err := get("/v1/system", nil, &sys)
				playing := 0
				for _, p := range s.playback.All() {
					if p.State != playback.StateEnded {
						playing++
					}
				}
				return map[string]any{"name": sys.Name, "version": sys.Version, "streams": playing}, toolError(err)
			}})
	}
	return tools
}

func (s *Server) mcpList(get func(string, url.Values, any) error, q url.Values, kind, cursor string, unwatched bool) (any, error) {
	if kind != "" {
		q.Set("kind", kind)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if unwatched {
		q.Set("watched", "false")
	}
	var page ItemPage
	if err := get("/v1/items", q, &page); err != nil {
		return nil, toolError(err)
	}
	out := []map[string]any{}
	for _, it := range page.Items {
		out = append(out, mcpTitle(it))
	}
	return map[string]any{"titles": out, "nextCursor": page.NextCursor}, nil
}

func mcpTitle(it Item) map[string]any {
	out := map[string]any{"id": it.ID, "kind": it.Kind, "title": it.Title, "watched": it.State.Watched}
	if it.Year != 0 {
		out["year"] = it.Year
	}
	if it.DurationMs != 0 {
		out["durationMs"] = it.DurationMs
	}
	if it.Index != nil {
		out["number"] = *it.Index
	}
	return out
}

// toolError says in a sentence why a tool's API call was refused.
func toolError(err error) error {
	if e, ok := asError(err); ok {
		return errors.New(e.Message)
	}
	return err
}
