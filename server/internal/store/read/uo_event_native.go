package read

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// UO-18 native event page. This is a bounded composition component, never a
// delivery authority: the parent supplies an explicitly authorized audience,
// one snapshot-confined query closure and the current hidden-title rules.
// Command 630 is composition-only and is not registered here; 631 stays
// reserved/unregistered. The sole append/oldest-delete owner is WC's
// appendWCAPIEvent (#1267). No head, sqlite_sequence or pruner is added.

var errUOEventPageFull = errors.New("event page byte budget exhausted")

type uoStoredEvent struct {
	id, atMS                                       int64
	kind, resourceKind, resourceID, revision, data string
}
type uoWireEvent struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	At       string `json:"at"`
	Resource struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"resource"`
	Revision string          `json:"revision"`
	Data     json.RawMessage `json:"data,omitempty"`
}
type uoEventPageInput struct {
	audience             string
	after, upper, oldest int64
	limit, wireBytes     int
	binding              uoEventBinding
}
type uoEventPage struct {
	events             []uoWireEvent
	next               string
	after              int64
	exhausted          bool
	rawRows, wireBytes int
}
type uoEventQuery func(stmt.ID, ...any) ([][]any, error)

// One explicitly authorized audience stream, not a whole multi-audience feed.
// The parent composes a bounded ordered merge and current US disclosure/final
// publication. query is confined to the SAME admitted snapshot; redact receives
// values only and uses that snapshot's current content rules. No callback gets
// a handle, transaction, authority capability or SQL. Nothing here writes SSE.
func readUOEventPage(ctx context.Context, query uoEventQuery, codec *uoEventCursorCodec, q uoEventPageInput, redact func(uoStoredEvent) (uoStoredEvent, bool, error)) (out uoEventPage, err error) {
	if ctx == nil || query == nil || codec == nil || redact == nil || q.audience == "" || len(q.audience) > 512 || !utf8.ValidString(q.audience) || q.limit < 1 || q.limit > 100 || q.wireBytes < 1024 || q.wireBytes > 64<<10 {
		return out, ErrInput
	}
	if err := uoEventResumeWindow(q.after, q.oldest, q.upper); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	// Bind one extra raw row so the cap is observed, never silently assumed.
	positions, err := query(uoEventNext, q.audience, q.after, q.upper, int64(q.limit)+1)
	if err != nil {
		return out, err
	}
	more := len(positions) > q.limit
	if more {
		positions = positions[:q.limit]
	}
	out.after = q.after
	out.wireBytes = 1024
	for _, position := range positions {
		if err := ctx.Err(); err != nil {
			return uoEventPage{}, err
		}
		if len(position) != 1 {
			return uoEventPage{}, ErrUnqualified
		}
		id, ok := position[0].(int64)
		if !ok || id <= out.after || id > q.upper {
			return uoEventPage{}, ErrStale
		}
		sizes, err := query(uoEventOctets, id, q.audience)
		if err != nil {
			return uoEventPage{}, err
		}
		if len(sizes) != 1 || len(sizes[0]) != 5 {
			return uoEventPage{}, ErrStale
		}
		limits := [5]int64{64, 128, 128, 128, 4096}
		var lengths [5]int64
		for i, v := range sizes[0] {
			n, ok := v.(int64)
			if !ok || n < 0 || n > limits[i] {
				return uoEventPage{}, errUOEventResync
			}
			lengths[i] = n
		}
		rows, err := query(uoEventPoint, id, q.audience)
		if err != nil {
			return uoEventPage{}, err
		}
		if len(rows) != 1 || len(rows[0]) != 7 || rows[0][0] != id {
			return uoEventPage{}, ErrStale
		}
		values := [5]string{}
		for i := range values {
			v, ok := rows[0][i+1].(string)
			if !ok || int64(len(v)) != lengths[i] || !utf8.ValidString(v) {
				return uoEventPage{}, ErrStale
			}
			values[i] = v
		}
		at, ok := rows[0][6].(int64)
		if !ok || at < 0 {
			return uoEventPage{}, ErrUnqualified
		}
		e := uoStoredEvent{id: id, atMS: at, kind: values[0], resourceKind: values[1], resourceID: values[2], revision: values[3], data: values[4]}
		visible, keep, err := redact(e)
		if err != nil {
			return uoEventPage{}, err
		}
		if keep {
			if visible.id != id || visible.atMS != at || visible.kind == "" || len(visible.kind) > 64 || strings.ContainsAny(visible.kind, "\r\n") || len(visible.data) > 4096 || visible.data != "" && !json.Valid([]byte(visible.data)) {
				return uoEventPage{}, ErrUnqualified
			}
			for i, v := range []string{visible.kind, visible.resourceKind, visible.resourceID, visible.revision, visible.data} {
				if !utf8.ValidString(v) || int64(len(v)) > limits[i] {
					return uoEventPage{}, ErrUnqualified
				}
			}
			cursor, err := codec.encode(q.binding, id)
			if err != nil {
				return uoEventPage{}, err
			}
			wire := uoWireEvent{ID: cursor, Type: visible.kind, At: time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z"), Revision: visible.revision, Data: json.RawMessage(visible.data)}
			wire.Resource.Kind, wire.Resource.ID = visible.resourceKind, visible.resourceID
			encoded, err := json.Marshal(wire)
			if err != nil {
				return uoEventPage{}, err
			}
			cost := len("id: \nevent: \ndata: \n\n") + len(cursor) + len(wire.Type) + len(encoded)
			if cost > q.wireBytes-out.wireBytes {
				if out.rawRows == 0 {
					return uoEventPage{}, errUOEventPageFull
				}
				break
			}
			out.wireBytes += cost
			out.events = append(out.events, wire)
		}
		out.after = id
		out.rawRows++
	}
	out.exhausted = out.rawRows == len(positions) && !more
	if out.exhausted {
		out.after = q.upper
	} // Advance over other audiences without scanning them.
	out.next, err = codec.encode(q.binding, out.after)
	if err != nil {
		return uoEventPage{}, err
	}
	// Current authority must still hold when the final page token is ready.
	if err := ctx.Err(); err != nil {
		return uoEventPage{}, err
	}
	return out, nil
}

const (
	uoEventNext   stmt.ID = "read.uo.events.next"
	uoEventOctets stmt.ID = "read.uo.events.octets"
	uoEventPoint  stmt.ID = "read.uo.events.point"
)

func uoEventDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	d := func(id stmt.ID, query string, params []stmt.Param, cohorts []stmt.Cohort, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: id, Kind: stmt.Read, SQL: query, Params: params, Cohorts: cohorts, Owner: "store/read UO events", CallSites: []string{"store/read/uo_event_native.go"}, Plan: p}
	}
	point := []stmt.Param{i("id"), s("audience")}
	return []stmt.Definition{
		d(uoEventNext, `SELECT id FROM api_events INDEXED BY api_events_audience WHERE audience=? AND id>? AND id<=? ORDER BY id LIMIT ?`, []stmt.Param{s("audience"), i("after"), i("upper"), i("limit")}, []stmt.Cohort{{Name: "own_audience_window", Bindings: []any{"p", int64(0), int64(3), int64(101)}}}, stmt.Plan{Hot: true, Indexes: []string{"api_events_audience"}}),
		d(uoEventOctets, `SELECT octet_length(type),octet_length(resource_kind),octet_length(resource_id),octet_length(revision),octet_length(data) FROM api_events WHERE id=? AND audience=?`, point, []stmt.Cohort{{Name: "point", Bindings: []any{int64(1), "p"}}}, stmt.Plan{Hot: true, PrimaryKeys: []string{"api_events"}}),
		d(uoEventPoint, `SELECT id,type,resource_kind,resource_id,revision,data,at_ms FROM api_events WHERE id=? AND audience=?`, point, []stmt.Cohort{{Name: "point", Bindings: []any{int64(1), "p"}}}, stmt.Plan{Hot: true, PrimaryKeys: []string{"api_events"}}),
	}
}

// UOEventDefinitions returns the inert UO event page and feed definitions for
// explicit harness composition alongside HarnessDefinitions. Capture them on
// the actual reader before registration; the product does not select this
// layer before cutover. Every seek is a keyed SEARCH on the api_events PK or
// the (audience,id) index; nothing scans or sorts into a temporary B-tree.
func UOEventDefinitions() []stmt.Definition {
	return append(uoEventDefinitions(), uoEventFeedDefinitions()...)
}

// Current content checks are supplied by the admitted composition, never by a
// client or retained event. A hidden member removes the ENTIRE title path so a
// visible suffix cannot imply that unrelated authorized pages may skip refresh.
func redactUOEventTitles(e uoStoredEvent, allowed func(string) bool) (uoStoredEvent, bool, error) {
	if allowed == nil {
		return uoStoredEvent{}, false, ErrDenied
	}
	switch e.resourceKind {
	case "item", "title", "movie", "show", "season", "episode", "artist", "album", "song", "book", "audiobook_file", "collection", "extra", "disc", "author", "chapter", "ebook", "comic":
		if !allowed(e.resourceID) {
			return e, false, nil
		}
	}
	if e.data == "" {
		return e, true, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(e.data), &fields); err != nil {
		return uoStoredEvent{}, false, ErrUnqualified
	}
	raw, present := fields["titles"]
	if !present {
		return e, true, nil
	}
	var titles []string
	visible := json.Unmarshal(raw, &titles) == nil && len(titles) > 0
	if visible {
		for _, id := range titles {
			if !allowed(id) {
				visible = false
				break
			}
		}
	}
	if visible {
		return e, true, nil
	}
	delete(fields, "titles")
	encoded, err := json.Marshal(fields)
	if err != nil {
		return uoStoredEvent{}, false, err
	}
	e.data = string(encoded)
	return e, true, nil
}

// Serialization is outside the snapshot and under the parent's original
// output/network deadline. A frame carries no open SQL/resource custody.
func encodeUOSSEFrame(e uoWireEvent) ([]byte, error) {
	if e.ID == "" || len(e.ID) > 256 || strings.ContainsAny(e.ID, "\r\n") || e.Type == "" || len(e.Type) > 64 || strings.ContainsAny(e.Type, "\r\n") || len(e.Data) > 4096 {
		return nil, ErrInput
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	frame := []byte("id: " + e.ID + "\nevent: " + e.Type + "\ndata: " + string(encoded) + "\n\n")
	if len(frame) > 64<<10 {
		return nil, ErrUnqualified
	}
	return frame, nil
}
