package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mpNextSession struct {
	id, device, account, profile, authority, queue, entry, state, media string
	generation, mediaGeneration, lease, ended                           int64
}
type mpNextPrepared struct {
	predecessorMedia                                                                                                                                        string
	token, queue, device, family, session, sessionState, currentEntry, entry, media, sourceRevision, state, reason                                          string
	sessionGeneration, queueRevision, custody, nextPosition, nextLap, item, mediaGeneration, preparedRevision, predecessorMediaGeneration, expires, created int64
}
type mpNextTransfer struct {
	queue, fromSession, toSession, fromMedia, toMedia, entry, state string
	fromGeneration, toMediaGeneration, queueRevision, custody       int64
}
type mpNextEffect struct {
	result   mpQueueEffect
	transfer mpNextTransfer
}

// Exact F witness/stop definitions, pinned in the coverage note; not embedded
// or executable until parent adoption/Capture/composition. No canonical DDL is
// changed here. No token, JSON response or source digest acts as authority.
func mpNextDefinitions() []stmt.Definition {
	r, w := stmt.Read, stmt.Write
	return []stmt.Definition{
		mpDefinition("mp.next.item_kind", `SELECT k.name,k.listening FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?) AND e.retired=0`, "s", r),
		mpDefinition("mp.next.item", `SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND id=? AND retired=0`, "si", r),
		mpDefinition("mp.next.session", `SELECT id,device_id,account_id,profile_id,authority,queue_id,entry_id,state,media_session_id,generation,media_generation,lease_expires_ms,ended_ms FROM playback_v1_sessions WHERE id=?`, "s", r),
		mpDefinition("mp.next.prepared", `SELECT token,queue_id,device_id,mp_family_id,session_id,session_state,mp_current_entry_id,entry_id,media_session_id,mp_source_revision,state,cancel_reason,session_generation,queue_revision,mp_custody_generation,next_position,mp_next_lap,item_id,mp_media_generation,mp_prepared_revision,expires_ms,created_ms,mp_predecessor_media_id,mp_predecessor_media_generation FROM playback_v1_prepared WHERE token=? AND queue_id=?`, "ss", r),
		mpDefinition("mp.next.outstanding", `SELECT token FROM playback_v1_prepared INDEXED BY playback_v1_prepared_queue WHERE queue_id=? AND state='prepared' ORDER BY rowid LIMIT 2`, "s", r),
		mpDefinition("mp.next.media", `SELECT p.generation,p.expires_at,a.prepared_source_revision FROM playback_sessions p JOIN authorization_family_tokens tok ON tok.token_hash=p.session_hash JOIN playback_source_pins pin ON pin.session_id=p.id JOIN playback_physical_source_pins phy ON phy.session_id=p.id JOIN catalog_assets a ON a.token=p.asset_id JOIN catalog_asset_links link ON link.asset_id=a.id AND link.entity_id=p.item_id JOIN inventory_objects o ON o.id=pin.inventory_object_id JOIN library_sources src ON src.id=phy.source_id JOIN playback_private_presentations private ON private.session_id=p.id WHERE p.id=? AND p.account_id=? AND p.profile_id=? AND p.item_id=? AND tok.family_id=? AND p.state NOT IN('stopped','ended','failed') AND p.generation=? AND a.available=1 AND link.available=1 AND pin.binding='inventory' AND pin.asset_id=a.token AND pin.size=a.size AND pin.modified_ns=a.modified_ns AND o.asset_id=a.token AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns AND o.source_id=src.id AND o.revision=? AND pin.object_identity=o.object_identity AND pin.root_identity=src.root_identity AND pin.root=src.root AND pin.source_id=src.id AND pin.relative_path=o.relative_path AND o.root_incarnation=src.incarnation AND phy.incarnation=src.incarnation AND phy.configuration_generation=src.generation AND src.enabled=1 AND src.health NOT IN('offline','root_changed','removing') AND private.owner_session_id=? AND private.expires_ms>?`, "sssisissi", r),
		mpDefinition("mp.next.insert", `INSERT INTO playback_v1_prepared(token,queue_id,device_id,create_key,digest,session_id,session_generation,session_state,queue_revision,next_position,entry_id,item_id,media_session_id,request,presentation,expires_ms,state,created_ms,mp_family_id,mp_current_entry_id,mp_custody_generation,mp_next_lap,mp_media_generation,mp_source_revision,mp_prepared_revision,mp_predecessor_media_id,mp_predecessor_media_generation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'','',?,'prepared',?,?,?,?,?,?,?,?,?,?)`, "ssssssisii sisii ssiiisisi", w),
		mpDefinition("mp.next.cancel", `UPDATE playback_v1_prepared SET state='canceled',cancel_reason=? WHERE token=? AND queue_id=? AND state='prepared'`, "sss", w),
		mpDefinition("mp.next.commit", `UPDATE playback_v1_prepared SET state='committed' WHERE token=? AND queue_id=? AND state='prepared' AND queue_revision=? AND mp_custody_generation=? AND session_generation=? AND mp_media_generation=?`, "ssiiii", w),
		mpDefinition("mp.next.stop", `INSERT INTO mp_next_stop_obligations(next_id,media_session_id,media_generation,request_id,reason,requested_ms) SELECT ?,p.id,?,p.mp_retire_request_id,?,? FROM playback_sessions p WHERE p.id=? AND p.mp_retired_generation=? AND p.mp_retire_request_id<>''`, "sisis i", w),
		mpDefinition("mp.next.revoked", `SELECT 1 FROM playback_v1_prepared n JOIN authorization_session_families f ON f.id=n.mp_family_id WHERE n.token=? AND n.queue_id=? AND n.media_session_id=? AND n.mp_media_generation=? AND n.state='prepared' AND (f.revoked=1 OR n.expires_ms<=?)`, "sssii", r),
		mpDefinition("mp.next.exit_fact", `SELECT o.request_id,o.requested_ms,o.exited_ms,p.mp_retired_generation,p.mp_exited_generation,p.mp_retire_request_id,p.mp_exit_request_id,p.mp_exit_supervisor_incarnation,p.mp_exited_ms FROM mp_next_stop_obligations o JOIN playback_sessions p ON p.id=o.media_session_id WHERE o.next_id=? AND o.media_session_id=? AND o.media_generation=?`, "ssi", r),
		mpDefinition("mp.next.exit", `UPDATE mp_next_stop_obligations SET exited_ms=? WHERE next_id=? AND media_session_id=? AND media_generation=? AND request_id=? AND exited_ms IS NULL AND requested_ms<=? AND EXISTS(SELECT 1 FROM playback_sessions p WHERE p.id=mp_next_stop_obligations.media_session_id AND p.mp_retired_generation=mp_next_stop_obligations.media_generation AND p.mp_exited_generation=mp_next_stop_obligations.media_generation AND p.mp_retire_request_id=mp_next_stop_obligations.request_id AND p.mp_exit_request_id=mp_next_stop_obligations.request_id AND p.mp_exit_supervisor_incarnation=? AND p.mp_exited_ms=?)`, "issisibi", w),
	}
}

