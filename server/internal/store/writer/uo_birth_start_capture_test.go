package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Capture the changed composed registry UNRECORDED. INT owns environment and
// golden integration; this writes no baseline and claims no command admission.
func TestUOBirthStartComposedCapture(t *testing.T) {
	w, p, q, v := uoBirthStartFixture(t)
	defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
	born := uoBirthStartDefinitions()
	born[0].Cohorts = []stmt.Cohort{{Name: "exact_preparation_birth", Bindings: []any{v.preparation, v.device, v.startKey, v.startBody, v.item, v.asset, v.part, v.replaces, v.replacesGeneration, v.tokenExpiresMS, v.createdMS, v.deadlineMS, v.worker[:], q.root, p.witness.issuer.memberIncarnation, p.witness.issuer.profileLifetime}}}
	defs = append(defs, born...)
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("uo_birth_start_test.go")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "UO-23 composed canonical-F current-session/root/birth component; unrecorded, no command or resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
}
