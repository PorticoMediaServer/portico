package writer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func TestMGRuleCommandSaveAcceptReplayAndRetired(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id := strings.Repeat("e", 64)
	config := mgRuleValuesConfig(v, []string{" news "})
	prepared := mgRulePreparedWire(config)
	request := mgRuleSaveRequestFor(v, id, 0, config, prepared)
	reference := mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request)

	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	out, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	want := mgRuleResult{
		id: id, owner: v.owner.key(), source: v.source, series: "series-one",
		revision: 1, updated: w.now().UnixMilli(), enabled: true,
		configBytes: int64(len(prepared)), configDigest: mgRuleConfigDigest(prepared),
	}
	if err != nil || out.decision != receiptFresh || out.rule != want || out.digest == (ResultDigest{}) {
		t.Fatal(out, err)
	}
	mgRuleCommit(t, turn)
	var storedConfig, owner, series string
	var revision, enabled, deleted, updated int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT config_json,owner_key,series_id,revision,enabled,deleted,updated_ms FROM dvr_rules WHERE id=?`, id).Scan(&storedConfig, &owner, &series, &revision, &enabled, &deleted, &updated); err != nil {
		t.Fatal(err)
	}
	if storedConfig != prepared || owner != v.owner.key() || series != "series-one" || revision != 1 || enabled != 1 || deleted != 0 || updated != want.updated {
		t.Fatal(storedConfig, owner, series, revision, enabled, deleted, updated)
	}

	// Identical retry: the original saved scalar is returned with no new row,
	// receipt or owner advance.
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	replay, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.rule != want || replay.digest != out.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A member/profile revision bump is fresh-only CAS, never original request
	// identity: an identical retry still resolves its original saved result.
	for _, q := range []string{`UPDATE direct_memberships SET revision=revision+1`, `UPDATE direct_profiles SET revision=revision+1`} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	replayAfterRevision, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replayAfterRevision.decision != receiptReplay || replayAfterRevision.rule != want {
		t.Fatal("revision bump broke original replay", replayAfterRevision, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A tampered retained payload is never decoded as the original result.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=substr(result_payload,1,length(result_payload)-1) WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrResolveRequired) || got != (mgRuleAcceptance{}) {
		t.Fatal("tampered payload replayed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A different original input under the same command identity conflicts; so
	// does a caller digest that disagrees with the typed request.
	changed := request
	changed.input.config.name = "Another rule"
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, changed); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("changed input under one identity", got, err)
	}
	bad := reference
	bad.RequestDigest[0] ^= 0xff
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), bad, request); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("caller digest not recomputed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// ACK retires the original: replay is a complete zero result.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=? AND last_command_sequence=1`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	retired, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || retired.decision != receiptRetired || retired.rule != (mgRuleResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	var rules, work, receipts, sequence int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM dvr_rules),(SELECT count(*) FROM dvr_rule_work),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root).Scan(&rules, &work, &receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	if rules != 1 || work != 1 || receipts != 1 || sequence != 1 {
		t.Fatal(rules, work, receipts, sequence)
	}
}

