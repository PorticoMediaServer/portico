package writer

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func mgRuleSeedNativeRule(t *testing.T, w *executor, v mgRuleCommandValues, id, owner, account, profile, series, title string, revision int64, config string, enabled, deleted int64) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_rules(id,owner_key,authority,account_id,profile_id,source_id,series_id,match_title,match_channel,revision,config_json,enabled,deleted,updated_ms) VALUES(?,?,'local',?,?,?,?,?,?,?,?,?,?,100)`,
		id, owner, account, profile, v.source, series, title, "", revision, config, enabled, deleted); err != nil {
		t.Fatal(err)
	}
}

// The prepared configuration is fresh-only. A replay or retired lookup must not
// depend on the adapter's current prepared string; only a fresh effect rechecks
// the stored identity against the digested original.
func TestMGRuleCommandSaveReplayIgnoresPreparedDrift(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id, config := strings.Repeat("e", 64), mgRuleValuesConfig(v, nil)
	prepared := mgRulePreparedWire(config)
	request := mgRuleSaveRequestFor(v, id, 0, config, prepared)
	reference := mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	first, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || first.decision != receiptFresh {
		t.Fatal(first, err)
	}
	mgRuleCommit(t, turn)

	// A different but valid prepared string is not part of original identity.
	driftedConfig := config
	driftedConfig.series = "series-two"
	drifted := request
	drifted.prepared = mgRulePreparedWire(driftedConfig)
	if drifted.prepared == prepared {
		t.Fatal("fixture prepared strings did not differ")
	}
	if _, err := newMGRuleSaveCommand(mgPersonalIdentity(1, 1), drifted); err != nil {
		t.Fatal("replay request depended on the prepared string", err)
	}
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	replay, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, drifted)
	if err != nil || replay.decision != receiptReplay || replay.rule != first.rule || replay.digest != first.digest {
		t.Fatal("prepared drift broke replay", replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM dvr_rules WHERE id=?`, id).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("replay changed the rule", revision, err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}

	// A fresh effect still refuses a stored identity that disagrees with the
	// digested original, and never depends on a malformed prepared string
	// succeeding anywhere. A different native identity keeps the live-duplicate
	// resolution from masking the prepared-identity check.
	freshConfig := config
	freshConfig.series = "series-two"
	freshPrepared := mgRulePreparedWire(freshConfig)
	fresh := mgRuleSaveRequestFor(v, strings.Repeat("f", 64), 0, freshConfig, freshPrepared)
	mismatch := fresh
	mismatch.prepared = strings.Replace(freshPrepared, v.source, strings.Repeat("c", 48), 1)
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), fresh), mismatch); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("fresh prepared identity drift accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	malformed := fresh
	malformed.prepared = `{"sourceId":`
	turn = mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(2, 2), fresh), malformed); !errors.Is(err, ErrInvalid) || got != (mgRuleAcceptance{}) {
		t.Fatal("fresh malformed prepared accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rules int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rules`).Scan(&rules); err != nil || rules != 1 {
		t.Fatal("fresh refusals wrote a rule", rules, err)
	}
}

// One fixture, two intent guards that removal would otherwise turn into a raw
// native mismatch class or a cross-owner overwrite.
func TestMGRuleCommandOwnershipGuards(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	id, config := strings.Repeat("e", 64), mgRuleValuesConfig(v, nil)
	prepared := mgRulePreparedWire(config)
	// The native row exists but belongs to someone else. The command identity is
	// the caller's; only the current owner tuple may satisfy it.
	mgRuleSeedNativeRule(t, w, v, id, strings.Repeat("9", 64), "foreign-account", "foreign-profile", config.series, config.title, 1, prepared, 1, 0)

	save := mgRuleSaveRequestFor(v, id, 1, config, prepared)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), save), save); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("save update crossed the owner guard", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	del := mgRuleDeleteRequestFor(v, id, 1)
	turn = mgRuleCommandTurn(t, w, MGDeleteRule)
	if got, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), mgRuleDeleteReference(t, mgPersonalIdentity(1, 1), del), del); !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("delete crossed the owner guard", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var revision, enabled, deleted int64
	var owner string
	if err := w.db.QueryRowContext(t.Context(), `SELECT owner_key,revision,enabled,deleted FROM dvr_rules WHERE id=?`, id).Scan(&owner, &revision, &enabled, &deleted); err != nil {
		t.Fatal(err)
	}
	if owner != strings.Repeat("9", 64) || revision != 1 || enabled != 1 || deleted != 0 {
		t.Fatal("guard refusal wrote the foreign rule", owner, revision, enabled, deleted)
	}
}

