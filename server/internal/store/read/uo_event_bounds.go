package read

import (
	"context"
	"errors"
	"math"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// UO-18 bounds and complete raw-window composition. WC's sole
// append/oldest-delete owner (#1267, appendWCAPIEvent) retains the newest row
// and deletes at most one oldest eligible full-PK row per append. Gaps are
// legal. There is no sqlite_sequence read, new head or pruner; command 631
// stays reserved/unregistered and 630 is composition-only. An empty-ring
// restore MUST issue a new database binding before serving cursors.

type uoEventFeedInput struct {
	cursor  string
	nowMS   int64
	binding uoEventBinding
}
type uoEventFeedPage struct {
	events             []uoWireEvent
	next               string
	more               bool
	rawRows, wireBytes int
}

// A complete raw-window fold across audiences, preserving the public feed's
// global 100-row lag policy. Bounds and both gates are from the same admitted
// current-authority snapshot. The fixed ID query copies no audience or payload;
// an unaddressed row never copies its payload. A final US publication fence is
// still required before releasing this owned page to the transport.
func readUOEventFeed(ctx context.Context, query uoEventQuery, codec *uoEventCursorCodec, q uoEventFeedInput, addressed func(string) bool, redact func(string, uoStoredEvent) (uoStoredEvent, bool, error)) (out uoEventFeedPage, err error) {
	if ctx == nil || query == nil || codec == nil || addressed == nil || redact == nil || q.nowMS <= 0 {
		return out, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	oldest, highWater, err := readUOEventBounds(ctx, query)
	if err != nil {
		return out, err
	}
	out.events = []uoWireEvent{}
	out.wireBytes = 1024
	out.next, err = codec.encode(q.binding, highWater)
	if err != nil {
		return uoEventFeedPage{}, err
	}
	resync := func() (uoEventFeedPage, error) {
		e := uoEventControl(out.next, "stream.resync", q.nowMS)
		e.Resource.Kind, e.Resource.ID = "stream", "all"
		return uoEventFeedPage{events: []uoWireEvent{e}, next: out.next, wireBytes: 1024}, nil
	}
	if q.cursor == "" {
		return out, nil
	}
	after, err := codec.decode(q.cursor, q.binding)
	if err != nil {
		return resync()
	}
	if err := uoEventResumeWindow(after, oldest, highWater); err != nil {
		return resync()
	}
	// One extra raw row detects excess lag, which resyncs instead of returning
	// a silently incomplete page.
	positions, err := query(uoEventRawWindow, after, highWater, int64(101))
	if err != nil {
		return uoEventFeedPage{}, err
	}
	if len(positions) > 100 {
		return resync()
	}
	consumed := after
	for _, row := range positions {
		if err := ctx.Err(); err != nil {
			return uoEventFeedPage{}, err
		}
		if len(row) != 1 {
			return uoEventFeedPage{}, ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= consumed || id > highWater {
			return uoEventFeedPage{}, ErrStale
		}
		sizes, err := query(uoEventAudienceOctets, id)
		if err != nil {
			return uoEventFeedPage{}, err
		}
		if len(sizes) != 1 || len(sizes[0]) != 1 {
			return uoEventFeedPage{}, ErrStale
		}
		n, ok := sizes[0][0].(int64)
		if !ok || n < 1 || n > 512 {
			return resync()
		}
		audiences, err := query(uoEventAudiencePoint, id)
		if err != nil {
			return uoEventFeedPage{}, err
		}
		if len(audiences) != 1 || len(audiences[0]) != 1 {
			return uoEventFeedPage{}, ErrStale
		}
		audience, ok := audiences[0][0].(string)
		if !ok || int64(len(audience)) != n || !utf8.ValidString(audience) {
			return uoEventFeedPage{}, ErrStale
		}
		if !addressed(audience) {
			consumed = id
			out.rawRows++
			continue
		}
		page, err := readUOEventPage(ctx, query, codec, uoEventPageInput{audience: audience, after: id - 1, upper: id, oldest: oldest, limit: 1, wireBytes: (64 << 10) - out.wireBytes + 1024, binding: q.binding}, func(e uoStoredEvent) (uoStoredEvent, bool, error) { return redact(audience, e) })
		if errors.Is(err, errUOEventResync) {
			return resync()
		}
		if errors.Is(err, errUOEventPageFull) && out.rawRows > 0 {
			out.more = true
			break
		}
		if err != nil {
			return uoEventFeedPage{}, err
		}
		if page.rawRows != 1 || page.after != id {
			return uoEventFeedPage{}, ErrStale
		}
		out.events = append(out.events, page.events...)
		out.wireBytes += page.wireBytes - 1024
		consumed = id
		out.rawRows++
	}
	if !out.more {
		consumed = highWater
	}
	if len(out.events) > 0 && out.events[len(out.events)-1].ID != "" && consumed == highWater {
		// Reuse exactly the last token only when it represents the final raw ID.
		last, err := codec.decode(out.events[len(out.events)-1].ID, q.binding)
		if err == nil && last == consumed {
			out.next = out.events[len(out.events)-1].ID
			return out, nil
		}
	}
	out.next, err = codec.encode(q.binding, consumed)
	if err != nil {
		return uoEventFeedPage{}, err
	}
	// No page bytes leave after authority/cancellation loss.
	if err := ctx.Err(); err != nil {
		return uoEventFeedPage{}, err
	}
	return out, nil
}

func uoEventControl(cursor, kind string, nowMS int64) uoWireEvent {
	return uoWireEvent{ID: cursor, Type: kind, At: time.UnixMilli(nowMS).UTC().Format("2006-01-02T15:04:05.000Z")}
}

// opened/position/closed are transport controls outside SQL custody. The parent
// enforces installation/stream limits, current authority and its original output
// deadline. A closed stream gets no further queued page after authority loss.
func uoEventStreamFrames(page uoEventFeedPage, opened, closed, authorityChanged bool, nowMS int64) ([][]byte, error) {
	if page.next == "" || nowMS <= 0 {
		return nil, ErrInput
	}
	var frames [][]byte
	appendFrame := func(e uoWireEvent) error {
		b, err := encodeUOSSEFrame(e)
		if err != nil {
			return err
		}
		frames = append(frames, b)
		return nil
	}
	if authorityChanged {
		e := uoEventControl(page.next, "stream.closed", nowMS)
		e.Data = []byte(`{"reason":"authority_changed","reconnectAfterMs":1000}`)
		if err := appendFrame(e); err != nil {
			return nil, err
		}
		return frames, nil
	}
	if opened {
		if err := appendFrame(uoEventControl(page.next, "stream.opened", nowMS)); err != nil {
			return nil, err
		}
	}
	for _, e := range page.events {
		if err := appendFrame(e); err != nil {
			return nil, err
		}
	}
	if len(page.events) == 0 || page.events[len(page.events)-1].ID != page.next {
		if err := appendFrame(uoEventControl(page.next, "stream.position", nowMS)); err != nil {
			return nil, err
		}
	}
	if closed {
		e := uoEventControl(page.next, "stream.closed", nowMS)
		e.Data = []byte(`{"reconnectAfterMs":1000}`)
		if err := appendFrame(e); err != nil {
			return nil, err
		}
	}
	total := 0
	for _, frame := range frames {
		total += len(frame)
	}
	if total > 64<<10 {
		return nil, ErrUnqualified
	}
	return frames, nil
}

const (
	uoEventOldest         stmt.ID = "read.uo.events.oldest"
	uoEventNewest         stmt.ID = "read.uo.events.newest"
	uoEventRawWindow      stmt.ID = "read.uo.events.raw_window"
	uoEventAudienceOctets stmt.ID = "read.uo.events.audience_octets"
	uoEventAudiencePoint  stmt.ID = "read.uo.events.audience_point"
)

func uoEventFeedDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	d := func(id stmt.ID, sql string, params []stmt.Param, cohorts []stmt.Cohort, p ...string) stmt.Definition {
		plan := stmt.Plan{Hot: true, PrimaryKeys: []string{"api_events"}}
		if len(p) > 0 {
			plan = stmt.Plan{Hot: true, Indexes: p}
		}
		return stmt.Definition{ID: id, Kind: stmt.Read, SQL: sql, Params: params, Cohorts: cohorts, Owner: "store/read UO event feed", CallSites: []string{"store/read/uo_event_bounds.go"}, Plan: plan}
	}
	return []stmt.Definition{
		d(uoEventOldest, `SELECT id FROM api_events WHERE id>=? ORDER BY id LIMIT 1`, []stmt.Param{i("minimum")}, []stmt.Cohort{{Name: "retained_floor", Bindings: []any{int64(1)}}}),
		d(uoEventNewest, `SELECT id FROM api_events WHERE id<=? ORDER BY id DESC LIMIT 1`, []stmt.Param{i("maximum")}, []stmt.Cohort{{Name: "high_water", Bindings: []any{int64(3)}}}),
		d(uoEventRawWindow, `SELECT id FROM api_events WHERE id>? AND id<=? ORDER BY id LIMIT ?`, []stmt.Param{i("after"), i("upper"), i("limit")}, []stmt.Cohort{{Name: "raw_window", Bindings: []any{int64(0), int64(3), int64(101)}}}),
		d(uoEventAudienceOctets, `SELECT octet_length(audience) FROM api_events WHERE id=?`, []stmt.Param{i("id")}, []stmt.Cohort{{Name: "point", Bindings: []any{int64(1)}}}),
		d(uoEventAudiencePoint, `SELECT audience FROM api_events WHERE id=?`, []stmt.Param{i("id")}, []stmt.Cohort{{Name: "point", Bindings: []any{int64(1)}}}),
	}
}

// WC's sole append/oldest-delete owner ALWAYS retains the newest row. Command
// 631 stays reserved: no standalone pruner and no sqlite_sequence access. An
// empty-ring restore MUST issue a new database binding before serving cursors.
func readUOEventBounds(ctx context.Context, query uoEventQuery) (oldest, highWater int64, err error) {
	if ctx == nil || query == nil {
		return 0, 0, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	low, err := query(uoEventOldest, int64(1))
	if err != nil {
		return 0, 0, err
	}
	high, err := query(uoEventNewest, int64(math.MaxInt64))
	if err != nil {
		return 0, 0, err
	}
	if len(low) == 0 && len(high) == 0 {
		return 0, 0, ctx.Err()
	}
	if len(low) != 1 || len(high) != 1 || len(low[0]) != 1 || len(high[0]) != 1 {
		return 0, 0, ErrUnqualified
	}
	oldest, ok1 := low[0][0].(int64)
	highWater, ok2 := high[0][0].(int64)
	if !ok1 || !ok2 || oldest <= 0 || highWater < oldest {
		return 0, 0, ErrUnqualified
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return oldest, highWater, nil
}
