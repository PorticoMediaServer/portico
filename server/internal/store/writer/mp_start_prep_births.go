package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Historical values copied by W from its admitted original public registration.
// These private values neither establish current disclosure nor mint authority.
// Kept distinct from the parent's shared birth values so this packet composes
// without depending on an uncommitted shared file.
type mpStartJobBirthScope struct {
	authority, account, profile, member, profileLifetime, installation, root, family, device string
	registration, issuedMS                                                                   int64
	keyDigest                                                                                [32]byte
}
type mpStartJobBirthHeader struct {
	birthID   [16]byte
	identity  CommandIdentity
	request   RequestDigest
	scope     mpStartJobBirthScope
	appliedMS int64
	digest    ResultDigest
}

func (s mpStartJobBirthScope) valid() bool {
	return (s.authority == "local" || s.authority == "hosted") && mpPrepTexts(s.account, s.profile, s.family, s.device) && lowerHex(s.member, 32) && lowerHex(s.profileLifetime, 32) && lowerHex(s.installation, 32) && len(s.root) == 22 && mpPrepTexts(s.root) && s.registration > 0 && s.issuedMS > 0 && s.keyDigest != ([32]byte{})
}
func (h mpStartJobBirthHeader) valid() bool {
	return h.birthID != ([16]byte{}) && h.identity.Incarnation != ([16]byte{}) && h.identity.OperationID != ([16]byte{}) && h.identity.Sequence == 1 && h.request != (RequestDigest{}) && h.scope.valid() && h.appliedMS >= h.scope.issuedMS
}
func (s mpStartJobBirthScope) matchesActor(a mpPrepActor) bool {
	return s.authority == a.authority && s.account == a.account && s.profile == a.profile && s.family == a.family && s.device == a.device
}
func (h mpStartJobBirthHeader) args(kind CommandID) []any {
	s := h.scope
	return []any{h.birthID[:], h.identity.Incarnation[:], int64(kind), h.identity.OperationID[:], h.request[:], s.authority, s.account, s.profile, s.member, s.profileLifetime, s.installation, s.root, s.registration, s.family, s.device, s.keyDigest[:], s.issuedMS, h.appliedMS, h.digest[:]}
}
func (h mpStartJobBirthHeader) encoder(kind CommandID, family string) (*requestEncoder, error) {
	if !h.valid() {
		return nil, ErrInvalid
	}
	e, err := mpPrepReceiptEncoder(mpPrepReceiptHeader{identity: h.identity, request: h.request, kind: kind, appliedMS: h.appliedMS}, family)
	if err != nil {
		return nil, err
	}
	e.bytes(h.birthID[:])
	s := h.scope
	for _, v := range []string{s.authority, s.account, s.profile, s.member, s.profileLifetime, s.installation, s.root, s.family, s.device} {
		e.text(v)
	}
	e.number(uint64(s.registration))
	e.number(uint64(s.issuedMS))
	e.bytes(s.keyDigest[:])
	return e, nil
}

// Creation results are the exact inline F projection, not a partially populated
// mutable domain effect or reconstructed public DTO.
type mpStartPrepBirthResult struct {
	preparation          string
	incarnation          [16]byte
	state                string
	revision, deadlineMS int64
	ids                  mpStartPrepStableIDs
}
type mpTranscodeBirthResult struct {
	job                     string
	incarnation             [16]byte
	state, sourceRevision   string
	revision, nextAttemptMS int64
}
type mpStartPrepBirthReceipt struct {
	header mpStartJobBirthHeader
	result mpStartPrepBirthResult
}
type mpTranscodeBirthReceipt struct {
	header mpStartJobBirthHeader
	result mpTranscodeBirthResult
}
type mpStartJobBirthDebt struct{ rows, variableBytes, rootControlBefore, rootControlAfter int64 }

