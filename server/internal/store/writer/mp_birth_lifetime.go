package writer

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Birth retirement is a fixed second result on EACH retained concrete birth,
// never a new generic receipt owner. The old public birth already has ACK1.
// Native scope checks require both the original root to be gone and its original
// installation registration to be permanently unavailable. Current maintenance
// disclosure remains W-owned; an expired family or deleted resource is insufficient.
type mpBirthRetirement struct {
	birthID                  [16]byte
	identity                 CommandIdentity
	request                  RequestDigest
	appliedMS, scopeCutoffMS int64
	decision                 receiptDecision
}
type mpBirthRetirementEffect struct {
	family                             string
	birthID                            [16]byte
	identity                           CommandIdentity
	request                            RequestDigest
	appliedMS, scopeCutoffMS           int64
	digest                             ResultDigest
	oldVariableBytes, newVariableBytes int64
}

func mpBirthRetirementDigest(q mpBirthRetirementEffect) (ResultDigest, error) {
	if (q.family != "start" && q.family != "session" && q.family != "queue" && q.family != "job" && q.family != "next") || q.birthID == ([16]byte{}) || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) || q.identity.Sequence != 2 || q.request == (RequestDigest{}) || q.appliedMS <= 0 || q.scopeCutoffMS < 0 || q.scopeCutoffMS > q.appliedMS {
		return ResultDigest{}, ErrResolveRequired
	}
	e, err := mpPrepReceiptEncoder(mpPrepReceiptHeader{identity: q.identity, request: q.request, kind: 788, appliedMS: q.appliedMS}, "mp_typed_birth_retirement")
	if err != nil {
		return ResultDigest{}, err
	}
	e.text(q.family)
	e.bytes(q.birthID[:])
	e.number(uint64(q.scopeCutoffMS))
	e.number(1)
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var d ResultDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}
func retireMPBirth(ctx context.Context, t *commandTurn, q mpBirthRetirement, family string, before, store, load stmt.ID) (out mpBirthRetirementEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, 788); err != nil {
		return out, err
	}
	out = mpBirthRetirementEffect{family: family, birthID: q.birthID, identity: q.identity, request: q.request, appliedMS: q.appliedMS, scopeCutoffMS: q.scopeCutoffMS}
	var d ResultDigest
	var e error
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	if q.decision == receiptReplay {
		row, e := mpPrepRow(ctx, t, load, q.birthID[:], q.identity.Incarnation[:], q.identity.OperationID[:], q.request[:])
		if e != nil {
			return out, e
		}
		var digest []byte
		var kind, version, count int64
		if e = row.Scan(&out.appliedMS, &out.scopeCutoffMS, &digest, &kind, &version, &count); e != nil {
			return out, e
		}
		d, e = mpBirthRetirementDigest(out)
		if e != nil || string(digest) != string(d[:]) || kind != 788 || version != 1 || count != 1 {
			return out, ErrResolveRequired
		}
	} else {
		if q.decision != receiptFresh {
			return out, ErrResolveRequired
		}
		d, e = mpBirthRetirementDigest(out)
		if e != nil {
			return out, e
		}
		row, e := mpPrepRow(ctx, t, before, q.birthID[:], q.identity.Incarnation[:])
		if e != nil {
			return out, e
		}
		if e = row.Scan(&out.oldVariableBytes); e != nil {
			return out, e
		}
		if out.oldVariableBytes <= 0 {
			return out, ErrResolveRequired
		}
		row, e = mpPrepRow(ctx, t, store, q.identity.OperationID[:], q.request[:], d[:], q.appliedMS, q.scopeCutoffMS, q.birthID[:], q.identity.Incarnation[:], out.oldVariableBytes)
		if e != nil {
			return out, e
		}
		if e = row.Scan(&out.newVariableBytes); e != nil {
			return out, e
		}
		if out.newVariableBytes < out.oldVariableBytes {
			return out, ErrResolveRequired
		}
	}
	out.digest = d
	return out, t.checkLifetimes(ctx)
}

type mpStartBirthRetirement struct{ invocation mpBirthRetirement }

func retireMPStartBirth(ctx context.Context, t *commandTurn, q mpStartBirthRetirement) (mpBirthRetirementEffect, error) {
	return retireMPBirth(ctx, t, q.invocation, "start", "mp.birth.start.before", "mp.birth.start.store", "mp.birth.start.load")
}

type mpSessionBirthRetirement struct{ invocation mpBirthRetirement }

func retireMPSessionBirth(ctx context.Context, t *commandTurn, q mpSessionBirthRetirement) (mpBirthRetirementEffect, error) {
	return retireMPBirth(ctx, t, q.invocation, "session", "mp.birth.session.before", "mp.birth.session.store", "mp.birth.session.load")
}