func TestMGRuleCommandDeleteAcceptReplayAndRetired(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id := strings.Repeat("f", 64)
	config := mgRuleValuesConfig(v, nil)
	prepared := mgRulePreparedWire(config)
	save := mgRuleSaveRequestFor(v, id, 0, config, prepared)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	saved, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), save), save)
	if err != nil || saved.decision != receiptFresh {
		t.Fatal(saved, err)
	}
	mgRuleCommit(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	// Live worker custody must survive logical deletion.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rule_work SET job_lease_owner=7,job_lease_generation=9,job_lease_expires_ms=900,job_attempts=2 WHERE rule_id=?`, id); err != nil {
		t.Fatal(err)
	}

	deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
	reference := mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest)
	turn = mgRuleCommandTurn(t, w, MGDeleteRule)
	deleted, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest)
	want := mgRuleResult{
		id: id, owner: v.owner.key(), source: v.source, series: "series-one",
		revision: 2, updated: w.now().UnixMilli(), deleted: true,
		configBytes: int64(len(prepared)), configDigest: mgRuleConfigDigest(prepared),
	}
	if err != nil || deleted.decision != receiptFresh || deleted.rule != want || deleted.digest == (ResultDigest{}) {
		t.Fatal(deleted, err)
	}
	mgRuleCommit(t, turn)
	var storedConfig string
	var revision, enabled, deletedFlag int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT config_json,revision,enabled,deleted FROM dvr_rules WHERE id=?`, id).Scan(&storedConfig, &revision, &enabled, &deletedFlag); err != nil {
		t.Fatal(err)
	}
	if storedConfig != prepared || revision != 2 || enabled != 0 || deletedFlag != 1 {
		t.Fatal(storedConfig, revision, enabled, deletedFlag)
	}
	var leaseOwner, leaseGeneration, leaseExpiry, attempts, jobRevision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_revision FROM dvr_rule_work WHERE rule_id=?`, id).Scan(&leaseOwner, &leaseGeneration, &leaseExpiry, &attempts, &jobRevision); err != nil {
		t.Fatal(err)
	}
	if leaseOwner != 7 || leaseGeneration != 9 || leaseExpiry != 900 || attempts != 2 || jobRevision != 2 {
		t.Fatal(leaseOwner, leaseGeneration, leaseExpiry, attempts, jobRevision)
	}

	// The source head may move after the original delete: replay reauthorizes
	// current disclosure but returns the original result with zero effects.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=4 WHERE id=?`, v.source); err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGDeleteRule)
	replay, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest)
	if err != nil || replay.decision != receiptReplay || replay.rule != want || replay.digest != deleted.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// An already deleted rule is a stale intent, never a second delete.
	stale := mgRuleDeleteRequestFor(v, id, 1)
	stale.input.requestID = strings.Repeat("9", 48)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=2 WHERE root_id=? AND last_command_sequence=2`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGDeleteRule)
	if got, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(3, 3), stale), stale); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("second delete accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	turn = mgRuleCommandTurn(t, w, MGDeleteRule)
	retired, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest)
	if err != nil || retired.decision != receiptRetired || retired.rule != (mgRuleResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestMGRuleCommandAcceptingAuthorityCells(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	config := mgRuleValuesConfig(v, nil)
	prepared := mgRulePreparedWire(config)
	id := strings.Repeat("e", 64)
	request := mgRuleSaveRequestFor(v, id, 0, config, prepared)
	reference := mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request)

	// A cached witness stamp that disagrees with its native strings is forged.
	forged := *v.proof
	forged.tokenExpiry = forged.tokenExpiry.Add(48 * time.Hour)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, &forged, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
		t.Fatal("forged cached witness", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A self-consistent witness for another issuer cannot select another owner.
	other := v.proof.witness
	other.issuer.account = "another-account"
	forgedIssuer, err := prepareUSOwnerAuthority(other)
	if err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, forgedIssuer, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
		t.Fatal("forged issuer", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A caller-selected owner tuple is not authority: a foreign profile is
	// denied before the seam, and a stale member revision is denied natively.
	foreign := request
	foreign.owner.profile = "another-profile"
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), foreign), foreign); !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
		t.Fatal("caller-selected owner", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	staleOwner := request
	staleOwner.owner.memberRevision = 2
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), staleOwner), staleOwner); !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
		t.Fatal("stale owner revision", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A stale source revision/head and a cancelled lifetime/context cause zero
	// effects; both refuse before a receipt or rule row exists.
	staleSource := request
	staleSource.sourceRevision++
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), staleSource), staleSource); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("stale source head", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(cancelled, turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, context.Canceled) || got != (mgRuleAcceptance{}) {
		t.Fatal("cancelled context", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	lifetime, stop := context.WithCancel(t.Context())
	stop()
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, lifetime, reference, request); !errors.Is(err, context.Canceled) || got != (mgRuleAcceptance{}) {
		t.Fatal("cancelled lifetime", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A revoked session denies replay after reauthorization.
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request); err != nil {
		t.Fatal(err)
	}
	mgRuleCommit(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1,revoked_reason='explicit_signout',revoked_at=? WHERE id=?`, w.now().UTC().Format("2006-01-02T15:04:05Z"), v.proof.witness.issuer.family); err != nil {
		t.Fatal(err)
	}
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
		t.Fatal("revoked session replayed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A FULL frame can never borrow the NORMAL rule purpose.
	w2, _, v2 := mgRuleCommandFixture(t)
	full, err := w2.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = full.rollback(nil) })
	full.descriptor.ID = MGSaveRule
	defs := mgRuleCommandTurnDefinitions()
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
	fullRequest := mgRuleSaveRequestFor(v2, id, 0, mgRuleValuesConfig(v2, nil), mgRulePreparedWire(mgRuleValuesConfig(v2, nil)))
	if got, err := acceptMGRuleSave(t.Context(), fullTurn, mgPersonalFake(t, v2.personal), v2.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), fullRequest), fullRequest); !errors.Is(err, ErrInvalid) || got != (mgRuleAcceptance{}) {
		t.Fatal("FULL frame borrowed a NORMAL rule purpose", got, err)
	}
}

