package writer

import (
	"context"
	"math"
	"time"
)

type mpAdvanceOption struct {
	entry         string
	position, lap int64
}
type mpAdvanceDraft struct {
	turn             *commandTurn // private SAME-turn binding; never part of public/UO values
	old              mpQueueLayout
	session          mpNextSession
	request          mpAdvanceInput
	options          []mpAdvanceOption
	postPlayPrepared bool
	automaticAfter   int64
}

// Actual current preference values come from UO in this same turn. These are
// policy values, never item or actor authority. MP owns the queue's automatic
// edge counter and derives episode/listening kind from the native current item.
type mpPostPlayValues struct {
	revision, episodes   int64
	autoplay, protection bool
}

func mpPostPlayCounter(current int64, episode, listening bool, p mpPostPlayValues) (int64, error) {
	if current < 0 || current > 1_000_000 || p.revision < 1 || p.episodes < 0 {
		return 0, ErrInvalid
	}
	if listening || !episode {
		return 0, nil
	}
	if p.autoplay && p.protection && p.episodes > 0 && current+1 >= p.episodes {
		return 0, errMPAttention
	}
	return min(current+1, int64(1_000_000)), nil
}

func mpPrepareAdvancePostPlay(ctx context.Context, t *commandTurn, d mpAdvanceDraft, p mpPostPlayValues) (out mpAdvanceDraft, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueAdvance); err != nil {
		return out, err
	}
	if d.turn != t || d.request.reason != "completion" {
		return out, ErrInvalid
	}
	item, err := mpQueueKey(ctx, t, d.old, d.old.head.entry)
	if err != nil {
		return out, err
	}
	row, err := mpQuery(ctx, t, "mp.next.item_kind", item)
	if err != nil {
		return out, err
	}
	var kind string
	var listening int64
	if err = row.Scan(&kind, &listening); err != nil {
		return out, err
	}
	if listening < 0 || listening > 1 {
		return out, ErrResolveRequired
	}
	after, err := mpPostPlayCounter(d.old.head.automatic, kind == "episode", listening == 1, p)
	if err != nil {
		return out, err
	}
	d.automaticAfter = after
	d.postPlayPrepared = true
	return d, nil
}

// At most the public 200 withheld-entry skips plus the initial candidate.
// Parent's core current-item policy selects the first admitted candidate, then
// accepts it in this same turn. There is no caller-supplied visibility callback,
// permission boolean or source substitution in the queue command.
func mpAdvanceOptions(l mpQueueLayout, q mpAdvanceInput) ([]mpAdvanceOption, error) {
	next, _, err := mpPlanAdvance(l, q)
	if err != nil {
		return nil, err
	}
	next, err = mpRebuildLap(l, next)
	if err != nil {
		return nil, err
	}
	var out []mpAdvanceOption
	direction := int64(1)
	if q.reason == "previous" {
		direction = -1
	}
	for skipped := 0; skipped <= 200; skipped++ {
		entry, err := next.entryAt(next.head.position)
		if err != nil {
			return nil, err
		}
		out = append(out, mpAdvanceOption{entry, next.head.position, next.head.lap})
		if q.reason == "entry" || q.reason == "completion" && l.head.repeat == "one" {
			break
		}
		old := next
		next.head.position += direction
		if next.head.position < 0 || next.head.position >= next.total() {
			if next.head.repeat != "all" {
				break
			}
			if direction > 0 {
				next.head.position = 0
				if next.head.shuffle {
					if next.head.lap == math.MaxUint32 {
						return nil, errMPBound
					}
					next.head.lap++
				}
			} else {
				next.head.position = next.total() - 1
				if next.head.shuffle && next.head.lap > 0 {
					next.head.lap--
				}
			}
		}
		next, err = mpRebuildLap(old, next)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func mpPrepareAdvanceSelection(ctx context.Context, t *commandTurn, q mpAdvanceInput) (out mpAdvanceDraft, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueAdvance); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, q.token, q.queue, q.now, false, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, q.revision, q.custody); err != nil {
		return out, err
	}
	a, err := mpCurrentActor(ctx, t, q.token, q.now)
	if err != nil {
		return out, err
	}
	if h.session != "" {
		out.session, err = mpLoadNextSession(ctx, t, h.session)
		if err != nil {
			return out, err
		}
		if !mpSessionMatches(h, out.session, a, q.now) {
			return out, ErrConflict
		}
		if q.reason == "completion" && (q.completionSession != out.session.id || q.completionGeneration != out.session.generation) {
			return out, ErrConflict
		}
	}
	out.old, err = mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	out.options, err = mpAdvanceOptions(out.old, q)
	if err != nil {
		return out, err
	}
	out.turn = t
	out.request = q
	return out, nil
}
func mpAdvanceOptionItem(ctx context.Context, t *commandTurn, d mpAdvanceDraft, index int) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueAdvance); err != nil {
		return out, err
	}
	if d.turn != t || index < 0 || index >= len(d.options) {
		return out, ErrInvalid
	}
	l := d.old
	l.head.lap = d.options[index].lap
	l, err = mpRebuildLap(d.old, l)
	if err != nil {
		return out, err
	}
	return mpQueueKey(ctx, t, l, d.options[index].entry)
}
func mpAcceptAdvanceSelection(ctx context.Context, t *commandTurn, d mpAdvanceDraft, index int, now time.Time) (out mpNextEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueAdvance); err != nil {
		return out, err
	}
	if d.turn != t || index < 0 || index >= len(d.options) {
		return out, ErrInvalid
	}
	h, err := mpQueueBoundary(ctx, t, d.request.token, d.request.queue, now, false, false)
	if err != nil {
		return out, err
	}
	if h != d.old.head {
		return out, ErrConflict
	}
	next := d.old.clone()
	choice := d.options[index]
	next.head.position = choice.position
	next.head.lap = choice.lap
	if d.request.reason != "completion" {
		next.head.automatic = 0
	} else {
		if !d.postPlayPrepared {
			return out, ErrInvalid
		}
		next.head.automatic = d.automaticAfter
	}
	next, err = mpRebuildLap(d.old, next)
	if err != nil {
		return out, err
	}
	if choice.position == h.position && choice.lap == h.lap && next.head.automatic == h.automatic && d.request.reason != "completion" {
		return mpNextEffect{result: mpEffect(d.old, false)}, nil
	}
	// UO post-play policy/attention and the selected current-item authority
	// must have succeeded before this accept, in the parent's closed recipe.
	out.result, err = mpPersistLayout(ctx, t, d.old, next, now)
	if err != nil {
		return out, err
	}
	s := d.session
	out.transfer = mpNextTransfer{queue: h.id, fromSession: s.id, fromMedia: s.media, fromGeneration: s.generation, entry: choice.entry, state: s.state, queueRevision: out.result.revision, custody: h.custody}
	return out, nil
}