func mpLoadNextSession(ctx context.Context, t *commandTurn, id string) (out mpNextSession, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mpQuery(ctx, t, "mp.next.session", id)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.id, &out.device, &out.account, &out.profile, &out.authority, &out.queue, &out.entry, &out.state, &out.media, &out.generation, &out.mediaGeneration, &out.lease, &out.ended)
	return out, err
}
func mpLoadPrepared(ctx context.Context, t *commandTurn, queue, token string) (out mpNextPrepared, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mpQuery(ctx, t, "mp.next.prepared", token, queue)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.token, &out.queue, &out.device, &out.family, &out.session, &out.sessionState, &out.currentEntry, &out.entry, &out.media, &out.sourceRevision, &out.state, &out.reason, &out.sessionGeneration, &out.queueRevision, &out.custody, &out.nextPosition, &out.nextLap, &out.item, &out.mediaGeneration, &out.preparedRevision, &out.expires, &out.created, &out.predecessorMedia, &out.predecessorMediaGeneration)
	return out, err
}

func mpSessionMatches(h mpQueueHead, s mpNextSession, a mpActor, now time.Time) bool {
	return h.session == s.id && h.entry == s.entry && h.id == s.queue && s.generation > 0 && s.mediaGeneration > 0 && s.ended == 0 && s.lease > now.UnixMilli() && s.device == a.device && s.account == a.account && s.profile == a.profile && s.authority == "local" && (s.state == "playing" || s.state == "paused")
}

