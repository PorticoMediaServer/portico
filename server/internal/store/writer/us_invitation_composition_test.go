package writer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usInvitationComposedFixture(t *testing.T, kind CommandID) (*executor, *stmt.Registry, *usLibraryPolicyCommand, *usOwnerAuthorityProof) {
	t.Helper()
	w, _, input, proof := usOwnerAuthorityFixture(t)
	input.kind = kind
	link, _ := usInvitationLinkFixture(t)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO libraries(id,name,kind,root) VALUES('library','Library','movies','/fixture')`, nil},
		{`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,'fixture')`, []any{link.serverID, link.publicKey[:], link.keyIncarnation}},
		{`INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,?)`, []any{link.serverID, link.publicKey[:], link.resetGeneration}},
	} {
		if _, err := w.db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	defs := append(usPolicyDefinitions(), usPolicyEffectDefinitions()...)
	defs = append(defs, usPolicyReceiptDefinitions()...)
	defs = append(defs, usAuthorityDefinitions()...)
	defs = append(defs, usInvitationSourceDefinitions()...)
	defs = append(defs, usInvitationEffectDefinitions()...)
	defs = append(defs, usInvitationReceiptDefinitions()...)
	defs = append(defs, usInvitationLinkDefinitions()...)
	return w, nativeInventoryFixtureRegistry(t, w, defs, "us_invitation_composition_test.go"), input, proof
}

func usInvitationComposedTurn(t *testing.T, w *executor, r *stmt.Registry, input *usLibraryPolicyCommand, proof *usOwnerAuthorityProof) (*commandTurn, *usPolicyAuthorization) {
	t.Helper()
	turn, synthetic, _ := usPolicyReceiptTurn(t, w, r, input)
	a, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, context.Background(), usPolicyAuthorityBudget{memory: 4 << 20, receipts: 1 << 20, fixture: synthetic.nativeFixture})
	if err != nil {
		t.Fatal(err)
	}
	return turn, a
}

func usInvitationComposedIssuance(t *testing.T, input *usLibraryPolicyCommand) (*usInvitationIssuanceCommand, usPolicyReceiptRequest) {
	t.Helper()
	i := input.identity
	scope := usInvitationScope{ownerID: input.request.ownerID, operationID: "invitation-create-operation", issuer: input.request.issuer}
	code := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	sum := sha256.Sum256([]byte(code))
	p, err := prepareUSInvitationCreate(i, scope, "created-invite", hex.EncodeToString(sum[:]), usInvitationCreateInput{libraries: []string{"library"}})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := prepareUSInvitationCode(p, "fixture-key", make([]byte, 32), make([]byte, 12), code)
	if err != nil {
		t.Fatal(err)
	}
	link, _ := usInvitationLinkFixture(t)
	c, err := freezeUSInvitationIssuance(p, protected, link)
	if err != nil {
		t.Fatal(err)
	}
	return c, usPolicyReceiptRequest{ownerID: scope.ownerID, kind: usInvitationCreate, reference: ReceiptReference{Identity: i, RequestDigest: c.digest}}
}

func TestUSInvitationCompositionCreationAndImmutableReplay(t *testing.T) {
	w, r, input, proof := usInvitationComposedFixture(t, usInvitationCreate)
	c, q := usInvitationComposedIssuance(t, input)
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	fresh, err := composeUSInvitationAfterAuthority(t.Context(), turn, a, q, nil, c)
	if err != nil || fresh.decision != receiptFresh || fresh.result.serverID != usLinkFixtureServer || fresh.result.issuerServer != "server" || fresh.result.revision != 1 || fresh.result.count != 1 || fresh.chargedBytes <= 0 {
		t.Fatal(fresh, err)
	}
	usPolicyFixtureCommit(t, turn)
	// Mutable source/link/library facts cannot change a saved issuance image.
	for _, sqlText := range []string{`DELETE FROM access_invitations WHERE id='created-invite'`, `DELETE FROM libraries WHERE id='library'`, `UPDATE networking_claim_identity SET reset_generation=reset_generation+1`} {
		if _, err := w.db.ExecContext(t.Context(), sqlText); err != nil {
			t.Fatal(err)
		}
	}
	turn, a = usInvitationComposedTurn(t, w, r, input, proof)
	for _, id := range []stmt.ID{usInvitationSourceFacts, usInvitationSourceOctets, usInvitationLiveCapacity, usInvitationLibraryPresent, usInvitationLinkKind, usInvitationLinkKey, usInvitationCreateWrite, usInvitationReceiptInsert, usInvitationReceiptResultInsert, usPolicyOwnerAdvance} {
		delete(turn.steps, id)
	}
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	replay, err := composeUSInvitationAfterAuthority(t.Context(), turn, a, q, nil, nil)
	if err != nil || replay.decision != receiptReplay || replay.digest != fresh.digest || !reflect.DeepEqual(replay.result, fresh.result) || replay.chargedBytes != 0 || replay.changedInvitation != "" {
		t.Fatal(replay, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("replay wrote", before, after, err)
	}
}

func TestUSInvitationCompositionEditAndRevoke(t *testing.T) {
	for _, kind := range []CommandID{usInvitationEdit, usInvitationRevoke} {
		t.Run(map[CommandID]string{usInvitationEdit: "edit", usInvitationRevoke: "revoke"}[kind], func(t *testing.T) {
			w, r, input, proof := usInvitationComposedFixture(t, kind)
			now := w.now().UnixMilli()
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO access_invitations(id,code_hash,role,allowed_libraries,created_ms,expires_ms,created_by,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,bound_username) VALUES('existing',?,'member','["library"]',?,?,'issuer','limit',3,0,0,'R','recipient')`, usInvitationFixtureHash, now-1000, now+2*usInvitationHourMS); err != nil {
				t.Fatal(err)
			}
			scope := usInvitationScope{ownerID: input.request.ownerID, operationID: "invitation-change-operation", issuer: input.request.issuer}
			var p *usInvitationPrepared
			var err error
			if kind == usInvitationEdit {
				p, err = prepareUSInvitationEdit(input.identity, scope, "existing", usInvitationEditInput{expectedRevision: 1, role: "admin", libraries: []string{"library"}, expiresAt: time.UnixMilli(now + 3*usInvitationHourMS).UTC().Format(time.RFC3339Nano)}, now)
			} else {
				p, err = prepareUSInvitationRevoke(input.identity, scope, "existing", 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			c, err := freezeUSInvitation(p)
			if err != nil {
				t.Fatal(err)
			}
			q := usPolicyReceiptRequest{ownerID: scope.ownerID, kind: kind, reference: ReceiptReference{Identity: input.identity, RequestDigest: c.digest}}
			turn, a := usInvitationComposedTurn(t, w, r, input, proof)
			out, err := composeUSInvitationAfterAuthority(t.Context(), turn, a, q, c, nil)
			if err != nil || out.decision != receiptFresh || out.result.revision != 2 || out.result.serverID != "" || out.result.code.body != nil || out.result.access.mode != "limit" || out.result.access.streams != 3 || out.result.boundUsername != "recipient" {
				t.Fatal(out, err)
			}
			if kind == usInvitationEdit && out.result.role != "admin" || kind == usInvitationRevoke && out.result.state != "revoked" {
				t.Fatal(out)
			}
			usPolicyFixtureCommit(t, turn)
			turn, a = usInvitationComposedTurn(t, w, r, input, proof)
			replay, err := composeUSInvitationAfterAuthority(t.Context(), turn, a, q, nil, nil)
			if err != nil || replay.digest != out.digest || !reflect.DeepEqual(replay.result, out.result) {
				t.Fatal(replay, err)
			}
		})
	}
}

