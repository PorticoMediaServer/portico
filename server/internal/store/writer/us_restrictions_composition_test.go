package writer

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usRestrictionsComposedFixture(t *testing.T, present bool) (*executor, *stmt.Registry, *usLibraryPolicyCommand, *usOwnerAuthorityProof, *usRestrictionsCommand, usPolicyReceiptRequest) {
	t.Helper()
	w, _, input, proof := usOwnerAuthorityFixture(t)
	input.kind = usProfileRestrictionsEdit
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO direct_profiles(id,account_id,lifetime,name) VALUES('target-profile','target','ffffffffffffffffffffffffffffffff','Profile')`); err != nil {
		t.Fatal(err)
	}
	if present {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO profile_restrictions(profile_id,revision) VALUES('target-profile',1)`); err != nil {
			t.Fatal(err)
		}
	}
	target := usRestrictionsTarget{profile: usProfileTarget{member: usMemberTarget{account: "target", incarnation: strings.Repeat("e", 32), expectedRevision: 1, expectedEpoch: 1}, profile: "target-profile", lifetime: strings.Repeat("f", 32), revision: 1}, present: present, revision: 1}
	edit := usRestrictionsEdit{ExpectedRevision: 1, RatingSystem: "mpaa", MaximumAgeRating: "PG-13", AllowUnrated: true, BlockedLabels: []string{string([]byte{255}), string([]byte{254})}, AllowDownloads: true, AllowLiveTV: true, AllowDVR: true, AllowWatchTogether: true}
	c, err := prepareUSRestrictions(input.identity, input.request.ownerID, input.request.issuer, target, edit)
	if err != nil {
		t.Fatal(err)
	}
	q := usPolicyReceiptRequest{ownerID: input.request.ownerID, kind: usProfileRestrictionsEdit, reference: ReceiptReference{Identity: input.identity, RequestDigest: c.digest}}
	defs := append(usPolicyDefinitions(), usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	defs = append(defs, usAuthorityDefinitions()...)
	defs = append(defs, usRestrictionsEffectDefinitions()...)
	defs = append(defs, usRestrictionSourceDefinitions()...)
	defs = append(defs, usRestrictionsReceiptDefinitions()...)
	return w, nativeInventoryFixtureRegistry(t, w, defs, "us_restrictions_composition_test.go"), input, proof, c, q
}

func TestUSRestrictionsCompositionAndImmutableReplay(t *testing.T) {
	for _, present := range []bool{false, true} {
		w, r, input, proof, c, q := usRestrictionsComposedFixture(t, present)
		turn, a := usInvitationComposedTurn(t, w, r, input, proof)
		fresh, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, c)
		var labels []string
		if e := json.Unmarshal([]byte(c.request.labelsJSON), &labels); e != nil {
			t.Fatal(e)
		}
		if err != nil || fresh.decision != receiptFresh || fresh.result.revision != 2 || fresh.result.maximumAge != 13 || !fresh.result.allowDVR || !reflect.DeepEqual(fresh.result.labels, labels) || fresh.chargedBytes <= 0 {
			t.Fatal(fresh, err)
		}
		usPolicyFixtureCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `UPDATE profile_restrictions SET maximum_age=0,allow_dvr=0,blocked_labels='[]',revision=revision+1 WHERE profile_id='target-profile'; UPDATE direct_profiles SET deleted=1 WHERE id='target-profile'`); err != nil {
			t.Fatal(err)
		}
		turn, a = usInvitationComposedTurn(t, w, r, input, proof)
		for _, id := range []stmt.ID{usRestrictionsSource, usRestrictionsFacts, usRestrictionSourceOctets, usRestrictionTargetOctets, usRestrictionsInsert, usRestrictionsUpdate, usRestrictionsReceiptInsert, usRestrictionsReceiptResultInsert, usPolicyOwnerAdvance} {
			delete(turn.steps, id)
		}
		var before, after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		replay, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, nil)
		if err != nil || replay.decision != receiptReplay || replay.digest != fresh.digest || !reflect.DeepEqual(replay.result, fresh.result) || replay.chargedBytes != 0 || replay.changedProfile != "" {
			t.Fatal(replay, err)
		}
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
			t.Fatal("replay wrote", before, after, err)
		}
		if _, err := turn.transaction.rollback(nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUSRestrictionsFailedReceiptRollsBackSourceAndOwner(t *testing.T) {
	w, r, input, proof, c, q := usRestrictionsComposedFixture(t, false)
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	delete(turn.steps, usRestrictionsReceiptLabelInsert)
	out, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, c)
	if err == nil || !reflect.DeepEqual(out, usRestrictionsComposition{}) {
		t.Fatal("partial result escaped", out, err)
	}
	if _, err := turn.transaction.rollback(err); err == nil {
		t.Fatal("original failure lost")
	}
	for _, query := range []string{`SELECT count(*) FROM profile_restrictions WHERE profile_id='target-profile'`, `SELECT count(*) FROM security_command_receipts`, `SELECT last_sequence FROM security_command_owners`} {
		var n int64
		if err := w.db.QueryRowContext(t.Context(), query).Scan(&n); err != nil || n != 0 {
			t.Fatal("failed prefix survived", query, n, err)
		}
	}
}

