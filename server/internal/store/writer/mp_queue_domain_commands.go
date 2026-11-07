package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"time"
)

func mpPurpose(t *commandTurn, id CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.ownerWindow.commandID != id {
		return ErrInvalid
	}
	return t.checkLifetimes(t.transaction.ctx)
}

type mpQueueBuildInput struct {
	birth        mpQueueWorkerBirth
	token, queue string
	snapshots    []mpQueueSnapshot
	now          time.Time
}

func mpBeginQueue(ctx context.Context, t *commandTurn, q mpQueueBuildInput) (out mpQueueBirthEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueBegin); err != nil {
		return out, err
	}
	if !mpID(q.queue) || len(q.snapshots) < 1 || len(q.snapshots) > 16 {
		return out, ErrInvalid
	}
	incarnation, err := mpFreshQueueWorkerIncarnation(q.birth)
	if err != nil {
		return out, err
	}
	a, err := mpCurrentActor(ctx, t, q.token, q.now)
	if err != nil {
		return out, err
	}
	var total int64
	seen := map[string]bool{}
	for _, s := range q.snapshots {
		if err = mpValidateSnapshot(s, q.queue); err != nil {
			return out, err
		}
		if seen[s.id] || s.count > mpMaxQueueKeys-total {
			return out, ErrInvalid
		}
		seen[s.id] = true
		total += s.count
	}
	if err = mpExec(ctx, t, "mp.queue.begin", q.queue, "~"+a.device, a.account, a.profile, q.now.UnixMilli(), q.now.UnixMilli()); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.queue.worker.birth", incarnation[:], q.birth.root, q.birth.member, q.birth.profileLifetime, q.queue); err != nil {
		return out, err
	}
	out.workerIncarnation = incarnation
	for _, s := range q.snapshots {
		if err = mpInsertSnapshot(ctx, t, s); err != nil {
			return out, err
		}
	}
	out.result = mpQueueEffect{queue: q.queue, revision: 1, changed: true, count: total}
	for _, s := range q.snapshots {
		out.snapshots = append(out.snapshots, mpQueueSnapshotBirthResult{id: s.id, sourceRevision: s.sourceRevision, count: s.count})
	}
	return out, nil
}
func mpValidateSnapshot(s mpQueueSnapshot, queue string) error {
	if !mpSnapshotID(s.id) || s.queue != queue || s.count < 0 || s.count > mpMaxQueueKeys || len(s.selector) > 65536 || len(s.label) > 1024 || len(s.sourceRevision) > 256 || s.sourceRevision == "" {
		return ErrInvalid
	}
	switch s.kind {
	case "items", "playlist", "album", "show", "season", "artist", "book", "collection", "query", "radio", "journey":
	default:
		return ErrInvalid
	}
	return nil
}
func mpInsertSnapshot(ctx context.Context, t *commandTurn, s mpQueueSnapshot) error {
	return mpExec(ctx, t, "mp.queue.snapshot_begin", s.id, s.queue, s.kind, s.selector, s.label, s.count, s.sourceRevision)
}
func mpBeginQueueSnapshot(ctx context.Context, t *commandTurn, token string, s mpQueueSnapshot, revision, custody int64, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSnapshotBegin); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, token, s.queue, now, false, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, revision, custody); err != nil {
		return out, err
	}
	if err = mpValidateSnapshot(s, s.queue); err != nil {
		return out, err
	}
	if err = mpInsertSnapshot(ctx, t, s); err != nil {
		return out, err
	}
	return mpEffect(mpQueueLayout{head: h}, true), nil
}

type mpQueueStageInput struct {
	token, queue, snapshot string
	built, count           int64
	keys                   []string
	staging                bool
	now                    time.Time
}

