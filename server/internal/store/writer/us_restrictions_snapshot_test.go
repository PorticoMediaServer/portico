package writer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestUSRestrictionsSourceStatementInventory(t *testing.T) {
	defs := usRestrictionsDefinitions()
	if len(defs) != 5 {
		t.Fatal("source statement inventory changed", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Cohorts) == 0 {
			t.Fatal("source statement must stay hot, captured and waiver-free", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		want := stmt.Read
		if d.ID == usRestrictionsUpdate || d.ID == usRestrictionsInsert {
			want = stmt.Write
		}
		if d.Kind != want {
			t.Fatal(d.ID, "kind", d.Kind)
		}
	}
}

// The current family role must still equal the current membership role, and
// this first recipe admits the owner role only. Both are decided inside SQLite,
// before any source row is materialized.
func TestUSRestrictionsScopeNarrowing(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{"session_role_drift", `UPDATE authorization_session_families SET role='admin' WHERE id='issuer-family'`},
		{"membership_role_drift", `UPDATE direct_memberships SET role='admin' WHERE account_id='issuer'`},
		{"membership_disabled", `UPDATE direct_memberships SET disabled=1 WHERE account_id='issuer'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
			turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
			// The fixture pool has ONE writer connection and the open frame owns
			// it, so drift current source on the frame's own connection, exactly
			// as a prior statement in this same frame would.
			if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, tc.sql); err != nil {
				t.Fatal(tc.sql, err)
			}
			before, err := readUSRestrictionsFreshTarget(t.Context(), turn, a, input)
			if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(before, usRestrictionsBefore{}) {
				t.Fatal("role narrowing", before, err)
			}
		})
	}
}

// The predecessor is the exact current row: revision, recording flag and the
// documented default for a profile with no stored document.
func TestUSRestrictionsPredecessor(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	before, err := readUSRestrictionsFreshTarget(t.Context(), turn, a, input)
	if err != nil || !before.restricted || before.revision != 1 || before.allowDVR || !before.ownerGrant {
		t.Fatal(before, err)
	}
	// A profile with no stored row defaults to revision 1 and Recording on only
	// for the owner's main profile.
	w, _, r, proof, primary := usRestrictionsFixture(t, false, func(q *usRestrictionsRequest) {
		q.profile, q.profileLifetime = q.issuer.profile, q.issuer.profileLifetime
	})
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, primary.kind, 0)
	before, err = readUSRestrictionsFreshTarget(t.Context(), turn, a, primary)
	if err != nil || before.restricted || before.revision != 1 || !before.allowDVR || !before.ownerGrant {
		t.Fatal("missing main-profile row default", before, err)
	}
	// A secondary profile without a row does not record by default.
	w, _, r, proof, secondary := usRestrictionsFixture(t, false, nil)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, secondary.kind, 0)
	before, err = readUSRestrictionsFreshTarget(t.Context(), turn, a, secondary)
	if err != nil || before.restricted || before.allowDVR {
		t.Fatal("missing secondary row default", before, err)
	}
	// A stale expected revision conflicts before any write.
	w, _, r, proof, stale := usRestrictionsFixture(t, true, func(q *usRestrictionsRequest) { q.expectedRevision = 2 })
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, stale.kind, 0)
	if before, err = readUSRestrictionsFreshTarget(t.Context(), turn, a, stale); !errors.Is(err, ErrConflict) {
		t.Fatal("stale predecessor", before, err)
	}
}

// One replacement writes exactly the prepared projection and reports the ACTUAL
// post-effect row. A concurrent/stale distinction is a typed conflict.
func TestUSRestrictionsEffect(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	before, err := readUSRestrictionsFreshTarget(t.Context(), turn, a, input)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := applyUSRestrictions(t.Context(), turn, input, before)
	if err != nil {
		t.Fatal(err)
	}
	want := usRestrictionsEffect{revision: 2, ratingSystem: "mpaa", maximumAgeRating: "PG-13", blocked: `["language","violence"]`,
		maximumAge: 13, allowDownloads: true, allowWatchTogether: true}
	if !reflect.DeepEqual(effect, want) {
		t.Fatal(effect, want)
	}
	// The same stale CAS can never write again.
	if effect, err = applyUSRestrictions(t.Context(), turn, input, before); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(effect, usRestrictionsEffect{}) {
		t.Fatal("stale effect", effect, err)
	}
	// A predecessor that claims a row for an insert-only profile is a conflict,
	// not a silent overwrite.
	w, _, r, proof, insert := usRestrictionsFixture(t, false, nil)
	turn, a, _ = usRestrictionsTurn(t, w, r, proof, insert.kind, 0)
	before, err = readUSRestrictionsFreshTarget(t.Context(), turn, a, insert)
	if err != nil || before.restricted {
		t.Fatal(before, err)
	}
	before.restricted = true
	if effect, err = applyUSRestrictions(t.Context(), turn, insert, before); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(effect, usRestrictionsEffect{}) {
		t.Fatal("absent row claimed present", effect, err)
	}
}

// Who can record: switching Recordings on needs the owner; leaving it on or off
// never does.
func TestUSRestrictionsDVRGrantRule(t *testing.T) {
	for _, tc := range []struct {
		before, requested, owner, want bool
	}{
		{false, false, false, true},
		{false, true, false, false},
		{false, true, true, true},
		{true, true, false, true},
		{true, false, false, true},
		{false, false, true, true},
	} {
		if got := usRestrictionsDVRAllowed(tc.before, tc.requested, tc.owner); got != tc.want {
			t.Fatalf("allowed(%t,%t,%t)=%t", tc.before, tc.requested, tc.owner, got)
		}
	}
}

// A cross-account target is a denial with no detail. The product edits only a
// profile of the caller's own account.
func TestUSRestrictionsForeignAccountDenied(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, func(q *usRestrictionsRequest) { q.account = "other-account" })
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	if before, err := readUSRestrictionsFreshTarget(t.Context(), turn, a, input); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(before, usRestrictionsBefore{}) {
		t.Fatal(before, err)
	}
}

// The effect never runs without the exact prepared projection and the expected
// revision; both are checked before the single source write.
func TestUSRestrictionsEffectInputRefusals(t *testing.T) {
	w, _, r, proof, input := usRestrictionsFixture(t, true, nil)
	turn, a, _ := usRestrictionsTurn(t, w, r, proof, input.kind, 0)
	before, err := readUSRestrictionsFreshTarget(t.Context(), turn, a, input)
	if err != nil {
		t.Fatal(err)
	}
	turn.transaction.descriptor.ID = USProfileLibraries
	if effect, err := applyUSRestrictions(t.Context(), turn, input, before); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(effect, usRestrictionsEffect{}) {
		t.Fatal("wrong purpose", effect, err)
	}
	turn.transaction.descriptor.ID = USProfileRestrictions
	if effect, err := applyUSRestrictions(t.Context(), turn, nil, before); !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(effect, usRestrictionsEffect{}) {
		t.Fatal("nil input", effect, err)
	}
	if effect, err := applyUSRestrictions(t.Context(), turn, input, usRestrictionsBefore{}); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(effect, usRestrictionsEffect{}) {
		t.Fatal("absent predecessor", effect, err)
	}
}