// mgRuleSameIdentity reads only authority='local', but the native UNIQUE does
// not include authority. A fresh create against a non-local row with the same
// identity must be a typed conflict, not a raw constraint diagnostic.
func TestMGRuleCommandNonLocalIdentityCollision(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	config := mgRuleValuesConfig(v, nil)
	prepared := mgRulePreparedWire(config)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO dvr_rules(id,owner_key,authority,account_id,profile_id,source_id,series_id,match_title,match_channel,revision,config_json,enabled,deleted,updated_ms) VALUES(?,?,'legacy',?,?,?,?,?,'',1,?,1,0,100)`,
		strings.Repeat("e", 64), v.owner.key(), v.owner.account, v.owner.profile, v.source, config.series, config.title, prepared); err != nil {
		t.Fatal(err)
	}
	request := mgRuleSaveRequestFor(v, strings.Repeat("f", 64), 0, config, prepared)
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request), request)
	if !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("non-local identity collision was not a typed conflict", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rules int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rules`).Scan(&rules); err != nil || rules != 1 {
		t.Fatal("collision wrote a rule", rules, err)
	}
}

// Reviving a deleted identity at MaxInt64 has no next revision; it is a conflict,
// not an overflowing success or a generic invalid effect.
func TestMGRuleCommandReviveRevisionCeiling(t *testing.T) {
	w, _, v := mgRuleCommandFixture(t)
	fake := mgPersonalFake(t, v.personal)
	config := mgRuleValuesConfig(v, nil)
	mgRuleSeedNativeRule(t, w, v, strings.Repeat("e", 64), v.owner.key(), v.owner.account, v.owner.profile, config.series, config.title, math.MaxInt64, mgRulePreparedWire(config), 0, 1)
	request := mgRuleSaveRequestFor(v, strings.Repeat("f", 64), 0, config, mgRulePreparedWire(config))
	turn := mgRuleCommandTurn(t, w, MGSaveRule)
	got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request), request)
	if !errors.Is(err, ErrConflict) || got != (mgRuleAcceptance{}) {
		t.Fatal("ceiling revival accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var rules int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_rules`).Scan(&rules); err != nil || rules != 1 {
		t.Fatal("ceiling revival wrote a row", rules, err)
	}
}

// A retained receipt classified as a replay must carry exactly the original
// single result the rule codec decodes; anything else needs durable resolution.
func TestMGRuleCommandReplayResultCountGuard(t *testing.T) {
	t.Run("save", func(t *testing.T) {
		w, _, v := mgRuleCommandFixture(t)
		fake := mgPersonalFake(t, v.personal)
		config := mgRuleValuesConfig(v, nil)
		request := mgRuleSaveRequestFor(v, strings.Repeat("e", 64), 0, config, mgRulePreparedWire(config))
		reference := mgRuleSaveReference(t, mgPersonalIdentity(1, 1), request)
		turn := mgRuleCommandTurn(t, w, MGSaveRule)
		if _, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request); err != nil {
			t.Fatal(err)
		}
		mgRuleCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_count=2 WHERE root_id=? AND command_sequence=1`, v.root); err != nil {
			t.Fatal(err)
		}
		turn = mgRuleCommandTurn(t, w, MGSaveRule)
		if got, err := acceptMGRuleSave(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrResolveRequired) || got != (mgRuleAcceptance{}) {
			t.Fatal("save replay accepted a foreign result count", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("delete", func(t *testing.T) {
		w, v, id := mgRuleDeleteKeepFixture(t)
		fake := mgPersonalFake(t, v.personal)
		deleteRequest := mgRuleDeleteRequestFor(v, id, 1)
		reference := mgRuleDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest)
		turn := mgRuleCommandTurn(t, w, MGDeleteRule)
		if _, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest); err != nil {
			t.Fatal(err)
		}
		mgRuleCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_count=0 WHERE root_id=? AND command_sequence=2`, v.root); err != nil {
			t.Fatal(err)
		}
		turn = mgRuleCommandTurn(t, w, MGDeleteRule)
		if got, err := acceptMGRuleDelete(t.Context(), turn, fake, v.proof, t.Context(), reference, deleteRequest); !errors.Is(err, ErrResolveRequired) || got != (mgRuleAcceptance{}) {
			t.Fatal("delete replay accepted a foreign result count", got, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	})
}
