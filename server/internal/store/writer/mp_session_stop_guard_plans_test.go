package writer

import (
	"crypto/sha256"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"testing"
)

// Candidate-only Capture, never a refresh of the shared baseline or an old
// subset overlay. INT owns selective adoption of composed golden changes.
func TestMPSessionGuardCandidateCapture(t *testing.T) {
	w := mpSessionStopFixture(t)
	defs := mpSessionStopDefinitions()
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
	c, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("mp14-full-f-484242fb-guard-v1")))
	g, err := cachedCapture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hash, Statistics: plan.Unanalyzed, Implementations: "MP14 full canonical F parent guard; outer admission and authority required"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
}
