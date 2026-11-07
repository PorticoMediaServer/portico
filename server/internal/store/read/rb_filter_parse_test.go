package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"
)

// This frozen normalized oracle was generated through the existing published
// catalog.ParseBrowseQuery, outside the native test binary. Production and
// native tests therefore do not acquire its legacy initialization graph.
func TestRBScalarParserPublishedSubsetParity(t *testing.T) {
	b, e := os.ReadFile("testdata/rb-scalar-parser-oracle.json.gz")
	if e != nil {
		t.Fatal(e)
	}
	z, e := gzip.NewReader(bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	b, e = io.ReadAll(z)
	if e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Source string
		Rows   []struct {
			Raw   string
			Valid bool
			Node  *rbFilterNode
		}
	}
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.Source != "catalog.ParseBrowseQuery closed scalar subset; normalized through the existing published parser" || len(fixture.Rows) != 1813 {
		t.Fatal("incomplete parser oracle")
	}
	for _, v := range fixture.Rows {
		got, e := parseRBScalarNode(json.RawMessage(v.Raw))
		if (e == nil) != v.Valid || e == nil && !reflect.DeepEqual(got, v.Node) {
			t.Fatalf("%s\nexpected=%+v valid=%v\nactual=%+v error=%v", v.Raw, v.Node, v.Valid, got, e)
		}
	}
}
