package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The built-in MCP server.
//
// It speaks the Model Context Protocol's Streamable HTTP transport at one path,
// POST /v1/mcp: each request body is a JSON-RPC 2.0 message and each answer is
// one JSON document. The server never starts a stream of its own, so GET is
// answered 405, as the transport allows.
//
// It holds no authority of its own and reaches no service directly. A tool is a
// description of one or two calls to this server's own API, and a tool call is
// made by sending those calls through the same router, with the caller's own
// credential, in this process. So everything the API enforces — who the member
// is, what their role and restrictions allow, the credential's scopes, request
// validation, admission — is enforced for a tool call by the same code, and a
// tool can never do what the API would refuse.
//
// Authentication is an API key or a personal access token. The tool list a
// caller sees is the tools its scopes and its member's role can use.

const (
	mcpPath            = "/v1/mcp"
	mcpMaxBody         = 1 << 20
	mcpMaxBatch        = 16
	mcpMaxResult       = 256 << 10
	mcpLatestProtocol  = "2025-06-18"
	mcpDefaultProtocol = "2025-03-26"
)

var mcpProtocols = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// MCPMessage documents the JSON-RPC envelope for the published contract. The
// handler reads the body itself: params and result differ by method.
type MCPMessage struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method,omitempty"`
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// selfHandler lets a handler built before the router call the finished router.
type selfHandler struct {
	h        http.Handler
	mux      *http.ServeMux
	registry *apikit.Registry
}

// captured collects an in-process response, to a bound.
type captured struct {
	header    http.Header
	status    int
	body      bytes.Buffer
	truncated bool
}

func (c *captured) Header() http.Header { return c.header }
func (c *captured) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *captured) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	if room := mcpMaxResult - c.body.Len(); len(p) > room {
		c.truncated = true
		if room > 0 {
			c.body.Write(p[:room])
		}
		return len(p), nil
	}
	return c.body.Write(p)
}
func (c *captured) Flush() {}

// mcpCall is one API call a tool makes.
type mcpCall struct {
	Method string
	Path   string
	Query  url.Values
	Body   any
	Header map[string]string
}

// mcpInvoker makes API calls as the caller of the MCP request.
type mcpInvoker func(call mcpCall) (status int, header http.Header, body []byte, err error)

type mcpRoute struct{ Method, Path string }

// mcpTool is one tool. Routes lists every API route it may call, which is what
// decides whether a credential's scopes can use it; Run makes the calls.
type mcpTool struct {
	Name        string
	Title       string
	Description string
	Properties  map[string]any
	Required    []string
	// Role is the lowest tier whose member can use the tool: "" for anyone.
	Role        string
	ReadOnly    bool
	Destructive bool
	Routes      []mcpRoute
	Run         func(args map[string]any, call mcpInvoker) (int, []byte, error)
}

