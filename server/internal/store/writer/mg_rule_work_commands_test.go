package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// 826 accepting composition: one bounded rule-work claim/advance, exact CAS,
// saved scalar replay and ACK retirement. No intent, reservation or physical
// fact is touched by this command.
func TestMGReconcileRuleAcceptClaimAdvanceReplayAndRetired(t *testing.T) {
	w, path, v := mgReconcileFixture(t)
	fake := mgPersonalFake(t, v.ruleValues.personal)
	lease := v.now + 10000
	before := mgReconcileStoredWork(t, w, v.rule)
	if before.phase != "existing" || before.jobRevision != 1 || before.jobOwner != 0 {
		t.Fatal("fixture work", before)
	}

	// Claim: due and unleased. Owner token, lease generation and attempt
	// history advance together in the same row CAS.
	claim := v.ruleRequest("claim", 7, lease, before, mgReconcileWorkPage{})
	claimRef := mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), claim)
	turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
	out, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), claimRef, claim)
	want := mgReconcileResult{
		variant: mgReconcileResultVariant, rule: v.rule, owner: v.owner().key(), generation: v.ruleValues.generation,
		phase: "existing", ruleRevision: 1, workRevision: 2, jobOwner: 7, jobGeneration: 1, jobExpires: lease,
		attempts: 1, dueMS: v.now, hasDue: true, pending: true,
	}
	if err != nil || out.decision != receiptFresh || !mgReconcileResultEqual(out.result, want) || out.digest == (ResultDigest{}) {
		t.Fatal(out, err)
	}
	mgRuleCommit(t, turn)
	stored := mgReconcileStoredWork(t, w, v.rule)
	if stored.jobOwner != 7 || stored.leaseGeneration != 1 || stored.attempts != 1 || stored.expires != lease || stored.jobRevision != 2 {
		t.Fatal("claim not stored", stored)
	}

	// Identical retry returns the original saved scalar with zero effects.
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	replay, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), claimRef, claim)
	if err != nil || replay.decision != receiptReplay || !mgReconcileResultEqual(replay.result, want) || replay.digest != out.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	ackMGReconcile(t, w, v.ruleValues.root, 1)

	// A second claim under a NEW identity but the same observed row is a claim
	// race: the native work CAS refuses and nothing is written.
	race := mgReconcileRuleReference(t, mgPersonalIdentity(2, 2), claim)
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	if got, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), race, claim); !errors.Is(err, ErrConflict) || !mgReconcileAcceptanceZero(got) {
		t.Fatal("claim race accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Advance existing -> discover with zero composed children.
	discover := v.ruleRequest("advance", 7, lease, stored, mgReconcileWorkPage{phase: "discover"})
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	discovered, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(),
		mgReconcileRuleReference(t, mgPersonalIdentity(2, 2), discover), discover)
	if err != nil || discovered.decision != receiptFresh || discovered.result.phase != "discover" ||
		discovered.result.workRevision != 3 || !discovered.result.pending || !discovered.result.hasDue {
		t.Fatal(discovered, err)
	}
	mgRuleCommit(t, turn)
	ackMGReconcile(t, w, v.ruleValues.root, 2)

	// One bounded page of three composed child results, cursor strictly ahead.
	advanced := mgReconcileStoredWork(t, w, v.rule)
	page := v.ruleRequest("advance", 7, lease, advanced, mgReconcileWorkPage{phase: "discover", cursor: "c1", units: 3})
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	paged, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(),
		mgReconcileRuleReference(t, mgPersonalIdentity(3, 3), page), page)
	if err != nil || paged.decision != receiptFresh || paged.result.cursor != "c1" || paged.result.units != 3 || paged.result.workRevision != 4 {
		t.Fatal(paged, err)
	}
	mgRuleCommit(t, turn)
	ackMGReconcile(t, w, v.ruleValues.root, 3)

	// Same-phase zero units, a non-advancing cursor, a backwards phase
	// transition and an oversize page are all refusals with zero effects.
	for _, bad := range []struct {
		name string
		page mgReconcileWorkPage
	}{
		{"zero-units", mgReconcileWorkPage{phase: "discover", cursor: "c2", units: 0}},
		{"stale-cursor", mgReconcileWorkPage{phase: "discover", cursor: "c1", units: 1}},
		{"backwards", mgReconcileWorkPage{phase: "existing", units: 1}},
		{"oversize", mgReconcileWorkPage{phase: "discover", cursor: "c2", units: 9}},
	} {
		t.Run(bad.name, func(t *testing.T) {
			stored := mgReconcileStoredWork(t, w, v.rule)
			request := v.ruleRequest("advance", 7, lease, stored, bad.page)
			ref := ReceiptReference{Identity: mgPersonalIdentity(4, 4)}
			if command, cerr := newMGReconcileRuleCommand(ref.Identity, request); cerr == nil {
				ref.RequestDigest = command.digest
			}
			turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
			if got, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), ref, request); err == nil || !mgReconcileAcceptanceZero(got) {
				t.Fatal("invalid page accepted", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}

	// Idle retirement clears the due slot and lease owner but preserves attempts
	// and lease generation.
	idleBefore := mgReconcileStoredWork(t, w, v.rule)
	idle := v.ruleRequest("advance", 7, lease, idleBefore, mgReconcileWorkPage{phase: "idle"})
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	retiredWork, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(),
		mgReconcileRuleReference(t, mgPersonalIdentity(4, 4), idle), idle)
	if err != nil || retiredWork.decision != receiptFresh || retiredWork.result.phase != "idle" ||
		retiredWork.result.hasDue || retiredWork.result.jobOwner != 0 || retiredWork.result.jobExpires != 0 ||
		retiredWork.result.attempts != 1 || retiredWork.result.jobGeneration != 1 || retiredWork.result.pending {
		t.Fatal("idle retirement", retiredWork, err)
	}
	mgRuleCommit(t, turn)
	ackMGReconcile(t, w, v.ruleValues.root, 4)
	stored = mgReconcileStoredWork(t, w, v.rule)
	if stored.phase != "idle" || stored.hasDue || stored.jobOwner != 0 || stored.expires != 0 || stored.attempts != 1 || stored.leaseGeneration != 1 {
		t.Fatal("retired work history", stored)
	}
	var intents, reservations, receipts, sequence int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM dvr_recordings),(SELECT count(*) FROM live_reservations),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.ruleValues.root).
		Scan(&intents, &reservations, &receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	if intents != 0 || reservations != 0 || receipts != 4 || sequence != 4 {
		t.Fatal("unexpected effects", intents, reservations, receipts, sequence)
	}
	// A fresh read-only connection sees the same committed work.
	readOnly := mgPersonalReadOnly(t, path)
	if got := mgReconcileStoredWorkAt(t, readOnly, v.rule); got != stored {
		t.Fatal("fresh connection work", got)
	}

	// ACK retires every original: replay is a complete zero result.
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	done, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), claimRef, claim)
	if err != nil || done.decision != receiptRetired || !mgReconcileResultEqual(done.result, mgReconcileResult{}) || done.digest != (ResultDigest{}) {
		t.Fatal(done, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestMGReconcileRuleAcceptingAuthorityCells(t *testing.T) {
	w, _, v := mgReconcileFixture(t)
	fake := mgPersonalFake(t, v.ruleValues.personal)
	lease := v.now + 10000
	before := mgReconcileStoredWork(t, w, v.rule)
	request := v.ruleRequest("claim", 7, lease, before, mgReconcileWorkPage{})
	reference := mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), request)
	denied := func(t *testing.T, name string, got mgReconcileAcceptance, err error, want error) {
		t.Helper()
		if !errors.Is(err, want) || !mgReconcileAcceptanceZero(got) {
			t.Fatal(name, got, err)
		}
	}

	// Forged cached witness stamp.
	forged := *v.ruleValues.proof
	forged.tokenExpiry = forged.tokenExpiry.Add(48 * time.Hour)
	turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err := acceptMGReconcileRule(t.Context(), turn, fake, &forged, t.Context(), reference, request)
	denied(t, "forged witness", got, err, ErrDisclosureDenied)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A self-consistent witness for another issuer selects nothing.
	other := v.ruleValues.proof.witness
	other.issuer.account = "another-account"
	forgedIssuer, err := prepareUSOwnerAuthority(other)
	if err != nil {
		t.Fatal(err)
	}
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, forgedIssuer, t.Context(), reference, request)
	denied(t, "forged issuer", got, err, ErrDisclosureDenied)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A caller-selected foreign owner and a stale member revision are denied
	// before and inside the seam.
	foreign := request
	foreign.owner.profile = "another-profile"
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), foreign), foreign)
	denied(t, "foreign owner", got, err, ErrDisclosureDenied)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	staleOwner := request
	staleOwner.owner.memberRevision = 2
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), staleOwner), staleOwner)
	denied(t, "stale owner revision", got, err, ErrDisclosureDenied)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Stale native heads: rule revision, source revision and generation.
	for _, mutate := range []struct {
		name string
		edit func(*mgReconcileRuleRequest)
	}{
		{"stale rule revision", func(r *mgReconcileRuleRequest) { r.ruleRevision++ }},
		{"stale source revision", func(r *mgReconcileRuleRequest) { r.sourceRevision++ }},
		{"changed generation", func(r *mgReconcileRuleRequest) { r.generation = strings.Repeat("c", 48) }},
	} {
		edited := request
		mutate.edit(&edited)
		turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
		got, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), edited), edited)
		denied(t, mutate.name, got, err, ErrConflict)
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}

	// A disabled native source head is a conflict even when revision and
	// generation strings are unchanged.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET state='disabled' WHERE id=?`, v.ruleValues.source); err != nil {
		t.Fatal(err)
	}
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request)
	denied(t, "disabled source", got, err, ErrConflict)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET state='active' WHERE id=?`, v.ruleValues.source); err != nil {
		t.Fatal(err)
	}

	// A rule whose native owner is not this caller is a conflict, never a
	// caller-selected identity.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rules SET owner_key=? WHERE id=?`, strings.Repeat("9", 64), v.rule); err != nil {
		t.Fatal(err)
	}
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request)
	denied(t, "foreign rule row", got, err, ErrConflict)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rules SET owner_key=? WHERE id=?`, v.owner().key(), v.rule); err != nil {
		t.Fatal(err)
	}

	// Cancelled request context and cancelled caller lifetime.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(cancelled, turn, fake, v.ruleValues.proof, t.Context(), reference, request)
	denied(t, "cancelled context", got, err, context.Canceled)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	lifetime, stop := context.WithCancel(t.Context())
	stop()
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, lifetime, reference, request)
	denied(t, "cancelled lifetime", got, err, context.Canceled)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A revoked session denies replay after reauthorization.
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	if _, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request); err != nil {
		t.Fatal(err)
	}
	mgRuleCommit(t, turn)
	afterClaim := mgReconcileStoredWork(t, w, v.rule)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1,revoked_reason='explicit_signout',revoked_at=? WHERE id=?`, w.now().UTC().Format("2006-01-02T15:04:05Z"), v.ruleValues.proof.witness.issuer.family); err != nil {
		t.Fatal(err)
	}
	turn = mgReconcileTurn(t, w, MGRuleWorkReconcile)
	got, err = acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request)
	denied(t, "revoked session", got, err, ErrDisclosureDenied)
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A FULL frame can never borrow the NORMAL reconcile purpose.
	w2, _, v2 := mgReconcileFixture(t)
	fake2 := mgPersonalFake(t, v2.ruleValues.personal)
	fullBefore := mgReconcileStoredWork(t, w2, v2.rule)
	full, err := w2.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = full.rollback(nil) })
	full.descriptor.ID = MGRuleWorkReconcile
	defs := mgReconcileTurnDefinitions()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{64, 200000, 64, 4 << 20, time.Microsecond}})
	}
	fullTurn, err := newCommandTurn(full, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fullTurn.closeRows() })
	fullRequest := v2.ruleRequest("claim", 7, v2.now+10000, fullBefore, mgReconcileWorkPage{})
	got, err = acceptMGReconcileRule(t.Context(), fullTurn, fake2, v2.ruleValues.proof, t.Context(), mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), fullRequest), fullRequest)
	denied(t, "FULL frame", got, err, ErrInvalid)

	// Nothing above left the first fixture changed.
	if stored := mgReconcileStoredWork(t, w, v.rule); stored != afterClaim {
		t.Fatal("refused frames changed work", stored)
	}
}

