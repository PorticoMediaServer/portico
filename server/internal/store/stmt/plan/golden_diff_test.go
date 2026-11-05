package plan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanGoldenDiagnosticsIdentifyRowsAndEnvironment(t *testing.T) {
	conn, registry, f, _ := fixture(t)
	baseline, err := plan.Capture(context.Background(), conn, registry, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := baseline.JSON()
	if err != nil {
		t.Fatal(err)
	}
	clone := func() plan.Golden {
		var result plan.Golden
		decoder := json.NewDecoder(bytes.NewReader(expected))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for name, control := range map[string]struct {
		change func(*plan.Golden)
		want   []string
	}{
		"opcode":         {func(g *plan.Golden) { g.Plans[0].Explain.Values[0][1] = "MutatedOpcode" }, []string{"statement sort.next / cohort next-key", "$.plans[0].explain.values[0][1]", `expected: "Init"`, `actual: "MutatedOpcode"`, "- explain row 0:", "+ explain row 0:"}},
		"eqp":            {func(g *plan.Golden) { g.Plans[0].QueryPlan.Values[0][3] = "SEARCH changed-index" }, []string{"statement sort.next / cohort next-key", "$.plans[0].queryPlan.values[0][3]", "SEARCH items USING COVERING INDEX item_sort", "SEARCH changed-index", "- queryPlan row 0:", "+ queryPlan row 0:"}},
		"environment":    {func(g *plan.Golden) { g.Environment.Connection.TempStore = 2 }, []string{"$.environment.connection.TempStore", "expected: 1", "actual: 2"}},
		"manifest":       {func(g *plan.Golden) { g.Manifest[0].SQL += " " }, []string{"$.manifest[0].sql", "statement sort.next / cohorts empty-library,next-key", "expected:", "actual:"}},
		"missing-cohort": {func(g *plan.Golden) { g.Plans = g.Plans[:len(g.Plans)-1] }, []string{"$.plans[2]", "statement sort.tail / cohort ties", "actual: <missing>"}},
	} {
		t.Run(name, func(t *testing.T) {
			changed := clone()
			control.change(&changed)
			err := changed.Compare(expected)
			if err == nil {
				t.Fatal("changed golden accepted")
			}
			for _, want := range control.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("diagnostic lacks %q:\n%s", want, err)
				}
			}
			// Repeating a comparison must preserve both the original golden and the
			// actual capture, with a deterministic first-difference diagnostic.
			again := changed.Compare(expected)
			if again == nil || again.Error() != err.Error() {
				t.Fatalf("unstable diagnostic: %v / %v", err, again)
			}
			if err := baseline.Compare(expected); err != nil {
				t.Fatalf("comparison changed reviewed inputs: %v", err)
			}
		})
	}
}

func TestPlanFKGoldenDiagnosticAndExactJSONValues(t *testing.T) {
	expectedAudit := plan.ForeignKeyAudit{Plans: []plan.ForeignKeyPlan{{Child: "api_credentials", ID: 0, Parent: "api_credentials", Delete: plan.Rows{Columns: []string{"id", "parent", "unused", "detail"}, Values: [][]any{{int64(1), int64(0), int64(0), "SEARCH child USING INDEX api_credentials_parent"}}}}}}
	expected, err := json.MarshalIndent(expectedAudit, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	expectedAudit.Plans[0].Delete.Values[0][3] = "SCAN child"
	actual, err := json.MarshalIndent(expectedAudit, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	err = plan.CompareExactJSON(expected, actual)
	if err == nil {
		t.Fatal("changed FK plan accepted")
	}
	for _, want := range []string{"FK api_credentials/0 -> api_credentials", "$.plans[0].delete.values[0][3]", "SEARCH child USING INDEX api_credentials_parent", "SCAN child", "- delete row 0:", "+ delete row 0:"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("FK diagnostic lacks %q:\n%s", want, err)
		}
	}
	if err := plan.CompareJSON([]byte(`{"n":9007199254740992}`), []byte(`{"n":9007199254740993}`)); err == nil || !strings.Contains(err.Error(), "9007199254740992") || !strings.Contains(err.Error(), "9007199254740993") {
		t.Fatalf("numeric precision lost: %v", err)
	}
	if err := plan.CompareJSON([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`)); err == nil || !strings.Contains(err.Error(), "JSON encoding") {
		t.Fatalf("exact compacted-JSON gate weakened: %v", err)
	}
}

func TestPlanGoldenDiagnosticShowsLongValueDifference(t *testing.T) {
	old := map[string]string{"sql": strings.Repeat("x", 1000) + "OLD" + strings.Repeat("x", 1000)}
	current := map[string]string{"sql": strings.Repeat("x", 1000) + "NEW" + strings.Repeat("x", 1000)}
	expected, _ := json.Marshal(old)
	actual, _ := json.Marshal(current)
	err := plan.CompareJSON(expected, actual)
	if err == nil || !strings.Contains(err.Error(), "offset=1000") || !strings.Contains(err.Error(), "OLD") || !strings.Contains(err.Error(), "NEW") || len(err.Error()) > 1000 {
		t.Fatalf("long difference is not useful/compact: %v", err)
	}
}

func TestPlanFKGoldenRetainsExactDecompressedByteGate(t *testing.T) {
	if err := plan.CompareExactJSON(nil, nil); err == nil {
		t.Fatal("missing FK golden accepted")
	}
	expected := []byte("{\"plans\":[]}\n")
	actual := []byte("{\"plans\":[]}")
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatalf("ordinary compacted gate changed: %v", err)
	}
	if err := plan.CompareExactJSON(expected, actual); err == nil || !strings.Contains(err.Error(), "JSON whitespace") || !strings.Contains(err.Error(), "expected:") || !strings.Contains(err.Error(), "actual:") {
		t.Fatalf("FK exact-byte gate weakened: %v", err)
	}
}