func mpStartPrepBirthDigest(v mpStartPrepBirthReceipt) (ResultDigest, error) {
	h, r := v.header, v.result
	if !mpPrepTexts(r.preparation) || r.incarnation == ([16]byte{}) || r.incarnation == h.identity.Incarnation || r.state != "pending" || r.revision != 1 || r.deadlineMS <= h.appliedMS || !r.ids.valid() {
		return ResultDigest{}, ErrInvalid
	}
	e, err := h.encoder(MPStartPrepBegin, "mp_start_birth")
	if err != nil {
		return ResultDigest{}, err
	}
	for _, s := range []string{r.preparation, r.state, r.ids.session, r.ids.legacySession, r.ids.snapshot} {
		e.text(s)
	}
	e.bytes(r.incarnation[:])
	for _, n := range []int64{r.revision, r.deadlineMS, r.ids.legacyGeneration} {
		e.number(uint64(n))
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var d ResultDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}
func mpTranscodeBirthDigest(v mpTranscodeBirthReceipt) (ResultDigest, error) {
	h, r := v.header, v.result
	if !mpPrepTexts(r.job) || r.incarnation == ([16]byte{}) || r.incarnation == h.identity.Incarnation || r.state != "queued" || r.revision != 1 || r.nextAttemptMS < 0 || !lowerHex(r.sourceRevision, 64) {
		return ResultDigest{}, ErrInvalid
	}
	e, err := h.encoder(MPTranscodeCreate, "mp_job_birth")
	if err != nil {
		return ResultDigest{}, err
	}
	for _, s := range []string{r.job, r.state, r.sourceRevision} {
		e.text(s)
	}
	e.bytes(r.incarnation[:])
	e.number(uint64(r.revision))
	e.number(uint64(r.nextAttemptMS))
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var d ResultDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}

// Creation scope is checked against the actual root's historical actor in the
// same fresh turn. Remaining registration/lifetime values are W-owned facts;
// no query here substitutes a security owner or a public accepting API.
func storeMPStartPrepBirthReceipt(ctx context.Context, t *commandTurn, v mpStartPrepBirthReceipt) (out mpStartJobBirthDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepBegin); err != nil {
		return
	}
	d, e := mpStartPrepBirthDigest(v)
	if e != nil || d != v.header.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	h, r := v.header, v.result
	var actual mpStartPrepBirthResult
	actual.preparation = r.preparation
	var inc []byte
	var actor mpPrepActor
	row, e := mpPrepRow(ctx, t, "mp.start.birth_native", r.preparation)
	if e != nil {
		return out, e
	}
	e = row.Scan(&inc, &actual.state, &actual.revision, &actual.deadlineMS, &actual.ids.session, &actual.ids.legacySession, &actual.ids.legacyGeneration, &actual.ids.snapshot, &actor.authority, &actor.account, &actor.profile, &actor.family, &actor.device, &out.rootControlBefore)
	if e != nil {
		return out, e
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(actual.incarnation[:], inc)
	if actual != r || !h.scope.matchesActor(actor) {
		return out, ErrConflict
	}
	args := append(h.args(MPStartPrepBegin), r.ids.session, r.ids.legacySession, r.ids.legacyGeneration, r.ids.snapshot, r.preparation, r.incarnation[:], r.state, r.revision, r.deadlineMS)
	row, e = mpPrepRow(ctx, t, "mp.start.birth_insert", args...)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.variableBytes); e != nil {
		return out, e
	}
	row, e = mpPrepRow(ctx, t, "mp.start.birth_link", h.birthID[:], r.preparation, r.incarnation[:], out.rootControlBefore)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.rootControlAfter); e != nil {
		return out, e
	}
	if out.variableBytes <= 0 || out.rootControlAfter-out.rootControlBefore != 16 {
		return out, ErrResolveRequired
	}
	out.rows = 1
	return out, nil
}
func storeMPTranscodeBirthReceipt(ctx context.Context, t *commandTurn, v mpTranscodeBirthReceipt) (out mpStartJobBirthDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPTranscodeCreate); err != nil {
		return
	}
	d, e := mpTranscodeBirthDigest(v)
	if e != nil || d != v.header.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	h, r := v.header, v.result
	var actual mpTranscodeBirthResult
	actual.job = r.job
	var inc []byte
	var actor mpPrepActor
	row, e := mpPrepRow(ctx, t, "mp.transcode.birth_native", r.job)
	if e != nil {
		return out, e
	}
	e = row.Scan(&inc, &actual.state, &actual.revision, &actual.sourceRevision, &actual.nextAttemptMS, &actor.authority, &actor.account, &actor.profile, &actor.family, &actor.device, &out.rootControlBefore)
	if e != nil {
		return out, e
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(actual.incarnation[:], inc)
	if actual != r || !h.scope.matchesActor(actor) {
		return out, ErrConflict
	}
	args := append(h.args(MPTranscodeCreate), r.nextAttemptMS, r.job, r.incarnation[:], r.revision, r.state, r.sourceRevision)
	row, e = mpPrepRow(ctx, t, "mp.transcode.birth_insert", args...)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.variableBytes); e != nil {
		return out, e
	}
	row, e = mpPrepRow(ctx, t, "mp.transcode.birth_link", h.birthID[:], r.job, r.incarnation[:], out.rootControlBefore)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.rootControlAfter); e != nil {
		return out, e
	}
	if out.variableBytes <= 0 || out.rootControlAfter-out.rootControlBefore != 16 {
		return out, ErrResolveRequired
	}
	out.rows = 1
	return out, nil
}

