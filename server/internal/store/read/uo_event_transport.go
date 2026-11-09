package read

import (
	"context"
	"errors"
	"reflect"
	"time"
	"unicode/utf8"
)

// UO-19 event transport custody. This is an unwired, bounded harness, not the
// product HTTP adapter (phase 4). It composes exactly three merged read
// components: UO-18's bounded feed/page and frame builder, and #1393's
// generation-keyed wake hub plus process-local cursor codec. It adds no SQL,
// no authority recipe, no writer-core protocol and no registration.
//
// Custody rules:
//   - Subscribe BEFORE the first read and observe the wake generation before
//     EVERY read, then re-test that generation immediately before parking. A
//     publish that lands while the durable read runs therefore costs one extra
//     read turn, never a lost wake. The wake is subscribed to every addressed
//     audience key, so a hint for any audience this principal is addressed by
//     wakes the stream directly instead of waiting for the heartbeat.
//   - No query, snapshot or connection exists while parked. A parked transport
//     holds only its subscriptions, one ticker and one life timer.
//   - The current read authority is checked before the durable read and before
//     EVERY frame. A revocation drops the batch's remaining queued hints and
//     emits only the authority-changed close control (the merged product
//     behavior); no page frame ever goes to a stale principal.
//   - Every sink write runs under its own bounded deadline, so a stalled
//     consumer is abandoned at that deadline and the stream releases. Turns,
//     frame bytes, lifetime, concurrent streams and the wake map are bounded,
//     and every exit path releases the subscriptions, ticker and timer
//     idempotently, including the deadline-abandoned write.
var errUOEventTransportRevoked = errors.New("event transport authority is no longer current")

// errUOEventTransportAtCapacity is the named refusal for a configured stream
// cap. It is returned before any subscription or SQL work.
var errUOEventTransportAtCapacity = errors.New("event transport capacity exhausted")

const (
	uoEventTransportHeartbeat = 25 * time.Second
	uoEventTransportLife      = 30 * time.Minute
	uoEventTransportTurns     = 1024
	uoEventTransportRequest   = 128
	// uoEventTransportWrite bounds exactly one sink write. It is independent of
	// the heartbeat so a stalled consumer can never hold the subscription,
	// hub refcount, ticker or life timer past this deadline.
	uoEventTransportWrite     = 5 * time.Second
	uoEventTransportAudiences = 64
)

// One current read fact for the transport's principal. It is not bearer
// authority: the callback must consult current source state, and an unchanged
// binding with a denied decision still ends the stream.
type uoEventCurrentAuthority func(context.Context) (uoEventBinding, bool, error)

type uoEventTransportConfig struct {
	heartbeat time.Duration
	life      time.Duration
	write     time.Duration
	turns     int
}

type uoEventTransportInput struct {
	hub       *uoEventWakeHub
	codec     *uoEventCursorCodec
	query     uoEventQuery
	authority uoEventCurrentAuthority
	addressed func(string) bool
	redact    func(string, uoStoredEvent) (uoStoredEvent, bool, error)
	// emit is the physical sink. It receives a context that carries the
	// per-write deadline; a sink must return when that context is done.
	emit func(context.Context, []byte) error
	now  func() time.Time
	// wakeAudiences lists every addressed audience key for this principal. It
	// must contain audience; an empty list means audience alone.
	wakeAudiences []string
	audience      string
	cursor        string
	binding       uoEventBinding
	config        uoEventTransportConfig
}

type uoEventTransport struct {
	hub       *uoEventWakeHub
	codec     *uoEventCursorCodec
	query     uoEventQuery
	authority uoEventCurrentAuthority
	addressed func(string) bool
	redact    func(string, uoStoredEvent) (uoStoredEvent, bool, error)
	emit      func(context.Context, []byte) error
	now       func() time.Time
	audiences []string
	audience  string
	cursor    string
	binding   uoEventBinding
	write     time.Duration
}

type uoEventTransportWake uint8

const (
	uoEventTransportWoke uoEventTransportWake = iota
	uoEventTransportHeartbeatTick
	uoEventTransportExpired
)

