package writer

import (
	"errors"
	"testing"
)

func TestMGSourceSavedOriginalDenial(t *testing.T) {
	turn, p, b := mgAuthorityFixture(t, MGRetireSource, mgActorOwner)
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='removed',removal_revision=8,removal_removed_at='now'; UPDATE live_sources SET revision=8,state='disabled'`); err != nil {
		t.Fatal(err)
	}
	f := openMGAuthorityFixture(t, turn, p, b)
	v, err := originalMGSourceDenial(t.Context(), f)
	if err != nil || v.Code != "source_retired" || v.Revision != 8 {
		t.Fatal(v, err)
	}
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	payload, d, err := mgSourceDenialPayload(id, q, v)
	if err != nil {
		t.Fatal(err)
	}
	// Head drift does not prevent current owner disclosure of the ORIGINAL
	// receipt. It still blocks fresh effects and all new acquisitions.
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET active_generation='new',revision=9`); err != nil {
		t.Fatal(err)
	}
	if err = f.recheck(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	got, err := f.discloseOriginalDenial(t.Context(), id, q, payload, d)
	if err != nil || got != v {
		t.Fatal(got, err)
	}
	if err = f.requireFreshHead(t.Context(), mgSourceHeadWitness{"source", "new", "owner-only", 9}); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	// Current revocation wins before the receipt value may be disclosed.
	if _, err = turn.transaction.conn.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1`); err != nil {
		t.Fatal(err)
	}
	if denied, e := f.discloseOriginalDenial(t.Context(), id, q, payload, d); denied != (mgSourceOriginalDenial{}) || !errors.Is(e, ErrDisclosureDenied) {
		t.Fatal("revoked replay", denied, e)
	}
	if err = f.recheck(t.Context(), turn); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(err)
	}
	if got, err = originalMGSourceDenial(t.Context(), f); got != (mgSourceOriginalDenial{}) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(got, err)
	}
	for _, at := range []int{0, 20, len(payload) - 1} {
		bad := append([]byte(nil), payload...)
		bad[at] ^= 1
		if got, err = decodeMGSourceDenial(id, q, bad, d); got != (mgSourceOriginalDenial{}) || err == nil {
			t.Fatal("corrupt payload", at, got, err)
		}
	}
	for _, bad := range []struct {
		id CommandIdentity
		q  RequestDigest
		d  ResultDigest
	}{{CommandIdentity{Incarnation: id.Incarnation, Sequence: 2, OperationID: id.OperationID}, q, d}, {id, RequestDigest{4}, d}, {id, q, ResultDigest{5}}} {
		if got, err = decodeMGSourceDenial(bad.id, bad.q, payload, bad.d); got != (mgSourceOriginalDenial{}) || err == nil {
			t.Fatal("mismatched receipt", got, err)
		}
	}
}

func TestMGSourceDenialRequiresRetirementAndBindsSourceCommand(t *testing.T) {
	turn, p, b := mgAuthorityFixture(t, MGRetireSource, mgActorOwner)
	f := openMGAuthorityFixture(t, turn, p, b)
	if v, err := originalMGSourceDenial(t.Context(), f); v != (mgSourceOriginalDenial{}) || !errors.Is(err, ErrConflict) {
		t.Fatal("non-retired source minted denial", v, err)
	}
	id := CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}
	q := RequestDigest{3}
	for _, v := range []mgSourceOriginalDenial{
		{Command: MGRetireSource, Version: 1, Revision: 8, Source: "foreign", Code: "source_retired", AppliedMS: 101},
		{Command: MGSetSourceEnabled, Version: 1, Revision: 8, Source: "source", Code: "source_retired", AppliedMS: 101},
	} {
		payload, d, err := mgSourceDenialPayload(id, q, v)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := f.discloseOriginalDenial(t.Context(), id, q, payload, d); got != (mgSourceOriginalDenial{}) || !errors.Is(err, ErrResolveRequired) {
			t.Fatal("foreign saved denial disclosed", got, err)
		}
	}
}
