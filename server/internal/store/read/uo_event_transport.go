package read

import (
	"context"
	"errors"
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
//     read turn, never a lost wake.
//   - No query, snapshot or connection exists while parked. A parked transport
//     holds only its subscription, one ticker and one life timer.
//   - The current read authority is checked before the durable read and before
//     EVERY frame. A revocation drops the batch's remaining queued hints and
//     sends nothing: no frame ever goes to a stale principal.
//   - One bounded heartbeat safety check re-reads after a missed wake. Turns,
//     frame bytes and lifetime are bounded, and every exit path releases the
//     subscription, ticker and timer.
var errUOEventTransportRevoked = errors.New("event transport authority is no longer current")

const (
	uoEventTransportHeartbeat = 25 * time.Second
	uoEventTransportLife      = 30 * time.Minute
	uoEventTransportTurns     = 1024
	uoEventTransportRequest   = 128
)

// One current read fact for the transport's principal. It is not bearer
// authority: the callback must consult current source state, and an unchanged
// binding with a denied decision still ends the stream.
type uoEventCurrentAuthority func(context.Context) (uoEventBinding, bool, error)

type uoEventTransportConfig struct {
	heartbeat time.Duration
	life      time.Duration
	turns     int
}

type uoEventTransportInput struct {
	hub       *uoEventWakeHub
	codec     *uoEventCursorCodec
	query     uoEventQuery
	authority uoEventCurrentAuthority
	addressed func(string) bool
	redact    func(string, uoStoredEvent) (uoStoredEvent, bool, error)
	emit      func([]byte) error
	now       func() time.Time
	audience  string
	cursor    string
	binding   uoEventBinding
	config    uoEventTransportConfig
}

type uoEventTransport struct {
	hub       *uoEventWakeHub
	codec     *uoEventCursorCodec
	query     uoEventQuery
	authority uoEventCurrentAuthority
	addressed func(string) bool
	redact    func(string, uoStoredEvent) (uoStoredEvent, bool, error)
	emit      func([]byte) error
	now       func() time.Time
	audience  string
	cursor    string
	binding   uoEventBinding
}

type uoEventTransportWake uint8

const (
	uoEventTransportWoke uoEventTransportWake = iota
	uoEventTransportHeartbeatTick
	uoEventTransportExpired
)

func (in uoEventTransportInput) configOrDefault() (uoEventTransportConfig, error) {
	cfg := in.config
	if cfg.heartbeat == 0 {
		cfg.heartbeat = uoEventTransportHeartbeat
	}
	if cfg.life == 0 {
		cfg.life = uoEventTransportLife
	}
	if cfg.turns == 0 {
		cfg.turns = uoEventTransportTurns
	}
	zero := in.binding.database == [16]byte{} || in.binding.viewer == [32]byte{} || in.binding.audience == [32]byte{}
	if in.hub == nil || in.codec == nil || in.query == nil || in.authority == nil || in.addressed == nil || in.redact == nil || in.emit == nil || in.now == nil {
		return cfg, ErrInput
	}
	if in.audience == "" || len(in.audience) > 512 || !utf8.ValidString(in.audience) || zero || len(in.cursor) > uoEventTransportRequest {
		return cfg, ErrInput
	}
	// Bounds are checked, never assumed: one ticker, one life timer, a finite
	// turn budget and a lifetime no longer than the cursor's own life.
	if cfg.heartbeat <= 0 || cfg.heartbeat > cfg.life || cfg.life > 24*time.Hour || cfg.turns < 1 || cfg.turns > uoEventTransportTurns {
		return cfg, ErrInput
	}
	return cfg, nil
}

// serveUOEventTransport runs one principal's bounded stream until the caller
// cancels, the life ends, authority is revoked or the turn budget is spent.
// Frames reach the caller's sink only through deliver; the sink is the
// physical write and its error ends the stream.
func serveUOEventTransport(ctx context.Context, in uoEventTransportInput) error {
	if ctx == nil {
		return ErrInput
	}
	cfg, err := in.configOrDefault()
	if err != nil {
		return err
	}
	t := &uoEventTransport{
		hub: in.hub, codec: in.codec, query: in.query, authority: in.authority,
		addressed: in.addressed, redact: in.redact, emit: in.emit, now: in.now,
		audience: in.audience, cursor: in.cursor, binding: in.binding,
	}
	return t.serve(ctx, cfg)
}

func (t *uoEventTransport) serve(ctx context.Context, cfg uoEventTransportConfig) error {
	// Subscribe before the first read. The one refcount is the whole external
	// memory grant; release closes it on every exit path below.
	sub, err := t.hub.subscribe(t.audience)
	if err != nil {
		return err
	}
	defer sub.release()
	heartbeat := time.NewTicker(cfg.heartbeat)
	defer heartbeat.Stop()
	end := time.NewTimer(cfg.life)
	defer end.Stop()

	next := t.cursor
	opened := false
	for turn := 0; ; turn++ {
		if err := t.current(ctx); err != nil {
			return err
		}
		// Generation witness from BEFORE this read; a publish anywhere during
		// the read or its frame emission turns into a no-park next turn.
		before, err := sub.observe()
		if err != nil {
			return err
		}
		// Bounded, snapshot-confined read. No custody survives this call, so
		// nothing below parks with SQL open.
		page, err := readUOEventFeed(ctx, t.query, t.codec, uoEventFeedInput{cursor: next, nowMS: t.now().UnixMilli(), binding: t.binding}, t.addressed, t.redact)
		if err != nil {
			return err
		}
		// opened only when nothing waits, matching UO-18 and the product
		// adapter: a client that connects at head learns its position.
		frames, err := uoEventStreamFrames(page, !opened && len(page.events) == 0, false, false, t.now().UnixMilli())
		if err != nil {
			return err
		}
		if err := t.deliver(ctx, frames); err != nil {
			return err
		}
		opened = true
		next = page.next
		if turn+1 >= cfg.turns {
			return t.close(ctx, next)
		}
		wake, err := t.await(ctx, sub, before, heartbeat.C, end.C)
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
// instead of disclosed, and no closing control is invented for a stale
// principal.
func (t *uoEventTransport) deliver(ctx context.Context, frames [][]byte) error {
	for _, frame := range frames {
		if err := t.current(ctx); err != nil {
			return err
		}
		if err := t.emit(frame); err != nil {
			return err
		}
	}
	return nil
}

func (t *uoEventTransport) close(ctx context.Context, next string) error {
	frames, err := uoEventStreamFrames(uoEventFeedPage{next: next}, false, true, false, t.now().UnixMilli())
	if err != nil {
		return err
	}
	return t.deliver(ctx, frames)
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
// repeated here, immediately before the select: a publish that replaced the
// captured channel while the read ran must not be lost. The heartbeat is the
// bounded safety recheck for a publish that was never delivered at all.
func (t *uoEventTransport) await(ctx context.Context, sub *uoEventSubscription, before uoEventWakePosition, heartbeat, end <-chan time.Time) (uoEventTransportWake, error) {
	now, err := sub.observe()
	if err != nil {
		return uoEventTransportWoke, err
	}
	if now.generation != before.generation || now.changed != before.changed {
		return uoEventTransportWoke, nil
	}
	select {
	case <-ctx.Done():
		return uoEventTransportWoke, ctx.Err()
	case <-end:
		return uoEventTransportExpired, nil
	case <-heartbeat:
		return uoEventTransportHeartbeatTick, nil
	case <-before.changed:
		return uoEventTransportWoke, nil
	}
}