func (in uoEventTransportInput) configOrDefault() (uoEventTransportConfig, []string, error) {
	cfg := in.config
	if cfg.heartbeat == 0 {
		cfg.heartbeat = uoEventTransportHeartbeat
	}
	if cfg.life == 0 {
		cfg.life = uoEventTransportLife
	}
	if cfg.write == 0 {
		cfg.write = uoEventTransportWrite
	}
	if cfg.turns == 0 {
		cfg.turns = uoEventTransportTurns
	}
	zero := in.binding.database == [16]byte{} || in.binding.viewer == [32]byte{} || in.binding.audience == [32]byte{}
	if in.hub == nil || in.codec == nil || in.query == nil || in.authority == nil || in.addressed == nil || in.redact == nil || in.emit == nil || in.now == nil {
		return cfg, nil, ErrInput
	}
	if in.audience == "" || len(in.audience) > 512 || !utf8.ValidString(in.audience) || zero || len(in.cursor) > uoEventTransportRequest {
		return cfg, nil, ErrInput
	}
	audiences := in.wakeAudiences
	if len(audiences) == 0 {
		audiences = []string{in.audience}
	}
	if len(audiences) > uoEventTransportAudiences {
		return cfg, nil, ErrInput
	}
	seen := make(map[string]bool, len(audiences))
	own := false
	for _, audience := range audiences {
		if audience == "" || len(audience) > 512 || !utf8.ValidString(audience) || seen[audience] {
			return cfg, nil, ErrInput
		}
		seen[audience] = true
		if audience == in.audience {
			own = true
		}
	}
	if !own {
		return cfg, nil, ErrInput
	}
	// Bounds are checked, never assumed: one ticker, one life timer, one
	// bounded write deadline, a finite turn budget and a lifetime no longer
	// than the cursor's own life.
	if cfg.heartbeat <= 0 || cfg.heartbeat > cfg.life || cfg.life > 24*time.Hour || cfg.write <= 0 || cfg.write > 24*time.Hour || cfg.turns < 1 || cfg.turns > uoEventTransportTurns {
		return cfg, nil, ErrInput
	}
	return cfg, audiences, nil
}

// serveUOEventTransport runs one principal's bounded stream until the caller
// cancels, the life ends, authority is revoked, the write deadline is exceeded
// or the turn budget is spent. Frames reach the caller's sink only through
// deliver; the sink is the physical write and its error ends the stream.
func serveUOEventTransport(ctx context.Context, in uoEventTransportInput) error {
	if ctx == nil {
		return ErrInput
	}
	cfg, audiences, err := in.configOrDefault()
	if err != nil {
		return err
	}
	t := &uoEventTransport{
		hub: in.hub, codec: in.codec, query: in.query, authority: in.authority,
		addressed: in.addressed, redact: in.redact, emit: in.emit, now: in.now,
		audiences: audiences, audience: in.audience, cursor: in.cursor, binding: in.binding,
		write: cfg.write,
	}
	return t.serve(ctx, cfg)
}

func (t *uoEventTransport) serve(ctx context.Context, cfg uoEventTransportConfig) error {
	// The configured stream cap is a named refusal before any subscription or
	// SQL work, and the map stays bounded per admitted stream.
	if err := t.hub.acquireStream(); err != nil {
		return err
	}
	defer t.hub.releaseStream()
	// Subscribe before the first read. The subscriptions are the whole
	// external memory grant; release closes them on every exit path below,
	// including an abandoned write.
	subs, err := t.hub.subscribeAll(t.audiences)
	if err != nil {
		return err
	}
	defer subs.release()
	heartbeat := time.NewTicker(cfg.heartbeat)
	defer heartbeat.Stop()
	end := time.NewTimer(cfg.life)
	defer end.Stop()

	next := t.cursor
	opened := false
	for turn := 0; ; turn++ {
		if err := t.current(ctx); err != nil {
			if errors.Is(err, errUOEventTransportRevoked) {
				t.revokeClose(ctx, next)
			}
			return err
		}
		// Generation witness from BEFORE this read; a publish anywhere during
		// the read or its frame emission turns into a no-park next turn.
		before, err := subs.observe()
		if err != nil {
			return err
		}
		// Bounded, snapshot-confined read. No custody survives this call, so
		// nothing below parks with SQL open.
		page, err := readUOEventFeed(ctx, t.query, t.codec, uoEventFeedInput{cursor: next, nowMS: t.now().UnixMilli(), binding: t.binding}, t.addressed, t.redact)
		if err != nil {
			// A deferred authority recheck denial REPLACES any read error. A
			// stale row after revocation is never reported as the cause.
			if authErr := t.current(ctx); authErr != nil {
				if errors.Is(authErr, errUOEventTransportRevoked) {
					t.revokeClose(ctx, next)
				}
				return authErr
			}
			return err
		}
		// opened only when nothing waits, matching UO-18 and the product
		// adapter: a client that connects at head learns its position.
		frames, err := uoEventStreamFrames(page, !opened && len(page.events) == 0, false, false, t.now().UnixMilli())
		if err != nil {
			return err
		}
		if err := t.deliver(ctx, frames, page.next); err != nil {
			return err
		}
		opened = true
		next = page.next
		if turn+1 >= cfg.turns {
			return t.close(ctx, next)
		}
		// A page that stopped at the byte budget re-reads immediately inside
		// the same turn bound instead of waiting for the next heartbeat.
		if page.more {
			continue
		}
		wake, err := t.await(ctx, subs, before, heartbeat.C, end.C)
		if err != nil {
			return err
		}
		if wake == uoEventTransportExpired {
			return t.close(ctx, next)
		}
	}
}

