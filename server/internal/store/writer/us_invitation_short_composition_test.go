package writer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usShortComposedFixture(t *testing.T, replace bool) (*executor, *stmt.Registry, *usLibraryPolicyCommand, *usOwnerAuthorityProof, *usInvitationShortCommand, usPolicyReceiptRequest) {
	t.Helper()
	defs := append(usInvitationLinkDefinitions(), usInvitationShortFreshDefinitions()...)
	defs = append(defs, usInvitationShortSourceDefinitions()...)
	defs = append(defs, usInvitationShortEffectDefinitions()...)
	w, r, input, proof, c, q := usShortReceiptFixture(t, defs...)
	v := c.request.original
	for _, row := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO access_invitations(id,code_hash,role,allowed_libraries,created_ms,expires_ms,created_by) VALUES(?,?,?,'[]',?,?,'issuer')`, []any{v.invitationID, v.codeHash, v.role, v.issuedMS - 1, v.invitationExpiryMS}},
		{`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,?,?,'fixture')`, []any{v.serverID, v.publicKey, v.keyIncarnation}},
		{`INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,?,?)`, []any{v.serverID, v.publicKey, v.resetGeneration}},
	} {
		if _, err := w.db.ExecContext(t.Context(), row.sql, row.args...); err != nil {
			t.Fatal(err)
		}
	}
	if replace {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO access_invitation_short_codes(invitation_id,lookup_hash,binding_hash,server_id,invitation_revision,sign_in_kind,kind_revision,purpose,expires_ms) VALUES(?,?,?,?,1,'local',1,'accept',?)`, v.invitationID, strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("界", 2000), v.issuedMS); err != nil {
			t.Fatal(err)
		}
	}
	return w, r, input, proof, c, q
}

func TestUSInvitationShortCompleteCompositionAndReplay(t *testing.T) {
	for _, replace := range []bool{false, true} {
		w, r, input, proof, c, q := usShortComposedFixture(t, replace)
		turn, a := usInvitationComposedTurn(t, w, r, input, proof)
		fresh, err := composeUSInvitationShortAfterAuthority(t.Context(), turn, a, q, c)
		if err != nil || fresh.decision != receiptFresh || fresh.changedInvitation != "invite" || fresh.chargedBytes <= 0 || fresh.result.invitationRevision != 1 || len(fresh.result.wire.body) != 35 {
			t.Fatal(fresh, err)
		}
		want, err := usInvitationShortPreparedResult(c, w.now().UnixMilli())
		if err != nil || !reflect.DeepEqual(fresh.result, want) {
			t.Fatal("incomplete native image", fresh, err)
		}
		var revision int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT revision FROM access_invitations WHERE id='invite'`).Scan(&revision); err != nil || revision != 1 {
			t.Fatal("short issuance changed invitation revision", revision, err)
		}
		usPolicyFixtureCommit(t, turn)
		if _, err := w.db.ExecContext(t.Context(), `DELETE FROM access_invitations WHERE id='invite'; UPDATE networking_claim_identity SET reset_generation=reset_generation+1`); err != nil {
			t.Fatal(err)
		}
		turn, a = usInvitationComposedTurn(t, w, r, input, proof)
		for _, defs := range [][]stmt.Definition{usInvitationShortFreshDefinitions(), usInvitationLinkDefinitions(), usInvitationShortSourceDefinitions(), usInvitationShortEffectDefinitions()} {
			for _, d := range defs {
				delete(turn.steps, d.ID)
			}
		}
		for _, id := range []stmt.ID{usInvitationShortReceiptInsert, usInvitationShortReceiptResultInsert, usPolicyOwnerAdvance} {
			delete(turn.steps, id)
		}
		var before, after int64
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		replay, err := composeUSInvitationShortAfterAuthority(t.Context(), turn, a, q, nil)
		if err != nil || replay.decision != receiptReplay || replay.digest != fresh.digest || !reflect.DeepEqual(replay.result, fresh.result) || replay.chargedBytes != 0 || replay.changedInvitation != "" {
			t.Fatal(replay, err)
		}
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
			t.Fatal("replay performed an effect", before, after, err)
		}
	}
}

func TestUSInvitationShortFailedReceiptRollsBackWholePrefix(t *testing.T) {
	w, r, input, proof, c, q := usShortComposedFixture(t, false)
	turn, a := usInvitationComposedTurn(t, w, r, input, proof)
	delete(turn.steps, usInvitationShortReceiptResultInsert)
	out, err := composeUSInvitationShortAfterAuthority(t.Context(), turn, a, q, c)
	if err == nil || !reflect.DeepEqual(out, usInvitationShortComposition{}) {
		t.Fatal("partial result escaped", out, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(err); err == nil {
		t.Fatal("lost original prefix failure")
	}
	for _, table := range []string{"access_invitation_short_codes", "security_command_receipts", "security_invitation_short_results"} {
		var count int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed prefix left rows", table, count, err)
		}
	}
	var last, count, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT last_sequence,retained_count,retained_variable_bytes FROM security_command_owners WHERE id=?`, q.ownerID).Scan(&last, &count, &bytes); err != nil || last != 0 || count != 0 || bytes != 0 {
		t.Fatal("failed prefix advanced owner", last, count, bytes, err)
	}
}
