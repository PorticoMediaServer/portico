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
func TestMPGuidePinCandidateCapture(t *testing.T) {
	w, _, _, _ := mpGuidePinFixture(t)
	defs := mpExternalGuidePinDefinitions()
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
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("mp15-full-f-484242fb-guide-v1")))
	g, err := cachedCapture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hash, Statistics: plan.Unanalyzed, Implementations: "MP15 full canonical F point effects; outer authority and acceptance gated"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
}