func TestUSInvitationCompositionFailedReceiptRollsBackPrefix(t *testing.T) {
	w, r, input, proof := usInvitationComposedFixture(t, usInvitationCreate)
	c, q := usInvitationComposedIssuance(t, input)
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	delete(turn.steps, usInvitationReceiptResultInsert)
	out, err := composeUSInvitationAfterAuthority(t.Context(), turn, a, q, nil, c)
	if err == nil || !reflect.DeepEqual(out, usInvitationComposition{}) {
		t.Fatal("partial issuance escaped", out, err)
	}
	if _, err := turn.transaction.rollback(err); err == nil {
		t.Fatal("lost original error")
	}
	for _, query := range []string{`SELECT count(*) FROM access_invitations WHERE id='created-invite'`, `SELECT count(*) FROM security_command_receipts`, `SELECT last_sequence FROM security_command_owners`} {
		var n int64
		if err := w.db.QueryRowContext(t.Context(), query).Scan(&n); err != nil || n != 0 {
			t.Fatal("failed prefix survived", query, n, err)
		}
	}
}

func TestUSInvitationCompositionProjectionAndIdentityConflict(t *testing.T) {
	for _, projection := range []bool{false, true} {
		w, r, input, proof := usInvitationComposedFixture(t, usInvitationCreate)
		c, q := usInvitationComposedIssuance(t, input)
		if projection {
			c.request.invitation.librariesJSON = `[]`
			var err error
			c.digest, err = canonicalRequestDigest(q.kind, 1, q.reference.Identity, c.request)
			if err != nil {
				t.Fatal(err)
			}
			q.reference.RequestDigest = c.digest
		} else {
			q.reference.Identity.OperationID[0]++
		}
		turn, a := usInvitationComposedTurn(t, w, r, input, proof)
		delete(turn.steps, usInvitationCreateWrite)
		out, err := composeUSInvitationAfterAuthority(t.Context(), turn, a, q, nil, c)
		if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usInvitationComposition{}) {
			t.Fatal(out, err)
		}
	}
}