// deliver is the only path to the sink. Current authority and cancellation are
// tested before EVERY frame, so a hint queued behind a revocation is dropped
// instead of disclosed. The revocation close control is the merged product's
// authority-changed frame; no page frame is invented for a stale principal.
func (t *uoEventTransport) deliver(ctx context.Context, frames [][]byte, next string) error {
	for _, frame := range frames {
		if err := t.current(ctx); err != nil {
			if errors.Is(err, errUOEventTransportRevoked) {
				t.revokeClose(ctx, next)
			}
			return err
		}
		if err := t.writeFrame(ctx, frame); err != nil {
			return err
		}
	}
	return nil
}

// writeFrame bounds one physical sink call. The sink observes the deadline
// through its context, so a stalled consumer is abandoned and the deferred
// release paths run instead of the goroutine, refcount and timers being held.
func (t *uoEventTransport) writeFrame(ctx context.Context, frame []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, t.write)
	defer cancel()
	return t.emit(writeCtx, frame)
}

// revokeClose emits the merged product's authority-changed close control. It
// carries no page data and is skipped when no opaque position exists yet.
func (t *uoEventTransport) revokeClose(ctx context.Context, next string) {
	if next == "" {
		return
	}
	frames, err := uoEventStreamFrames(uoEventFeedPage{next: next}, false, false, true, t.now().UnixMilli())
	if err != nil {
		return
	}
	for _, frame := range frames {
		if err := t.writeFrame(ctx, frame); err != nil {
			return
		}
	}
}

func (t *uoEventTransport) close(ctx context.Context, next string) error {
	frames, err := uoEventStreamFrames(uoEventFeedPage{next: next}, false, true, false, t.now().UnixMilli())
	if err != nil {
		return err
	}
	return t.deliver(ctx, frames, next)
}

func (t *uoEventTransport) current(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binding, allowed, err := t.authority(ctx)
	if err != nil {
		return err
	}
	if !allowed || binding != t.binding {
		return errUOEventTransportRevoked
	}
	return nil
}

// await parks without SQL custody. The same-lock generation and channel test is
// repeated here, immediately before the select: a publish that replaced one of
// the captured channels while the read ran must not be lost. Every addressed
// audience key is watched, and the heartbeat is the bounded safety recheck for
// a publish that was never delivered at all.
func (t *uoEventTransport) await(ctx context.Context, subs *uoEventSubscriptionSet, before []uoEventWakePosition, heartbeat, end <-chan time.Time) (uoEventTransportWake, error) {
	now, err := subs.observe()
	if err != nil {
		return uoEventTransportWoke, err
	}
	if len(now) != len(before) {
		return uoEventTransportWoke, ErrInput
	}
	for i := range now {
		if now[i].generation != before[i].generation || now[i].changed != before[i].changed {
			return uoEventTransportWoke, nil
		}
	}
	cases := make([]reflect.SelectCase, 0, len(now)+3)
	cases = append(cases,
		reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
		reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(end)},
		reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(heartbeat)},
	)
	for _, position := range now {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(position.changed)})
	}
	chosen, _, _ := reflect.Select(cases)
	switch chosen {
	case 0:
		return uoEventTransportWoke, ctx.Err()
	case 1:
		return uoEventTransportExpired, nil
	case 2:
		return uoEventTransportHeartbeatTick, nil
	}
	return uoEventTransportWoke, nil
}
