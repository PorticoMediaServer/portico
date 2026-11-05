package plan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanVMPhysicalLayoutCanonicalAndIndexIdentityExact(t *testing.T) {
	capture := func(padding bool) plan.Golden {
		conn, registry, f, _ := fixture(t)
		ctx := context.Background()
		if _, err := conn.ExecContext(ctx, "DROP TABLE items"); err != nil {
			t.Fatal(err)
		}
		if padding {
			for _, sql := range []string{"CREATE TABLE padding_a(id INTEGER PRIMARY KEY)", "CREATE TABLE padding_b(id INTEGER PRIMARY KEY)"} {
				if _, err := conn.ExecContext(ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, sql := range []string{
			"CREATE TABLE items(id INTEGER PRIMARY KEY, library_id INTEGER NOT NULL, kind INTEGER NOT NULL, key INTEGER)",
			"CREATE INDEX item_sort ON items(library_id,kind,key,id) WHERE " + predicate,
		} {
			if _, err := conn.ExecContext(ctx, sql); err != nil {
				t.Fatal(err)
			}
		}
		g, err := plan.Capture(ctx, conn, registry, f, indexPolicy(t))
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	before, after := capture(false), capture(true)
	rootPage := func(g plan.Golden) any {
		for _, row := range g.Environment.Schema.Values {
			if row[1] == "item_sort" {
				return row[3]
			}
		}
		t.Fatal("fixture index missing")
		return nil
	}
	if rootPage(before) == rootPage(after) {
		t.Fatal("fixture did not move the physical index root")
	}
	a, _ := json.Marshal(before.Plans)
	b, _ := json.Marshal(after.Plans)
	if !bytes.Equal(a, b) {
		t.Fatalf("physical root/cookie changes affect canonical VM: %v", plan.CompareJSON(a, b))
	}
	a, _ = json.Marshal(before.Environment.Schema)
	b, _ = json.Marshal(after.Environment.Schema)
	if bytes.Equal(a, b) {
		t.Fatal("physical environment evidence was lost")
	}
	changed := after
	changed.Plans = append([]plan.StatementPlan(nil), after.Plans...)
	changed.Plans[0].Explain.Values = append([][]any(nil), after.Plans[0].Explain.Values...)
	for i, row := range changed.Plans[0].Explain.Values {
		if row[1] == "OpenRead" {
			changed.Plans[0].Explain.Values[i] = append([]any(nil), row...)
			changed.Plans[0].Explain.Values[i][3] = `"main"."different_index"`
			break
		}
	}
	a, _ = after.JSON()
	if err := changed.Compare(a); err == nil {
		t.Fatal("different canonical root object inherited the baseline")
	}
}
