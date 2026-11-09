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
// It is a subtest of TestUOBirthJob so the lane's one source-bound prefix proves
// the composed plan together with the native behavior.
func uoBirthJobComposedCapture(t *testing.T) {
	w, p, q, src, v := uoBirthJobFixture(t)
	defs := append(usCurrentSessionDefinitions(), uoRootDefinitions()...)
	born := uoBirthJobDefinitions()
	born[0].Cohorts = []stmt.Cohort{{Name: "exact_prepared_job_birth", Bindings: []any{v.id, src.item, src.public[:], src.kind, src.library, src.key, src.sourceRevision, v.sourceID, v.profileID, v.targetID, v.state, v.phase, v.generation, v.bytes, v.errorCode, v.revision, v.createdMS, v.updatedMS, v.versionID, v.worker[:], q.root, p.witness.issuer.memberIncarnation, p.witness.issuer.profileLifetime}}}
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
	data, err := os.ReadFile("uo_birth_job_test.go")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "UO-25 composed canonical-F current-session/root/prepared-job component; unrecorded, no command or resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
}
