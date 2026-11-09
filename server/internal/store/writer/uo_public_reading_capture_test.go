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

// The changed composed registry is captured UNRECORDED. This deliberately
// writes no baseline; integration/environment qualification belongs to INT.
func TestUOPublicReadingComposedCapture(t *testing.T) {
	w, _, _ := uoPublicReadingFixture(t, false)
	defs := uoPublicReadingFixtureDefinitions()
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
	data, err := os.ReadFile("uo_public_reading_receipts_test.go")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	golden, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "UO public reading canonical-F component; unrecorded, no command/resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
}

func TestUOPublicReadingCurrentAndOwnerStatementsCompose(t *testing.T) {
	// Shared source points must have byte-identical definitions; the changed
	// current-session device predicate has its own ID so neither arm can erase it.
	defs := append(usAuthorityDefinitions(), usCurrentSessionDefinitions()...)
	unique := make([]stmt.Definition, 0, len(defs))
	seen := map[stmt.ID]stmt.Definition{}
	for _, d := range defs {
		if old, ok := seen[d.ID]; ok {
			if old.SQL != d.SQL {
				t.Fatalf("different SQL under shared ID %s", d.ID)
			}
			continue
		}
		seen[d.ID] = d
		unique = append(unique, d)
	}
	if _, err := stmt.Describe(unique); err != nil {
		t.Fatal(err)
	}
	if seen[usCurrentDevice].SQL == seen[usAuthorityDevice].SQL {
		t.Fatal("current device lost its API exclusion")
	}
}
