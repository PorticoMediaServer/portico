package writer

import (
	"errors"
	"strings"
	"testing"
)

func TestUSInvitationShortPreflightsActualReplacementBytes(t *testing.T) {
	w, _, input := usPolicyReceiptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), usInvitationSeed); err != nil {
		t.Fatal(err)
	}
	server := strings.Repeat("界", 2000)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO access_invitation_short_codes(invitation_id,lookup_hash,binding_hash,server_id,invitation_revision,sign_in_kind,kind_revision,purpose,expires_ms) VALUES('us-invite',?,?,?,1,'local',1,'accept',1000)`, strings.Repeat("a", 64), strings.Repeat("b", 64), server); err != nil {
		t.Fatal(err)
	}
	r := nativeInventoryFixtureRegistry(t, w, usInvitationShortSourceDefinitions(), "us_invitation_short_snapshot.go", "us_invitation_short_snapshot_test.go")
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	bytes, err := readUSInvitationShortReplacementBytes(t.Context(), turn, a, "us-invite")
	if err != nil || bytes != int64(9+64+64+6000+5+6) {
		t.Fatal("actual UTF-8 footprint replaced by nominal width", bytes, err)
	}
	bytes, err = readUSInvitationShortReplacementBytes(t.Context(), turn, a, "missing")
	if err != nil || bytes != 0 {
		t.Fatal("absent source", bytes, err)
	}
	a.memoryBytes = 64 << 10
	bytes, err = readUSInvitationShortReplacementBytes(t.Context(), turn, a, "us-invite")
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || bytes != 0 {
		t.Fatal("oversized replacement accepted", bytes, err)
	}
}
