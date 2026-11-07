package read

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Native Capture on the owned closed read definitions. This mints only the
// statement receipt for this exact draft fixture, not performance qualification
// or a new public route. Runner evidence preserves the current source-bound
// full plan; no shared canonical golden/registry is edited by the helper.
func TestUOPersonalReadCapture(t *testing.T) {
	f := newUOPersonalReadSource(t)
	definitions := uoPersonalReadDefinitions()
	catalogue, err := stmt.Describe(definitions)
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
	journal := sha256.New()
	for _, path := range []string{"uo_personal_intent_test.go", "uo_personal_intent.go", "uo_personal_membership.go", "uo_personal_history.go"} {
		data, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		journal.Write(data)
	}
	db, _, err := sqliteconn.Open(t.Context(), f.path, browseReaderConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	golden, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: browseReaderConfig(), SHA256: hex.EncodeToString(journal.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "UO personal DRAFT native component, UO-001 fixture-only indexes; no authority, route or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(definitions, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	if root := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); root != "" {
		data, e := json.MarshalIndent(golden, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(root, "uo-personal-read-capture.json"), data, 0644); e != nil {
			t.Fatal(e)
		}
	}
}
