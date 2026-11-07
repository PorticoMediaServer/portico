package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"os"
	"testing"
)

var recordMGTunerPlans = flag.Bool("record-mg-tuner-plans", false, "record private tuner statement plans")

const mgTunerIndexSQL = `CREATE INDEX live_allocations_source_state_channel ON live_allocations(source_id,state,channel_id,id)`

func TestMGTunerNativeStatementCapture(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), mgTunerIndexSQL); err != nil {
		t.Fatal(err)
	}
	defs := mgTunerDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(mgTunerIndexSQL))
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Empty canonical fixture plus exact pinned G source/state/channel/id index only; component statement capture, no stream work or parent qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != len(defs) {
		t.Fatal(len(g.Plans))
	}
	if _, err = stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(g.Plans, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-tuner-plans.json"
	if *recordMGTunerPlans {
		if err = os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
