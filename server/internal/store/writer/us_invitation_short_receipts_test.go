package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usShortReceiptFixture(t *testing.T, extra ...stmt.Definition) (*executor, *stmt.Registry, *usLibraryPolicyCommand, *usOwnerAuthorityProof, *usInvitationShortCommand, usPolicyReceiptRequest) {
	t.Helper()
	w, _, input, proof := usOwnerAuthorityFixture(t)
	input.kind = usInvitationShortIssue
	r := shortFixture()
	r.identity = input.identity
	r.scope.ownerID, r.scope.issuer = input.request.ownerID, input.request.issuer
	r.issuedMS, r.invitationExpiryMS = w.now().UnixMilli(), w.now().UnixMilli()+900000
	c := shortPrepare(t, r)
	q := usPolicyReceiptRequest{ownerID: r.scope.ownerID, kind: usInvitationShortIssue, reference: ReceiptReference{Identity: r.identity, RequestDigest: c.digest}}
	defs := append(usPolicyDefinitions(), usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	defs = append(defs, usAuthorityDefinitions()...)
	defs = append(defs, usInvitationShortReceiptDefinitions()...)
	defs = append(defs, extra...)
	return w, nativeInventoryFixtureRegistry(t, w, defs, "us_invitation_short_receipts_test.go"), input, proof, c, q
}

func storeUSShortFixture(t *testing.T, w *executor, r *stmt.Registry, input *usLibraryPolicyCommand, proof *usOwnerAuthorityProof, c *usInvitationShortCommand, q usPolicyReceiptRequest) (usInvitationShortSavedResult, ResultDigest, int64) {
	t.Helper()
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	lookup, err := lookupUSInvitationShort(t.Context(), turn, a, q)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup, err)
	}
	v, err := usInvitationShortPreparedResult(c, w.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	digest, err := usInvitationShortResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		t.Fatal(err)
	}
	charged, err := storeUSInvitationShortResult(t.Context(), turn, q, lookup.owner, v, digest)
	if err != nil {
		t.Fatal(err)
	}
	usPolicyFixtureCommit(t, turn)
	return v, digest, charged
}

func TestUSInvitationShortImmutableReceiptReplay(t *testing.T) {
	w, r, input, proof, c, q := usShortReceiptFixture(t)
	v, digest, charged := storeUSShortFixture(t, w, r, input, proof, c, q)
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	for _, id := range []stmt.ID{usInvitationSourceFacts, usInvitationSourceOctets, usInvitationLinkKind, usInvitationLinkKey, usInvitationShortSourceOctets, usInvitationShortInsert, usInvitationShortUpdate, usInvitationShortReceiptInsert, usInvitationShortReceiptResultInsert, usPolicyOwnerAdvance} {
		delete(turn.steps, id)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	got, err := lookupUSInvitationShort(t.Context(), turn, a, q)
	if err != nil || got.decision != receiptReplay || got.digest != digest || !reflect.DeepEqual(got.result, v) || got.owner.bytes != charged || got.owner.count != 1 {
		t.Fatal(got, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("replay changed source", before, after, err)
	}
	// Original request binding wins even if result decoding is unavailable.
	delete(turn.steps, usInvitationShortReceiptResult)
	q.reference.RequestDigest[0] ^= 1
	if got, err := lookupUSInvitationShort(t.Context(), turn, a, q); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, usInvitationShortReceiptLookup{}) {
		t.Fatal("payload preceded original identity", got, err)
	}
}

func TestUSInvitationShortReceiptCorruptionAndPreflight(t *testing.T) {
	for _, mode := range []string{"aad", "hash", "memory", "current-deny"} {
		t.Run(mode, func(t *testing.T) {
			w, r, input, proof, c, q := usShortReceiptFixture(t)
			storeUSShortFixture(t, w, r, input, proof, c, q)
			if mode == "aad" || mode == "hash" {
				query, value := `UPDATE security_invitation_short_results SET result_aad_digest=? WHERE owner_id=?`, any(make([]byte, 32))
				if mode == "hash" {
					query, value = `UPDATE security_invitation_short_results SET lookup_hash=? WHERE owner_id=?`, strings.Repeat("f", 64)
				}
				if _, err := w.db.ExecContext(t.Context(), query, value, q.ownerID); err != nil {
					t.Fatal(err)
				}
			}
			turn, a := usInvitationComposedTurn(t, w, r, input, proof)
			if mode == "memory" {
				a.memoryBytes = 64 << 10
				delete(turn.steps, usInvitationShortReceiptHeader)
			}
			if mode == "current-deny" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE direct_memberships SET disabled=1 WHERE account_id=?`, a.issuer.account); err != nil {
					t.Fatal(err)
				}
				delete(turn.steps, usSecurityOwner)
			}
			got, err := lookupUSInvitationShort(t.Context(), turn, a, q)
			if err == nil || !reflect.DeepEqual(got, usInvitationShortReceiptLookup{}) {
				t.Fatal("corrupt/unauthorized image escaped", got, err)
			}
			if mode == "memory" {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
					t.Fatal("payload scan preceded memory preflight", err)
				}
			}
			if mode == "current-deny" && !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal("owner read preceded current deny", err)
			}
		})
	}
}
