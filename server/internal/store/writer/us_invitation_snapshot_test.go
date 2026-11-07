package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func usInvitationSnapshotFixture(t *testing.T) (*executor, *stmt.Registry, *usLibraryPolicyCommand) {
	t.Helper()
	w, _, input := usPolicyReceiptFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO access_invitations(id,notes,code_hash,role,allowed_libraries,created_ms,expires_ms,created_by) VALUES('us-invitation',?,?,'member','["library"]',1000,2000,'issuer')`, strings.Repeat("界", 500), usInvitationFixtureHash); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO libraries(id,name,kind,root) VALUES('library','Library','movies','/fixture')`); err != nil {
		t.Fatal(err)
	}
	input.kind = usInvitationCreate
	return w, nativeInventoryFixtureRegistry(t, w, usInvitationSourceDefinitions(), "us_invitation_snapshot.go"), input
}
func TestUSInvitationSourcePreflightAndFreshOnlyChecks(t *testing.T) {
	w, r, input := usInvitationSnapshotFixture(t)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	out, err := readUSInvitationSource(t.Context(), turn, a, "us-invitation", 1)
	if err != nil || out.id != "us-invitation" || len(out.notes.String) != 1500 || !out.notes.Valid || out.count != 1 || out.libraries[0] != "library" || out.revision != 1 {
		t.Fatal("complete native invitation point", err)
	}
	if err := checkUSInvitationFreshLibraries(t.Context(), turn, a, []string{"library"}); err != nil {
		t.Fatal(err)
	}
	if err := checkUSInvitationFreshLibraries(t.Context(), turn, a, []string{"missing"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing library admitted", err)
	}
	if err := checkUSInvitationFreshCapacity(t.Context(), turn, a, 1000); err != nil {
		t.Fatal("below capacity refused", err)
	}
}
func TestUSInvitationSourceReservesBeforeTextAndZeroesFailures(t *testing.T) {
	w, r, input := usInvitationSnapshotFixture(t)
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	a.memoryBytes = 64 << 10
	delete(turn.steps, usInvitationSourceFacts)
	out, err := readUSInvitationSource(t.Context(), turn, a, "us-invitation", 1)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || out.id != "" || out.libraries != nil {
		t.Fatal("payload step reached before actual-byte reservation", err)
	}
	a.memoryBytes = 4 << 20
	out, err = readUSInvitationSource(t.Context(), turn, a, "us-invitation", 2)
	if !errors.Is(err, ErrConflict) || out.id != "" || out.libraries != nil {
		t.Fatal("stale point returned a partial result", err)
	}
}
func TestUSInvitationLiveCapacityUsesExisting200Bound(t *testing.T) {
	w, r, input := usInvitationSnapshotFixture(t)
	// Fixture setup precedes writer custody. Exactly 200 live source rows total.
	tx, err := w.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for j := 1; j < 200; j++ {
		if _, err = tx.ExecContext(t.Context(), `INSERT INTO access_invitations(id,code_hash,role,created_ms,expires_ms,created_by) VALUES(?,?,'member',1000,2000,'issuer')`, fmt.Sprintf("capacity-%03d", j), fmt.Sprintf("%064x", j)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	turn, a, _ := usPolicyReceiptTurn(t, w, r, input)
	err = checkUSInvitationFreshCapacity(t.Context(), turn, a, 1000)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity {
		t.Fatal("200 live invitations admitted", err)
	}
	// Expired pending rows remain source history but consume no live capacity.
	if err = checkUSInvitationFreshCapacity(t.Context(), turn, a, 2000); err != nil {
		t.Fatal("expired invitations consume live capacity", err)
	}
}
