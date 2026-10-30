package catalog

import (
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestHomeDocumentBoundsKeepRequiredRowsAndCatalogue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	libraries := []string{}
	order := []string{}
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("lib-%02d", i)
		libraries = append(libraries, id)
		library := c.Library(id, id, "movie", "/"+id)
		c.Movie(library, "/"+id+"/"+id+".mp4", id, 2026)
	}
	c.Drain()
	s := New(c.DB)
	req := homeRequestFixture(libraries...)
	req.RowOrder = order
	doc, err := s.HomeRows(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) != 2 || homeRowByID(doc.Rows, "continue") == nil {
		t.Fatalf("rows %d required missing", len(doc.Rows))
	}
	catalogue, err := s.HomeRowCatalogue(req)
	if err != nil || homeRowByID(catalogue, "new_on_server") == nil {
		t.Fatalf("catalogue lost overflow %v", err)
	}
	spec, schema, err := apispec.Schema("HomeDocument")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	if issues := spec.ValidateJSON(schema, raw); len(issues) > 0 {
		t.Fatal(issues)
	}
	for len(doc.Rows) <= 64 {
		doc.Rows = append(doc.Rows, doc.Rows[0])
	}
	raw, _ = json.Marshal(doc)
	if len(spec.ValidateJSON(schema, raw)) == 0 {
		t.Fatal("schema accepted 65 rows")
	}
	if err := s.ValidateHomeLayout(req, order, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateHomeLayout(req, make([]string, 65), nil); err == nil {
		t.Fatal("accepted 65 layout rows")
	}
}
