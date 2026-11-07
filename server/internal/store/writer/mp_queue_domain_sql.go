package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Definitions remain inert until captured on the adopted schema. Compact type
// strings below describe STATIC SQL, never request-selected SQL or identifiers.
func mpDefinition(id, query, types string, kind stmt.Kind) stmt.Definition {
	d := stmt.Definition{ID: stmt.ID(id), SQL: query, Owner: "writer.mp", Kind: kind, CallSites: []string{"writer/mp_queue_domain_sql.go", "writer/mp_queue_domain_commands.go", "writer/mp_next.go", "writer/mp_reading.go"}}
	types = strings.ReplaceAll(types, " ", "")
	var bindings []any
	for i, c := range types {
		p := stmt.Param{Name: fmt.Sprintf("p%d", i)}
		switch c {
		case 's':
			p.Type = stmt.Text
			bindings = append(bindings, "missing")
		case 'i':
			p.Type = stmt.Integer
			bindings = append(bindings, int64(1))
		case 'b':
			p.Type = stmt.Blob
			bindings = append(bindings, make([]byte, 32))
		case 'r':
			p.Type = stmt.Real
			bindings = append(bindings, float64(0))
		case 'n':
			p.Type = stmt.Integer
			p.Nullable = true
			bindings = append(bindings, nil)
		}
		d.Params = append(d.Params, p)
	}
	d.Cohorts = []stmt.Cohort{{Name: "draft_shape_only", Bindings: bindings}}
	return d
}

const mpPrincipalSQL = `SELECT f.id,f.device_id,f.account_id,f.profile_id,m.incarnation,p.lifetime,t.expires_at,f.authorization_horizon,f.authority_generation
 FROM authorization_family_tokens t
 CROSS JOIN authorization_session_families f ON f.id=t.family_id
 CROSS JOIN identity_state z ON z.singleton=1 AND z.authority_generation=f.authority_generation
 CROSS JOIN identity_devices d ON d.id=f.device_id AND d.account_id=f.account_id AND d.authority=f.authority
 CROSS JOIN accounts a ON a.id=f.account_id AND a.epoch=f.epoch
 CROSS JOIN direct_memberships m ON m.account_id=a.id
 CROSS JOIN direct_profiles p ON p.account_id=a.id AND p.id=f.profile_id
 WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation
 AND f.authority='local' AND f.revoked=0 AND f.pin_gate=0 AND m.disabled=0 AND p.deleted=0
 AND f.command_member_incarnation=m.incarnation AND f.command_profile_lifetime=p.lifetime`

const mpQueueHeadSQL = `SELECT id,owner_kind,owner_id,authority,account_id,profile_id,session_id,current_entry_id,repeat,
 revision,custody_generation,current_position,shuffle_seed,shuffle_lap,shuffle_domain,shuffle_first,automatic_advances,created_ms,updated_ms,shuffle_on
 FROM queues_v1 WHERE id=?`

