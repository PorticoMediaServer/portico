package catalog

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// rbFilterMembershipOracleVector mirrors the frozen vector in
// ../store/read/testdata/rb-filter-membership-legacy-oracle.json. The read
// package keeps the compiled SQL as a frozen fixture so its native tests do not
// acquire the legacy catalog initialization graph; this test is the other half
// of that trade: it recompiles every frozen query through the real public
// compiler so the fixture cannot drift from CompileBrowseSelection.
type rbFilterMembershipOracleVector struct {
	Query string
	Where string
	Args  []any
}

func TestRBFilterMembershipLegacyOracleVectorsMatchCompiler(t *testing.T) {
	data, err := os.ReadFile("../store/read/testdata/rb-filter-membership-legacy-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Source  string
		Vectors []rbFilterMembershipOracleVector
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Source == "" || len(fixture.Vectors) != 14 {
		t.Fatalf("incomplete legacy browse oracle: %q %d", fixture.Source, len(fixture.Vectors))
	}
	for _, vector := range fixture.Vectors {
		node, err := ParseBrowseQuery(json.RawMessage(vector.Query), "query")
		if err != nil {
			t.Fatalf("frozen query no longer parses %s: %v", vector.Query, err)
		}
		where, args, err := CompileBrowseSelection(node)
		if err != nil {
			t.Fatalf("frozen query no longer compiles %s: %v", vector.Query, err)
		}
		if where != vector.Where || !reflect.DeepEqual(args, vector.Args) {
			t.Fatalf("frozen legacy vector drifted %s:\n got %q %#v\nwant %q %#v", vector.Query, where, args, vector.Where, vector.Args)
		}
	}
}
