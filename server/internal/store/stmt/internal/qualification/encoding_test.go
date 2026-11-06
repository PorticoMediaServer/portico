package qualification_test

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/internal/qualification"
)

func TestEncodingEveryDefinitionStringRetainsRawBytes(t *testing.T) {
	for _, set := range []func(*stmt.Definition, string){
		func(d *stmt.Definition, s string) { d.ID = stmt.ID(s) },
		func(d *stmt.Definition, s string) { d.SQL = s },
		func(d *stmt.Definition, s string) { d.Owner = s },
		func(d *stmt.Definition, s string) { d.CallSites[0] = s },
		func(d *stmt.Definition, s string) { d.Kind = stmt.Kind(s) },
		func(d *stmt.Definition, s string) { d.Params[0].Name = s },
		func(d *stmt.Definition, s string) { d.Params[0].Type = stmt.Type(s) },
		func(d *stmt.Definition, s string) { d.Cohorts[0].Name = s },
		func(d *stmt.Definition, s string) { d.Cohorts[0].Bindings[0] = s },
		func(d *stmt.Definition, s string) { d.Plan.Indexes[0] = s },
		func(d *stmt.Definition, s string) { d.Plan.PrimaryKeys[0] = s },
		func(d *stmt.Definition, s string) { d.Plan.Seeks[0] = s },
		func(d *stmt.Definition, s string) { d.Plan.AllowedScans[0] = s },
		func(d *stmt.Definition, s string) { d.Plan.Waivers[0].StatementID = stmt.ID(s) },
		func(d *stmt.Definition, s string) { d.Plan.Waivers[0].Stage = s },
		func(d *stmt.Definition, s string) { d.Plan.Waivers[0].Form = s },
		func(d *stmt.Definition, s string) { d.Plan.Waivers[0].Match = s },
		func(d *stmt.Definition, s string) { d.Plan.Waivers[0].Reason = s },
		func(d *stmt.Definition, s string) { d.Plan.AutoIndexes[0].Table = s },
		func(d *stmt.Definition, s string) { d.Plan.AutoIndexes[0].Name = s },
		func(d *stmt.Definition, s string) { d.Plan.AutoIndexes[0].Origin = s },
		func(d *stmt.Definition, s string) { d.Plan.AutoIndexes[0].Columns[0].Name = s },
		func(d *stmt.Definition, s string) { d.Plan.AutoIndexes[0].Columns[0].Collation = s },
	} {
		makeDefinition := func(s string) stmt.Definition {
			d := stmt.Definition{CallSites: []string{"site"}, Params: []stmt.Param{{}}, Cohorts: []stmt.Cohort{{Bindings: []any{"value"}}}, Plan: stmt.Plan{Indexes: []string{"i"}, PrimaryKeys: []string{"p"}, Seeks: []string{"s"}, AllowedScans: []string{"a"}, Waivers: []stmt.Waiver{{}}, AutoIndexes: []stmt.AutoIndexBinding{{Columns: []stmt.IndexColumn{{}}}}}}
			set(&d, s)
			return d
		}
		a, b := makeDefinition("\xff"), makeDefinition("\xfe")
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if !bytes.Equal(ja, jb) {
			t.Fatal("test no longer exercises JSON collapse")
		}
		ea, err := qualification.Encode(a)
		if err != nil {
			t.Fatal(err)
		}
		eb, err := qualification.Encode(b)
		if err != nil || bytes.Equal(ea, eb) {
			t.Fatal("receipt collapsed raw definition bytes", err)
		}
	}
}

func TestEncodingTypesLengthsNilAndOrder(t *testing.T) {
	for _, pair := range [][2]any{
		{[]string{"ab", "c"}, []string{"a", "bc"}},
		{[]byte(nil), []byte{}}, {nil, []byte(nil)},
		{int64(1), float64(1)}, {"x", []byte("x")},
		{float64(0), math.Copysign(0, -1)},
		{struct{ A string }{"x"}, struct{ B string }{"x"}},
	} {
		a, err := qualification.Encode(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := qualification.Encode(pair[1])
		if err != nil || bytes.Equal(a, b) {
			t.Fatal("ambiguous receipt encoding", pair, err)
		}
	}
	a, err := qualification.Encode(map[string]string{"b": "2", "a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := qualification.Encode(map[string]string{"a": "1", "b": "2"})
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("map order changed identity", err)
	}
	for _, value := range []any{struct{ hidden string }{"x"}, make(chan int), map[int]string{1: "x"}} {
		if _, err := qualification.Encode(value); err == nil {
			t.Fatal("unsupported field silently omitted", value)
		}
	}
}