type mpQueueBirthRetirement struct{ invocation mpBirthRetirement }

func retireMPQueueBirth(ctx context.Context, t *commandTurn, q mpQueueBirthRetirement) (mpBirthRetirementEffect, error) {
	return retireMPBirth(ctx, t, q.invocation, "queue", "mp.birth.queue.before", "mp.birth.queue.store", "mp.birth.queue.load")
}

type mpJobBirthRetirement struct{ invocation mpBirthRetirement }

func retireMPJobBirth(ctx context.Context, t *commandTurn, q mpJobBirthRetirement) (mpBirthRetirementEffect, error) {
	return retireMPBirth(ctx, t, q.invocation, "job", "mp.birth.job.before", "mp.birth.job.store", "mp.birth.job.load")
}

type mpNextBirthRetirement struct{ invocation mpBirthRetirement }

func retireMPNextBirth(ctx context.Context, t *commandTurn, q mpNextBirthRetirement) (mpBirthRetirementEffect, error) {
	return retireMPBirth(ctx, t, q.invocation, "next", "mp.birth.next.before", "mp.birth.next.store", "mp.birth.next.load")
}

func mpBirthRetirementDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpBirthDefinition("mp.birth.start.before", `SELECT variable_bytes FROM mp_start_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_start_births.root_id AND r.installation_incarnation=mp_start_births.installation_incarnation AND r.registration_revision=mp_start_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_start_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_start_births.registration_revision)`, "bb", stmt.Read),
		mpBirthDefinition("mp.birth.start.store", `UPDATE mp_start_births SET last_sequence=2,retired=1,retire_operation_id=?,retire_request_digest=?,retire_result_digest=?,retire_applied_ms=?,retire_scope_cutoff_ms=?,retire_command_kind=788,retire_result_version=1,retire_result_count=1 WHERE birth_id=? AND command_incarnation=? AND variable_bytes=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_start_births.root_id AND r.installation_incarnation=mp_start_births.installation_incarnation AND r.registration_revision=mp_start_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_start_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_start_births.registration_revision) RETURNING variable_bytes`, "bbbiibbi", stmt.Write),
		mpBirthDefinition("mp.birth.start.load", `SELECT retire_applied_ms,retire_scope_cutoff_ms,retire_result_digest,retire_command_kind,retire_result_version,retire_result_count FROM mp_start_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=2 AND retire_operation_id=? AND retire_request_digest=?`, "bbbb", stmt.Read),
		mpBirthDefinition("mp.birth.session.before", `SELECT variable_bytes FROM mp_session_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_session_births.root_id AND r.installation_incarnation=mp_session_births.installation_incarnation AND r.registration_revision=mp_session_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_session_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_session_births.registration_revision)`, "bb", stmt.Read),
		mpBirthDefinition("mp.birth.session.store", `UPDATE mp_session_births SET last_sequence=2,retired=1,retire_operation_id=?,retire_request_digest=?,retire_result_digest=?,retire_applied_ms=?,retire_scope_cutoff_ms=?,retire_command_kind=788,retire_result_version=1,retire_result_count=1 WHERE birth_id=? AND command_incarnation=? AND variable_bytes=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_session_births.root_id AND r.installation_incarnation=mp_session_births.installation_incarnation AND r.registration_revision=mp_session_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_session_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_session_births.registration_revision) RETURNING variable_bytes`, "bbbiibbi", stmt.Write),
		mpBirthDefinition("mp.birth.session.load", `SELECT retire_applied_ms,retire_scope_cutoff_ms,retire_result_digest,retire_command_kind,retire_result_version,retire_result_count FROM mp_session_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=2 AND retire_operation_id=? AND retire_request_digest=?`, "bbbb", stmt.Read),
		mpBirthDefinition("mp.birth.queue.before", `SELECT variable_bytes FROM mp_queue_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_queue_births.root_id AND r.installation_incarnation=mp_queue_births.installation_incarnation AND r.registration_revision=mp_queue_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_queue_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_queue_births.registration_revision)`, "bb", stmt.Read),
		mpBirthDefinition("mp.birth.queue.store", `UPDATE mp_queue_births SET last_sequence=2,retired=1,retire_operation_id=?,retire_request_digest=?,retire_result_digest=?,retire_applied_ms=?,retire_scope_cutoff_ms=?,retire_command_kind=788,retire_result_version=1,retire_result_count=1 WHERE birth_id=? AND command_incarnation=? AND variable_bytes=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_queue_births.root_id AND r.installation_incarnation=mp_queue_births.installation_incarnation AND r.registration_revision=mp_queue_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_queue_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_queue_births.registration_revision) RETURNING variable_bytes`, "bbbiibbi", stmt.Write),
		mpBirthDefinition("mp.birth.queue.load", `SELECT retire_applied_ms,retire_scope_cutoff_ms,retire_result_digest,retire_command_kind,retire_result_version,retire_result_count FROM mp_queue_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=2 AND retire_operation_id=? AND retire_request_digest=?`, "bbbb", stmt.Read),
		mpBirthDefinition("mp.birth.job.before", `SELECT variable_bytes FROM mp_job_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_job_births.root_id AND r.installation_incarnation=mp_job_births.installation_incarnation AND r.registration_revision=mp_job_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_job_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_job_births.registration_revision)`, "bb", stmt.Read),
		mpBirthDefinition("mp.birth.job.store", `UPDATE mp_job_births SET last_sequence=2,retired=1,retire_operation_id=?,retire_request_digest=?,retire_result_digest=?,retire_applied_ms=?,retire_scope_cutoff_ms=?,retire_command_kind=788,retire_result_version=1,retire_result_count=1 WHERE birth_id=? AND command_incarnation=? AND variable_bytes=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_job_births.root_id AND r.installation_incarnation=mp_job_births.installation_incarnation AND r.registration_revision=mp_job_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_job_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_job_births.registration_revision) RETURNING variable_bytes`, "bbbiibbi", stmt.Write),
		mpBirthDefinition("mp.birth.job.load", `SELECT retire_applied_ms,retire_scope_cutoff_ms,retire_result_digest,retire_command_kind,retire_result_version,retire_result_count FROM mp_job_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=2 AND retire_operation_id=? AND retire_request_digest=?`, "bbbb", stmt.Read),
		mpBirthDefinition("mp.birth.next.before", `SELECT variable_bytes FROM mp_next_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_next_births.root_id AND r.installation_incarnation=mp_next_births.installation_incarnation AND r.registration_revision=mp_next_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_next_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_next_births.registration_revision)`, "bb", stmt.Read),
		mpBirthDefinition("mp.birth.next.store", `UPDATE mp_next_births SET last_sequence=2,retired=1,retire_operation_id=?,retire_request_digest=?,retire_result_digest=?,retire_applied_ms=?,retire_scope_cutoff_ms=?,retire_command_kind=788,retire_result_version=1,retire_result_count=1 WHERE birth_id=? AND command_incarnation=? AND variable_bytes=? AND last_sequence=1 AND acknowledged_sequence=1 AND retired=0 AND NOT EXISTS(SELECT 1 FROM user_mutation_roots r WHERE r.id=mp_next_births.root_id AND r.installation_incarnation=mp_next_births.installation_incarnation AND r.registration_revision=mp_next_births.registration_revision) AND NOT EXISTS(SELECT 1 FROM identity_installations i WHERE i.incarnation=mp_next_births.installation_incarnation AND i.retired=0 AND i.registration_revision<=mp_next_births.registration_revision) RETURNING variable_bytes`, "bbbiibbi", stmt.Write),
		mpBirthDefinition("mp.birth.next.load", `SELECT retire_applied_ms,retire_scope_cutoff_ms,retire_result_digest,retire_command_kind,retire_result_version,retire_result_count FROM mp_next_births WHERE birth_id=? AND command_incarnation=? AND last_sequence=2 AND retire_operation_id=? AND retire_request_digest=?`, "bbbb", stmt.Read),
	}
}
func mpBirthDefinition(id, sql, types string, kind stmt.Kind) stmt.Definition {
	d := mpPrepDefinition(id, sql, types, kind)
	d.Owner = "store/writer MP typed birth retirement"
	d.CallSites = []string{"store/writer/mp_birth_lifetime.go"}
	return d
}

