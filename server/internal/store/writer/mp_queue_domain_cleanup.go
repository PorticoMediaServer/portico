package writer

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mpQueueCleanupDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.queue.stop", `INSERT INTO queue_v1_stop_obligations(queue_id,session_id,session_generation,media_session_id,media_generation,reason,requested_ms) VALUES(?,?,?,?,?,?,?)`, "ssisis i", stmt.Write),
		mpDefinition("mp.queue.cleanup_segments", `SELECT id,snapshot_id,first_ordinal,count,removed FROM queue_v1_segments WHERE queue_id=? ORDER BY sort_key LIMIT 49`, "s", stmt.Read),
		mpDefinition("mp.queue.cleanup_spans", `SELECT rank,source,start,count FROM queue_v1_play_spans WHERE queue_id=? ORDER BY rank LIMIT 49`, "s", stmt.Read),
		mpDefinition("mp.queue.cleanup_snapshots", `SELECT id FROM queue_v1_snapshots INDEXED BY queue_v1_snapshots_queue WHERE queue_id=? ORDER BY rowid LIMIT 49`, "s", stmt.Read),
		mpDefinition("mp.queue.stop_exit", `UPDATE queue_v1_stop_obligations SET exited_ms=? WHERE queue_id=? AND media_session_id=? AND media_generation=? AND exited_ms IS NULL AND EXISTS(SELECT 1 FROM playback_physical_exits x WHERE x.session_id=queue_v1_stop_obligations.media_session_id AND x.generation=queue_v1_stop_obligations.media_generation AND x.supervisor_incarnation=? AND x.exited_ms=?)`, "issibi", stmt.Write),
	}
}

func mpQueueStopCurrent(ctx context.Context, t *commandTurn, h mpQueueHead, reason string, now time.Time) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if h.session == "" {
		return nil
	}
	s, err := mpLoadNextSession(ctx, t, h.session)
	if err != nil {
		return err
	}
	if s.queue != h.id || s.account != h.account || s.profile != h.profile || s.authority != h.authority || s.generation < 1 || s.mediaGeneration < 1 || !mpID(s.media) {
		return ErrConflict
	}
	return mpExec(ctx, t, "mp.queue.stop", h.id, s.id, s.generation, s.media, s.mediaGeneration, reason, now.UnixMilli())
}

// Called only after the parent's same-turn session recipe has installed its
// actual successor. The draft advance never pretends a queue position effect
// alone completed the logical/physical session transfer.
func mpFinalizeQueueAdvance(ctx context.Context, t *commandTurn, token, nextSession, from string, e mpQueueEffect, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueAdvance); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, token, e.queue, now, false, false)
	if err != nil {
		return out, err
	}
	if h.revision != e.revision || h.custody != e.custody || h.entry != e.entry || h.session != from {
		return out, ErrConflict
	}
	a, err := mpCurrentActor(ctx, t, token, now)
	if err != nil {
		return out, err
	}
	s, err := mpLoadNextSession(ctx, t, nextSession)
	if err != nil {
		return out, err
	}
	next := h
	next.session = nextSession
	if !mpSessionMatches(next, s, a, now) {
		return out, ErrConflict
	}
	if err = mpQueueStopCurrent(ctx, t, h, "advanced", now); err != nil {
		return out, err
	}
	if err = mpPublishQueue(ctx, t, h, next); err != nil {
		return out, err
	}
	e.session = nextSession
	return e, nil
}

// A native worker cleanup, scoped by an already-retired durable queue head.
// No principal is fabricated for a revoked family and no personal row/event is
// touched. Parent supplies the existing trusted internal worker disclosure.
func mpCleanupRetiredQueueStructure(ctx context.Context, t *commandTurn, queue string, revision, custody int64) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueCleanup); err != nil {
		return out, err
	}
	h, err := mpLoadQueue(ctx, t, queue)
	if err != nil {
		return out, err
	}
	if !strings.HasPrefix(h.owner, "!") || h.session != "" || h.revision != revision || h.custody != custody {
		return out, ErrConflict
	}
	out = mpEffect(mpQueueLayout{head: h}, false)
	rows, err := mpRows(ctx, t, "mp.queue.cleanup_segments", queue)
	if err != nil {
		return out, err
	}
	var segments []mpQueueSegment
	for rows.Next() {
		var g mpQueueSegment
		if err = rows.Scan(&g.id, &g.snapshot, &g.first, &g.count, &g.removed); err != nil {
			break
		}
		segments = append(segments, g)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	for _, g := range segments[:min(len(segments), mpMaxSlice)] {
		if err = mpExec(ctx, t, "mp.queue.segment_delete", g.id, queue, g.snapshot, g.first, g.count, g.removed); err != nil {
			return out, err
		}
		out.count++
		out.changed = true
	}
	if len(segments) > 0 {
		out.reason = "segments"
		return out, nil
	}
	rows, err = mpRows(ctx, t, "mp.queue.cleanup_spans", queue)
	if err != nil {
		return out, err
	}
	type span struct{ rank, source, start, count int64 }
	var spans []span
	for rows.Next() {
		var s span
		if err = rows.Scan(&s.rank, &s.source, &s.start, &s.count); err != nil {
			break
		}
		spans = append(spans, s)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	for _, s := range spans[:min(len(spans), mpMaxSlice)] {
		if err = mpExec(ctx, t, "mp.queue.span_delete", queue, s.rank, s.source, s.start, s.count); err != nil {
			return out, err
		}
		out.count++
		out.changed = true
	}
	if len(spans) > 0 {
		out.reason = "spans"
		return out, nil
	}
	rows, err = mpRows(ctx, t, "mp.queue.cleanup_snapshots", queue)
	if err != nil {
		return out, err
	}
	var snapshots []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		snapshots = append(snapshots, id)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	if len(snapshots) > 0 {
		out.reason = snapshots[0]
		return out, nil
	}
	// The root/typed receipt and stop children remain retained until W's floor,
	// history lifetime and actual physical-exit debt permit their own reclaim.
	out.done = true
	return out, nil
}
func mpRecordQueueStopExit(ctx context.Context, t *commandTurn, queue, media string, generation, exited int64, supervisor [16]byte) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpQueueCleanup); err != nil {
		return out, err
	}
	if generation < 1 || exited < 1 || supervisor == [16]byte{} {
		return out, ErrInvalid
	}
	h, err := mpLoadQueue(ctx, t, queue)
	if err != nil {
		return out, err
	}
	if err = mpExec(ctx, t, "mp.queue.stop_exit", exited, queue, media, generation, supervisor[:], exited); err != nil {
		return out, err
	}
	out = mpEffect(mpQueueLayout{head: h}, true)
	out.done = true
	return out, nil
}