// Public-key lookup returns a reference/header only. W must first establish
// CURRENT disclosure, then classify creation/retirement/floor before typed decode.
type mpStartJobBirthRef struct {
	header             mpStartJobBirthHeader
	last, ack, retired int64
}
type mpStartJobBirthScan struct {
	header                               mpStartJobBirthHeader
	birth, inc, op, request, key, digest []byte
	kind                                 int64
}

func (s *mpStartJobBirthScan) destinations() []any {
	h, v := &s.header, &s.header.scope
	return []any{&s.birth, &s.inc, &s.kind, &s.op, &s.request, &v.authority, &v.account, &v.profile, &v.member, &v.profileLifetime, &v.installation, &v.root, &v.registration, &v.family, &v.device, &s.key, &v.issuedMS, &h.appliedMS, &s.digest}
}
func (s *mpStartJobBirthScan) finish(kind CommandID) (mpStartJobBirthHeader, error) {
	h := s.header
	if len(s.birth) != 16 || len(s.inc) != 16 || len(s.op) != 16 || len(s.request) != 32 || len(s.key) != 32 || len(s.digest) != 32 || s.kind != int64(kind) {
		return h, ErrResolveRequired
	}
	copy(h.birthID[:], s.birth)
	copy(h.identity.Incarnation[:], s.inc)
	h.identity.Sequence = 1
	copy(h.identity.OperationID[:], s.op)
	copy(h.request[:], s.request)
	copy(h.scope.keyDigest[:], s.key)
	copy(h.digest[:], s.digest)
	if !h.valid() {
		return mpStartJobBirthHeader{}, ErrResolveRequired
	}
	return h, nil
}
func lookupMPStartPrepBirth(ctx context.Context, t *commandTurn, scope mpStartJobBirthScope) (mpStartJobBirthRef, error) {
	return lookupMPStartJobBirth(ctx, t, scope, MPStartPrepBegin, "mp.start.birth_lookup")
}
func lookupMPTranscodeBirth(ctx context.Context, t *commandTurn, scope mpStartJobBirthScope) (mpStartJobBirthRef, error) {
	return lookupMPStartJobBirth(ctx, t, scope, MPTranscodeCreate, "mp.transcode.birth_lookup")
}
func lookupMPStartJobBirth(ctx context.Context, t *commandTurn, scope mpStartJobBirthScope, kind CommandID, id stmt.ID) (out mpStartJobBirthRef, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, kind); err != nil {
		return
	}
	if !scope.valid() {
		return out, ErrInvalid
	}
	row, e := mpPrepRow(ctx, t, id, scope.installation, scope.root, scope.registration, scope.device, scope.keyDigest[:])
	if e != nil {
		return out, e
	}
	var s mpStartJobBirthScan
	args := append(s.destinations(), &out.last, &out.ack, &out.retired)
	if e = row.Scan(args...); errors.Is(e, sql.ErrNoRows) {
		return out, ErrResolveRequired
	} else if e != nil {
		return out, e
	}
	out.header, e = s.finish(kind)
	if e != nil {
		return out, e
	}
	original := out.header.scope
	original.issuedMS = scope.issuedMS
	if original != scope {
		return out, ErrDisclosureDenied
	}
	if out.last < 1 || out.last > 2 || out.ack < 0 || out.ack > out.last || out.last-out.ack > 1 || out.retired < 0 || out.retired > 1 || out.retired != out.last-1 {
		return out, ErrResolveRequired
	}
	return out, nil
}

