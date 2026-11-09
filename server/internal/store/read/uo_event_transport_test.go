package read

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// UO-19 transport controls. Timing is entirely on the testing/synctest clock:
// no assertion here depends on real elapsed time. The ring is a plain in-memory
// oracle for the UO-18 statement set, so transport custody tests never hold a
// real SQL snapshot open across a park.

type uoEventTransportRow struct {
	id, atMS                                                 int64
	audience, kind, resourceKind, resourceID, revision, data string
}

type uoEventTransportRing struct {
	mu    sync.Mutex
	rows  []uoEventTransportRow
	calls []stmt.ID
}

func (r *uoEventTransportRing) insert(row uoEventTransportRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	at := sort.Search(len(r.rows), func(i int) bool { return r.rows[i].id >= row.id })
	r.rows = append(r.rows, uoEventTransportRow{})
	copy(r.rows[at+1:], r.rows[at:])
	r.rows[at] = row
}
func (r *uoEventTransportRing) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}
func (r *uoEventTransportRing) statements() []stmt.ID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stmt.ID(nil), r.calls...)
}

func uoEventTransportFind(rows []uoEventTransportRow, id int64) (uoEventTransportRow, bool) {
	i := sort.Search(len(rows), func(n int) bool { return rows[n].id >= id })
	if i < len(rows) && rows[i].id == id {
		return rows[i], true
	}
	return uoEventTransportRow{}, false
}

func (r *uoEventTransportRing) query(id stmt.ID, args ...any) ([][]any, error) {
	r.mu.Lock()
	r.calls = append(r.calls, id)
	rows := append([]uoEventTransportRow(nil), r.rows...)
	r.mu.Unlock()
	at := func(id int64) int { return sort.Search(len(rows), func(i int) bool { return rows[i].id >= id }) }
	switch id {
	case uoEventOldest:
		minimum := args[0].(int64)
		if i := at(minimum); i < len(rows) {
			return [][]any{{rows[i].id}}, nil
		}
	case uoEventNewest:
		maximum := args[0].(int64)
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i].id <= maximum {
				return [][]any{{rows[i].id}}, nil
			}
		}
	case uoEventRawWindow:
		after, upper, limit := args[0].(int64), args[1].(int64), args[2].(int64)
		var out [][]any
		for i := at(after + 1); i < len(rows) && int64(len(out)) < limit && rows[i].id <= upper; i++ {
			out = append(out, []any{rows[i].id})
		}
		return out, nil
	case uoEventAudienceOctets:
		if row, ok := uoEventTransportFind(rows, args[0].(int64)); ok {
			return [][]any{{int64(len(row.audience))}}, nil
		}
	case uoEventAudiencePoint:
		if row, ok := uoEventTransportFind(rows, args[0].(int64)); ok {
			return [][]any{{row.audience}}, nil
		}
	case uoEventNext:
		audience, after, upper, limit := args[0].(string), args[1].(int64), args[2].(int64), args[3].(int64)
		var out [][]any
		for i := at(after + 1); i < len(rows) && int64(len(out)) < limit && rows[i].id <= upper; i++ {
			if rows[i].audience == audience {
				out = append(out, []any{rows[i].id})
			}
		}
		return out, nil
	case uoEventOctets:
		if row, ok := uoEventTransportFind(rows, args[0].(int64)); ok && row.audience == args[1].(string) {
			return [][]any{{int64(len(row.kind)), int64(len(row.resourceKind)), int64(len(row.resourceID)), int64(len(row.revision)), int64(len(row.data))}}, nil
		}
	case uoEventPoint:
		if row, ok := uoEventTransportFind(rows, args[0].(int64)); ok && row.audience == args[1].(string) {
			return [][]any{{row.id, row.kind, row.resourceKind, row.resourceID, row.revision, row.data, row.atMS}}, nil
		}
	default:
		return nil, errors.New("unexpected read statement")
	}
	return nil, nil
}

type uoEventTransportSink struct {
	mu     sync.Mutex
	frames [][]byte
}