func TestUSRestrictionsCurrentDenyBeforeSavedOwner(t *testing.T) {
	w, r, input, proof, c, q := usRestrictionsComposedFixture(t, false)
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	// Another prefix changes CURRENT authority in this already stamped frame.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE identity_security_fences SET generation=generation+1`); err != nil {
		t.Fatal(err)
	}
	delete(turn.steps, usSecurityOwner)
	out, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, c)
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usRestrictionsComposition{}) {
		t.Fatal("saved owner reached before current deny", out, err)
	}
}

func TestUSRestrictionsReplayPreflightsBeforeTextAndRejectsCorruption(t *testing.T) {
	for _, fault := range []string{"memory", "extra", "digest"} {
		t.Run(fault, func(t *testing.T) {
			w, r, input, proof, c, q := usRestrictionsComposedFixture(t, true)
			turn, a := usInvitationComposedTurn(t, w, r, input, proof)
			fresh, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, c)
			if err != nil {
				t.Fatal(err)
			}
			usPolicyFixtureCommit(t, turn)
			if fault == "extra" {
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO security_restriction_result_labels(owner_id,command_incarnation,command_sequence,ordinal,label) VALUES(?,?,?,?,?)`, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, len(fresh.result.labels), "extra"); err != nil {
					t.Fatal(err)
				}
			} else if fault == "digest" {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE security_restriction_results SET maximum_age=99`); err != nil {
					t.Fatal(err)
				}
			}
			turn, a = usInvitationComposedTurn(t, w, r, input, proof)
			if fault == "memory" {
				a.memoryBytes = 64 << 10
				delete(turn.steps, usRestrictionsReceiptHeader)
				delete(turn.steps, usRestrictionsReceiptResult)
				delete(turn.steps, usRestrictionsReceiptLabels)
			}
			out, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, nil)
			if err == nil || !reflect.DeepEqual(out, usRestrictionsComposition{}) {
				t.Fatal(out, err)
			}
			if fault == "memory" {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
					t.Fatal("text before preflight", err)
				}
			} else if !errors.Is(err, ErrResolveRequired) {
				t.Fatal(err)
			}
		})
	}
}

func TestUSRestrictionsCompositionCohortBoundaries(t *testing.T) {
	for _, count := range []int{0, 64} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			w, r, input, proof, old, q := usRestrictionsComposedFixture(t, false)
			labels := make([]string, count)
			for n := range labels {
				labels[n] = strings.Repeat(string([]byte{255}), 117) + fmt.Sprintf("-%02d", n)
			}
			v := old.request
			c, err := prepareUSRestrictions(old.identity, v.ownerID, v.issuer, v.target, usRestrictionsEdit{ExpectedRevision: 1, BlockedLabels: labels, AllowUnrated: true, AllowDownloads: true, AllowLiveTV: true, AllowDVR: true, AllowWatchTogether: true})
			if err != nil {
				t.Fatal(err)
			}
			q.reference.RequestDigest = c.digest
			turn, a := usInvitationComposedTurn(t, w, r, input, proof)
			fresh, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, c)
			if err != nil || len(fresh.result.labels) != count || fresh.result.labels == nil {
				t.Fatal("fresh boundary", count, err)
			}
			if count > 0 && len(fresh.result.labels[0]) != 354 {
				t.Fatal("repaired UTF-8 width", len(fresh.result.labels[0]))
			}
			usPolicyFixtureCommit(t, turn)
			turn, a = usInvitationComposedTurn(t, w, r, input, proof)
			for _, id := range []stmt.ID{usRestrictionsReceiptLabelOctets, usRestrictionsReceiptLabels} {
				step := turn.steps[id]
				step.work.Rows, step.work.VMOps, step.work.Pages = 128, 32000, 128
				turn.steps[id] = step
			}
			replay, err := composeUSRestrictionsAfterAuthority(t.Context(), turn, a, q, nil)
			if err != nil || replay.digest != fresh.digest || !reflect.DeepEqual(replay.result, fresh.result) {
				t.Fatal("replay boundary", count, err)
			}
		})
	}
}
