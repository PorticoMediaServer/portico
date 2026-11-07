package writer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestUSRestrictionSourceReservesActualBytesBeforePayload(t *testing.T) {
	w, _, input := usPolicyReceiptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO direct_profiles(id,account_id,lifetime,name) VALUES('target-profile','target','ffffffffffffffffffffffffffffffff','Profile')`); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal([]string{"界", "a\u0001b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('target-profile',?)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	defs := append(usRestrictionsEffectDefinitions(), usRestrictionSourceDefinitions()...)
	r := nativeInventoryFixtureRegistry(t, w, defs, "us_restrictions_snapshot.go", "us_restrictions_snapshot_test.go")
	// Existing test-only stamped frame; this source helper grants no 542
	// acceptance or additional DVR/manager authority.
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	out, present, err := readUSRestrictionSource(t.Context(), turn, a, "target-profile")
	if err != nil || !present || out.profile != "target-profile" || out.labelsJSON != string(raw) || out.revision != 1 {
		t.Fatal(out, present, err)
	}
	target := usRestrictionsTarget{profile: usProfileTarget{member: usMemberTarget{account: "target", incarnation: strings.Repeat("e", 32), expectedRevision: 1, expectedEpoch: 1}, profile: "target-profile", lifetime: strings.Repeat("f", 32), revision: 1}, present: true, revision: 1}
	role, primary, err := readUSRestrictionTarget(t.Context(), turn, a, target)
	if err != nil || role != "member" || primary {
		t.Fatal(role, primary, err)
	}
	stale := target
	stale.profile.revision++
	role, primary, err = readUSRestrictionTarget(t.Context(), turn, a, stale)
	if !errors.Is(err, ErrConflict) || role != "" || primary {
		t.Fatal("stale target admitted", role, primary, err)
	}
	delete(turn.steps, usRestrictionsFacts)
	out, present, err = readUSRestrictionSource(t.Context(), turn, a, "missing-profile")
	if err != nil || present || out != (usRestrictionsEffect{}) {
		t.Fatal("absence inferred a payload/default", out, present, err)
	}
	large, err := json.Marshal([]string{strings.Repeat("界", 10000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE profile_restrictions SET blocked_labels=? WHERE profile_id='target-profile'`, string(large)); err != nil {
		t.Fatal(err)
	}
	a.memoryBytes = 64 << 10
	delete(turn.steps, usRestrictionsSource)
	role, primary, err = readUSRestrictionTarget(t.Context(), turn, a, target)
	var targetRefusal *Refusal
	if !errors.As(err, &targetRefusal) || targetRefusal.Code != RefusalCapacity || role != "" || primary {
		t.Fatal("target text before reservation", role, primary, err)
	}
	out, present, err = readUSRestrictionSource(t.Context(), turn, a, "target-profile")
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || present || out != (usRestrictionsEffect{}) {
		t.Fatal("text step reached before byte reservation", out, present, err)
	}
}
