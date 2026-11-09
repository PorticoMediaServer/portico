package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSearchEntityRearmCaptureProfiles(t *testing.T) {
	w, _ := searchHeadNativeSeed(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := searchEntityRearmDefinitions()
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
	h := sha256.New()
	for _, name := range []string{"search_entity_rearm.go", "search_entity_rearm_statements.go", "search_entity_rearm_test.go", "search_entity_rearm_plans_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write(data)
	}
	for _, profile := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		t.Run(string(profile), func(t *testing.T) {
			if profile == plan.Analyzed {
				if _, err := conn.ExecContext(t.Context(), "ANALYZE"); err != nil {
					t.Fatal(err)
				}
			}
			g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: profile, Implementations: "existing-entity source rearm; native points and exact CAS; no dispatcher/physical cost qualification"}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stmt.New(defs, g.Receipt()); err != nil {
				t.Fatal(err)
			}
			if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				b, err := json.Marshal(g)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "search-entity-rearm-"+string(profile)+".json"), b, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