func TestMGRuleCommandWorkCustody(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id := strings.Repeat("e", 64)
	config := mgRuleValuesConfig(v, nil)
	prepared := mgRulePreparedWire(config)
	save := mgRuleSaveRequestFor(v, id, 0, config, prepared)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), save), save); err != nil {
		t.Fatal(err)
	}
	mgRuleCommit(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_rule_work SET job_lease_owner=7,job_lease_generation=9,job_lease_expires_ms=900,job_attempts=2 WHERE rule_id=?`, id); err != nil {
		t.Fatal(err)
	}
	// A save update and a delete never reset a live worker's lease/attempt debt.
	updateConfig := config
	updateConfig.name = "Renamed rule"
	updatePrepared := mgRulePreparedWire(updateConfig)
	update := mgRuleSaveRequestFor(v, id, 1, updateConfig, updatePrepared)
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	updated, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), update), update)
	if err != nil || updated.decision != receiptFresh || updated.rule.revision != 2 || !updated.rule.enabled {
		t.Fatal(updated, err)
	}
	mgRuleCommit(t, turn)
	for _, want := range []struct {
		name             string
		owner, gen, exp  int64
		attempts, jobRev int64
	}{
		{"after-update", 7, 9, 900, 2, 2},
	} {
		var owner, generation, expiry, attempts, jobRevision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_revision FROM dvr_rule_work WHERE rule_id=?`, id).Scan(&owner, &generation, &expiry, &attempts, &jobRevision); err != nil {
			t.Fatal(want.name, err)
		}
		if owner != want.owner || generation != want.gen || expiry != want.exp || attempts != want.attempts || jobRevision != want.jobRev {
			t.Fatal(want.name, owner, generation, expiry, attempts, jobRevision)
		}
	}
}