func mpStageQueueChunk(ctx context.Context, t *commandTurn, q mpQueueStageInput) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueStage); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, q.token, q.queue, q.now, q.staging, false)
	if err != nil {
		return out, err
	}
	s, err := mpLoadSnapshot(ctx, t, q.queue, q.snapshot)
	if err != nil {
		return out, err
	}
	if s.state != "building" || s.built != q.built || s.count != q.count || q.built < 0 || q.built%mpChunkKeys != 0 || int64(len(q.keys)) > s.count-s.built || len(q.keys) < 1 || (len(q.keys) != mpChunkKeys && s.built+int64(len(q.keys)) != s.count) {
		return out, ErrConflict
	}
	b, err := mpEncodeChunk(q.keys)
	if err != nil {
		return out, err
	}
	if err = mpExec(ctx, t, "mp.queue.chunk_stage", s.id, s.built/mpChunkKeys, b); err != nil {
		return out, err
	}
	next := s.built + int64(len(q.keys))
	if err = mpExec(ctx, t, "mp.queue.stage_cursor", next, int64(b[0]), s.id, q.queue, s.built, s.count); err != nil {
		return out, err
	}
	out = mpEffect(mpQueueLayout{head: h}, true)
	out.cursor = next
	out.count = int64(len(q.keys))
	out.done = next == s.count
	return out, nil
}

type mpQueueSealInput struct {
	token, queue        string
	snapshots, segments []string
	revision, custody   int64
	staging             bool
	now                 time.Time
}

func mpSealQueue(ctx context.Context, t *commandTurn, q mpQueueSealInput) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSeal); err != nil {
		return out, err
	}
	if len(q.snapshots) < 1 || len(q.snapshots) > 16 || q.staging && len(q.snapshots) != len(q.segments) {
		return out, ErrInvalid
	}
	h, err := mpQueueBoundary(ctx, t, q.token, q.queue, q.now, q.staging, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, q.revision, q.custody); err != nil {
		return out, err
	}
	l := mpQueueLayout{head: h, snapshots: map[string]mpQueueSnapshot{}, removals: map[string][]int64{}}
	seen := map[string]bool{}
	for i, id := range q.snapshots {
		if seen[id] {
			return out, ErrInvalid
		}
		seen[id] = true
		s, err := mpLoadSnapshot(ctx, t, q.queue, id)
		if err != nil {
			return out, err
		}
		if s.state != "building" || s.built != s.count {
			return out, ErrConflict
		}
		if err = mpExec(ctx, t, "mp.queue.snapshot_seal", id, q.queue); err != nil {
			return out, err
		}
		s.state = "ready"
		l.snapshots[id] = s
		if q.staging && s.count > 0 {
			g := mpQueueSegment{id: q.segments[i], key: mpOrdinalKey(int64(i + 1)), snapshot: s.id, count: s.count}
			l.segments = append(l.segments, g)
			if err = mpExec(ctx, t, "mp.queue.segment_insert", g.id, q.queue, g.key, g.snapshot, g.first, g.count, g.removed); err != nil {
				return out, err
			}
		}
	}
	if !q.staging {
		out = mpEffect(l, true)
		out.done = true
		return out, nil
	}
	if err = l.validate(); err != nil {
		return out, err
	}
	if l.total() > 0 {
		l.head.entry, err = l.entryAt(0)
		if err != nil {
			return out, err
		}
	}
	l.head.owner = l.head.owner[1:]
	l.head.revision++
	l.head.updated = q.now.UnixMilli()
	if err = mpPublishQueue(ctx, t, h, l.head); err != nil {
		return out, err
	}
	out = mpEffect(l, true)
	out.done = true
	return out, nil
}

func mpPublishQueue(ctx context.Context, t *commandTurn, old, h mpQueueHead) error {
	shuffle := int64(0)
	if h.shuffle {
		shuffle = 1
	}
	return mpExec(ctx, t, "mp.queue.publish", h.owner, h.revision, h.custody, h.position, h.entry, h.session, shuffle, h.seed, h.lap, h.domain, h.first, h.automatic, h.updated, h.id, old.owner, old.account, old.profile, old.revision, old.custody)
}