// ACK of creation1 or the fixed retirement2 is floor-only. The retained birth
// and its typed child result remain charged; no receipt/resource is deleted.
type mpBirthAck struct {
	birthID                  [16]byte
	owner                    receiptOwnerFacts
	reference                ReceiptReference
	decision                 receiptDecision
	variableBytes, appliedMS int64
	due                      sql.NullInt64
}

func acknowledgeMPBirth(ctx context.Context, t *commandTurn, q mpBirthAck, empty, due stmt.ID) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(t), floor, err) }()
	if err = mpPrepTurn(t, 780); err != nil {
		return 0, err
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	o, i := q.owner, q.reference.Identity
	if q.birthID == ([16]byte{}) || !o.exists || o.incarnation == ([16]byte{}) || o.incarnation != i.Incarnation || i.OperationID == ([16]byte{}) || i.Sequence <= 0 || q.reference.RequestDigest == (RequestDigest{}) || o.lastSequence < 1 || o.lastSequence > 2 || o.acknowledgedSequence < 0 || o.acknowledgedSequence > o.lastSequence || o.lastSequence-o.acknowledgedSequence > 1 {
		return 0, ErrResolveRequired
	}
	if q.decision == receiptRetired && i.Sequence <= o.acknowledgedSequence {
		return o.acknowledgedSequence, nil
	}
	if q.decision != receiptReplay || i.Sequence != o.lastSequence || i.Sequence <= o.acknowledgedSequence || q.variableBytes <= 0 || q.appliedMS <= 0 || q.due.Valid && q.due.Int64 < 0 || !q.due.Valid && q.due.Int64 != 0 {
		return 0, ErrResolveRequired
	}
	next := q.appliedMS
	if q.due.Valid {
		next = min(next, q.due.Int64)
	}
	args := []any{i.Sequence, next, q.birthID[:], i.Incarnation[:], o.lastSequence, o.acknowledgedSequence, q.variableBytes}
	id := empty
	if q.due.Valid {
		id = due
		args = append(args, q.due.Int64)
	}
	row, e := mpPrepRow(ctx, t, id, args...)
	if e != nil {
		return 0, e
	}
	if e = row.Scan(&floor); e != nil {
		return 0, e
	}
	if floor != i.Sequence {
		return 0, ErrResolveRequired
	}
	return floor, t.checkLifetimes(ctx)
}