type mpAdvanceInput struct {
	token, queue, reason, entry, completionSession string
	revision, custody, completionGeneration        int64
	now                                            time.Time
}

func mpPlanAdvance(l mpQueueLayout, q mpAdvanceInput) (mpQueueLayout, bool, error) {
	l = l.clone()
	total := l.total()
	if total == 0 {
		return l, false, errMPEnd
	}
	old := l.head.position
	oldAutomatic := l.head.automatic
	switch q.reason {
	case "entry":
		p, err := l.position(q.entry)
		if err != nil {
			return l, false, err
		}
		l.head.position = p
	case "previous":
		if old == 0 && l.head.repeat == "all" {
			l.head.position = total - 1
			if l.head.shuffle && l.head.lap > 0 {
				l.head.lap--
			}
		} else {
			l.head.position = max(int64(0), old-1)
		}
	case "completion":
		if l.head.repeat == "one" {
			return l, true, nil
		}
		fallthrough
	case "next":
		if old+1 < total {
			l.head.position++
		} else if l.head.repeat == "all" {
			l.head.position = 0
			if l.head.shuffle {
				if l.head.lap == math.MaxUint32 {
					return l, false, errMPBound
				}
				l.head.lap++
			}
		} else {
			return l, false, errMPEnd
		}
	default:
		return l, false, ErrInvalid
	}

	if q.reason != "completion" {
		l.head.automatic = 0
	}
	return l, l.head.position != old || l.head.automatic != oldAutomatic || q.reason == "completion" || q.reason == "next", nil
}
func mpRebuildLap(old, next mpQueueLayout) (mpQueueLayout, error) {
	if !next.head.shuffle || old.head.lap == next.head.lap {
		return next, nil
	}
	next.spans = []mpQueueSpan{{false, 0, next.head.domain}}
	if tail := next.rawTotal() - next.head.domain; tail > 0 {
		next.spans = append(next.spans, mpQueueSpan{true, next.head.domain, tail})
	}
	// Cut in descending selected order, keeping the pass bounded by tombstones.
	var cuts []int64
	var base int64
	for _, g := range next.segments {
		for _, o := range next.removals[g.snapshot] {
			if o >= g.first && o < g.first+g.count {
				cuts = append(cuts, next.basePosition(base+o-g.first))
			}
		}
		base += g.count
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i] > cuts[j] })
	for _, p := range cuts {
		var err error
		next.spans, err = mpSpanCut(next.spans, p, 1, nil)
		if err != nil {
			return next, err
		}
	}
	return next, next.validate()
}

type mpPrepareNextInput struct {
	token, queue, nextToken, key, digest, media, sourceRevision                   string
	revision, custody, sessionGeneration, mediaGeneration, item, preparedRevision int64
	now                                                                           time.Time
}

