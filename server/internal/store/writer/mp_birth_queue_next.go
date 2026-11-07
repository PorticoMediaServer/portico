package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
	"unicode/utf8"
)

type mpBirthStoredDebt struct{ rows, variableBytes, oldControlBytes, newControlBytes int64 }
type mpQueueBirthReceipt struct {
	header          mpPublicBirthHeader
	rootIncarnation [16]byte
	effect          mpQueueBirthEffect
}
type mpNextBirthReceipt struct {
	header          mpPublicBirthHeader
	rootIncarnation [16]byte
	effect          mpNextBirthEffect
	presentationURL string
}

func mpBirthText(s string, maxBytes int) bool {
	if len(s) == 0 || len(s) > maxBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func mpQueueBirthDigest(v mpQueueBirthReceipt) (ResultDigest, error) {
	h, e := v.header, v.effect
	if !h.valid() || v.rootIncarnation == ([16]byte{}) || v.rootIncarnation == h.identity.Incarnation || !mpID(e.result.queue) || e.result.revision != 1 || e.result.custody != 0 || !e.result.changed || e.result.done || e.result.entry != "" || e.result.session != "" || e.result.token != "" || e.result.reason != "" || e.result.position != 0 || e.result.lap != 0 || e.result.cursor != 0 || len(e.snapshots) < 1 || len(e.snapshots) > 16 {
		return ResultDigest{}, ErrResolveRequired
	}
	enc, err := mpPrepReceiptEncoder(mpPrepReceiptHeader{identity: h.identity, request: h.request, kind: 750, appliedMS: h.appliedMS}, "mp_queue_birth")
	if err != nil {
		return ResultDigest{}, err
	}
	mpEncodePublicBirth(enc, h)
	enc.bytes(v.rootIncarnation[:])
	enc.text(e.result.queue)
	enc.number(uint64(e.result.count))
	enc.number(uint64(len(e.snapshots)))
	total := int64(0)
	seen := map[string]bool{}
	for _, s := range e.snapshots {
		if !mpSnapshotID(s.id) || seen[s.id] || !mpBirthText(s.sourceRevision, 256) || s.count < 0 || s.count > mpMaxQueueKeys-total {
			return ResultDigest{}, ErrResolveRequired
		}
		seen[s.id] = true
		total += s.count
		enc.text(s.id)
		enc.text(s.sourceRevision)
		enc.number(uint64(s.count))
	}
	if total != e.result.count || enc.err != nil {
		return ResultDigest{}, ErrResolveRequired
	}
	var d ResultDigest
	copy(d[:], enc.h.Sum(nil))
	return d, nil
}
func mpNextBirthDigest(v mpNextBirthReceipt) (ResultDigest, error) {
	h, e := v.header, v.effect
	p := e.prepared
	if !h.valid() || v.rootIncarnation == ([16]byte{}) || v.rootIncarnation == h.identity.Incarnation || len(v.presentationURL) == 0 || len(v.presentationURL) > 16384 || !mpBirthText(v.presentationURL, 16384) || p.device != h.scope.device || p.family != h.scope.family || p.state != "prepared" || p.reason != "" || p.item <= 0 || p.sessionGeneration <= 0 || p.queueRevision <= 0 || p.custody < 0 || p.nextPosition < 0 || p.nextLap < 0 || p.mediaGeneration <= 0 || p.predecessorMediaGeneration <= 0 || !mpBirthText(p.sourceRevision, 4096) || p.preparedRevision <= 0 || p.expires <= p.created || p.created <= 0 {
		return ResultDigest{}, ErrResolveRequired
	}
	for _, s := range []string{p.token, p.queue, p.device, p.family, p.session, p.currentEntry, p.entry, p.media, p.predecessorMedia} {
		if !mpID(s) {
			return ResultDigest{}, ErrResolveRequired
		}
	}
	if p.sessionState != "playing" && p.sessionState != "paused" {
		return ResultDigest{}, ErrResolveRequired
	}
	r := e.result
	if r.queue != p.queue || r.session != p.session || r.token != p.token || r.entry != p.entry || r.revision != p.queueRevision || r.custody != p.custody || r.position != p.nextPosition || r.lap != p.nextLap || !r.changed || r.done || r.reason != "" || r.count != 0 || r.cursor != 0 {
		return ResultDigest{}, ErrResolveRequired
	}
	enc, err := mpPrepReceiptEncoder(mpPrepReceiptHeader{identity: h.identity, request: h.request, kind: 761, appliedMS: h.appliedMS}, "mp_next_birth")
	if err != nil {
		return ResultDigest{}, err
	}
	mpEncodePublicBirth(enc, h)
	enc.bytes(v.rootIncarnation[:])
	for _, s := range []string{p.token, p.queue, p.device, p.family, p.session, p.sessionState, p.currentEntry, p.entry, p.media, p.sourceRevision, p.predecessorMedia, v.presentationURL} {
		enc.text(s)
	}
	for _, n := range []int64{p.sessionGeneration, p.queueRevision, p.custody, p.nextPosition, p.nextLap, p.item, p.mediaGeneration, p.preparedRevision, p.predecessorMediaGeneration, p.expires, p.created} {
		enc.number(uint64(n))
	}
	if enc.err != nil {
		return ResultDigest{}, enc.err
	}
	var d ResultDigest
	copy(d[:], enc.h.Sum(nil))
	return d, nil
}
func storeMPQueueBirth(ctx context.Context, t *commandTurn, v mpQueueBirthReceipt) (out mpBirthStoredDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 750); err != nil {
		return out, err
	}
	d, e := mpQueueBirthDigest(v)
	if e != nil || d != v.header.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	h, b := v.header, v.effect
	row, e := mpPrepRow(ctx, t, "mp.birth.queue.root_before", b.result.queue)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.oldControlBytes); e != nil {
		return out, e
	}
	args := mpPublicBirthArgs(h, 750)
	args = append(args, b.result.queue, v.rootIncarnation[:], b.result.revision, b.result.custody, b.snapshots[0].id, "building", b.result.count, int64(len(b.snapshots)), int64(1+len(b.snapshots)))
	row, e = mpPrepRow(ctx, t, "mp.birth.queue.insert", args...)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.variableBytes); e != nil {
		return out, e
	}
	out.rows = 1
	for i, s := range b.snapshots {
		row, e = mpPrepRow(ctx, t, "mp.birth.queue.snapshot_insert", h.birthID[:], int64(i), s.id, s.sourceRevision, s.count)
		if e != nil {
			return out, e
		}
		var bytes int64
		if e = row.Scan(&bytes); e != nil {
			return out, e
		}
		if bytes <= 0 || bytes > math.MaxInt64-out.variableBytes {
			return out, ErrResolveRequired
		}
		out.rows++
		out.variableBytes += bytes
	}
	row, e = mpPrepRow(ctx, t, "mp.birth.queue.link", v.rootIncarnation[:], h.birthID[:], b.result.queue, h.scope.authority, h.scope.account, h.scope.profile, h.scope.device, out.oldControlBytes)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.newControlBytes); e != nil {
		return out, e
	}
	if out.variableBytes <= 0 || out.newControlBytes < out.oldControlBytes {
		return out, ErrResolveRequired
	}
	return out, t.checkLifetimes(ctx)
}
func storeMPNextBirth(ctx context.Context, t *commandTurn, v mpNextBirthReceipt) (out mpBirthStoredDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 761); err != nil {
		return out, err
	}
	d, e := mpNextBirthDigest(v)
	if e != nil || d != v.header.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	h, p := v.header, v.effect.prepared
	row, e := mpPrepRow(ctx, t, "mp.birth.next.root_before", p.token)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.oldControlBytes); e != nil {
		return out, e
	}
	args := mpPublicBirthArgs(h, 761)
	args = append(args, p.device, p.sessionState, p.currentEntry, p.item, p.nextPosition, p.nextLap, p.mediaGeneration, p.sourceRevision, p.preparedRevision, p.predecessorMedia, p.predecessorMediaGeneration, p.created, p.token, v.rootIncarnation[:], p.queue, p.queueRevision, p.custody, p.session, p.sessionGeneration, p.entry, p.media, p.expires, v.presentationURL)
	row, e = mpPrepRow(ctx, t, "mp.birth.next.insert", args...)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.variableBytes); e != nil {
		return out, e
	}
	out.rows = 1
	row, e = mpPrepRow(ctx, t, "mp.birth.next.link", v.rootIncarnation[:], h.birthID[:], p.token, p.queue, p.device, p.family, p.session, p.sessionGeneration, p.queueRevision, p.custody, p.media, p.mediaGeneration, p.sourceRevision, p.preparedRevision, p.predecessorMedia, p.predecessorMediaGeneration, out.oldControlBytes)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.newControlBytes); e != nil {
		return out, e
	}
	if out.variableBytes <= 0 || out.newControlBytes < out.oldControlBytes {
		return out, ErrResolveRequired
	}
	return out, t.checkLifetimes(ctx)
}