// Retained creation decode uses ONLY inline historical values. No current root
// join, authority fallback, birth-head reconstruction or second receipt lifetime.
func loadMPStartPrepBirthReceipt(ctx context.Context, t *commandTurn, h mpStartJobBirthHeader) (out mpStartPrepBirthReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepBegin); err != nil {
		return
	}
	if !h.valid() || h.digest == (ResultDigest{}) {
		return out, ErrInvalid
	}
	row, e := mpPrepRow(ctx, t, "mp.start.birth_load", h.birthID[:], h.identity.Incarnation[:])
	if e != nil {
		return out, e
	}
	var s mpStartJobBirthScan
	var inc []byte
	var version int64
	r := &out.result
	args := append(s.destinations(), &version, &r.ids.session, &r.ids.legacySession, &r.ids.legacyGeneration, &r.ids.snapshot, &r.preparation, &inc, &r.state, &r.revision, &r.deadlineMS)
	if e = row.Scan(args...); errors.Is(e, sql.ErrNoRows) {
		return out, ErrResolveRequired
	} else if e != nil {
		return out, e
	}
	out.header, e = s.finish(MPStartPrepBegin)
	if e != nil {
		return out, e
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(r.incarnation[:], inc)
	d, e := mpStartPrepBirthDigest(out)
	if e != nil || version != 1 || out.header != h || d != h.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	return out, nil
}
func loadMPTranscodeBirthReceipt(ctx context.Context, t *commandTurn, h mpStartJobBirthHeader) (out mpTranscodeBirthReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPTranscodeCreate); err != nil {
		return
	}
	if !h.valid() || h.digest == (ResultDigest{}) {
		return out, ErrInvalid
	}
	row, e := mpPrepRow(ctx, t, "mp.transcode.birth_load", h.birthID[:], h.identity.Incarnation[:])
	if e != nil {
		return out, e
	}
	var s mpStartJobBirthScan
	var inc []byte
	var version int64
	r := &out.result
	args := append(s.destinations(), &version, &r.nextAttemptMS, &r.job, &inc, &r.revision, &r.state, &r.sourceRevision)
	if e = row.Scan(args...); errors.Is(e, sql.ErrNoRows) {
		return out, ErrResolveRequired
	} else if e != nil {
		return out, e
	}
	out.header, e = s.finish(MPTranscodeCreate)
	if e != nil {
		return out, e
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	copy(r.incarnation[:], inc)
	d, e := mpTranscodeBirthDigest(out)
	if e != nil || version != 1 || out.header != h || d != h.digest {
		return out, errors.Join(ErrResolveRequired, e)
	}
	return out, nil
}

func mpStartJobBirthDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpPrepDefinition("mp.start.birth_insert", `INSERT INTO mp_start_births(birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,last_sequence,acknowledged_sequence,result_version,retired,result_session_id,result_media_session_id,result_media_generation,result_snapshot_id,result_preparation_id,result_preparation_incarnation,result_state,result_revision,result_deadline_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,1,0,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "bbibbsssssssissbiibssissbsii", stmt.Write),
		mpPrepDefinition("mp.start.birth_load", `SELECT birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,result_version,result_session_id,result_media_session_id,result_media_generation,result_snapshot_id,result_preparation_id,result_preparation_incarnation,result_state,result_revision,result_deadline_ms FROM mp_start_births WHERE birth_id=? AND command_incarnation=?`, "bb", stmt.Read),
		mpPrepDefinition("mp.start.birth_lookup", `SELECT birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,last_sequence,acknowledged_sequence,retired FROM mp_start_births WHERE installation_incarnation=? AND root_id=? AND registration_revision=? AND device_id=? AND client_key_digest=?`, "ssisb", stmt.Read),
		mpPrepDefinition("mp.start.birth_link", `UPDATE playback_start_preparations SET mp_birth_id=? WHERE id=? AND mp_command_incarnation=? AND mp_control_variable_bytes=? AND mp_birth_id IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0 RETURNING mp_control_variable_bytes`, "bsbi", stmt.Write),
		mpPrepDefinition("mp.transcode.birth_insert", `INSERT INTO mp_job_births(birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,last_sequence,acknowledged_sequence,result_version,retired,result_next_attempt_ms,result_job_id,result_job_incarnation,result_revision,result_state,result_source_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,0,1,0,?,?,?,?,?,?) RETURNING variable_bytes`, "bbibbsssssssissbiibisbiss", stmt.Write),
		mpPrepDefinition("mp.transcode.birth_load", `SELECT birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,result_version,result_next_attempt_ms,result_job_id,result_job_incarnation,result_revision,result_state,result_source_revision FROM mp_job_births WHERE birth_id=? AND command_incarnation=?`, "bb", stmt.Read),
		mpPrepDefinition("mp.transcode.birth_lookup", `SELECT birth_id,command_incarnation,command_kind,operation_id,request_digest,authority,account_id,profile_id,member_incarnation,profile_lifetime,installation_incarnation,root_id,registration_revision,family_id,device_id,client_key_digest,issued_ms,applied_ms,result_digest,last_sequence,acknowledged_sequence,retired FROM mp_job_births WHERE installation_incarnation=? AND root_id=? AND registration_revision=? AND device_id=? AND client_key_digest=?`, "ssisb", stmt.Read),
		mpPrepDefinition("mp.transcode.birth_link", `UPDATE prepared_media_jobs SET mp_birth_id=? WHERE id=? AND mp_command_incarnation=? AND mp_control_variable_bytes=? AND mp_birth_id IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0 RETURNING mp_control_variable_bytes`, "bsbi", stmt.Write),
		mpPrepDefinition("mp.start.birth_native", `SELECT p.mp_command_incarnation,l.state,l.revision,p.deadline_ms,r.session_id,r.legacy_session_id,r.legacy_generation,r.snapshot_id,a.authority,a.account_id,a.profile_id,a.family_id,a.device_id,p.mp_control_variable_bytes FROM playback_start_preparations p JOIN playback_start_preparation_lifecycle l ON l.operation_id=p.id JOIN playback_start_preparation_results r ON r.operation_id=p.id JOIN playback_start_preparation_actor a ON a.operation_id=p.id WHERE p.id=? AND p.mp_birth_id IS NULL AND p.mp_command_last_sequence=0 AND p.mp_command_acknowledged_sequence=0 AND p.mp_receipt_rows=0 AND p.mp_receipt_variable_bytes=0 AND l.state='pending' AND l.revision=1 AND l.execution_id='' AND l.sealed=0`, "s", stmt.Read),
		mpPrepDefinition("mp.transcode.birth_native", `SELECT j.mp_command_incarnation,j.state,j.revision,j.source_revision,j.job_next_attempt_ms,i.authority,i.account_id,i.actor_profile_id,i.family_id,i.device_id,j.mp_control_variable_bytes FROM prepared_media_jobs j JOIN mp_job_inputs i ON i.job_id=j.id JOIN mp_job_custody c ON c.job_id=j.id WHERE j.id=? AND j.mp_birth_id IS NULL AND j.mp_command_last_sequence=0 AND j.mp_command_acknowledged_sequence=0 AND j.mp_receipt_rows=0 AND j.mp_receipt_variable_bytes=0 AND j.state='queued' AND j.revision=1 AND j.generation=0 AND j.cleaned_generation=0 AND j.job_attempts=0 AND j.job_lease_owner=0 AND j.job_lease_generation=0 AND j.job_lease_expires_ms=0 AND j.version_id='' AND c.generation=0 AND c.execution_id='' AND c.physical_exited=1 AND c.cleaned=1 AND c.cleanup_cursor=0`, "s", stmt.Read),
	}
}