type mpStartBirthAck struct{ observation mpBirthAck }

func acknowledgeMPStartBirth(ctx context.Context, t *commandTurn, q mpStartBirthAck) (int64, error) {
	return acknowledgeMPBirth(ctx, t, q.observation, "mp.birth.start.ack", "mp.birth.start.ack_due")
}

type mpSessionBirthAck struct{ observation mpBirthAck }

func acknowledgeMPSessionBirth(ctx context.Context, t *commandTurn, q mpSessionBirthAck) (int64, error) {
	return acknowledgeMPBirth(ctx, t, q.observation, "mp.birth.session.ack", "mp.birth.session.ack_due")
}

type mpQueueBirthAck struct{ observation mpBirthAck }

func acknowledgeMPQueueBirth(ctx context.Context, t *commandTurn, q mpQueueBirthAck) (int64, error) {
	return acknowledgeMPBirth(ctx, t, q.observation, "mp.birth.queue.ack", "mp.birth.queue.ack_due")
}

type mpJobBirthAck struct{ observation mpBirthAck }

func acknowledgeMPJobBirth(ctx context.Context, t *commandTurn, q mpJobBirthAck) (int64, error) {
	return acknowledgeMPBirth(ctx, t, q.observation, "mp.birth.job.ack", "mp.birth.job.ack_due")
}

type mpNextBirthAck struct{ observation mpBirthAck }

func acknowledgeMPNextBirth(ctx context.Context, t *commandTurn, q mpNextBirthAck) (int64, error) {
	return acknowledgeMPBirth(ctx, t, q.observation, "mp.birth.next.ack", "mp.birth.next.ack_due")
}

func mpBirthAckDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpBirthDefinition("mp.birth.start.ack", `UPDATE mp_start_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms IS NULL RETURNING acknowledged_sequence`, "iibbiii", stmt.Write),
		mpBirthDefinition("mp.birth.start.ack_due", `UPDATE mp_start_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms=? RETURNING acknowledged_sequence`, "iibbiiii", stmt.Write),
		mpBirthDefinition("mp.birth.session.ack", `UPDATE mp_session_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms IS NULL RETURNING acknowledged_sequence`, "iibbiii", stmt.Write),
		mpBirthDefinition("mp.birth.session.ack_due", `UPDATE mp_session_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms=? RETURNING acknowledged_sequence`, "iibbiiii", stmt.Write),
		mpBirthDefinition("mp.birth.queue.ack", `UPDATE mp_queue_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms IS NULL RETURNING acknowledged_sequence`, "iibbiii", stmt.Write),
		mpBirthDefinition("mp.birth.queue.ack_due", `UPDATE mp_queue_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms=? RETURNING acknowledged_sequence`, "iibbiiii", stmt.Write),
		mpBirthDefinition("mp.birth.job.ack", `UPDATE mp_job_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms IS NULL RETURNING acknowledged_sequence`, "iibbiii", stmt.Write),
		mpBirthDefinition("mp.birth.job.ack_due", `UPDATE mp_job_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms=? RETURNING acknowledged_sequence`, "iibbiiii", stmt.Write),
		mpBirthDefinition("mp.birth.next.ack", `UPDATE mp_next_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms IS NULL RETURNING acknowledged_sequence`, "iibbiii", stmt.Write),
		mpBirthDefinition("mp.birth.next.ack_due", `UPDATE mp_next_births SET acknowledged_sequence=?,retire_due_ms=? WHERE birth_id=? AND command_incarnation=? AND last_sequence=? AND acknowledged_sequence=? AND variable_bytes=? AND retire_due_ms=? RETURNING acknowledged_sequence`, "iibbiiii", stmt.Write),
	}
}
