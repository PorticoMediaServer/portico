package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Capture UNRECORDED: the new read recipe neither updates another task's golden
// nor claims installed command/governor/large-corpus qualification.
func TestUSInvitationAuthorityNativeCapture(t *testing.T) {
	w, _ := usInvitationAuthorityFixture(t, "owner")
	defs := usInvitationAuthorityDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("us_invitation_authority_test.go")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(source)
	wc008NativePrepare(t, w, defs)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	captured, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "US private invitation-manager native authority; unrecorded, no command/resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(defs, captured.Receipt()); err != nil {
		t.Fatal(err)
	}
}
func TestUSInvitationAuthorityComposesWithOwnerRecipe(t *testing.T) {
	defs := append(usAuthorityDefinitions(), usInvitationAuthorityDefinitions()...)
	seen := map[stmt.ID]stmt.Definition{}
	var unique []stmt.Definition
	for _, d := range defs {
		if previous, ok := seen[d.ID]; ok {
			if !reflect.DeepEqual(previous, d) {
				t.Fatalf("different definition under shared ID %s", d.ID)
			}
			continue
		}
		seen[d.ID] = d
		unique = append(unique, d)
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	if _, err := stmt.Describe(unique); err != nil {
		t.Fatal(err)
	}
}
