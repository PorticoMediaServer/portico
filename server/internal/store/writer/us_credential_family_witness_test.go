package writer

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The 522 prepared request carries the credential family's OWN
// command_incarnation (16-byte BLOB) and current_generation as OWNED comparison
// witnesses frozen into the canonical request digest. Reading and CASing them
// only inside the applying frame proves snapshot consistency, not
// prepare->apply identity: a family re-minted under the same family id would
// look current. These controls replace only the family tuple between prepare
// and apply and require the EXACT prepared tuple on BOTH branches.

// usCredentialReMintFamily replaces the family tuple under the SAME family id,
// which is the ABA case the prepared witness must catch. Plain SQL is the
// independent oracle here; nothing goes through the writer's own statements.
func usCredentialReMintFamily(t *testing.T, w *executor, incarnation [16]byte) {
	t.Helper()
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_incarnation=? WHERE id=?`, incarnation[:], usCredentialFamilyID)
}

func usCredentialAssertStaleFamilyWitnessRefused(t *testing.T, w *executor, r *stmt.Registry, proof *usOwnerAuthorityProof, revoke *usCredentialNativeCommand, credentialBefore, familyBefore, deviceBefore string, hintsBefore int64) {
	t.Helper()
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	out, floor, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usCredentialDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("stale family witness was not refused with zero output", out, floor, err)
	}
	credentialAfter, familyAfter, deviceAfter := usCredentialRevokeRows(t, w)
	if credentialAfter != credentialBefore || familyAfter != familyBefore || deviceAfter != deviceBefore {
		t.Fatal("stale family witness changed a source row")
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != hintsBefore {
		t.Fatal("stale family witness left a hint", n, hintsBefore)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 0 {
		t.Fatal("stale family witness kept a 522 receipt", n)
	}
	if n := usCredentialCount(t, w, `SELECT count(*) FROM security_credential_results WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 0 {
		t.Fatal("stale family witness kept a 522 result", n)
	}
}

// A re-mint under the same family id (new command_incarnation, same family row)
// with an unchanged generation is refused on the live AND the already-revoked
// branch: the prepared tuple is part of prepare->apply identity.
func TestUSCredentialRevokeFamilyWitnessReplacedIncarnation(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		name := "live"
		if revoked {
			name = "already_revoked"
		}
		t.Run(name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			if revoked {
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=1,revoked_reason='password_change',revoked_at='2020-01-02T03:04:05Z' WHERE id=?`, usCredentialFamilyID)
			}
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			usCredentialReMintFamily(t, w, [16]byte{0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f, 0x80})
			credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
			hintsBefore := usCredentialCount(t, w, `SELECT count(*) FROM api_events`)
			usCredentialAssertStaleFamilyWitnessRefused(t, w, r, proof, revoke, credentialBefore, familyBefore, deviceBefore, hintsBefore)
		})
	}
}

// A generation advance under the same family id and incarnation is the same
// stale prepared witness, refused on BOTH branches.
func TestUSCredentialRevokeFamilyWitnessChangedGeneration(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		name := "live"
		if revoked {
			name = "already_revoked"
		}
		t.Run(name, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			if revoked {
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=1,revoked_reason='password_change',revoked_at='2020-01-02T03:04:05Z' WHERE id=?`, usCredentialFamilyID)
			}
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			wc008NativeExec(t, w, `UPDATE authorization_session_families SET current_generation=current_generation+1 WHERE id=?`, usCredentialFamilyID)
			credentialBefore, familyBefore, deviceBefore := usCredentialRevokeRows(t, w)
			hintsBefore := usCredentialCount(t, w, `SELECT count(*) FROM api_events`)
			usCredentialAssertStaleFamilyWitnessRefused(t, w, r, proof, revoke, credentialBefore, familyBefore, deviceBefore, hintsBefore)
		})
	}
}

// Only a changed family tuple makes a different frozen request digest: the
// witness is part of the canonical 522 preimage, and the command digest equals
// a fresh digest over the sealed job.
func TestUSCredentialRevokeFamilyWitnessInDigest(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	identity := CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2}
	base := usCredentialRevokeRequestFor(t, w, issue)
	original, err := prepareUSCredentialNativeRevoke(identity, usCredentialOwnerID, proof.witness.issuer, base)
	if err != nil {
		t.Fatal(err)
	}
	recomputed, err := usCredentialCommandDigest(original)
	if err != nil || recomputed != original.digest {
		t.Fatal("command digest does not bind the sealed job", recomputed, original.digest, err)
	}
	changedIncarnation := base
	changedIncarnation.familyIncarnation[0] ^= 0xff
	other, err := prepareUSCredentialNativeRevoke(identity, usCredentialOwnerID, proof.witness.issuer, changedIncarnation)
	if err != nil {
		t.Fatal(err)
	}
	if other.digest == original.digest {
		t.Fatal("changed family incarnation did not change the request digest")
	}
	changedGeneration := base
	changedGeneration.familyGeneration++
	other, err = prepareUSCredentialNativeRevoke(identity, usCredentialOwnerID, proof.witness.issuer, changedGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if other.digest == original.digest {
		t.Fatal("changed family generation did not change the request digest")
	}
}