var mcpID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func mcpToken() string {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func str(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

// argQuery copies the named arguments into a query string, as text.
func argQuery(args map[string]any, keys ...string) url.Values {
	out := url.Values{}
	for _, key := range keys {
		switch value := args[key].(type) {
		case string:
			if value != "" {
				out.Set(key, value)
			}
		case json.Number:
			out.Set(key, value.String())
		case bool:
			out.Set(key, strconv.FormatBool(value))
		}
	}
	return out
}

// pathWith fills a route's {placeholders}, in order, from the named arguments,
// each of which must be a plain identifier. With no names given, a placeholder
// is filled from the argument of the same name.
func pathWith(template string, args map[string]any, names ...string) (string, error) {
	var failed error
	index := 0
	out := regexp.MustCompile(`\{([a-zA-Z]+)\}`).ReplaceAllStringFunc(template, func(part string) string {
		name := part[1 : len(part)-1]
		if index < len(names) {
			name = names[index]
		}
		index++
		value := str(args, name)
		if !mcpID.MatchString(value) {
			failed = fmt.Errorf("%s is not a valid id", name)
			return ""
		}
		return url.PathEscape(value)
	})
	return out, failed
}

// simple is a tool that is exactly one API call. The route's placeholders are
// filled from the arguments named in params (or of the same name), query
// parameters come from arguments of the same name, and body, when given, builds
// the request body.
func simple(method, template string, params, query []string, body func(map[string]any) any) ([]mcpRoute, func(map[string]any, mcpInvoker) (int, []byte, error)) {
	return []mcpRoute{{method, template}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
		path, err := pathWith(template, args, params...)
		if err != nil {
			return 0, nil, err
		}
		request := mcpCall{Method: method, Path: path, Query: argQuery(args, query...)}
		if body != nil {
			request.Body = body(args)
		}
		status, _, raw, err := call(request)
		return status, raw, err
	}
}

func text(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}
func whole(description string, minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "maximum": maximum}
}
func flag(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
func choice(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func pick(args map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if value, ok := args[key]; ok {
			out[key] = value
		}
	}
	return out
}

// field reads one value out of a JSON document by a dotted path.
func field(raw []byte, path string) (any, bool) {
	var document any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&document) != nil {
		return nil, false
	}
	for _, part := range strings.Split(path, ".") {
		object, ok := document.(map[string]any)
		if !ok {
			return nil, false
		}
		if document, ok = object[part]; !ok {
			return nil, false
		}
	}
	return document, true
}

// mcpTools is the curated tool set. Descriptions are written for the person or
// model deciding which tool to use: what it does, in plain words, and what to
// call first when a tool needs an id.
func mcpTools() []mcpTool {
	tools := []mcpTool{}
	add := func(tool mcpTool, routes []mcpRoute, run func(map[string]any, mcpInvoker) (int, []byte, error)) {
		tool.Routes, tool.Run = routes, run
		if tool.Properties == nil {
			tool.Properties = map[string]any{}
		}
		tools = append(tools, tool)
	}
	page := func(properties map[string]any) map[string]any {
		properties["cursor"] = text("To continue a list, the nextCursor value from the previous answer.")
		return properties
	}

	// ---- looking at what is here
	r, run := simple("GET", "/v1/libraries", nil, nil, nil)
	add(mcpTool{Name: "list_libraries", Title: "List libraries", Description: "List the libraries this member can see, with each library's id, name and kind (movie, tv, music and so on). Most other tools need a library id from here.", ReadOnly: true}, r, run)

	r, run = simple("GET", "/v1/search", nil, []string{"q", "limit", "groups"}, nil)
	add(mcpTool{Name: "search", Title: "Search", Description: "Search titles and people by name across every library this member can see. Results come grouped: movies, shows, episodes, artists, albums, songs, books, people.", ReadOnly: true, Required: []string{"q"},
		Properties: map[string]any{"q": text("What to search for: 2 to 128 characters."), "limit": whole("How many results in each group.", 1, 40), "groups": text("Optional comma-separated groups to search, for example \"movies,shows\".")}}, r, run)

	r, run = simple("GET", "/v1/titles", nil, []string{"library", "kind", "sort", "order", "yearFrom", "yearTo", "addedAfter", "addedBefore", "cursor", "limit"}, nil)
	add(mcpTool{Name: "list_titles", Title: "List titles in a library", Description: "List the titles of one kind in one library, a page at a time, sorted by title, date added or year. Use it to walk a library or to see what was added recently (sort \"added\", order \"desc\").", ReadOnly: true, Required: []string{"library", "kind"},
		Properties: page(map[string]any{"library": text("The library id, from list_libraries."), "kind": choice("The kind of title to list.", "movie", "show", "season", "episode", "artist", "album", "song", "book", "author", "collection"),
			"sort": choice("What to sort by. Default: title.", "title", "added", "year"), "order": choice("Ascending or descending. Default: asc.", "asc", "desc"),
			"yearFrom": whole("With sort \"year\": the earliest year to include.", 1, 9999), "yearTo": whole("With sort \"year\": the latest year to include.", 1, 9999),
			"addedAfter": text("With sort \"added\": only titles added after this time (RFC 3339, for example 2026-01-31T00:00:00Z)."), "addedBefore": text("With sort \"added\": only titles added before this time (RFC 3339)."),
			"limit": whole("How many titles in a page. Default 50.", 1, 100)})}, r, run)

	r, run = simple("GET", "/v1/titles", nil, []string{"provider", "providerId", "library", "kind"}, nil)
	add(mcpTool{Name: "find_title_by_provider_id", Title: "Find a title by its TMDB, TVDB or IMDb id", Description: "Answer \"is this title in the library?\" from an id at TMDB, TVDB, IMDb or AniList. Returns the matching titles, or an empty list.", ReadOnly: true, Required: []string{"provider", "providerId"},
		Properties: map[string]any{"provider": choice("Whose id this is.", "tmdb", "tvdb", "imdb", "anilist"), "providerId": text("The id at that provider, for example 603 or tt0133093."), "library": text("Optional: look in this library only."), "kind": text("Optional: only this kind, for example movie or show.")}}, r, run)

	r, run = simple("GET", "/v1/items/{id}/detail", []string{"itemId"}, nil, nil)
	add(mcpTool{Name: "get_title", Title: "Title details", Description: "Everything about one film, episode, song or other playable title: its details, files, this member's watch state and what can be done with it.", ReadOnly: true, Required: []string{"itemId"},
		Properties: map[string]any{"itemId": text("The title's id, from search or list_titles.")}}, r, run)

	r, run = simple("GET", "/v1/shows/{id}/seasons", []string{"showId"}, nil, nil)
	add(mcpTool{Name: "list_seasons", Title: "List a show's seasons", Description: "List the seasons of a show.", ReadOnly: true, Required: []string{"showId"}, Properties: map[string]any{"showId": text("The show's id, from search or list_titles.")}}, r, run)

	r, run = simple("GET", "/v1/seasons/{id}/episodes", []string{"seasonId"}, []string{"cursor", "limit"}, nil)
	add(mcpTool{Name: "list_episodes", Title: "List a season's episodes", Description: "List the episodes of one season, in order.", ReadOnly: true, Required: []string{"seasonId"},
		Properties: page(map[string]any{"seasonId": text("The season's id, from list_seasons."), "limit": whole("How many episodes in a page.", 1, 100)})}, r, run)

	// ---- this member's own state
	add(mcpTool{Name: "mark_watched", Title: "Mark watched or unwatched", Description: "Mark up to 200 titles as watched or unwatched for this member.", Required: []string{"itemIds", "watched"},
		Properties: map[string]any{"itemIds": map[string]any{"type": "array", "description": "The ids of the titles.", "minItems": 1, "maxItems": 200, "items": map[string]any{"type": "string"}}, "watched": flag("true to mark watched, false to mark unwatched.")}},
		[]mcpRoute{{"PUT", "/v1/items/personal-state:batch"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			ids, _ := args["itemIds"].([]any)
			items := []map[string]any{}
			for _, raw := range ids {
				id, _ := raw.(string)
				if !mcpID.MatchString(id) {
					return 0, nil, errors.New("itemIds must be title ids")
				}
				items = append(items, map[string]any{"itemId": id, "watched": args["watched"]})
			}
			status, _, raw, err := call(mcpCall{Method: "PUT", Path: "/v1/items/personal-state:batch", Body: map[string]any{"operationId": "mcp-" + mcpToken(), "items": items}})
			return status, raw, err
		})

	r, run = simple("GET", "/v1/notifications/inbox", nil, []string{"cursor", "limit"}, nil)
	add(mcpTool{Name: "list_notifications", Title: "List notifications", Description: "List this member's notifications from the server, newest first.", ReadOnly: true, Properties: page(map[string]any{"limit": whole("How many in a page.", 1, 100)})}, r, run)

	// ---- scanning and matching
	r, run = simple("POST", "/v1/libraries/{id}/scans", []string{"libraryId"}, []string{"mode"}, nil)
	add(mcpTool{Name: "start_library_scan", Title: "Scan a library", Description: "Start scanning a library for new, changed and removed files. If a scan is already running, that scan is returned. To scan only particular folders, use report_changed_paths instead.", Role: identity.TierOwner, Required: []string{"libraryId"},
		Properties: map[string]any{"libraryId": text("The library id, from list_libraries."), "mode": choice("Leave out for the usual quick scan. \"full\" re-reads every folder; \"integrity\" also re-checks files.", "incremental", "full", "integrity")}}, r, run)

	add(mcpTool{Name: "report_changed_paths", Title: "Scan only the folders that changed", Description: "Tell the server which files or folders were added, changed or removed, so it scans only those folders. Paths must be absolute and written the way the server sees them. The answer says, for each path, which library folder will be scanned, or that the path is outside every library.", Role: identity.TierOwner, Required: []string{"paths"},
		Properties: map[string]any{"paths": map[string]any{"type": "array", "description": "Absolute paths of the files or folders.", "minItems": 1, "maxItems": 200, "items": map[string]any{"type": "string"}}}},
		[]mcpRoute{{"POST", "/v1/libraries/paths-changed"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			raw, _ := args["paths"].([]any)
			paths := []map[string]any{}
			for _, value := range raw {
				path, _ := value.(string)
				paths = append(paths, map[string]any{"path": path})
			}
			status, _, body, err := call(mcpCall{Method: "POST", Path: "/v1/libraries/paths-changed", Body: map[string]any{"paths": paths}})
			return status, body, err
		})

	r, run = simple("GET", "/v1/libraries/{id}/inventory-status", []string{"libraryId"}, nil, nil)
	add(mcpTool{Name: "library_scan_status", Title: "Scan status of a library", Description: "Show whether a library is being scanned right now, how far the scan has got, and when each of its folders was last scanned completely.", ReadOnly: true, Required: []string{"libraryId"}, Properties: map[string]any{"libraryId": text("The library id, from list_libraries.")}}, r, run)

	r, run = simple("GET", "/v1/admin/jobs", nil, []string{"libraryId", "status", "limit", "cursor"}, nil)
	add(mcpTool{Name: "list_scan_jobs", Title: "List scans", Description: "List recent and running scans across libraries, with what each one processed and any error.", Role: identity.TierOwner, ReadOnly: true,
		Properties: page(map[string]any{"libraryId": text("Optional: scans of this library only."), "status": choice("Optional: only scans in this state.", "queued", "running", "paused", "complete", "complete_with_warnings", "failed", "cancelled"), "limit": whole("How many in a page.", 1, 40)})}, r, run)

	r, run = simple("GET", "/v1/libraries/{id}/episode-issues", []string{"libraryId"}, []string{"cursor", "limit"}, nil)
	add(mcpTool{Name: "list_unmatched_files", Title: "Files that could not be matched", Description: "List the files in a TV library that the scan could not place in a show, season and episode, so they can be fixed or renamed.", Role: identity.TierOwner, ReadOnly: true, Required: []string{"libraryId"},
		Properties: page(map[string]any{"libraryId": text("The library id, from list_libraries."), "limit": whole("How many in a page.", 1, 100)})}, r, run)

	r, run = simple("GET", "/v1/admin/metadata/attention", nil, []string{"cursor", "limit", "includeInProgress"}, nil)
	add(mcpTool{Name: "list_needs_attention", Title: "Titles needing attention", Description: "List titles needing the owner's attention across every library. Work in progress is excluded when includeInProgress is false.", Role: identity.TierOwner, ReadOnly: true, Properties: page(map[string]any{"limit": whole("How many in a page.", 1, 50), "includeInProgress": map[string]any{"type": "boolean"}})}, r, run)
	r, run = simple("GET", "/v1/libraries/{id}/metadata/attention", []string{"libraryId"}, []string{"cursor", "limit", "includeInProgress"}, nil)
	add(mcpTool{Name: "library_needs_attention", Title: "Library titles needing attention", Description: "List unmatched or uncertain titles in one library, with the exact total waiting for attention.", Role: identity.TierOwner, ReadOnly: true, Required: []string{"libraryId"}, Properties: page(map[string]any{"libraryId": text("The library id."), "limit": whole("How many in a page.", 1, 50), "includeInProgress": map[string]any{"type": "boolean"}})}, r, run)
	r, run = simple("GET", "/v1/admin/metadata/attention/counts", nil, nil, nil)
	add(mcpTool{Name: "count_needs_attention", Title: "Count titles needing attention", Description: "Get exact counts per library and across the server without loading titles.", Role: identity.TierOwner, ReadOnly: true, Properties: map[string]any{}}, r, run)

	r, run = simple("GET", "/v1/metadata/{kind}/{id}", nil, nil, nil)
	add(mcpTool{Name: "get_match", Title: "How a title was matched", Description: "Show how one title is matched to its metadata provider: the current match, and the other candidates the server found with how confident it is in each. Call this before fix_match.", Role: identity.TierOwner, ReadOnly: true, Required: []string{"kind", "id"},
		Properties: map[string]any{"kind": choice("What the id is.", "item", "show", "season", "album", "artist", "book"), "id": text("The title's id.")}}, r, run)

	matchRoutes := []mcpRoute{{"GET", "/v1/metadata/{kind}/{id}"}, {"POST", "/v1/metadata/{kind}/{id}"}}
	match := func(command func(args map[string]any) map[string]any) func(map[string]any, mcpInvoker) (int, []byte, error) {
		return func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			path, err := pathWith("/v1/metadata/{kind}/{id}", args)
			if err != nil {
				return 0, nil, err
			}
			status, _, current, err := call(mcpCall{Method: "GET", Path: path})
			if err != nil || status != 200 {
				return status, current, err
			}
			revision, ok := field(current, "revision")
			if !ok {
				return 0, nil, errors.New("the server did not say which version of the match this is")
			}
			body := command(args)
			body["expectedRevision"] = revision
			status, _, raw, err := call(mcpCall{Method: "POST", Path: path, Body: body})
			return status, raw, err
		}
	}
	add(mcpTool{Name: "search_match_candidates", Title: "Look for a better match", Description: "Ask the metadata provider for candidates for a film or show under a different name or year. The search runs in the background: call get_match a few seconds later to see the candidates.", Role: identity.TierOwner, Required: []string{"kind", "id", "query"},
		Properties: map[string]any{"kind": choice("What the id is.", "item", "show"), "id": text("The title's id."), "query": text("The name to search for."), "year": whole("Optional release year.", 1800, 2200), "provider": text("Optional provider to search, for example tmdb or tvdb.")}},
		matchRoutes, match(func(args map[string]any) map[string]any {
			out := pick(args, "query", "year", "provider")
			out["action"] = "search"
			return out
		}))
	add(mcpTool{Name: "fix_match", Title: "Fix a match", Description: "Match a title to one of the candidates from get_match. The title's name, artwork and details are then taken from that candidate.", Role: identity.TierOwner, Required: []string{"kind", "id", "candidateId", "provider"},
		Properties: map[string]any{"kind": choice("What the id is.", "item", "show", "season", "album", "artist", "book"), "id": text("The title's id."), "candidateId": text("The candidate's id, from get_match."), "provider": text("The candidate's provider, from get_match.")}},
		matchRoutes, match(func(args map[string]any) map[string]any {
			out := pick(args, "candidateId", "provider")
			out["action"], out["confirm"], out["operationId"] = "identify", true, "mcp-"+mcpToken()
			return out
		}))

	// ---- playback
	r, run = simple("GET", "/v1/admin/sessions", nil, []string{"limit", "cursor"}, nil)
	add(mcpTool{Name: "now_playing", Title: "What is playing", Description: "List what is being played on the server right now: who is watching what, on which device, how far in, and whether it is being converted.", Role: identity.TierAdmin, ReadOnly: true, Properties: page(map[string]any{"limit": whole("How many in a page.", 1, 200)})}, r, run)

	add(mcpTool{Name: "stop_playback", Title: "Stop someone's playback", Description: "Stop one playback session from now_playing. The person watching sees the message, if one is given.", Role: identity.TierAdmin, Destructive: true, Required: []string{"sessionId"},
		Properties: map[string]any{"sessionId": text("The session's id, from now_playing."), "message": text("Optional short message shown to the person watching.")}},
		[]mcpRoute{{"POST", "/v1/admin/sessions/{id}:terminate"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			if !mcpID.MatchString(str(args, "sessionId")) {
				return 0, nil, errors.New("sessionId is not a valid id")
			}
			request := mcpCall{Method: "POST", Path: "/v1/admin/sessions/" + str(args, "sessionId") + ":terminate"}
			if message := str(args, "message"); message != "" {
				request.Body = map[string]any{"message": message}
			}
			status, _, raw, err := call(request)
			return status, raw, err
		})

	add(mcpTool{Name: "control_playback", Title: "Pause, resume or seek playback", Description: "Control your own playback session.", Required: []string{"sessionId", "action"},
		Properties: map[string]any{"sessionId": text("The playback session's id."), "action": choice("What to do.", "pause", "resume", "seek"), "positionMs": whole("For seek: where to go, in milliseconds from the start.", 0, 1<<40)}},
		[]mcpRoute{{"GET", "/v1/playback/sessions/{id}"}, {"PATCH", "/v1/playback/sessions/{id}"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			path, err := pathWith("/v1/playback/sessions/{id}", args, "sessionId")
			if err != nil {
				return 0, nil, err
			}
			status, header, current, err := call(mcpCall{Method: "GET", Path: path})

			if err != nil || status != 200 {
				return status, current, err
			}
			revision := header.Get("ETag")
			if revision == "" {
				if value, ok := field(current, "revision"); ok {
					revision = fmt.Sprint(value)
				}
			}
			body := map[string]any{}
			switch str(args, "action") {
			case "pause":
				body["state"] = "paused"
			case "resume":
				body["state"] = "playing"
			case "seek":
				position, ok := args["positionMs"]
				if !ok {
					return 0, nil, errors.New("positionMs is needed to seek")
				}
				body["seek"] = map[string]any{"positionMs": position, "id": "mcp-" + mcpToken()}
			}
			status, _, raw, err := call(mcpCall{Method: "PATCH", Path: path, Body: body, Header: map[string]string{"If-Match": revision}})
			return status, raw, err
		})

	add(mcpTool{Name: "control_member_playback", Title: "Pause, resume or seek a member's playback", Description: "Control another member's playback session from now_playing. Requires permission to administer playback.", Role: identity.TierAdmin, Required: []string{"sessionId", "action"},
		Properties: map[string]any{"sessionId": text("The playback session's id, from now_playing."), "action": choice("What to do.", "pause", "resume", "seek"), "positionMs": whole("For seek: where to go, in milliseconds from the start.", 0, 1<<40)}},
		[]mcpRoute{{"GET", "/v1/admin/sessions/{id}/controls"}, {"POST", "/v1/admin/sessions/{id}/controls"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			path, err := pathWith("/v1/admin/sessions/{id}/controls", args, "sessionId")
			if err != nil {
				return 0, nil, err
			}
			status, _, current, err := call(mcpCall{Method: "GET", Path: path})
			if err != nil || status != 200 {
				return status, current, err
			}
			revision, ok := field(current, "revision")
			if !ok {
				return 0, nil, errors.New("the session revision was missing")
			}
			body := map[string]any{"action": str(args, "action"), "expectedRevision": revision}
			if str(args, "action") == "seek" {
				position, ok := args["positionMs"]
				if !ok {
					return 0, nil, errors.New("positionMs is needed to seek")
				}
				body["positionMs"] = position
			}
			status, _, raw, err := call(mcpCall{Method: "POST", Path: path, Body: body})
			return status, raw, err
		})

	// ---- people
	r, run = simple("GET", "/v1/admin/access/members", nil, []string{"cursor", "limit"}, nil)
	add(mcpTool{Name: "list_members", Title: "List members", Description: "List the people who have an account on this server, with each one's role, whether they are disabled, which libraries they can see and their limits.", Role: identity.TierAdmin, ReadOnly: true, Properties: page(map[string]any{"limit": whole("How many in a page.", 1, 100)})}, r, run)

	r, run = simple("GET", "/v1/admin/play-history", nil, []string{"accountId", "libraryId", "period", "cursor", "limit", "timeZone", "until", "media", "q", "sort", "direction"}, nil)
	add(mcpTool{Name: "play_history", Title: "Play history", Description: "List what has been played on the server and by whom, newest first.", Role: identity.TierAdmin, ReadOnly: true, Required: []string{"timeZone", "until"},
		Properties: page(map[string]any{"accountId": text("Optional: one member's plays only."), "libraryId": text("Optional: plays from one library only."), "period": choice("How far back. Default: 24h.", "24h", "7d", "30d", "90d", "all"), "limit": whole("How many in a page.", 1, 200), "timeZone": text("The viewer's IANA time zone."), "until": text("The viewer's completed quarter-hour boundary, as a UTC timestamp.")})}, r, run)

	// ---- the server itself
	add(mcpTool{Name: "server_status", Title: "Server status", Description: "Say whether the server is up and ready, its name and its version.", ReadOnly: true},
		[]mcpRoute{{"GET", "/v1/system"}, {"GET", "/v1/readiness"}}, func(_ map[string]any, call mcpInvoker) (int, []byte, error) {
			_, _, system, err := call(mcpCall{Method: "GET", Path: "/v1/system"})
			if err != nil {
				return 0, nil, err
			}
			_, _, ready, err := call(mcpCall{Method: "GET", Path: "/v1/readiness"})
			if err != nil {
				return 0, nil, err
			}
			out := map[string]json.RawMessage{}
			if json.Valid(system) {
				out["server"] = system
			}
			if json.Valid(ready) {
				out["readiness"] = ready
			}
			raw, _ := json.Marshal(out)
			return 200, raw, nil
		})

	r, run = simple("GET", "/v1/admin/console/panels/{panel}", nil, nil, nil)
	add(mcpTool{Name: "server_health", Title: "Server health and free space", Description: "Read one of the server's health panels: \"health\" for an overall view, \"storage\" for free disk space and database size, \"resources\" for memory and processor, \"network\" for connectivity.", Role: identity.TierOwner, ReadOnly: true, Required: []string{"panel"},
		Properties: map[string]any{"panel": choice("Which panel.", "health", "storage", "resources", "network")}}, r, run)

	r, run = simple("GET", "/v1/admin/storage-usage", nil, nil, nil)
	add(mcpTool{Name: "storage_usage", Title: "What is using the server's disk", Description: "Show how much disk the server's own data uses, by category: database, artwork, converted video, logs, backups and so on. For free space use server_health with the storage panel.", Role: identity.TierOwner, ReadOnly: true}, r, run)

	r, run = simple("GET", "/v1/admin/logs", nil, []string{"level", "category", "cursor", "limit"}, nil)
	add(mcpTool{Name: "read_logs", Title: "Read the server log", Description: "Read recent lines from the server's log, newest first.", Role: identity.TierAdmin, ReadOnly: true,
		Properties: page(map[string]any{"level": choice("The least serious level to include. Default: everything.", "debug", "info", "warn", "error"), "category": text("Optional: lines from one part of the server only, for example playback or scan."), "limit": whole("How many lines. Default 100.", 1, 500)})}, r, run)

	r, run = simple("GET", "/v1/admin/console/settings", nil, nil, nil)
	add(mcpTool{Name: "get_settings", Title: "Read server settings", Description: "Read every server setting and its current value. Call this before change_settings to see the setting names.", Role: identity.TierOwner, ReadOnly: true}, r, run)

	add(mcpTool{Name: "change_settings", Title: "Change server settings", Description: "Change one or more server settings, leaving the rest as they are. Give only the settings to change, using the names from get_settings. The answer is the settings after the change, or which values were refused.", Role: identity.TierOwner, Required: []string{"values"},
		Properties: map[string]any{"values": map[string]any{"type": "object", "description": "The settings to change: each key is a setting's name, each value its new value.", "minProperties": 1, "maxProperties": 64}}},
		[]mcpRoute{{"GET", "/v1/admin/console/settings"}, {"PATCH", "/v1/admin/console/settings"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			changes, _ := args["values"].(map[string]any)
			if len(changes) == 0 || len(changes) > 64 {
				return 0, nil, errors.New("values must name between 1 and 64 settings")
			}
			status, _, current, err := call(mcpCall{Method: "GET", Path: "/v1/admin/console/settings"})
			if err != nil || status != 200 {
				return status, current, err
			}
			revision, _ := field(current, "data.revision")
			requested, ok := field(current, "data.requested")
			values, isObject := requested.(map[string]any)
			if !ok || !isObject || revision == nil {
				return 0, nil, errors.New("the server's settings could not be read")
			}
			for key, value := range changes {
				// Only a setting that exists can be changed; a new name is a
				// mistake, not a new setting.
				if _, known := values[key]; !known {
					return 0, nil, fmt.Errorf("there is no setting called %q", key)
				}
				values[key] = value
			}
			status, _, raw, err := call(mcpCall{Method: "PATCH", Path: "/v1/admin/console/settings", Body: map[string]any{"expectedRevision": revision, "idempotencyKey": "mcp-" + mcpToken(), "values": values}})
			return status, raw, err
		})

	r, run = simple("GET", "/v1/admin/backups", nil, nil, nil)
	add(mcpTool{Name: "list_backups", Title: "List backups", Description: "List the server's backups, whether one is running now, and how the last restore went.", Role: identity.TierOwner, ReadOnly: true}, r, run)

	r, run = simple("POST", "/v1/admin/backups", nil, nil, func(map[string]any) any { return map[string]any{"operationId": "mcp-" + mcpToken()} })
	add(mcpTool{Name: "run_backup", Title: "Back up now", Description: "Start a backup of the server's database and settings now. It runs in the background; use list_backups to see when it finishes.", Role: identity.TierOwner}, r, run)

	// ---- live TV and recordings
	add(mcpTool{Name: "tv_guide", Title: "TV guide", Description: "Show what is on live TV: channels with their programmes between two times. Leave the times out for the next three hours. When paging, reuse the returned start and end with the cursor. Each programme carries what schedule_recording needs.", ReadOnly: true,
		Properties: page(map[string]any{"start": text("Start of the window (RFC 3339, whole seconds). Default: now."), "end": text("End of the window, at most 24 hours after the start. Default: three hours after the start."), "search": text("Optional: only channels or programmes matching this text."), "limit": whole("How many channels in a page. Default 25.", 1, 50)})},
		[]mcpRoute{{"GET", "/v1/guide"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			query := argQuery(args, "start", "end", "search", "limit", "cursor")
			if query.Get("start") == "" {
				query.Set("start", time.Now().UTC().Truncate(time.Second).Format(time.RFC3339))
			}
			if query.Get("end") == "" {
				if start, err := time.Parse(time.RFC3339, query.Get("start")); err == nil {
					query.Set("end", start.Add(3*time.Hour).Format(time.RFC3339))
				}
			}
			if query.Get("limit") == "" {
				query.Set("limit", "25")
			}
			query.Set("kind", "live-source")
			status, _, raw, err := call(mcpCall{Method: "GET", Path: "/v1/guide", Query: query})
			return status, raw, err
		})

	r, run = simple("GET", "/v1/dvr", nil, []string{"state", "cursor", "limit"}, nil)
	add(mcpTool{Name: "list_recordings", Title: "List recordings", Description: "List this member's recordings and recording rules: upcoming, recorded, or past ones that failed or were cancelled.", ReadOnly: true,
		Properties: page(map[string]any{"state": choice("Which to list. Default: upcoming.", "all", "upcoming", "recorded", "history", "rules"), "limit": whole("How many in a page.", 1, 50)})}, r, run)

	r, run = simple("POST", "/v1/dvr/recordings", nil, nil, func(args map[string]any) any {
		return map[string]any{"requestId": "mcp-" + mcpToken(), "useDefaults": true, "occurrence": pick(args, "sourceId", "channelId", "generation", "programmeId")}
	})
	add(mcpTool{Name: "schedule_recording", Title: "Record a programme", Description: "Record one programme from the TV guide, with this member's usual recording options. The four values come from tv_guide: the channel's sourceId, id and generation, and the programme's id.", Required: []string{"sourceId", "channelId", "generation", "programmeId"},
		Properties: map[string]any{"sourceId": text("The channel's sourceId, from tv_guide."), "channelId": text("The channel's id, from tv_guide."), "generation": map[string]any{"description": "The channel's generation, from tv_guide, exactly as given."}, "programmeId": text("The programme's id, from tv_guide.")}}, r, run)

	add(mcpTool{Name: "cancel_recording", Title: "Cancel a recording", Description: "Cancel one upcoming or running recording of this member's.", Destructive: true, Required: []string{"recordingId"}, Properties: map[string]any{"recordingId": text("The recording's id, from list_recordings.")}},
		[]mcpRoute{{"GET", "/v1/dvr/recordings/{id}"}, {"POST", "/v1/dvr/recordings/{id}/cancel"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			path, err := pathWith("/v1/dvr/recordings/{id}", args, "recordingId")
			if err != nil {
				return 0, nil, err
			}
			status, _, current, err := call(mcpCall{Method: "GET", Path: path})
			if err != nil || status != 200 {
				return status, current, err
			}
			var revision any
			for _, where := range []string{"recording.revision", "result.recording.revision", "result.revision", "revision"} {
				if value, ok := field(current, where); ok {
					revision = value
					break
				}
			}
			if revision == nil {
				return 0, nil, errors.New("the server did not say which version of the recording this is")
			}
			status, _, raw, err := call(mcpCall{Method: "POST", Path: path + "/cancel", Body: map[string]any{"requestId": "mcp-" + mcpToken(), "expectedRevision": revision}})
			return status, raw, err
		})

	// ---- webhooks
	r, run = simple("GET", "/v1/admin/webhooks", nil, nil, nil)
	add(mcpTool{Name: "list_webhooks", Title: "List webhooks", Description: "List the server's outbound webhooks, the events each one is sent, whether deliveries are failing, and every event that can be chosen.", Role: identity.TierOwner, ReadOnly: true}, r, run)

	r, run = simple("POST", "/v1/admin/webhooks", nil, nil, func(args map[string]any) any { return pick(args, "name", "url", "events", "secret") })
	add(mcpTool{Name: "create_webhook", Title: "Add a webhook", Description: "Add a webhook: the server will POST the chosen events to the URL, signed with a secret. The secret is in the answer and is shown only this once.", Role: identity.TierOwner, Required: []string{"name", "url", "events"},
		Properties: map[string]any{"name": text("A name to recognise it by."), "url": text("Where to send events: an http or https address."), "events": map[string]any{"type": "array", "description": "The events to send, from list_webhooks.", "minItems": 1, "items": map[string]any{"type": "string"}}, "secret": text("Optional signing secret of your own, at least 16 characters. Leave out to have one made.")}}, r, run)

	// A change is made against the webhook as it is when the tool runs: it is
	// read first and its revision sent with the change.
	add(mcpTool{Name: "update_webhook", Title: "Change a webhook", Description: "Change a webhook's name, address or events, turn it on or off, or replace its signing secret. Anything left out stays as it is.", Role: identity.TierOwner, Required: []string{"webhookId"},
		Properties: map[string]any{"webhookId": text("The webhook's id, from list_webhooks."), "name": text("A new name."), "url": text("A new address."), "events": map[string]any{"type": "array", "description": "The complete new list of events.", "minItems": 1, "items": map[string]any{"type": "string"}}, "enabled": flag("true to turn it on, false to turn it off."), "rotateSecret": flag("true to replace the signing secret; the new one is in the answer, once.")}},
		[]mcpRoute{{"GET", "/v1/admin/webhooks/{id}"}, {"PATCH", "/v1/admin/webhooks/{id}"}}, func(args map[string]any, call mcpInvoker) (int, []byte, error) {
			path, err := pathWith("/v1/admin/webhooks/{id}", args, "webhookId")
			if err != nil {
				return 0, nil, err
			}
			status, _, current, err := call(mcpCall{Method: "GET", Path: path})
			if err != nil || status != 200 {
				return status, current, err
			}
			revision, ok := field(current, "revision")
			if !ok {
				return 0, nil, errors.New("the server did not say which version of the webhook this is")
			}
			body := pick(args, "name", "url", "events", "enabled", "rotateSecret")
			body["expectedRevision"] = revision
			status, _, raw, err := call(mcpCall{Method: "PATCH", Path: path, Body: body})
			return status, raw, err
		})

	r, run = simple("DELETE", "/v1/admin/webhooks/{id}", []string{"webhookId"}, nil, nil)
	add(mcpTool{Name: "delete_webhook", Title: "Remove a webhook", Description: "Remove a webhook, with anything still waiting to be sent to it and its delivery log.", Role: identity.TierOwner, Destructive: true, Required: []string{"webhookId"}, Properties: map[string]any{"webhookId": text("The webhook's id, from list_webhooks.")}}, r, run)

	r, run = simple("POST", "/v1/admin/webhooks/{id}/test", []string{"webhookId"}, nil, nil)
	add(mcpTool{Name: "test_webhook", Title: "Send a test to a webhook", Description: "Send a test event to a webhook. The result appears in its delivery log a moment later: use list_webhook_deliveries.", Role: identity.TierOwner, Required: []string{"webhookId"}, Properties: map[string]any{"webhookId": text("The webhook's id, from list_webhooks.")}}, r, run)

	r, run = simple("GET", "/v1/admin/webhooks/{id}/deliveries", []string{"webhookId"}, []string{"cursor", "limit"}, nil)
	add(mcpTool{Name: "list_webhook_deliveries", Title: "A webhook's delivery log", Description: "Show a webhook's recent deliveries, newest first: what was sent, whether it arrived, how many attempts it took and why it failed.", Role: identity.TierOwner, ReadOnly: true, Required: []string{"webhookId"},
		Properties: page(map[string]any{"webhookId": text("The webhook's id, from list_webhooks."), "limit": whole("How many in a page.", 1, 100)})}, r, run)

	return tools
}

// mcpToolScopes is the scopes a tool's routes declare. A route that is not
// served by this server (a feature that is not set up) makes the tool absent.
func (d Dependencies) mcpToolScopes(tool mcpTool) ([]apikit.Scope, bool) {
	if d.self == nil || d.self.mux == nil {
		return nil, false
	}
	mux := d.self.mux
	out := []apikit.Scope{}
	for _, route := range tool.Routes {
		path := regexp.MustCompile(`\{[a-zA-Z]+\}`).ReplaceAllString(route.Path, "x")
		probe, err := http.NewRequest(route.Method, path, nil)
		if err != nil {
			return nil, false
		}
		scope, routed := d.routeScope(mux, probe)
		if !routed {
			return nil, false
		}
		out = append(out, scope)
	}
	return out, true
}

// mcpVisible lists the tools this credential and this member can use.
func (d Dependencies) mcpVisible(credential identity.APICredential, role string) []mcpTool {
	granted := make([]apikit.Scope, 0, len(credential.Scopes))
	for _, scope := range credential.Scopes {
		granted = append(granted, apikit.Scope(scope))
	}
	out := []mcpTool{}
	for _, tool := range mcpTools() {
		if tool.Role != "" && !identity.Grants(role, tool.Role) {
			continue
		}
		scopes, served := d.mcpToolScopes(tool)
		if !served {
			continue
		}
		allowed := true
		for _, scope := range scopes {
			allowed = allowed && scopeRefusal(granted, scope) == nil
		}
		if allowed {
			out = append(out, tool)
		}
	}
	return out
}

// checkArguments holds a call to the tool's published schema: no unknown
// arguments, the required ones present, and each of the declared simple type.
func checkArguments(tool mcpTool, args map[string]any) error {
	for key := range args {
		if _, known := tool.Properties[key]; !known {
			return fmt.Errorf("%q is not an argument of %s", key, tool.Name)
		}
	}
	for _, key := range tool.Required {
		if _, ok := args[key]; !ok {
			return fmt.Errorf("%s needs %q", tool.Name, key)
		}
	}
	for key, value := range args {
		schema, _ := tool.Properties[key].(map[string]any)
		kind, _ := schema["type"].(string)
		ok := true
		switch kind {
		case "string":
			text, isString := value.(string)
			ok = isString && len(text) <= 8192
			if options, has := schema["enum"].([]string); ok && has {
				ok = false
				for _, option := range options {
					ok = ok || option == text
				}
			}
		case "integer":
			number, isNumber := value.(json.Number)
			n, err := number.Int64()
			ok = isNumber && err == nil
			if minimum, has := schema["minimum"].(int); ok && has {
				ok = n >= int64(minimum)
			}
			if maximum, has := schema["maximum"].(int); ok && has {
				ok = n <= int64(maximum)
			}
		case "boolean":
			_, ok = value.(bool)
		case "array":
			list, isList := value.([]any)
			ok = isList && len(list) > 0 && len(list) <= 200
		case "object":
			_, ok = value.(map[string]any)
		}
		if !ok {
			return fmt.Errorf("%q is not a valid value for %s", key, tool.Name)
		}
	}
	return nil
}

func mcpToolDocument(tool mcpTool) map[string]any {
	schema := map[string]any{"type": "object", "properties": tool.Properties, "additionalProperties": false}
	if len(tool.Required) > 0 {
		schema["required"] = tool.Required
	}
	return map[string]any{
		"name": tool.Name, "title": tool.Title, "description": tool.Description, "inputSchema": schema,
		"annotations": map[string]any{"title": tool.Title, "readOnlyHint": tool.ReadOnly, "destructiveHint": tool.Destructive, "openWorldHint": false},
	}
}

// mcpInvoke builds the function a tool uses to call this server's own API as
// the caller. Each call goes through the whole router — the credential gate,
// admission and the handler — and is charged to nothing twice.
func (d Dependencies) mcpInvoke(r *http.Request) mcpInvoker {
	return func(call mcpCall) (int, http.Header, []byte, error) {
		if d.self == nil || d.self.h == nil {
			return 0, nil, nil, errors.New("the server is not ready")
		}
		var body io.Reader
		if call.Body != nil {
			raw, err := json.Marshal(call.Body)
			if err != nil {
				return 0, nil, nil, err
			}
			body = bytes.NewReader(raw)
		}
		target := call.Path
		if len(call.Query) > 0 {
			target += "?" + call.Query.Encode()
		}
		// A context of its own: the outer request's lane budget and class are
		// the MCP request's, not the API call's. The caller leaving still ends it.
		ctx, cancel := context.WithCancel(withNestedCredential(context.Background()))
		defer cancel()
		stop := context.AfterFunc(r.Context(), cancel)
		defer stop()
		inner, err := http.NewRequestWithContext(ctx, call.Method, target, body)
		if err != nil {
			return 0, nil, nil, err
		}
		inner.RemoteAddr, inner.Host, inner.TLS, inner.RequestURI = r.RemoteAddr, r.Host, r.TLS, target
		inner.Header.Set("Authorization", r.Header.Get("Authorization"))
		for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "Forwarded"} {
			for _, value := range r.Header.Values(name) {
				inner.Header.Add(name, value)
			}
		}
		if call.Body != nil {
			inner.Header.Set("Content-Type", "application/json")
		}
		for name, value := range call.Header {
			if value != "" {
				inner.Header.Set(name, value)
			}
		}
		out := &captured{header: http.Header{}}
		d.self.h.ServeHTTP(out, inner)
		if out.truncated {
			return out.status, out.header, nil, errors.New("the answer is too large; ask for less at a time")
		}
		return out.status, out.header, out.body.Bytes(), nil
	}
}

func mcpResult(status int, raw []byte, err error) map[string]any {
	failed := err != nil || status >= 400 || status == 0
	text := string(raw)
	switch {
	case err != nil:
		text = err.Error()
	case len(raw) == 0 && !failed:
		text = "Done."
	case failed:
		// The API's own plain message, when it sent one.
		if message, ok := field(raw, "error.message"); ok {
			text = fmt.Sprint(message)
			if fields, has := field(raw, "error.fields"); has {
				if detail, e := json.Marshal(fields); e == nil {
					text += " " + string(detail)
				}
			}
		}
	}
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": failed}
	if !failed && json.Valid(raw) {
		var structured map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&structured) == nil && structured != nil {
			out["structuredContent"] = structured
		}
	}
	return out
}

// mcpHandle answers one JSON-RPC message. A notification has no answer.
func (d Dependencies) mcpHandle(r *http.Request, credential identity.APICredential, role string, message mcpRequest) (mcpResponse, bool) {
	answer := mcpResponse{JSONRPC: "2.0", ID: message.ID}
	notification := len(message.ID) == 0
	fail := func(code int, text string) (mcpResponse, bool) {
		answer.Error = &mcpError{Code: code, Message: text}
		return answer, !notification
	}
	if message.JSONRPC != "2.0" || message.Method == "" {
		answer.ID = json.RawMessage("null")
		return fail(-32600, "This is not a JSON-RPC 2.0 request.")
	}
	if len(message.ID) > 0 {
		var id any
		if json.Unmarshal(message.ID, &id) != nil {
			return fail(-32600, "The request id is not valid.")
		}
		switch id.(type) {
		case string, float64:
		default:
			answer.ID = json.RawMessage("null")
			return fail(-32600, "The request id must be a string or a number.")
		}
	}
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(message.Params, &params)
		version := mcpLatestProtocol
		if mcpProtocols[params.ProtocolVersion] {
			version = params.ProtocolVersion
		}
		name := "Portico"
		if d.Identity != nil {
			name = d.Identity.Name()
		}
		answer.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "portico", "title": name, "version": buildinfo.Info()["version"]},
			"instructions":    "This is a Portico media server. The tools act as the member whose API key or access token you are using, and only the tools that member and that key may use are listed. Ids come from other tools: start with list_libraries or search.",
		}
	case "ping":
		answer.Result = map[string]any{}
	case "tools/list":
		tools := []map[string]any{}
		for _, tool := range d.mcpVisible(credential, role) {
			tools = append(tools, mcpToolDocument(tool))
		}
		answer.Result = map[string]any{"tools": tools}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(message.Params, &params) != nil || params.Name == "" {
			return fail(-32602, "A tool call needs the tool's name.")
		}
		args := map[string]any{}
		if len(params.Arguments) > 0 && string(params.Arguments) != "null" {
			decoder := json.NewDecoder(bytes.NewReader(params.Arguments))
			decoder.UseNumber()
			if decoder.Decode(&args) != nil {
				return fail(-32602, "The tool's arguments must be an object.")
			}
		}
		// A tool this caller may not use does not exist for this caller: the
		// answer is the same as for a name that was never a tool.
		var tool *mcpTool
		for _, candidate := range d.mcpVisible(credential, role) {
			if candidate.Name == params.Name {
				found := candidate
				tool = &found
			}
		}
		if tool == nil {
			return fail(-32602, "There is no tool called "+strconv.Quote(params.Name)+" for this key.")
		}
		if err := checkArguments(*tool, args); err != nil {
			return fail(-32602, err.Error())
		}
		status, raw, err := tool.Run(args, d.mcpInvoke(r))
		answer.Result = mcpResult(status, raw, err)
	default:
		if strings.HasPrefix(message.Method, "notifications/") {
			return answer, false
		}
		return fail(-32601, "This server does not offer "+strconv.Quote(message.Method)+".")
	}
	return answer, !notification
}

// mcp serves POST /v1/mcp.
func (d Dependencies) mcp(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, code, message string) {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Portico"`)
		}
		write(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": false}})
	}
	credential, ok := identity.APICredentialFrom(r.Context())
	if !ok {
		refuse(http.StatusUnauthorized, "unauthorized", "Use an API key or a personal access token in the Authorization header.")
		return
	}
	// The member behind the credential is proved live exactly as for any call.
	p, err := d.principal(r)
	if err != nil {
		refuse(http.StatusUnauthorized, "unauthorized", "Authentication is required.")
		return
	}
	if version := r.Header.Get("MCP-Protocol-Version"); version != "" && !mcpProtocols[version] {
		refuse(http.StatusBadRequest, "invalid_request", "This protocol version is not supported.")
		return
	}
	if media := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(media), "application/json") {
		refuse(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send application/json.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcpMaxBody))
	if err != nil {
		refuse(http.StatusRequestEntityTooLarge, "body_too_large", "The request is too large.")
		return
	}
	trimmed := bytes.TrimSpace(raw)
	parseError := mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "The request is not valid JSON."}}
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []mcpRequest
		if json.Unmarshal(trimmed, &batch) != nil {
			write(w, http.StatusBadRequest, parseError)
			return
		}
		if len(batch) == 0 || len(batch) > mcpMaxBatch {
			write(w, http.StatusBadRequest, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32600, Message: "Send between 1 and 16 messages at a time."}})
			return
		}
		answers := []mcpResponse{}
		for _, message := range batch {
			if answer, wanted := d.mcpHandle(r, credential, p.Role, message); wanted {
				answers = append(answers, answer)
			}
		}
		if len(answers) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		write(w, http.StatusOK, answers)
		return
	}
	var message mcpRequest
	if json.Unmarshal(trimmed, &message) != nil {
		write(w, http.StatusBadRequest, parseError)
		return
	}
	answer, wanted := d.mcpHandle(r, credential, p.Role, message)
	if !wanted {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	write(w, http.StatusOK, answer)
}