// Current opaque disclosure, public-key resolution and canonical classifier
// precede these immutable decoders. No removable resource is looked up here.
func loadMPQueueBirth(ctx context.Context, t *commandTurn, expected mpPublicBirthHeader) (out mpQueueBirthReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 750); err != nil {
		return out, err
	}
	row, e := mpPrepRow(ctx, t, "mp.birth.queue.load", expected.birthID[:], expected.identity.Incarnation[:])
	if e != nil {
		return out, e
	}
	var scan mpPublicBirthScan
	var inc []byte
	var first, state string
	var snapshots, count int64
	b := &out.effect.result
	dest := scan.destinations()
	dest = append(dest, &b.queue, &inc, &b.revision, &b.custody, &first, &state, &b.count, &snapshots, &count)
	if e = row.Scan(dest...); e != nil {
		return out, e
	}
	out.header, e = scan.finish(expected, 750)
	if e != nil {
		return out, e
	}
	if len(inc) != 16 || snapshots < 1 || snapshots > 16 || count != 1+snapshots || state != "building" {
		return out, ErrResolveRequired
	}
	copy(out.rootIncarnation[:], inc)
	b.changed = true
	step, ok := t.steps["mp.birth.queue.snapshots"]
	if !ok {
		return out, ErrInvalid
	}
	rows, e := t.query(ctx, step.statementStep, expected.birthID[:])
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var ordinal int64
		var s mpQueueSnapshotBirthResult
		if e = rows.Scan(&ordinal, &s.id, &s.sourceRevision, &s.count); e != nil {
			break
		}
		if ordinal != int64(len(out.effect.snapshots)) || ordinal >= snapshots {
			e = ErrResolveRequired
			break
		}
		out.effect.snapshots = append(out.effect.snapshots, s)
	}
	if e = errors.Join(e, rows.Err(), rows.Close()); e != nil {
		return out, e
	}
	if int64(len(out.effect.snapshots)) != snapshots || out.effect.snapshots[0].id != first {
		return out, ErrResolveRequired
	}
	d, e := mpQueueBirthDigest(out)
	if e != nil || d != out.header.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	return out, t.checkLifetimes(ctx)
}
func loadMPNextBirth(ctx context.Context, t *commandTurn, expected mpPublicBirthHeader) (out mpNextBirthReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 761); err != nil {
		return out, err
	}
	row, e := mpPrepRow(ctx, t, "mp.birth.next.load", expected.birthID[:], expected.identity.Incarnation[:])
	if e != nil {
		return out, e
	}
	var scan mpPublicBirthScan
	var inc []byte
	p := &out.effect.prepared
	dest := scan.destinations()
	dest = append(dest, &p.device, &p.sessionState, &p.currentEntry, &p.item, &p.nextPosition, &p.nextLap, &p.mediaGeneration, &p.sourceRevision, &p.preparedRevision, &p.predecessorMedia, &p.predecessorMediaGeneration, &p.created, &p.token, &inc, &p.queue, &p.queueRevision, &p.custody, &p.session, &p.sessionGeneration, &p.entry, &p.media, &p.expires, &out.presentationURL)
	if e = row.Scan(dest...); e != nil {
		return out, e
	}
	out.header, e = scan.finish(expected, 761)
	if e != nil {
		return out, e
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.rootIncarnation[:], inc)
	p.family = out.header.scope.family
	p.state = "prepared"
	out.effect.result = mpQueueEffect{queue: p.queue, session: p.session, token: p.token, entry: p.entry, revision: p.queueRevision, custody: p.custody, position: p.nextPosition, lap: p.nextLap, changed: true}
	d, e := mpNextBirthDigest(out)
	if e != nil || d != out.header.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	return out, t.checkLifetimes(ctx)
}

func mpQueueNextBirthDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpQueueNextBirthDefinition("mp.birth.queue.insert", `INSERT INTO mp_queue_births(birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,result_queue_id,result_queue_incarnation,result_revision,result_custody_generation,result_snapshot_id,result_state,result_total,result_snapshot_count,result_count,last_sequence,acknowledged_sequence,result_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,1) RETURNING variable_bytes`, "bbibbsssssssissbiibsbiissiii", stmt.Write),
		mpQueueNextBirthDefinition("mp.birth.queue.load", `SELECT birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,result_queue_id,result_queue_incarnation,result_revision,result_custody_generation,result_snapshot_id,result_state,result_total,result_snapshot_count,result_count FROM mp_queue_births WHERE birth_id=? AND command_incarnation=? AND command_kind=750 AND result_version=1 AND last_sequence=1 AND acknowledged_sequence=0`, "bb", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.queue.lookup", `SELECT birth_id,command_incarnation,last_sequence,acknowledged_sequence,operation_id,request_digest,result_digest FROM mp_queue_births WHERE installation_incarnation=? AND root_id=? AND registration_revision=? AND device_id=? AND client_key_digest=?`, "ssisb", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.next.insert", `INSERT INTO mp_next_births(birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,result_device_id,result_session_state,result_current_entry_id,result_item_id,result_next_position,result_next_lap,result_media_generation,result_source_revision,result_prepared_revision,result_predecessor_media_id,result_predecessor_media_generation,result_created_ms,result_token,result_next_incarnation,result_queue_id,result_queue_revision,result_custody_generation,result_session_id,result_session_generation,result_entry_id,result_media_session_id,result_expires_ms,result_presentation_url,last_sequence,acknowledged_sequence,result_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,1) RETURNING variable_bytes`, "bbibbsssssssissbiibsssiiiisisiisbsiisissis", stmt.Write),
		mpQueueNextBirthDefinition("mp.birth.next.load", `SELECT birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,result_device_id,result_session_state,result_current_entry_id,result_item_id,result_next_position,result_next_lap,result_media_generation,result_source_revision,result_prepared_revision,result_predecessor_media_id,result_predecessor_media_generation,result_created_ms,result_token,result_next_incarnation,result_queue_id,result_queue_revision,result_custody_generation,result_session_id,result_session_generation,result_entry_id,result_media_session_id,result_expires_ms,result_presentation_url FROM mp_next_births WHERE birth_id=? AND command_incarnation=? AND command_kind=761 AND result_version=1 AND last_sequence=1 AND acknowledged_sequence=0`, "bb", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.next.lookup", `SELECT birth_id,command_incarnation,last_sequence,acknowledged_sequence,operation_id,request_digest,result_digest FROM mp_next_births WHERE installation_incarnation=? AND root_id=? AND registration_revision=? AND device_id=? AND client_key_digest=?`, "ssisb", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.queue.root_before", `SELECT mp_control_variable_bytes FROM queues_v1 WHERE id=? AND mp_command_incarnation IS NULL AND mp_birth_id IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0`, "s", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.queue.link", `UPDATE queues_v1 SET mp_command_incarnation=?,mp_birth_id=? WHERE id=? AND authority=? AND account_id=? AND profile_id=? AND owner_kind='device' AND owner_id='~'||? AND revision=1 AND mp_command_incarnation IS NULL AND mp_birth_id IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "bbsssssi", stmt.Write),
		mpQueueNextBirthDefinition("mp.birth.queue.snapshot_insert", `INSERT INTO mp_queue_birth_snapshots(birth_id,ordinal,snapshot_id,source_revision,count) VALUES(?,?,?,?,?) RETURNING variable_bytes`, "bissi", stmt.Write),
		mpQueueNextBirthDefinition("mp.birth.queue.snapshots", `SELECT ordinal,snapshot_id,source_revision,count FROM mp_queue_birth_snapshots WHERE birth_id=? ORDER BY ordinal LIMIT 17`, "b", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.next.root_before", `SELECT mp_control_variable_bytes FROM playback_v1_prepared WHERE token=? AND mp_command_incarnation IS NULL AND mp_birth_id IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0`, "s", stmt.Read),
		mpQueueNextBirthDefinition("mp.birth.next.link", `UPDATE playback_v1_prepared SET mp_command_incarnation=?,mp_birth_id=? WHERE token=? AND queue_id=? AND device_id=? AND mp_family_id=? AND session_id=? AND session_generation=? AND queue_revision=? AND mp_custody_generation=? AND media_session_id=? AND mp_media_generation=? AND mp_source_revision=? AND mp_prepared_revision=? AND mp_predecessor_media_id=? AND mp_predecessor_media_generation=? AND mp_command_incarnation IS NULL AND mp_birth_id IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND state='prepared' AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "bbsssssiiisisisii", stmt.Write),
	}
}

func mpQueueNextBirthDefinition(id, sql, types string, kind stmt.Kind) stmt.Definition {
	d := mpPrepDefinition(id, sql, types, kind)
	d.CallSites = []string{"store/writer/mp_birth_queue_next.go"}
	return d
}