func (s *uoEventTransportSink) emit(frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, append([]byte(nil), frame...))
	return nil
}
func (s *uoEventTransportSink) snapshot() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.frames...)
}
func (s *uoEventTransportSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func uoEventTransportFrameID(t *testing.T, frame []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(frame), "\n") {
		if rest, ok := strings.CutPrefix(line, "id: "); ok {
			return rest
		}
	}
	t.Fatalf("frame carries no id line: %q", string(frame))
	return ""
}
func uoEventTransportFrameIs(frame []byte, event string) bool {
	return bytes.Contains(frame, []byte("event: "+event+"\n"))
}
func uoEventTransportFrameHasType(frames [][]byte, event string) bool {
	for _, frame := range frames {
		if uoEventTransportFrameIs(frame, event) {
			return true
		}
	}
	return false
}

func uoEventTransportTestInput(hub *uoEventWakeHub, codec *uoEventCursorCodec, ring *uoEventTransportRing, binding uoEventBinding, cursor string, sink *uoEventTransportSink) uoEventTransportInput {
	return uoEventTransportInput{
		hub: hub, codec: codec, query: ring.query,
		authority: func(context.Context) (uoEventBinding, bool, error) { return binding, true, nil },
		addressed: func(a string) bool { return a == "p" },
		redact:    func(_ string, e uoStoredEvent) (uoStoredEvent, bool, error) { return e, true, nil },
		emit:      sink.emit, now: time.Now,
		audience: "p", cursor: cursor, binding: binding,
		config: uoEventTransportConfig{heartbeat: 10 * time.Second, life: 10 * time.Minute, turns: 64},
	}
}

func TestUOEventTransportOpenedPositionClosedAndResync(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := &uoEventWakeHub{}
		codec, err := newUOEventCursorCodec()
		if err != nil {
			t.Fatal(err)
		}
		binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
		ring := &uoEventTransportRing{}
		ring.insert(uoEventTransportRow{id: 5, atMS: 1, audience: "p", kind: "personal.changed", data: "{}"})
		sink := &uoEventTransportSink{}
		in := uoEventTransportTestInput(hub, codec, ring, binding, "", sink)
		in.config = uoEventTransportConfig{heartbeat: 10 * time.Second, life: 61 * time.Second, turns: 16}
		done := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { done <- serveUOEventTransport(ctx, in) }()

		// Opening at head says where the stream stands: opened then position,
		// both carrying the opaque high-water token and nothing else.
		synctest.Wait()
		frames := sink.snapshot()
		if len(frames) != 2 || !uoEventTransportFrameIs(frames[0], "stream.opened") || !uoEventTransportFrameIs(frames[1], "stream.position") {
			t.Fatalf("opening frames %q", frames)
		}
		for _, frame := range frames {
			id := uoEventTransportFrameID(t, frame)
			if len(id) > uoEventTransportRequest {
				t.Fatalf("cursor %d bytes exceeds the request cap", len(id))
			}
			if position, err := codec.decode(id, binding); err != nil || position != 5 {
				t.Fatalf("opening token %d %v", position, err)
			}
		}
		hub.mu.Lock()
		onlyCurrent := len(hub.audiences) == 1 && hub.audiences["p"] != nil
		hub.mu.Unlock()
		if !onlyCurrent {
			t.Fatal("transport subscribed outside its own audience")
		}

		// A bounded heartbeat recheck with nothing new still reports position.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		frames = sink.snapshot()
		if len(frames) != 3 || !uoEventTransportFrameIs(frames[2], "stream.position") {
			t.Fatalf("heartbeat frames %q", frames)
		}

		// The life deadline closes the stream and releases every resource.
		time.Sleep(51 * time.Second)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		frames = sink.snapshot()
		if len(frames) == 0 || !uoEventTransportFrameIs(frames[len(frames)-1], "stream.closed") {
			t.Fatalf("closing frames %q", frames)
		}
		if !uoEventTransportFrameHasType(frames, "stream.opened") {
			t.Fatal("opened control lost")
		}
		hub.mu.Lock()
		left := len(hub.audiences)
		hub.mu.Unlock()
		if left != 0 {
			t.Fatal("subscription not physically released", left)
		}

		// A cursor whose floor aged out resyncs instead of claiming emptiness.
		resyncRing := &uoEventTransportRing{}
		resyncRing.insert(uoEventTransportRow{id: 6, atMS: 2, audience: "p", kind: "first", data: "{}"})
		resyncRing.insert(uoEventTransportRow{id: 7, atMS: 3, audience: "p", kind: "second", data: "{}"})
		resyncCursor, err := codec.encode(binding, 0)
		if err != nil {
			t.Fatal(err)
		}
		resyncSink := &uoEventTransportSink{}
		resyncIn := uoEventTransportTestInput(hub, codec, resyncRing, binding, resyncCursor, resyncSink)
		resyncDone := make(chan error, 1)
		go func() { resyncDone <- serveUOEventTransport(ctx, resyncIn) }()
		synctest.Wait()
		resyncFrames := resyncSink.snapshot()
		if len(resyncFrames) != 1 || !uoEventTransportFrameIs(resyncFrames[0], "stream.resync") {
			t.Fatalf("resync frames %q", resyncFrames)
		}
		if position, err := codec.decode(uoEventTransportFrameID(t, resyncFrames[0]), binding); err != nil || position != 7 {
			t.Fatalf("resync token %d %v", position, err)
		}
		cancel()
		if err := <-resyncDone; !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation was not clean", err)
		}
		hub.mu.Lock()
		left = len(hub.audiences)
		hub.mu.Unlock()
		if left != 0 {
			t.Fatal("canceled subscription retained", left)
		}
	})
}