func mpQueueDefinitions() []stmt.Definition {
	r, w := stmt.Read, stmt.Write
	return []stmt.Definition{
		mpDefinition("mp.principal", mpPrincipalSQL, "s", r),
		mpDefinition("mp.queue.head", mpQueueHeadSQL, "s", r),
		mpDefinition("mp.queue.metadata_bytes", `SELECT COALESCE(sum(length(CAST(g.id AS BLOB))+length(CAST(g.sort_key AS BLOB))+length(CAST(g.snapshot_id AS BLOB))+length(CAST(s.queue_id AS BLOB))+length(CAST(s.kind AS BLOB))+length(CAST(s.state AS BLOB))+length(CAST(s.source_revision AS BLOB))+128),0) FROM (SELECT id,sort_key,snapshot_id FROM queue_v1_segments WHERE queue_id=? ORDER BY sort_key LIMIT 1001) g JOIN queue_v1_snapshots s ON s.id=g.snapshot_id`, "s", r),
		mpDefinition("mp.queue.segments", `SELECT g.id,g.sort_key,g.snapshot_id,g.first_ordinal,g.count,g.removed,s.queue_id,s.kind,s.state,s.count,s.built,s.source_revision FROM queue_v1_segments g JOIN queue_v1_snapshots s ON s.id=g.snapshot_id WHERE g.queue_id=? ORDER BY g.sort_key LIMIT 1001`, "s", r),
		mpDefinition("mp.queue.removals", `SELECT ordinal FROM queue_v1_removals WHERE snapshot_id=? AND ordinal>=? AND ordinal<? ORDER BY ordinal LIMIT 10001`, "sii", r),
		mpDefinition("mp.queue.spans", `SELECT source,start,count FROM queue_v1_play_spans WHERE queue_id=? ORDER BY rank LIMIT 1001`, "s", r),
		mpDefinition("mp.queue.snapshot_bytes", `SELECT length(CAST(id AS BLOB))+length(CAST(queue_id AS BLOB))+length(CAST(kind AS BLOB))+length(CAST(selector AS BLOB))+length(CAST(label AS BLOB))+length(CAST(state AS BLOB))+length(CAST(source_revision AS BLOB)) FROM queue_v1_snapshots WHERE id=? AND queue_id=?`, "ss", r),
		mpDefinition("mp.queue.snapshot", `SELECT id,queue_id,kind,selector,label,state,count,built,source_revision FROM queue_v1_snapshots WHERE id=? AND queue_id=?`, "ss", r),
		mpDefinition("mp.queue.chunk_bytes", `SELECT length(keys) FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, "si", r),
		mpDefinition("mp.queue.chunk", `SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, "si", r),
		mpDefinition("mp.queue.begin", `INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,repeat,created_ms,updated_ms) VALUES(?,'device',?,'local',?,?,1,'off',?,?)`, "ssssii", w),
		mpDefinition("mp.queue.snapshot_begin", `INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width,state,built,source_revision) VALUES(?,?,?,?,?,?,0,'building',0,?)`, "sssss is", w),
		mpDefinition("mp.queue.chunk_stage", `INSERT INTO queue_v1_chunks(snapshot_id,chunk_no,keys) VALUES(?,?,?)`, "sib", w),
		mpDefinition("mp.queue.stage_cursor", `UPDATE queue_v1_snapshots SET built=?,width=max(width,?) WHERE id=? AND queue_id=? AND state='building' AND built=? AND count=?`, "iissii", w),
		mpDefinition("mp.queue.snapshot_seal", `UPDATE queue_v1_snapshots SET state='ready' WHERE id=? AND queue_id=? AND built=count AND state='building'`, "ss", w),
		mpDefinition("mp.queue.segment_insert", `INSERT INTO queue_v1_segments(id,queue_id,sort_key,snapshot_id,first_ordinal,count,removed) VALUES(?,?,?,?,?,?,?)`, "ssssiii", w),
		mpDefinition("mp.queue.segment_update", `UPDATE queue_v1_segments SET sort_key=?,first_ordinal=?,count=?,removed=? WHERE id=? AND queue_id=? AND snapshot_id=? AND sort_key=? AND first_ordinal=? AND count=? AND removed=?`, "siiissssiii", w),
		mpDefinition("mp.queue.segment_delete", `DELETE FROM queue_v1_segments WHERE id=? AND queue_id=? AND snapshot_id=? AND first_ordinal=? AND count=? AND removed=?`, "sssiii", w),
		mpDefinition("mp.queue.removal_insert", `INSERT INTO queue_v1_removals(snapshot_id,ordinal) VALUES(?,?)`, "si", w),
		mpDefinition("mp.queue.span_insert", `INSERT INTO queue_v1_play_spans(queue_id,rank,source,start,count) VALUES(?,?,?,?,?)`, "siiii", w),
		mpDefinition("mp.queue.span_delete", `DELETE FROM queue_v1_play_spans WHERE queue_id=? AND rank=? AND source=? AND start=? AND count=?`, "siiii", w),
		mpDefinition("mp.queue.publish", `UPDATE queues_v1 SET owner_id=?,revision=?,custody_generation=?,current_position=?,current_entry_id=?,session_id=?,shuffle_on=?,shuffle_seed=?,shuffle_lap=?,shuffle_domain=?,shuffle_first=?,automatic_advances=?,updated_ms=? WHERE id=? AND owner_kind='device' AND owner_id=? AND authority='local' AND account_id=? AND profile_id=? AND revision=? AND custody_generation=?`, "siiissiiiiiiissssii", w),
		mpDefinition("mp.queue.retire", `UPDATE queues_v1 SET owner_id='!'||owner_id,revision=revision+1,custody_generation=custody_generation+1,session_id='',updated_ms=? WHERE id=? AND owner_kind='device' AND owner_id=? AND account_id=? AND profile_id=? AND authority='local' AND revision=? AND custody_generation=?`, "issssii", w),
		// Point cleanup only after children are physically absent. Never invoke
		// canonical ON DELETE CASCADE over arbitrary immutable chunk trees.
		mpDefinition("mp.queue.cleanup_chunks", `SELECT chunk_no,length(keys) FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no>=? ORDER BY chunk_no LIMIT 49`, "si", r),
		mpDefinition("mp.queue.cleanup_chunk", `DELETE FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, "si", w),
		mpDefinition("mp.queue.cleanup_removal", `DELETE FROM queue_v1_removals WHERE snapshot_id=? AND ordinal=?`, "si", w),
		mpDefinition("mp.queue.cleanup_snapshot", `DELETE FROM queue_v1_snapshots WHERE id=? AND queue_id=? AND NOT EXISTS(SELECT 1 FROM queue_v1_segments WHERE snapshot_id=?) AND NOT EXISTS(SELECT 1 FROM queue_v1_chunks WHERE snapshot_id=?) AND NOT EXISTS(SELECT 1 FROM queue_v1_removals WHERE snapshot_id=?)`, "sssss", w),
		mpDefinition("mp.queue.cleanup_snapshot_refs", `SELECT 1 FROM queue_v1_segments WHERE snapshot_id=? LIMIT 1`, "s", r),
	}
}

func mpQuery(ctx context.Context, t *commandTurn, id string, args ...any) (commandRow, error) {
	if t == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[stmt.ID(id)]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, step.statementStep, args...)
}
func mpRows(ctx context.Context, t *commandTurn, id string, args ...any) (*commandRows, error) {
	if t == nil {
		return nil, ErrInvalid
	}
	step, ok := t.steps[stmt.ID(id)]
	if !ok {
		return nil, ErrInvalid
	}
	return t.query(ctx, step.statementStep, args...)
}
func mpExec(ctx context.Context, t *commandTurn, id string, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if t == nil {
		return ErrInvalid
	}
	step, ok := t.steps[stmt.ID(id)]
	if !ok {
		return ErrInvalid
	}
	r, err := t.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

type mpActor struct {
	token, family, device, account, profile, member, lifetime string
	generation                                                int64
	until                                                     time.Time
}

// Current token/family/device/account epoch/membership/profile lifetimes are
// read by the CLOSED boundary, including before original receipt disclosure.
// An actor is not accepted from a request and its fields confer no authority.
func mpCurrentActor(ctx context.Context, t *commandTurn, token string, now time.Time) (out mpActor, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mpID(token) || now.IsZero() || now.UnixMilli() < 0 {
		return out, ErrInvalid
	}
	row, err := mpQuery(ctx, t, "mp.principal", token)
	if err != nil {
		return out, err
	}
	var expiry, horizon string
	err = row.Scan(&out.family, &out.device, &out.account, &out.profile, &out.member, &out.lifetime, &expiry, &horizon, &out.generation)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	}
	if err != nil {
		return out, err
	}
	e, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		return out, ErrDisclosureDenied
	}
	h, err := time.Parse(time.RFC3339Nano, horizon)
	if err != nil {
		return out, ErrDisclosureDenied
	}
	out.until = e
	if h.Before(e) {
		out.until = h
	}
	out.token = token
	if !now.Before(out.until) || len(out.member) != 32 || len(out.lifetime) != 32 || !mpID(out.device) || out.generation < 1 {
		return out, ErrDisclosureDenied
	}
	return out, nil
}
func mpLoadQueue(ctx context.Context, t *commandTurn, id string) (out mpQueueHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mpID(id) {
		return out, ErrInvalid
	}
	row, err := mpQuery(ctx, t, "mp.queue.head", id)
	if err != nil {
		return out, err
	}
	var shuffle int64
	err = row.Scan(&out.id, &out.ownerKind, &out.owner, &out.authority, &out.account, &out.profile, &out.session, &out.entry, &out.repeat, &out.revision, &out.custody, &out.position, &out.seed, &out.lap, &out.domain, &out.first, &out.automatic, &out.created, &out.updated, &shuffle)
	if err != nil {
		return out, err
	}
	if shuffle < 0 || shuffle > 1 {
		return out, ErrResolveRequired
	}
	out.shuffle = shuffle == 1
	return out, nil
}
func mpOwns(a mpActor, h mpQueueHead, staging, retired bool) bool {
	owner := a.device
	if staging {
		owner = "~" + owner
	}
	if retired {
		owner = "!" + owner
	}
	deviceOwns := h.owner == owner
	if !staging && !retired && h.custody == 0 {
		deviceOwns = !strings.HasPrefix(h.owner, "~") && !strings.HasPrefix(h.owner, "!")
	}
	return a.family != "" && h.ownerKind == "device" && deviceOwns && h.authority == "local" && h.account == a.account && h.profile == a.profile
}
func mpQueueBoundary(ctx context.Context, t *commandTurn, token, queue string, now time.Time, staging, retired bool) (out mpQueueHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	a, err := mpCurrentActor(ctx, t, token, now)
	if err != nil {
		return out, err
	}
	out, err = mpLoadQueue(ctx, t, queue)
	if err != nil {
		return out, err
	}
	if !mpOwns(a, out, staging, retired) {
		return out, ErrDisclosureDenied
	}
	return out, nil
}
func mpLoadSnapshot(ctx context.Context, t *commandTurn, queue, id string) (out mpQueueSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	pre, e := mpQuery(ctx, t, "mp.queue.snapshot_bytes", id, queue)
	if e != nil {
		return out, e
	}
	var bytes int64
	if e = pre.Scan(&bytes); e != nil {
		return out, e
	}
	if bytes < 0 || bytes > 70<<10 {
		return out, errMPBound
	}
	row, err := mpQuery(ctx, t, "mp.queue.snapshot", id, queue)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.id, &out.queue, &out.kind, &out.selector, &out.label, &out.state, &out.count, &out.built, &out.sourceRevision)
	return out, err
}
func mpLoadLayout(ctx context.Context, t *commandTurn, h mpQueueHead) (out mpQueueLayout, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out = mpQueueLayout{head: h, snapshots: map[string]mpQueueSnapshot{}, removals: map[string][]int64{}}
	row, err := mpQuery(ctx, t, "mp.queue.metadata_bytes", h.id)
	if err != nil {
		return out, err
	}
	var bytes int64
	if err = row.Scan(&bytes); err != nil {
		return out, err
	}
	if bytes > mpMaxLayoutBytes {
		return out, errMPBound
	}
	rows, err := mpRows(ctx, t, "mp.queue.segments", h.id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var g mpQueueSegment
		var s mpQueueSnapshot
		err = rows.Scan(&g.id, &g.key, &g.snapshot, &g.first, &g.count, &g.removed, &s.queue, &s.kind, &s.state, &s.count, &s.built, &s.sourceRevision)
		if err != nil {
			break
		}
		s.id = g.snapshot
		out.segments = append(out.segments, g)
		out.snapshots[s.id] = s
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	if len(out.segments) > mpMaxSegments {
		return out, errMPBound
	}
	removalCount := 0
	for id, s := range out.snapshots {
		rows, err = mpRows(ctx, t, "mp.queue.removals", id, int64(0), s.count)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var n int64
			if err = rows.Scan(&n); err != nil {
				break
			}
			removalCount++
			if removalCount > mpMaxRemovals {
				err = errMPBound
				break
			}
			out.removals[id] = append(out.removals[id], n)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return out, err
		}
	}
	rows, err = mpRows(ctx, t, "mp.queue.spans", h.id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s mpQueueSpan
		var source int64
		if err = rows.Scan(&source, &s.start, &s.count); err != nil {
			break
		}
		if source < 0 || source > 1 {
			err = ErrResolveRequired
			break
		}
		s.source = source == 1
		out.spans = append(out.spans, s)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	return out, out.validate()
}
func mpQueueKey(ctx context.Context, t *commandTurn, l mpQueueLayout, entry string) (out string, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	s, o, err := mpParseEntry(entry)
	if err != nil {
		return out, err
	}
	if _, err = l.position(entry); err != nil {
		return out, err
	}
	snap, ok := l.snapshots[s]
	if !ok {
		return out, ErrConflict
	}
	if o >= snap.built {
		return out, errMPPending
	}
	row, err := mpQuery(ctx, t, "mp.queue.chunk_bytes", s, o/mpChunkKeys)
	if err != nil {
		return out, err
	}
	var bytes int64
	if err = row.Scan(&bytes); err != nil {
		return out, err
	}
	if bytes < 2 || bytes > 1+mpChunkKeys*128 {
		return out, ErrResolveRequired
	}
	row, err = mpQuery(ctx, t, "mp.queue.chunk", s, o/mpChunkKeys)
	if err != nil {
		return out, err
	}
	var b []byte
	if err = row.Scan(&b); err != nil {
		return out, err
	}
	keys, err := mpDecodeChunk(b)
	if err != nil {
		return out, err
	}
	if o%mpChunkKeys >= int64(len(keys)) {
		return out, ErrResolveRequired
	}
	return keys[o%mpChunkKeys], nil
}
