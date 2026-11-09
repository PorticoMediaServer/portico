package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// ErrMGRootUnavailable is the typed seam for this build's missing
// authorizeUSCurrentSession mint. It is not a retry signal and never means a
// preference or reminder was applied.
var ErrMGRootUnavailable = errors.New("writer MG personal current-session root unavailable")

// mgPersonalRootAuthorization is the ONE opaque value MG composes against the
// SOL-USERS current-session mint. Its exact future producer is
// authorizeUSCurrentSession(ctx, turn, witness *usOwnerAuthorityProof, lifetime),
// which returns a *usCurrentSessionAuthorization with a recheck method. That
// type does not exist on main yet, so this MG-owned interface mirrors only the
// observable contract: the validated lifetime root, its exact frozen identity,
// a current-state observation and a current-authority recheck. No constructor,
// exported field, caller string or cached issuer stands in for the mint.
type mgPersonalRootAuthorization interface {
	mgPersonalRootID() string
	mgPersonalRootIdentity() uoRootIdentity
	state(ctx context.Context, turn *commandTurn) (string, error)
	recheck(ctx context.Context, turn *commandTurn) error
}

// mgPersonalRootSeam keeps the swap to the merged mint to ONE file and one
// commit. A production implementation refuses with ErrMGRootUnavailable until
// then; tests supply a fake session that still exercises the real receipt and
// native effect paths.
type mgPersonalRootSeam interface {
	authorizeUSCurrentSession(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgPersonalRootAuthorization, error)
}

// mgPersonalProductionRoots is the only production implementation in this
// build. It refuses with the typed seam error: the closed purpose set for the
// mint does not admit MG purposes yet, and MG invents no purpose or schema.
type mgPersonalProductionRoots struct{}