func TestMGReconcileRuleWorkCustodyBounds(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest
	}{
		{"future-due", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "existing", "", 0, 0, 0, 0, 1, v.now+60000)
			return v.ruleRequest("claim", 7, v.now+10000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"live-lease", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "existing", "", 9, 3, v.now+9000, 2, 5, v.now)
			return v.ruleRequest("claim", 7, v.now+10000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"attempts-exhausted", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "existing", "", 0, 0, 0, mgReconcileAttemptsMax, 2, v.now)
			return v.ruleRequest("claim", 7, v.now+10000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"lease-too-long", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			return v.ruleRequest("claim", 7, v.now+mgReconcileLeaseMaxMS+1, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"lease-expired", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			return v.ruleRequest("claim", 7, v.now, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"renew-shorter", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "existing", "", 7, 1, v.now+9000, 1, 2, v.now)
			return v.ruleRequest("renew", 7, v.now+8000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"renew-foreign-worker", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "existing", "", 9, 1, v.now+9000, 1, 2, v.now)
			return v.ruleRequest("renew", 7, v.now+10000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"renew-expired", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "existing", "", 7, 1, v.now-1, 1, 2, v.now)
			return v.ruleRequest("renew", 7, v.now+10000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
		{"claim-idle", func(t *testing.T, w *executor, v mgReconcileValues) mgReconcileRuleRequest {
			mgReconcileSeedWork(t, w, v.rule, v.ruleValues.generation, "idle", "", 0, 0, 0, 1, 3, nil)
			return v.ruleRequest("claim", 7, v.now+10000, mgReconcileStoredWork(t, w, v.rule), mgReconcileWorkPage{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, path, v := mgReconcileFixture(t)
			fake := mgPersonalFake(t, v.ruleValues.personal)
			request := tc.prepare(t, w, v)
			before := mgReconcileStoredWork(t, w, v.rule)
			reference := mgReconcileRuleReference(t, mgPersonalIdentity(1, 1), request)
			turn := mgReconcileTurn(t, w, MGRuleWorkReconcile)
			if got, err := acceptMGReconcileRule(t.Context(), turn, fake, v.ruleValues.proof, t.Context(), reference, request); err == nil || !mgReconcileAcceptanceZero(got) {
				t.Fatal("invalid custody accepted", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			readOnly := mgPersonalReadOnly(t, path)
			if stored := mgReconcileStoredWorkAt(t, readOnly, v.rule); stored != before {
				t.Fatal("refusal changed work", stored)
			}
		})
	}
}

// mgReconcileStoredWorkAt is the read-only-connection form used to prove a
// refusal or COMMIT through a FRESH connection, never the quarantined pool.
func mgReconcileStoredWorkAt(t *testing.T, db *sql.DB, rule string) mgReconcileWorkFacts {
	t.Helper()
	var f mgReconcileWorkFacts
	var due sql.NullInt64
	if err := db.QueryRowContext(context.Background(), `SELECT rule_id,rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision FROM dvr_rule_work WHERE rule_id=?`, rule).
		Scan(&f.rule, &f.ruleRevision, &f.generation, &f.phase, &f.cursor, &f.updated, &f.diagnostic, &f.jobOwner, &f.leaseGeneration, &f.expires, &f.attempts, &due, &f.jobRevision); err != nil {
		t.Fatal(err)
	}
	if due.Valid {
		f.dueMS, f.hasDue = due.Int64, true
	}
	return f
}