// Apply only point differences; bounded structure, no entire snapshot copies or
// cascade. Structural maximum is 1000 segments/10000 tombstones/1000 spans.
// Span replacement is bounded to 2000 small rows and MUST be runner-qualified;
// it is not claimed to satisfy the normal 25ms hold until measured.
func mpPersistLayout(ctx context.Context, t *commandTurn, old, next mpQueueLayout, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = next.validate(); err != nil {
		return out, err
	}
	oldSegments := map[string]mpQueueSegment{}
	newSegments := map[string]mpQueueSegment{}
	for _, g := range old.segments {
		oldSegments[g.id] = g
	}
	for _, g := range next.segments {
		newSegments[g.id] = g
	}
	writes := 0
	for id, g := range oldSegments {
		if n, ok := newSegments[id]; !ok || n != g {
			writes++
		}
	}
	for id := range newSegments {
		if _, ok := oldSegments[id]; !ok {
			writes++
		}
	}
	if !reflect.DeepEqual(old.spans, next.spans) {
		writes += len(old.spans) + len(next.spans)
	}
	if writes > 2*mpMaxSpans+mpMaxEditWrites {
		return out, errMPBound
	}
	// Delete departed keys before inserts to avoid transient unique sort ties.
	for _, g := range old.segments {
		if _, ok := newSegments[g.id]; !ok {
			if err = mpExec(ctx, t, "mp.queue.segment_delete", g.id, old.head.id, g.snapshot, g.first, g.count, g.removed); err != nil {
				return out, err
			}
		}
	}
	for _, g := range next.segments {
		prior, ok := oldSegments[g.id]
		if !ok {
			err = mpExec(ctx, t, "mp.queue.segment_insert", g.id, old.head.id, g.key, g.snapshot, g.first, g.count, g.removed)
		} else if prior != g {
			if prior.snapshot != g.snapshot {
				return out, ErrInvalid
			}
			err = mpExec(ctx, t, "mp.queue.segment_update", g.key, g.first, g.count, g.removed, g.id, old.head.id, g.snapshot, prior.key, prior.first, prior.count, prior.removed)
		}
		if err != nil {
			return out, err
		}
	}
	for snap, rs := range next.removals {
		for _, o := range rs {
			if !old.removed(snap, o) {
				if err = mpExec(ctx, t, "mp.queue.removal_insert", snap, o); err != nil {
					return out, err
				}
			}
		}
	}
	if !reflect.DeepEqual(old.spans, next.spans) {
		for i, s := range old.spans {
			if err = mpExec(ctx, t, "mp.queue.span_delete", old.head.id, int64(i), mpBool(s.source), s.start, s.count); err != nil {
				return out, err
			}
		}
		for i, s := range next.spans {
			if err = mpExec(ctx, t, "mp.queue.span_insert", old.head.id, int64(i), mpBool(s.source), s.start, s.count); err != nil {
				return out, err
			}
		}
	}
	next.head.entry = ""
	if next.total() > 0 {
		next.head.entry, err = next.entryAt(next.head.position)
		if err != nil {
			return out, err
		}
	}
	if _, err = mpCancelQueuePreparations(ctx, t, old.head.id, "queue_changed", now); err != nil {
		return out, err
	}
	next.head.revision++
	next.head.updated = now.UnixMilli()
	if err = mpPublishQueue(ctx, t, old.head, next.head); err != nil {
		return out, err
	}
	return mpEffect(next, true), nil
}
func mpBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

type mpQueueEditInput struct {
	token, queue, kind, entry, anchor, snapshot, segment, split string
	revision, custody                                           int64
	before                                                      bool
	now                                                         time.Time
}