func mpPrepareQueueNext(ctx context.Context, t *commandTurn, q mpPrepareNextInput) (out mpNextBirthEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpPrepareNext); err != nil {
		return out, err
	}
	if !mpID(q.nextToken) || !mpID(q.key) || len(q.digest) != 64 || q.item < 1 || q.mediaGeneration < 1 {
		return out, ErrInvalid
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
	s, err := mpLoadNextSession(ctx, t, h.session)
	if err != nil {
		return out, err
	}
	if !mpSessionMatches(h, s, a, q.now) || s.generation != q.sessionGeneration || q.media == s.media {
		return out, ErrConflict
	}
	l, err := mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	next, _, err := mpPlanAdvance(l, mpAdvanceInput{reason: "completion"})
	if err != nil {
		return out, err
	}
	next, err = mpRebuildLap(l, next)
	if err != nil {
		return out, err
	}
	entry, err := next.entryAt(next.head.position)
	if err != nil {
		return out, err
	}
	itemKey, e := mpQueueKey(ctx, t, next, entry)
	if e != nil {
		return out, e
	}
	row, e := mpQuery(ctx, t, "mp.next.item", itemKey, q.item)
	if e != nil {
		return out, e
	}
	var item int64
	if e = row.Scan(&item); e != nil {
		return out, e
	}
	// Exact media was prepared outside the writer and is still private to the
	// captured predecessor. Source/pin/config and lease are rechecked natively.
	if err = mpCheckNextMedia(ctx, t, a, q.media, s.media, q.item, q.mediaGeneration, q.preparedRevision, q.sourceRevision, q.now); err != nil {
		return out, err
	}
	if _, err = mpCancelQueuePreparations(ctx, t, h.id, "replaced", q.now); err != nil {
		return out, err
	}
	expires := min(q.now.Add(time.Minute).UnixMilli(), a.until.UnixMilli(), s.lease)
	err = mpExec(ctx, t, "mp.next.insert", q.nextToken, h.id, a.device, q.key, q.digest, h.session, s.generation, s.state, h.revision, next.head.position, entry, q.item, q.media, expires, q.now.UnixMilli(), a.family, h.entry, h.custody, next.head.lap, q.mediaGeneration, q.sourceRevision, q.preparedRevision, s.media, s.mediaGeneration)
	if err != nil {
		return out, err
	}
	out.result = mpEffect(l, true)
	out.result.token = q.nextToken
	out.result.entry = entry
	out.result.position = next.head.position
	out.result.lap = next.head.lap
	out.prepared = mpNextPrepared{token: q.nextToken, queue: h.id, device: a.device, family: a.family, session: h.session, sessionState: s.state, currentEntry: h.entry, entry: entry, media: q.media, sourceRevision: q.sourceRevision, state: "prepared", sessionGeneration: s.generation, queueRevision: h.revision, custody: h.custody, nextPosition: next.head.position, nextLap: next.head.lap, item: q.item, mediaGeneration: q.mediaGeneration, preparedRevision: q.preparedRevision, expires: expires, created: q.now.UnixMilli(), predecessorMedia: s.media, predecessorMediaGeneration: s.mediaGeneration}
	return out, nil
}
func mpCheckNextMedia(ctx context.Context, t *commandTurn, a mpActor, media, session string, item, generation, preparedRevision int64, sourceRevision string, now time.Time) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	row, err := mpQuery(ctx, t, "mp.next.media", media, a.account, a.profile, item, a.family, generation, sourceRevision, session, now.UnixMilli())
	if err != nil {
		return err
	}
	var got, revision int64
	var expiry string
	if err = row.Scan(&got, &expiry, &revision); err != nil {
		return err
	}
	deadline, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !now.Before(deadline) || got != generation || preparedRevision != revision {
		return ErrConflict
	}
	return nil
}

func mpCancelQueuePreparations(ctx context.Context, t *commandTurn, queue, reason string, now time.Time) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	rows, err := mpRows(ctx, t, "mp.next.outstanding", queue)
	if err != nil {
		return out, err
	}
	var tokens []string
	for rows.Next() {
		var token string
		if err = rows.Scan(&token); err != nil {
			break
		}
		tokens = append(tokens, token)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	if len(tokens) > 1 {
		return out, errMPBound
	}
	for _, token := range tokens {
		p, err := mpLoadPrepared(ctx, t, queue, token)
		if err != nil {
			return out, err
		}
		if err = mpCancelPrepared(ctx, t, p, reason, now); err != nil {
			return out, err
		}
		out++
	}
	return out, nil
}
func mpCancelPrepared(ctx context.Context, t *commandTurn, p mpNextPrepared, reason string, now time.Time) error {
	if !mpID(p.media) || p.mediaGeneration < 1 || len(reason) > 64 {
		return ErrInvalid
	}
	if err := mpRecordNextStop(ctx, t, p.token, p.media, p.mediaGeneration, reason, now); err != nil {
		return err
	}
	return mpExec(ctx, t, "mp.next.cancel", reason, p.token, p.queue)
}