func TestUOEventTransportHeartbeatRecoversLostWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := &uoEventWakeHub{}
		codec, err := newUOEventCursorCodec()
		if err != nil {
			t.Fatal(err)
		}
		binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
		ring := &uoEventTransportRing{}
		ring.insert(uoEventTransportRow{id: 1, atMS: 1, audience: "p", kind: "first", data: "{}"})
		sink := &uoEventTransportSink{}
		in := uoEventTransportTestInput(hub, codec, ring, binding, "", sink)
		done := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { done <- serveUOEventTransport(ctx, in) }()
		synctest.Wait()
		if sink.count() != 2 {
			t.Fatalf("opening frames %d", sink.count())
		}

		// The row is durable but its publish never arrives. No SQL may run while
		// the transport is parked, and nothing may be emitted on its own.
		parked := ring.callCount()
		ring.insert(uoEventTransportRow{id: 2, atMS: 2, audience: "p", kind: "second", data: "{}"})
		synctest.Wait()
		if ring.callCount() != parked {
			t.Fatal("transport queried SQL while parked")
		}
		if sink.count() != 2 {
			t.Fatalf("parked transport emitted %d", sink.count())
		}

		// The bounded heartbeat is the safety recheck that recovers the loss.
		time.Sleep(in.config.heartbeat)
		synctest.Wait()
		frames := sink.snapshot()
		found := false
		for _, frame := range frames {
			if uoEventTransportFrameIs(frame, "second") {
				found = true
				if position, err := codec.decode(uoEventTransportFrameID(t, frame), binding); err != nil || position != 2 {
					t.Fatalf("delivered token %d %v", position, err)
				}
			}
		}
		if !found {
			t.Fatalf("lost wake not recovered: %q", frames)
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}

		// Read/park race: a publish lands while this turn's read is still
		// running. The generation test must turn it into an immediate second
		// read turn, with no clock advance and no lost wake.
		raceRing := &uoEventTransportRing{}
		raceRing.insert(uoEventTransportRow{id: 11, atMS: 1, audience: "p", kind: "first", data: "{}"})
		raceRing.insert(uoEventTransportRow{id: 12, atMS: 2, audience: "p", kind: "second", data: "{}"})
		raceCursor, err := codec.encode(binding, 11)
		if err != nil {
			t.Fatal(err)
		}
		raceSink := &uoEventTransportSink{}
		raceIn := uoEventTransportTestInput(hub, codec, raceRing, binding, raceCursor, raceSink)
		turns := 0
		published := false
		raceIn.query = func(id stmt.ID, args ...any) ([][]any, error) {
			if id == uoEventOldest {
				turns++
			}
			out, err := raceRing.query(id, args...)
			if id == uoEventPoint && !published {
				published = true
				hub.publish("p")
			}
			return out, err
		}
		raceCtx, raceCancel := context.WithCancel(context.Background())
		raceDone := make(chan error, 1)
		go func() { raceDone <- serveUOEventTransport(raceCtx, raceIn) }()
		synctest.Wait()
		if !published {
			t.Fatal("race control never published")
		}
		if turns != 2 {
			t.Fatalf("read-park race parked through the publish: %d read turns", turns)
		}
		if !uoEventTransportFrameHasType(raceSink.snapshot(), "second") {
			t.Fatal("race turn did not deliver its queued hint")
		}
		raceCancel()
		if err := <-raceDone; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestUOEventTransportRevokeDropsQueuedHints(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := &uoEventWakeHub{}
		codec, err := newUOEventCursorCodec()
		if err != nil {
			t.Fatal(err)
		}
		binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
		ring := &uoEventTransportRing{}
		ring.insert(uoEventTransportRow{id: 1, atMS: 1, audience: "p", kind: "first", data: "{}"})
		ring.insert(uoEventTransportRow{id: 2, atMS: 2, audience: "p", kind: "second", data: "{}"})
		cursor, err := codec.encode(binding, 0)
		if err != nil {
			t.Fatal(err)
		}
		sink := &uoEventTransportSink{}
		in := uoEventTransportTestInput(hub, codec, ring, binding, cursor, sink)
		checks := 0
		in.authority = func(context.Context) (uoEventBinding, bool, error) {
			checks++
			return binding, checks < 3, nil
		}
		done := make(chan error, 1)
		go func() { done <- serveUOEventTransport(context.Background(), in) }()
		if err := <-done; !errors.Is(err, errUOEventTransportRevoked) {
			t.Fatal(err)
		}
		frames := sink.snapshot()
		if len(frames) != 1 {
			t.Fatalf("revocation did not drop queued hints and sent %d frames", len(frames))
		}
		if !uoEventTransportFrameIs(frames[0], "first") || uoEventTransportFrameIs(frames[0], "second") {
			t.Fatalf("stale-frame control %q", frames[0])
		}
		if uoEventTransportFrameHasType(frames, "stream.closed") {
			t.Fatal("invented a closing frame for a stale principal")
		}
		hub.mu.Lock()
		left := len(hub.audiences)
		hub.mu.Unlock()
		if left != 0 {
			t.Fatal("revoked subscription retained", left)
		}

		// Cancellation while parked is clean and emits nothing new.
		quietRing := &uoEventTransportRing{}
		quietRing.insert(uoEventTransportRow{id: 9, atMS: 9, audience: "p", kind: "hint", data: "{}"})
		quietSink := &uoEventTransportSink{}
		quietIn := uoEventTransportTestInput(hub, codec, quietRing, binding, "", quietSink)
		ctx, cancel := context.WithCancel(context.Background())
		quietDone := make(chan error, 1)
		go func() { quietDone <- serveUOEventTransport(ctx, quietIn) }()
		synctest.Wait()
		before := quietSink.count()
		cancel()
		if err := <-quietDone; !errors.Is(err, context.Canceled) {
			t.Fatal("canceled subscription", err)
		}
		if quietSink.count() != before {
			t.Fatal("frames emitted after cancellation")
		}
		hub.mu.Lock()
		left = len(hub.audiences)
		hub.mu.Unlock()
		if left != 0 {
			t.Fatal("canceled subscription retained", left)
		}
	})
}