func registerMCP(registry *apikit.Registry, d Dependencies) error {
	err := apikit.RegisterRaw(registry, apikit.RawRoute[MCPMessage, MCPMessage]{Metadata: apikit.Metadata{Audience: apikit.Clients,
		Admission: laneTools, ID: "mcp", Method: "POST", Path: mcpPath, Summary: "Model Context Protocol endpoint: JSON-RPC 2.0 over Streamable HTTP, authenticated by an API key or personal access token",
		Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Cost: apikit.PageSized, BodyLimit: mcpMaxBody, Status: 200, Errors: []string{"unauthorized", "invalid_request", "body_too_large", "unsupported_media_type", "internal"},
	}, Serve: d.mcp})
	if err != nil {
		return err
	}
	// The server starts no stream of its own, which the transport lets it say
	// with 405.
	return apikit.RegisterRaw(registry, apikit.RawRoute[struct{}, MCPMessage]{Metadata: apikit.Metadata{Audience: apikit.Clients,
		Admission: laneDefault, ID: "mcp_stream", Method: "GET", Path: mcpPath, Summary: "Not offered: this MCP server answers each request directly and opens no stream",
		Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"method_not_allowed"},
	}, Serve: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "POST")
		registry.Errors().Write(w, "", &apierror.Error{Code: "method_not_allowed"})
	}})
}