func mpCommitQueueNext(ctx context.Context, t *commandTurn, token, queue, nextToken, newSession string, now time.Time) (out mpNextEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpCommitNext); err != nil {
		return out, err
	}
	if !mpID(newSession) {
		return out, ErrInvalid
	}
	h, err := mpQueueBoundary(ctx, t, token, queue, now, false, false)
	if err != nil {
		return out, err
	}
	a, err := mpCurrentActor(ctx, t, token, now)
	if err != nil {
		return out, err
	}
	p, err := mpLoadPrepared(ctx, t, queue, nextToken)
	if err != nil {
		return out, err
	}
	// Original COMMITTED replay is served by W's saved receipt BEFORE this
	// fresh path. Never rebuild an answer from today's queue/session/source.
	if p.state != "prepared" {
		return out, ErrConflict
	}
	s, err := mpLoadNextSession(ctx, t, p.session)
	if err != nil {
		return out, err
	}
	reason := mpPreparedFence(h, s, p, a, now)
	if reason != "" {
		if err = mpCancelPrepared(ctx, t, p, reason, now); err != nil {
			return out, err
		}
		return mpNextEffect{result: mpQueueEffect{queue: queue, revision: h.revision, custody: h.custody, token: nextToken, reason: reason, changed: true, done: true}}, nil
	}
	old, err := mpLoadLayout(ctx, t, h)
	if err != nil {
		return out, err
	}
	next, _, err := mpPlanAdvance(old, mpAdvanceInput{reason: "completion"})
	if err != nil {
		return out, err
	}
	next, err = mpRebuildLap(old, next)
	if err != nil {
		return out, err
	}
	entry, err := next.entryAt(next.head.position)
	if err != nil {
		return out, err
	}
	if entry != p.entry || next.head.position != p.nextPosition || next.head.lap != p.nextLap {
		return out, ErrConflict
	}
	if err = mpCheckNextMedia(ctx, t, a, p.media, s.media, p.item, p.mediaGeneration, p.preparedRevision, p.sourceRevision, now); err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrConflict) {
			return out, err
		}
		if err = mpCancelPrepared(ctx, t, p, "source_changed", now); err != nil {
			return out, err
		}
		return mpNextEffect{result: mpQueueEffect{queue: queue, revision: h.revision, custody: h.custody, token: p.token, reason: "source_changed", changed: true, done: true}}, nil
	}
	if err = mpExec(ctx, t, "mp.next.commit", p.token, p.queue, p.queueRevision, p.custody, p.sessionGeneration, p.mediaGeneration); err != nil {
		return out, err
	}
	// Mark this preparation committed BEFORE the queue mutation cancels the
	// other outstanding preparations, so the adopted media is never stopped.
	next.head.session = newSession
	out.result, err = mpPersistLayout(ctx, t, old, next, now)
	if err != nil {
		return out, err
	}
	if err = mpRecordNextStop(ctx, t, p.token, s.media, s.mediaGeneration, "completed", now); err != nil {
		return out, err
	}
	out.result.done = true
	out.result.token = p.token
	out.result.session = newSession
	out.transfer = mpNextTransfer{queue: queue, fromSession: s.id, toSession: newSession, fromMedia: s.media, toMedia: p.media, entry: p.entry, state: s.state, fromGeneration: s.generation, toMediaGeneration: p.mediaGeneration, queueRevision: out.result.revision, custody: next.head.custody}
	return out, nil
}
func mpPreparedFence(h mpQueueHead, s mpNextSession, p mpNextPrepared, a mpActor, now time.Time) string {
	if p.family != a.family || p.device != a.device {
		return "authority_changed"
	}
	if p.expires <= now.UnixMilli() {
		return "expired"
	}
	if h.revision != p.queueRevision || h.custody != p.custody || h.entry != p.currentEntry {
		return "queue_changed"
	}
	if !mpSessionMatches(h, s, a, now) || s.generation != p.sessionGeneration || s.state != p.sessionState || s.media != p.predecessorMedia || s.mediaGeneration != p.predecessorMediaGeneration {
		return "session_changed"
	}
	return ""
}
func mpCancelQueueNext(ctx context.Context, t *commandTurn, token, queue, nextToken string, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpCancelNext); err != nil {
		return out, err
	}
	h, err := mpQueueBoundary(ctx, t, token, queue, now, false, false)
	if err != nil {
		return out, err
	}
	p, err := mpLoadPrepared(ctx, t, queue, nextToken)
	if err != nil {
		return out, err
	}
	out = mpEffect(mpQueueLayout{head: h}, false)
	out.token = nextToken
	if p.state != "prepared" {
		out.done = true
		return out, nil
	}
	if err = mpCancelPrepared(ctx, t, p, "explicit", now); err != nil {
		return out, err
	}
	out.changed = true
	out.done = true
	return out, nil
}