func TestUOEventTransportBoundedTurnsNoEnumerationOrPolling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := &uoEventWakeHub{}
		codec, err := newUOEventCursorCodec()
		if err != nil {
			t.Fatal(err)
		}
		binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
		ring := &uoEventTransportRing{}
		ring.insert(uoEventTransportRow{id: 1, atMS: 1, audience: "p", kind: "first", data: "{}"})
		sink := &uoEventTransportSink{}
		in := uoEventTransportTestInput(hub, codec, ring, binding, "", sink)
		in.config.turns = 2
		done := make(chan error, 1)
		go func() { done <- serveUOEventTransport(context.Background(), in) }()
		synctest.Wait()
		if sink.count() != 2 {
			t.Fatalf("opening frames %d", sink.count())
		}

		// Another audience's publish is invisible: the hub exposes one channel
		// per admitted audience and nothing enumerates subscribers.
		parked := ring.callCount()
		hub.publish("q")
		synctest.Wait()
		if ring.callCount() != parked || sink.count() != 2 {
			t.Fatal("foreign audience woke the transport")
		}

		// Its own wake costs one read turn, then the turn budget closes once.
		hub.publish("p")
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		frames := sink.snapshot()
		if !uoEventTransportFrameIs(frames[len(frames)-1], "stream.closed") {
			t.Fatalf("budget frames %q", frames)
		}
		closed := 0
		for _, frame := range frames {
			if uoEventTransportFrameIs(frame, "stream.closed") {
				closed++
			}
		}
		if closed != 1 {
			t.Fatalf("closing controls %d", closed)
		}
		if ring.callCount() <= parked {
			t.Fatal("wake did not re-read")
		}
		known := map[stmt.ID]bool{
			uoEventOldest: true, uoEventNewest: true, uoEventRawWindow: true,
			uoEventAudienceOctets: true, uoEventAudiencePoint: true,
			uoEventNext: true, uoEventOctets: true, uoEventPoint: true,
		}
		for _, id := range ring.statements() {
			if !known[id] {
				t.Fatalf("transport issued non-read statement %q", id)
			}
		}
		hub.mu.Lock()
		left := len(hub.audiences)
		hub.mu.Unlock()
		if left != 0 {
			t.Fatal("finished transport retained state", left)
		}
	})
}

