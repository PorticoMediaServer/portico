package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// Two different Go types that would get the same name in the contract stop the generator:
// written out, one would silently take the other's place and an app would be built against
// the wrong shape. The two filter conditions, which share a Go name, have names of their own.
func TestTwoShapesWithOneNameStopTheGenerator(t *testing.T) {
	type Folder struct{ Path string }
	type first struct{ Inner Folder }
	other := func() reflect.Type {
		type Folder struct{ Name string }
		return reflect.TypeFor[Folder]()
	}()
	g := &generator{shapes: map[string]reflect.Type{}}
	g.collect(reflect.TypeFor[first]())
	g.collect(other)
	if g.err == nil || !strings.Contains(g.err.Error(), "both called Folder") {
		t.Fatalf("two shapes called Folder: %v", g.err)
	}

	g = &generator{shapes: map[string]reflect.Type{}}
	g.collect(reflect.TypeFor[db.Condition]())
	g.collect(reflect.TypeFor[playqueue.Condition]())
	if g.err != nil || g.shapes["QueryCondition"] == nil || g.shapes["QueueCondition"] == nil {
		t.Fatalf("the two conditions: %v, shapes %v", g.err, g.names())
	}
}

// A body or a field that is any JSON value, such as a settings document, is written as
// unknown: the app passes it through as it is.
func TestAnyJSONValueIsWrittenAsUnknown(t *testing.T) {
	type Document struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	}
	g := &generator{shapes: map[string]reflect.Type{}}
	body, reply := g.use(reflect.TypeFor[json.RawMessage]()), g.use(reflect.TypeFor[Document]())
	if g.err != nil {
		t.Fatal(g.err)
	}
	out := string(g.typeScript([]route{{pattern: "PUT /v1/trial", access: "owner", request: body, response: reply}}))
	for _, want := range []string{"export type JSON = unknown;", "  value: unknown;", "request: JSON; response: Document}"} {
		if !strings.Contains(out, want) {
			t.Errorf("the TypeScript lacks %q:\n%s", want, out)
		}
	}
}