func TestMGRuleCommandIdentityResolution(t *testing.T) {
	t.Run("live-duplicate", func(t *testing.T) {
		w, _, v := mgRuleCommandFixture(t)
		fake := mgPersonalFake(t, v.personal)
		config := mgRuleValuesConfig(v, nil)
		prepared := mgRulePreparedWire(config)
		first := mgRuleSaveRequestFor(v, strings.Repeat("e", 64), 0, config, prepared)
		turn := mgRuleCommandTurn(t, w, MGSaveRule)
		if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), first), first); err != nil {
			t.Fatal(err)
		}
		mgRuleCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
			t.Fatal(err)
		}
		// A different rule id for the same native identity is a duplicate.
		second := mgRuleSaveRequestFor(v, strings.Repeat("f", 64), 0, config, prepared)
		turn = mgRuleCommandTurn(t, w, MGSaveRule)
		if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), second), second); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
			t.Fatal("duplicate identity accepted", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("revive-deleted-identity", func(t *testing.T) {
		w, _, v := mgRuleCommandFixture(t)
		fake := mgPersonalFake(t, v.personal)
		id := strings.Repeat("e", 64)
		config := mgRuleValuesConfig(v, nil)
		prepared := mgRulePreparedWire(config)
		save := mgRuleSaveRequestFor(v, id, 0, config, prepared)
		turn := mgRuleCommandTurn(t, w, MGSaveRule)
		if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), save), save); err != nil {
			t.Fatal(err)
		}
		mgRuleCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
			t.Fatal(err)
		}
		deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
		turn = mgRuleCommandTurn(t, w, MGDeleteRule)
		if _, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest), deleteRequest); err != nil {
			t.Fatal(err)
		}
		mgRuleCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=2 WHERE root_id=?`, v.root); err != nil {
			t.Fatal(err)
		}
		// Recreate by identity: the previously deleted native row is revived at
		// its next revision under its ORIGINAL id, not a second row.
		reviveConfig := config
		reviveConfig.episodes = "new"
		revive := mgRuleSaveRequestFor(v, strings.Repeat("f", 64), 0, reviveConfig, mgRulePreparedWire(reviveConfig))
		turn = mgRuleCommandTurn(t, w, MGSaveRule)
		out, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(3, 3), revive), revive)
		if err != nil || out.decision != receiptFresh || out.rule.id != id || out.rule.revision != 3 || out.rule.deleted || !out.rule.enabled {
			t.Fatal("revival", out, err)
		}
		mgRuleCommit(t, turn)
		var rules int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rules`).Scan(&rules); err != nil || rules != 1 {
			t.Fatal("revival created a second row", rules, err)
		}
	})

	t.Run("update-identity-and-revision", func(t *testing.T) {
		w, _, v := mgRuleCommandFixture(t)
		fake := mgPersonalFake(t, v.personal)
		id := strings.Repeat("e", 64)
		config := mgRuleValuesConfig(v, nil)
		prepared := mgRulePreparedWire(config)
		save := mgRuleSaveRequestFor(v, id, 0, config, prepared)
		turn := mgRuleCommandTurn(t, w, MGSaveRule)
		if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), save), save); err != nil {
			t.Fatal(err)
		}
		mgRuleCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
			t.Fatal(err)
		}
		// A stale expected revision never applies.
		stale := config
		stale.name = "Stale"
		turn = mgRuleCommandTurn(t, w, MGSaveRule)
		if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), mgRuleSaveRequestFor(v, id, 2, stale, mgRulePreparedWire(stale))), mgRuleSaveRequestFor(v, id, 2, stale, mgRulePreparedWire(stale))); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
			t.Fatal("stale revision", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// An edit may refine predicates but never change the native identity.
		moved := config
		moved.title, moved.series, moved.matchKind = "Moved rule", "", "title"
		moved.channel = "rule-channel"
		moveRequest := mgRuleSaveRequestFor(v, id, 1, moved, mgRulePreparedWire(moved))
		turn = mgRuleCommandTurn(t, w, MGSaveRule)
		if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), moveRequest), moveRequest); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
			t.Fatal("identity change accepted", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		// Deleting an unknown or foreign rule is one conflict class.
		turn = mgRuleCommandTurn(t, w, MGDeleteRule)
		missing := mgRuleDeleteRequestFor(v, strings.Repeat("9", 64), 1)
		if got, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), missing), missing); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
			t.Fatal("missing rule deleted", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
		foreign := mgRuleDeleteRequestFor(v, id, 1)
		foreign.owner = mgRecordingOwner{account: "foreign-account", profile: "foreign-profile", memberIncarnation: strings.Repeat("1", 32), profileLifetime: strings.Repeat("2", 32), accountEpoch: 1, memberRevision: 1, profileRevision: 1}
		turn = mgRuleCommandTurn(t, w, MGDeleteRule)
		if got, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), foreign), foreign); !errors.Is(err, ErrDisclosureDenied) || got != (mgRuleAcceptance{}) {
			t.Fatal("foreign owner delete", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}