func mpEditQueue(ctx context.Context, t *commandTurn, q mpQueueEditInput) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueEdit); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, q.token, q.queue, q.now, false, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, q.revision, q.custody); err != nil {
		return out, err
	}
	old, err := mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	next := old.clone()
	current := h.entry
	switch q.kind {
	case "remove":
		if err = next.removeEntry(q.entry); err != nil {
			return out, err
		}
	case "append", "move":
		if q.kind == "move" && next.head.shuffle {
			changed, e := next.moveShuffled(q.entry, q.anchor, q.before)
			if e != nil {
				return out, e
			}
			if !changed {
				return mpEffect(old, false), nil
			}
			return mpPersistLayout(ctx, t, old, next, q.now)
		}
		var s mpQueueSnapshot
		if q.kind == "move" {
			if q.entry == q.anchor {
				return mpEffect(old, false), nil
			}
			if _, err = next.position(q.anchor); err != nil {
				return out, err
			}
			key, err := mpQueueKey(ctx, t, next, q.entry)
			if err != nil {
				return out, err
			}
			if err = next.removeEntry(q.entry); err != nil {
				return out, err
			}
			s = mpQueueSnapshot{id: q.snapshot, queue: q.queue, kind: "items", selector: "", label: "1 item", state: "ready", count: 1, built: 1, sourceRevision: "move"}
			if !mpSnapshotID(s.id) {
				return out, ErrInvalid
			}
			if _, exists := old.snapshots[s.id]; exists {
				return out, ErrConflict
			}
			if err = mpInsertSnapshot(ctx, t, s); err != nil {
				return out, err
			}
			b, _ := mpEncodeChunk([]string{key})
			if err = mpExec(ctx, t, "mp.queue.chunk_stage", s.id, int64(0), b); err != nil {
				return out, err
			}
			if err = mpExec(ctx, t, "mp.queue.stage_cursor", int64(1), int64(b[0]), s.id, q.queue, int64(0), int64(1)); err != nil {
				return out, err
			}
			if err = mpExec(ctx, t, "mp.queue.snapshot_seal", s.id, q.queue); err != nil {
				return out, err
			}
		} else {
			s, err = mpLoadSnapshot(ctx, t, q.queue, q.snapshot)
			if err != nil {
				return out, err
			}
		}
		position := next.total()
		if q.anchor != "" {
			position, err = next.position(q.anchor)
			if err != nil {
				return out, err
			}
			if !q.before {
				position++
			}
		}
		if err = next.appendSnapshot(s, q.segment, q.split, position); err != nil {
			return out, err
		}
		if current == q.entry && q.kind == "move" {
			current = mpEntry(s.id, 0)
		}
	default:
		return out, ErrInvalid
	}
	if current != "" {
		if pos, e := next.position(current); e == nil {
			next.head.position = pos
		}
	}
	return mpPersistLayout(ctx, t, old, next, q.now)
}

func mpShuffleQueue(ctx context.Context, t *commandTurn, token, queue string, revision, custody int64, on bool, seed int64, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueSettings); err != nil {
		return out, err
	}
	if seed < 0 || seed > math.MaxUint32 {
		return out, ErrInvalid
	}
	h, err := mpQueueBoundary(ctx, t, token, queue, now, false, false)
	if err != nil {
		return out, err
	}
	if err = mpRequireRevision(h, revision, custody); err != nil {
		return out, err
	}
	old, err := mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	next, changed, err := mpSetShuffle(old, on, seed)
	if err != nil {
		return out, err
	}
	if !changed {
		return mpEffect(old, false), nil
	}
	return mpPersistLayout(ctx, t, old, next, now)
}