func (mgPersonalProductionRoots) authorizeUSCurrentSession(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (out mgPersonalRootAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	return nil, ErrMGRootUnavailable
}

const (
	mgPersonalRootPoint             stmt.ID = "writer.mg.personal.root-point"
	mgPersonalRootOwnerPoint        stmt.ID = "writer.mg.personal.root-owner"
	mgPersonalRootReceiptMeta       stmt.ID = "writer.mg.personal.root-receipt-meta"
	mgPersonalRootReceiptPayload    stmt.ID = "writer.mg.personal.root-receipt-payload"
	mgPersonalRootReceiptInsert     stmt.ID = "writer.mg.personal.root-receipt-insert"
	mgPersonalRootOwnerAdvance      stmt.ID = "writer.mg.personal.root-owner-advance"
	mgPersonalRootOwnerAdvanceEmpty stmt.ID = "writer.mg.personal.root-owner-advance-null"
)

// Shared lifetime-root custody, exact columns. These are the same reviewed
// user_mutation_root/owner/receipt tables as UO-01; this adapter owns no new
// schema, root, mint or purpose.
func mgPersonalRootDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: mgPersonalRootPoint, Kind: stmt.Read, SQL: `SELECT account_id,profile_id,account_incarnation,profile_incarnation,family_id,installation_incarnation,state FROM user_mutation_roots WHERE id=?`, Params: []stmt.Param{s("root")}},
		{ID: mgPersonalRootOwnerPoint, Kind: stmt.Read, SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,revision,receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms FROM user_mutation_command_owners WHERE root_id=?`, Params: []stmt.Param{s("root")}},
		{ID: mgPersonalRootReceiptMeta, Kind: stmt.Read, SQL: `SELECT command_kind,result_version,result_count,applied_ms,length(result_payload),variable_bytes,length(result_digest),operation_id,request_digest FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{s("root"), b("incarnation"), i("sequence")}},
		{ID: mgPersonalRootReceiptPayload, Kind: stmt.Read, SQL: `SELECT result_payload,result_digest FROM user_mutation_command_receipts WHERE root_id=? AND command_incarnation=? AND command_sequence=? AND operation_id=? AND command_kind=? AND request_digest=? AND length(result_payload)=? AND length(result_digest)=32`, Params: []stmt.Param{s("root"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), i("payload_bytes")}},
		{ID: mgPersonalRootReceiptInsert, Kind: stmt.Write, SQL: `INSERT INTO user_mutation_command_receipts(root_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, Params: []stmt.Param{s("root"), b("incarnation"), i("sequence"), b("operation"), i("kind"), b("request"), i("result_version"), i("result_count"), b("result_digest"), b("payload"), i("applied")}},
		{ID: mgPersonalRootOwnerAdvance, Kind: stmt.Write, SQL: `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms=? RETURNING last_command_sequence`, Params: []stmt.Param{i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_revision"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_cursor"), i("old_due")}},
		{ID: mgPersonalRootOwnerAdvanceEmpty, Kind: stmt.Write, SQL: `UPDATE user_mutation_command_owners SET last_command_sequence=?,receipt_rows=?,receipt_variable_bytes=?,revision=? WHERE root_id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND revision=? AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND receipt_reclaim_due_ms IS NULL RETURNING last_command_sequence`, Params: []stmt.Param{i("next_sequence"), i("next_rows"), i("next_bytes"), i("next_revision"), s("root"), b("incarnation"), i("old_sequence"), i("old_floor"), i("old_revision"), i("old_rows"), i("old_bytes"), i("old_cursor")}},
	}
	for n := range defs {
		defs[n].Owner = "writer.mg.personal root adapter"
		defs[n].CallSites = []string{"writer/mg_personal_root.go"}
		defs[n].Plan.Hot = true
	}
	root := "abcdefghijklmnopqrstuv"
	incarnation, operation := make([]byte, 16), make([]byte, 16)
	request, digest := make([]byte, 32), make([]byte, 32)
	for n := range incarnation {
		incarnation[n] = 1
		operation[n] = 2
	}
	for n := range request {
		request[n] = 3
		digest[n] = 4
	}
	bindings := [][]any{
		{root},
		{root},
		{root, incarnation, int64(1)},
		{root, incarnation, int64(1), operation, int64(811), request, int64(512)},
		{root, incarnation, int64(1), operation, int64(811), request, int64(1), int64(1), digest, []byte{1, 2, 3}, int64(100)},
		{int64(2), int64(1), int64(800), int64(2), root, incarnation, int64(1), int64(0), int64(1), int64(0), int64(0), int64(0), int64(0)},
		{int64(2), int64(1), int64(800), int64(2), root, incarnation, int64(1), int64(0), int64(1), int64(0), int64(0), int64(0)},
	}
	for n := range defs {
		defs[n].Cohorts = []stmt.Cohort{{Name: "root_custody_point", Bindings: bindings[n]}}
	}
	ownerIndex := "sqlite_autoindex_user_mutation_command_owners_1"
	defs[0].Plan.Indexes, defs[0].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_roots_1"}, []string{"id=?"}
	defs[1].Plan.Indexes, defs[1].Plan.Seeks = []string{ownerIndex}, []string{"root_id=?"}
	defs[2].Plan.PrimaryKeys = []string{"user_mutation_command_receipts"}
	defs[3].Plan.Indexes, defs[3].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_command_receipts_2"}, []string{"root_id=? AND command_incarnation=? AND operation_id=?"}
	defs[5].Plan.Indexes, defs[5].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_command_owners_2"}, []string{"root_id=? AND command_incarnation=?"}
	defs[6].Plan.Indexes, defs[6].Plan.Seeks = []string{"sqlite_autoindex_user_mutation_command_owners_2"}, []string{"root_id=? AND command_incarnation=?"}
	return defs
}

func mgPersonalRootQuery(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if turn == nil || turn.transaction == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}

// A root belongs to the exact frozen identity and must be the live owner for
// this validated session. Missing, deleted and every identity mismatch are one
// ErrUORootGone class; a draining root is observable for authorized replay.
func mgPersonalRootObserve(ctx context.Context, turn *commandTurn, id string, identity uoRootIdentity) (state string, err error) {
	defer func() { state, err = finishCommand(ctx, commandLifetime(turn), state, err) }()
	if len(id) != 22 || !identity.complete() {
		return "", ErrUORootGone
	}
	row, err := mgPersonalRootQuery(ctx, turn, mgPersonalRootPoint, id)
	if err != nil {
		return "", err
	}
	var account, profile, accountIncarnation, profileIncarnation, family, installation, rootState string
	if err = row.Scan(&account, &profile, &accountIncarnation, &profileIncarnation, &family, &installation, &rootState); errors.Is(err, sql.ErrNoRows) {
		return "", ErrUORootGone
	} else if err != nil {
		return "", err
	}
	if account != identity.account || profile != identity.profile || accountIncarnation != identity.memberIncarnation ||
		profileIncarnation != identity.profileLifetime || family != identity.family || installation != identity.installation ||
		(rootState != "active" && rootState != "draining") {
		return "", ErrUORootGone
	}
	return rootState, nil
}

type mgPersonalRootLookup struct {
	turn                                     *commandTurn
	session                                  mgPersonalRootAuthorization
	reference                                uoRootReference
	owner                                    uoRootOwner
	decision                                 receiptDecision
	payloadBytes, variableBytes, resultCount int64
	appliedMS                                int64
}

// Current authority and the root precede ANY retained owner/header/payload
// lookup. The exact native receipt header is validated before classification,
// and current authority/root are rechecked after every retained read.
func mgPersonalRootLookupCommand(ctx context.Context, turn *commandTurn, session mgPersonalRootAuthorization, q uoRootReference) (out mgPersonalRootLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.validFor(turn) || session == nil || q.root != session.mgPersonalRootID() {
		return out, ErrInvalid
	}
	if err = session.recheck(ctx, turn); err != nil {
		return out, err
	}
	rootState, err := session.state(ctx, turn)
	if err != nil {
		return out, err
	}
	row, err := mgPersonalRootQuery(ctx, turn, mgPersonalRootOwnerPoint, q.root)
	if err != nil {
		return out, err
	}
	var owner uoRootOwner
	var incarnation []byte
	err = row.Scan(&incarnation, &owner.facts.lastSequence, &owner.facts.acknowledgedSequence, &owner.revision, &owner.rows, &owner.variableBytes, &owner.afterSequence, &owner.due)
	if errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 || owner.revision <= 0 || owner.rows < 0 || owner.variableBytes < 0 ||
		(owner.rows == 0) != (owner.variableBytes == 0) || owner.afterSequence < 0 ||
		owner.afterSequence > owner.facts.acknowledgedSequence || owner.due.Valid && owner.due.Int64 < 0 {
		return out, ErrResolveRequired
	}
	copy(owner.facts.incarnation[:], incarnation)
	owner.facts.exists = true
	expected := receiptIdentity{command: q.identity, digest: q.request}
	found := receiptFacts{}
	var size, variable, appliedMS int64
	// An ACKed cohort never needs a payload/header to remain retired.
	if q.identity.Sequence > owner.facts.acknowledgedSequence {
		row, err = mgPersonalRootQuery(ctx, turn, mgPersonalRootReceiptMeta, q.root, q.identity.Incarnation[:], q.identity.Sequence)
		if err != nil {
			return out, err
		}
		var kind, version, count, digestBytes int64
		var operation, request []byte
		err = row.Scan(&kind, &version, &count, &appliedMS, &size, &variable, &digestBytes, &operation, &request)
		switch {
		case err == nil:
			if kind != int64(q.kind) || !bytes.Equal(operation, q.identity.OperationID[:]) || !bytes.Equal(request, q.request[:]) {
				return out, ErrConflict
			}
			if version != 1 || count < 0 || count > 10000 || appliedMS < 0 || size < 1 || size > uoRootPayloadCap ||
				variable != size+uoRootFixedBytes || digestBytes != 32 {
				return out, ErrResolveRequired
			}
			found = receiptFacts{exists: true, identity: expected, kind: q.kind, resultCount: count}
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
	}
	decision, err := classifyReceipt(expected, q.kind, owner.facts, found)
	if err != nil {
		return out, err
	}
	// A draining root keeps an authorized ORIGINAL replay but admits no Fresh
	// effect; this is not a "newest installation root" comparison.
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	if err = session.recheck(ctx, turn); err != nil {
		return out, err
	}
	if rootState, err = session.state(ctx, turn); err != nil {
		return out, err
	}
	if decision == receiptFresh && rootState != "active" {
		return out, ErrUORootGone
	}
	return mgPersonalRootLookup{turn: turn, session: session, reference: q, owner: owner, decision: decision, payloadBytes: size, variableBytes: variable, resultCount: found.resultCount, appliedMS: appliedMS}, nil
}

func mgPersonalResultPayload(id CommandIdentity, q RequestDigest, result any) ([]byte, ResultDigest, error) {
	switch v := result.(type) {
	case mgPersonalPreferenceResult:
		return mgPersonalPreferencePayload(id, q, v)
	case mgPersonalReminderResult:
		return mgPersonalReminderPayload(id, q, v)
	case mgSourceCommandResult:
		return mgSourceCommandPayload(id, q, v)
	case mgDVRPolicyResult:
		return mgDVRPolicyResultPayload(id, q, v)
	case mgRecordingIntentResult:
		return mgRecordingIntentPayload(id, q, v)
	case mgRuleResult:
		return mgRulePayload(id, q, v)
	}
	return nil, ResultDigest{}, ErrInvalid
}

// Fresh-only saved result. The SAME parent turn funds the complete receipt and
// owner/control delta; this is not Applied evidence, a mint or an ACK refund.
func mgPersonalRootPersist(ctx context.Context, turn *commandTurn, lookup mgPersonalRootLookup, result any, count int64) (out ResultDigest, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	q, o := lookup.reference, lookup.owner
	if lookup.turn != turn || lookup.session == nil || !q.validFor(turn) || lookup.decision != receiptFresh ||
		o.revision == math.MaxInt64 || o.rows == math.MaxInt64 || count < 0 || count > 10000 {
		return out, ErrResolveRequired
	}
	if err = lookup.session.recheck(ctx, turn); err != nil {
		return out, err
	}
	rootState, err := lookup.session.state(ctx, turn)
	if err != nil {
		return out, err
	}
	if rootState != "active" {
		return out, ErrUORootGone
	}
	decision, err := classifyReceipt(receiptIdentity{command: q.identity, digest: q.request}, q.kind, o.facts, receiptFacts{})
	if err != nil || decision != receiptFresh {
		return out, ErrResolveRequired
	}
	payload, digest, err := mgPersonalResultPayload(q.identity, q.request, result)
	if err != nil {
		return out, err
	}
	now, err := mgPersonalNow(ctx, turn)
	if err != nil {
		return out, err
	}
	appliedMS := now.UnixMilli()
	complete := int64(len(payload)) + uoRootFixedBytes
	if complete > math.MaxInt64-o.variableBytes {
		return out, ErrInvalid
	}
	actual, err := wc008DraftInteger(ctx, turn, mgPersonalRootReceiptInsert, q.root, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], int64(q.kind), q.request[:], int64(1), count, digest[:], payload, appliedMS)
	if err != nil {
		return out, uoRootInsertError(err)
	}
	if actual != complete {
		return out, ErrResolveRequired
	}
	advance, args := mgPersonalRootOwnerAdvanceEmpty, []any{q.identity.Sequence, o.rows + 1, o.variableBytes + actual, o.revision + 1, q.root, q.identity.Incarnation[:], o.facts.lastSequence, o.facts.acknowledgedSequence, o.revision, o.rows, o.variableBytes, o.afterSequence}
	if o.due.Valid {
		advance = mgPersonalRootOwnerAdvance
		args = append(args, o.due.Int64)
	}
	if _, err = wc008DraftInteger(ctx, turn, advance, args...); err != nil {
		return out, err
	}
	// A later same-turn revocation or root change never releases a digest that
	// looks Applied.
	rootState, err = lookup.session.state(ctx, turn)
	if err != nil {
		return out, err
	}
	if rootState != "active" {
		return out, ErrUORootGone
	}
	if err = lookup.session.recheck(ctx, turn); err != nil {
		return out, err
	}
	return digest, turn.checkLifetimes(ctx)
}

// The meta point precedes any payload copy. The payload is released only after
// current authority and the exact root identity are rechecked.
func mgPersonalRootReadPayload(ctx context.Context, turn *commandTurn, lookup mgPersonalRootLookup) (out []byte, digest ResultDigest, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(turn), struct {
			p []byte
			d ResultDigest
		}{out, digest}, err)
		out, digest, err = value.p, value.d, failure
	}()
	if lookup.turn != turn || lookup.session == nil || lookup.decision != receiptReplay ||
		lookup.payloadBytes < 1 || lookup.payloadBytes > uoRootPayloadCap || !lookup.reference.validFor(turn) {
		return nil, ResultDigest{}, ErrResolveRequired
	}
	if err = lookup.session.recheck(ctx, turn); err != nil {
		return nil, ResultDigest{}, err
	}
	if _, err = lookup.session.state(ctx, turn); err != nil {
		return nil, ResultDigest{}, err
	}
	q := lookup.reference
	row, err := mgPersonalRootQuery(ctx, turn, mgPersonalRootReceiptPayload, q.root, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], int64(q.kind), q.request[:], lookup.payloadBytes)
	if err != nil {
		return nil, ResultDigest{}, err
	}
	var stored []byte
	if err = row.Scan(&out, &stored); errors.Is(err, sql.ErrNoRows) {
		return nil, ResultDigest{}, ErrResolveRequired
	} else if err != nil {
		return nil, ResultDigest{}, err
	}
	if int64(len(out)) != lookup.payloadBytes || len(stored) != 32 {
		return nil, ResultDigest{}, ErrResolveRequired
	}
	copy(digest[:], stored)
	if _, err = lookup.session.state(ctx, turn); err != nil {
		return nil, ResultDigest{}, err
	}
	if err = lookup.session.recheck(ctx, turn); err != nil {
		return nil, ResultDigest{}, err
	}
	return out, digest, turn.checkLifetimes(ctx)
}