func TestUOEventTransportForeignCursorResyncs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := &uoEventWakeHub{}
		codec, err := newUOEventCursorCodec()
		if err != nil {
			t.Fatal(err)
		}
		binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
		foreign := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{9}, audience: [32]byte{3}}
		ring := &uoEventTransportRing{}
		ring.insert(uoEventTransportRow{id: 6, atMS: 2, audience: "p", kind: "first", data: "{}"})
		ring.insert(uoEventTransportRow{id: 7, atMS: 3, audience: "p", kind: "second", data: "{}"})
		cursor, err := codec.encode(foreign, 0)
		if err != nil {
			t.Fatal(err)
		}
		sink := &uoEventTransportSink{}
		in := uoEventTransportTestInput(hub, codec, ring, binding, cursor, sink)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- serveUOEventTransport(ctx, in) }()
		synctest.Wait()
		frames := sink.snapshot()
		if len(frames) != 1 || !uoEventTransportFrameIs(frames[0], "stream.resync") {
			t.Fatalf("foreign cursor frames %q", frames)
		}
		token := uoEventTransportFrameID(t, frames[0])
		if len(token) > uoEventTransportRequest {
			t.Fatalf("cursor %d bytes exceeds the request cap", len(token))
		}
		if position, err := codec.decode(token, binding); err != nil || position != 7 {
			t.Fatalf("current binding token %d %v", position, err)
		}
		if _, err := codec.decode(token, foreign); !errors.Is(err, errUOEventResync) {
			t.Fatal("foreign binding reused the resync token", err)
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestUOEventTransportFixturePagesWithRealSQL(t *testing.T) {
	// The UO-18 fixture is the independent oracle here: plain SQL row shapes,
	// a foreign audience with 4097 payload bytes, and a bounded one-turn
	// transport that never parks, so nothing is timing dependent.
	_, query := uoEventFeedFixture(t)
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
	cursor, err := codec.encode(binding, 0)
	if err != nil {
		t.Fatal(err)
	}
	foreignPayload := false
	guard := func(id stmt.ID, args ...any) ([][]any, error) {
		if (id == uoEventPoint || id == uoEventOctets) && len(args) > 0 && args[0] == int64(2) {
			foreignPayload = true
		}
		return query(id, args...)
	}
	sink := &uoEventTransportSink{}
	in := uoEventTransportInput{
		hub: &uoEventWakeHub{}, codec: codec, query: guard,
		authority: func(context.Context) (uoEventBinding, bool, error) { return binding, true, nil },
		addressed: func(a string) bool { return a == "p" },
		redact:    func(_ string, e uoStoredEvent) (uoStoredEvent, bool, error) { return e, true, nil },
		emit:      sink.emit, now: time.Now,
		audience: "p", cursor: cursor, binding: binding,
		config: uoEventTransportConfig{heartbeat: time.Second, life: 2 * time.Second, turns: 1},
	}
	if err := serveUOEventTransport(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	frames := sink.snapshot()
	if len(frames) == 0 || !uoEventTransportFrameIs(frames[0], "personal.changed") {
		t.Fatalf("fixture frames %q", frames)
	}
	if !uoEventTransportFrameIs(frames[len(frames)-1], "stream.closed") {
		t.Fatalf("fixture frames %q", frames)
	}
	if foreignPayload {
		t.Fatal("foreign payload was read")
	}
	for _, frame := range frames {
		if bytes.Contains(frame, []byte("secret")) || bytes.Contains(frame, []byte(strings.Repeat("x", 4097))) {
			t.Fatalf("foreign payload copied: %q", frame)
		}
	}
	if position, err := codec.decode(uoEventTransportFrameID(t, frames[0]), binding); err != nil || position != 1 {
		t.Fatalf("fixture token %d %v", position, err)
	}
}

func TestUOEventTransportRejectsUnboundedConfiguration(t *testing.T) {
	hub := &uoEventWakeHub{}
	codec, err := newUOEventCursorCodec()
	if err != nil {
		t.Fatal(err)
	}
	binding := uoEventBinding{database: [16]byte{1}, viewer: [32]byte{2}, audience: [32]byte{3}}
	ring := &uoEventTransportRing{}
	for name, mutate := range map[string]func(*uoEventTransportInput){
		"negative heartbeat":     func(in *uoEventTransportInput) { in.config.heartbeat = -time.Second },
		"heartbeat after life":   func(in *uoEventTransportInput) { in.config.heartbeat = 2 * time.Minute; in.config.life = time.Minute },
		"negative turns":         func(in *uoEventTransportInput) { in.config.turns = -1 },
		"turn budget above max":  func(in *uoEventTransportInput) { in.config.turns = uoEventTransportTurns + 1 },
		"lifetime above max":     func(in *uoEventTransportInput) { in.config.life = 25 * time.Hour; in.config.heartbeat = time.Minute },
		"oversize cursor":        func(in *uoEventTransportInput) { in.cursor = strings.Repeat("e", uoEventTransportRequest+1) },
		"missing write binding":  func(in *uoEventTransportInput) { in.binding = uoEventBinding{} },
		"missing current source": func(in *uoEventTransportInput) { in.authority = nil },
	} {
		t.Run(name, func(t *testing.T) {
			sink := &uoEventTransportSink{}
			in := uoEventTransportTestInput(hub, codec, ring, binding, "", sink)
			mutate(&in)
			if err := serveUOEventTransport(context.Background(), in); !errors.Is(err, ErrInput) {
				t.Fatalf("unbounded configuration accepted: %v", err)
			}
		})
	}
	if len(hub.audiences) != 0 {
		t.Fatal("rejected configuration subscribed")
	}
}