func mpRetireQueue(ctx context.Context, t *commandTurn, token, queue string, revision, custody int64, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueRetire); err != nil {
		return out, err
	}
	a, err := mpCurrentActor(ctx, t, token, now)
	if err != nil {
		return out, err
	}
	h, err := mpLoadQueue(ctx, t, queue)
	// Deleting another owner's queue has the same no-effect answer as absent.
	if errors.Is(err, sql.ErrNoRows) {
		return mpQueueEffect{queue: queue, revision: 1, done: true}, nil
	}
	if err != nil {
		return out, err
	}
	if !mpOwns(a, h, false, false) {
		return mpQueueEffect{queue: queue, revision: 1, done: true}, nil
	}
	if err = mpRequireRevision(h, revision, custody); err != nil {
		return out, err
	}
	if _, err = mpCancelQueuePreparations(ctx, t, queue, "queue_retired", now); err != nil {
		return out, err
	}
	if err = mpQueueStopCurrent(ctx, t, h, "queue_retired", now); err != nil {
		return out, err
	}
	if err = mpExec(ctx, t, "mp.queue.retire", now.UnixMilli(), queue, h.owner, h.account, h.profile, h.revision, h.custody); err != nil {
		return out, err
	}
	h.revision++
	h.custody++
	h.session = ""
	out = mpEffect(mpQueueLayout{head: h}, true)
	out.done = true
	return out, nil
}

// Reclaims ONE unreferenced snapshot in <=48-row slices. Refuse live references
// rather than invoke an arbitrary canonical cascade. Receipt/floor reclamation
// remains W's protocol; this function never acknowledges or erases its proof.
func mpCleanupQueueSnapshot(ctx context.Context, t *commandTurn, token, queue, snapshot string, cursor int64, now time.Time, retired bool) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueCleanup); err != nil {
		return out, err
	}
	if cursor < 0 {
		return out, ErrInvalid
	}
	var h mpQueueHead
	if retired && token == "" {
		h, err = mpLoadQueue(ctx, t, queue)
		if err == nil && (!strings.HasPrefix(h.owner, "!") || h.session != "") {
			err = ErrConflict
		}
	} else {
		h, err = mpQueueBoundary(ctx, t, token, queue, now, false, retired)
	}
	if err != nil {
		return out, err
	}
	if _, err = mpLoadSnapshot(ctx, t, queue, snapshot); err != nil {
		return out, err
	}
	row, err := mpQuery(ctx, t, "mp.queue.cleanup_snapshot_refs", snapshot)
	if err != nil {
		return out, err
	}
	var one int64
	err = row.Scan(&one)
	if err == nil {
		return out, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := mpRows(ctx, t, "mp.queue.cleanup_chunks", snapshot, cursor)
	if err != nil {
		return out, err
	}
	var chunks []int64
	for rows.Next() {
		var no, bytes int64
		if err = rows.Scan(&no, &bytes); err != nil {
			break
		}
		chunks = append(chunks, no)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	out = mpEffect(mpQueueLayout{head: h}, false)
	out.cursor = cursor
	for _, no := range chunks[:min(len(chunks), mpMaxSlice)] {
		if err = mpExec(ctx, t, "mp.queue.cleanup_chunk", snapshot, no); err != nil {
			return out, err
		}
		out.cursor = no + 1
		out.count++
		out.changed = true
	}
	if len(chunks) > 0 {
		return out, nil
	}
	// The final point delete is attempted only after both child classes have
	// been drained; a not-yet-empty snapshot is a pending cleanup result.
	rows, err = mpRows(ctx, t, "mp.queue.removals", snapshot, int64(0), mpMaxQueueKeys)
	if err != nil {
		return out, err
	}
	var ordinals []int64
	for rows.Next() {
		var n int64
		if err = rows.Scan(&n); err != nil {
			break
		}
		ordinals = append(ordinals, n)
		if len(ordinals) == mpMaxSlice {
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	for _, o := range ordinals {
		if err = mpExec(ctx, t, "mp.queue.cleanup_removal", snapshot, o); err != nil {
			return out, err
		}
		out.count++
		out.changed = true
	}
	if len(ordinals) > 0 {
		return out, nil
	}
	if err = mpExec(ctx, t, "mp.queue.cleanup_snapshot", snapshot, queue, snapshot, snapshot, snapshot); err != nil {
		return out, err
	}
	out.done = true
	out.changed = true
	return out, nil
}
