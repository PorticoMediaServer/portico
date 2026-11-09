package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// An unrecorded composed native capture on F018. INT owns selective final
// goldens and the sole #1556 baseline; this test changes neither artifact.
func TestIARetirementUnitNativePlans(t *testing.T) {
	w, registry, _, _, _ := iaRetirementUnitFixture(t, false)
	defs := iaRetirementUnitDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, p := range []string{"ia_retirement_step.go", "ia_retirement_step_statements.go", "ia_retirement_bytes.go", "ia_retirement_step_test.go", "mp_session_stop_guard.go", "mp_session_stop_guard_statements.go", "mp_external_guide_pin_reclaim.go", "mp_external_guide_pin_reclaim_statements.go"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(p))
		h.Write(b)
	}
	turn, _ := iaRetirementUnitTurn(t, w, registry)
	defer turn.transaction.rollback(nil)
	c := turn.transaction.conn
	cfg := w.config
	cfg.Synchronous = sqliteconn.Normal
	g, err := plan.Capture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: cfg, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "IA F018 canonical retirement participant; unrecorded composed native capture, no authority/HOLD/performance qualification; sole shared #1556 baseline unchanged"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != len(defs) || g.Receipt() == nil {
		t.Fatal("incomplete actual registry")
	}
	t.Logf("%d actual registered statements on full F018; unrecorded Capture, no golden/baseline change", len(g.Manifest))
}