// Cleanup is allowed after revocation, with no receipt disclosure or personal
// evidence. The durable original family/deadline is the native cleanup scope;
// a caller-supplied revoked flag or new actor is never accepted.
func mpRevokeQueueNext(ctx context.Context, t *commandTurn, queue, nextToken string, now time.Time) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpCancelNext); err != nil {
		return out, err
	}
	p, err := mpLoadPrepared(ctx, t, queue, nextToken)
	if err != nil {
		return out, err
	}
	row, err := mpQuery(ctx, t, "mp.next.revoked", p.token, p.queue, p.media, p.mediaGeneration, now.UnixMilli())
	if err != nil {
		return out, err
	}
	var one int64
	if err = row.Scan(&one); err != nil {
		return out, err
	}
	if err = mpCancelPrepared(ctx, t, p, "revoked_or_expired", now); err != nil {
		return out, err
	}
	return mpQueueEffect{queue: queue, revision: p.queueRevision, custody: p.custody, token: p.token, reason: "revoked_or_expired", changed: true, done: true}, nil
}
func mpRecordNextPhysicalExit(ctx context.Context, t *commandTurn, queue, nextToken, media, requestID string, generation, exited int64, supervisor [16]byte) (out mpQueueEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, mpNextPhysicalExit); err != nil {
		return out, err
	}
	if supervisor == [16]byte{} || generation < 1 || exited < 1 || requestID == "" || len(requestID) > 512 {
		return out, ErrInvalid
	}
	p, err := mpLoadPrepared(ctx, t, queue, nextToken)
	if err != nil {
		return out, err
	}
	fact, e := mpReadStopExit(ctx, t, "mp.next.exit_fact", nextToken, media, generation)
	if e != nil {
		return out, e
	}
	if !mpStopExitMatches(fact, requestID, generation, exited, supervisor) {
		return out, ErrConflict
	}
	if fact.exited.Valid {
		return mpQueueEffect{queue: queue, revision: p.queueRevision, custody: p.custody, token: nextToken, done: true}, nil
	}
	if err = mpExec(ctx, t, "mp.next.exit", exited, nextToken, media, generation, requestID, exited, supervisor[:], exited); err != nil {
		return out, err
	}
	return mpQueueEffect{queue: queue, revision: p.queueRevision, custody: p.custody, token: nextToken, changed: true, done: true}, nil
}

func mpRecordNextStop(ctx context.Context, t *commandTurn, token, media string, generation int64, reason string, now time.Time) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !mpID(token) || len(reason) > 64 || now.UnixMilli() < 1 {
		return ErrInvalid
	}
	proof, err := mpReadPhysicalStop(ctx, t, media, generation)
	if err != nil {
		return err
	}
	if !proof.pending {
		return nil
	}
	return mpExec(ctx, t, "mp.next.stop", token, generation, reason, now.UnixMilli(), media, generation)
}