// A matching already-revoked tuple still completes: the witness comparison is a
// bind, not a refusal of monotonic completion for the same exact family.
func TestUSCredentialRevokeFamilyWitnessMatchingAlreadyRevokedCompletes(t *testing.T) {
	w, proof := usCredentialAuthorityFixture(t, false)
	usCredentialOwnerFixture(t, w, proof, [16]byte{1})
	r := usCredentialRegistry(t)
	issue := usCredentialIssuedAcknowledged(t, w, proof, r)
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET revoked=1,revoked_reason='membership_removed',revoked_at='2020-01-02T03:04:05Z',teardown_due_ms=NULL WHERE id=? AND revoked=0`, usCredentialFamilyID)
	familyBefore := usCredentialTableRow(t, w, "authorization_session_families", "id", usCredentialFamilyID)
	revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
	turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
	out, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.revokedMS <= 0 {
		t.Fatal("matching already-revoked tuple did not complete", out, err)
	}
	if n := usCredentialCount(t, w, `SELECT revoked_ms FROM api_credentials WHERE id=?`, issue.issue.prepared.id); n != out.Value.revokedMS {
		t.Fatal("completion marker", n, out.Value.revokedMS)
	}
	if got := usCredentialTableRow(t, w, "authorization_session_families", "id", usCredentialFamilyID); got != familyBefore {
		t.Fatal("completion rewrote the same exact family")
	}
}

// Replay/Resolve is receipt-first: the saved original is returned under current
// issuer/disclosure authority without requiring the mutable target family to
// exist or match again. Only Fresh performs the target-source comparisons.
func TestUSCredentialRevokeFamilyWitnessReplayIgnoresFamily(t *testing.T) {
	for _, mode := range []string{"replaced", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			w, proof := usCredentialAuthorityFixture(t, false)
			usCredentialOwnerFixture(t, w, proof, [16]byte{1})
			r := usCredentialRegistry(t)
			issue := usCredentialIssuedAcknowledged(t, w, proof, r)
			revoke := usCredentialRevokeCommandFixture(t, w, proof, issue, CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{6}, Sequence: 2})
			turn, a, _ := usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			original, _, err := runUSCredentialDispatch(t.Context(), turn, a, usCredentialInput(t, revoke))
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "replaced":
				replacement := [16]byte{0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d, 0x8e, 0x8f, 0x90}
				wc008NativeExec(t, w, `UPDATE authorization_session_families SET command_incarnation=?,current_generation=current_generation+1 WHERE id=?`, replacement[:], usCredentialFamilyID)
			case "deleted":
				wc008NativeExec(t, w, `DELETE FROM authorization_family_security_fences WHERE family_id=?`, usCredentialFamilyID)
				wc008NativeExec(t, w, `DELETE FROM authorization_session_families WHERE id=?`, usCredentialFamilyID)
				if n := usCredentialCount(t, w, `SELECT count(*) FROM authorization_session_families WHERE id=?`, usCredentialFamilyID); n != 0 {
					t.Fatal("family fixture was not removed", n)
				}
			}
			changesBefore := usCredentialCount(t, w, `SELECT total_changes()`)
			turn, a, _ = usCredentialTurn(t, w, r, proof, usAPICredentialRevoke, 0)
			replay, _, err := runUSCredentialDispatch(t.Context(), turn, a, &usCredentialInvocation{request: usCredentialInput(t, revoke).request})
			if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
				t.Fatal("replay of the saved original", replay, err)
			}
			if changesAfter := usCredentialCount(t, w, `SELECT total_changes()`); changesAfter != changesBefore {
				t.Fatal("replay wrote changes", changesBefore, changesAfter)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=?`, int64(usAPICredentialRevoke)); n != 1 {
				t.Fatal("replay wrote a second receipt", n)
			}
			if n := usCredentialCount(t, w, `SELECT count(*) FROM api_events`); n != 1 {
				t.Fatal("replay wrote a second hint", n)
			}
		})
	}
}
