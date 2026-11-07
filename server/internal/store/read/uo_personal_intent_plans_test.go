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
	var definitions []stmt.Definition
	for _, d := range uoPersonalReadDefinitions() {
		// Closed octet shapes are draft native-tested pending MG's per-domain
		// preflight registry split. Do not loosen the shared plan lint to admit them.
		if d.ID == readStatementID("uo.personal.history.octets") || d.ID == readStatementID("uo.personal.history.anchor") {
			continue
		}
		definitions = append(definitions, d)
	}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	// Test-only draft policy extension mirrors UO-001 fixture DDL verbatim.
	var draft map[string]json.RawMessage
	if err = json.Unmarshal(contract, &draft); err != nil {
		t.Fatal(err)
	}
	var indexes []map[string]string
	if err = json.Unmarshal(draft["indexes"], &indexes); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ index, predicate string }{{"personal_current_watchlist", "watchlisted=1"}, {"personal_current_favorite", "favorite=1"}, {"personal_current_not_interested", "not_interested=1"}} {
		indexes = append(indexes, map[string]string{"index": e.index, "predicate": e.predicate, "choice": "repeat_exact_predicate"})
	}
	draft["indexes"], err = json.Marshal(indexes)
	if err != nil {
		t.Fatal(err)
	}
	contract, err = json.Marshal(draft)
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
