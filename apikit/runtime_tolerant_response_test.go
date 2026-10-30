package apikit

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// tolerantShelf models a 1.0 response: closed nested objects, a required and
// an optional enum, an array of enum-bearing items and an enum list.
type tolerantShelf struct{}

func (tolerantShelf) JSONSchema() map[string]any {
	format := map[string]any{"type": "string", "enum": []any{"epub", "pdf"}}
	item := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "format"}, "properties": map[string]any{
		"id": map[string]any{"type": "string", "minLength": 1}, "format": format,
	}}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "mode", "items"}, "properties": map[string]any{
		"id":       map[string]any{"type": "string", "minLength": 1},
		"mode":     map[string]any{"type": "string", "enum": []any{"direct", "stream"}},
		"quality":  map[string]any{"type": "string", "enum": []any{"original", "automatic"}},
		"pinned":   map[string]any{"type": "boolean"},
		"items":    map[string]any{"type": "array", "items": item},
		"kinds":    map[string]any{"type": "array", "items": format},
		"required": map[string]any{"type": "array", "minItems": 1, "items": item},
	}}
}

// R-09 (O-159): decodeOpen reads a "1.1" response that adds enum values. Only
// an unknown string enum value is tolerated; everything else stays exact.
func TestGeneratedRuntimeReadsNewerResponsesInTwoTiers(t *testing.T) {
	artifacts, err := Generate([]Definition{{Metadata: Metadata{Audience: Clients, ID: "shelf", Method: "POST", Path: "/v1/shelf", Summary: "Tolerant response fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[tolerantShelf](), Response: reflect.TypeFor[tolerantShelf]()}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, raw := range map[string][]byte{"schemas.ts": artifacts.Schemas, "runtime.ts": artifacts.Runtime, "package.json": []byte(`{"type":"module"}`)} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `import assert from 'node:assert/strict';import {decode,decodeOpen} from './runtime.ts';
const open=input=>decodeOpen('ShelfResponse',input), exact=input=>decode('ShelfResponse',input);
const base={id:'s',mode:'direct',items:[{id:'a',format:'epub'}],required:[{id:'r',format:'pdf'}]};
assert.deepEqual(open(base).items.map(i=>i.id),['a']);
// A newer server's vocabulary: a new optional enum value, an item and an enum-list value of a new format.
const newer={...base,quality:'hi-res',items:[{id:'a',format:'epub'},{id:'b',format:'djvu'},{id:'c',format:'pdf'}],kinds:['epub','djvu']};
const got=open(newer);
assert.deepEqual(got.items.map(i=>i.id),['a','c'],'an item with an unknown enum value is left out, the rest kept');
assert.deepEqual(got.kinds,['epub'],'an unknown enum value in a list is left out');
assert.equal(got.quality,undefined,'an unknown optional enum value is left out');
// Exact everywhere else: the default decoder, required members, malformed values,
// closed objects and minItems.
assert.throws(()=>exact(newer),'the default decoder stays exact');
assert.throws(()=>open({...base,mode:'teleport'}),'an unknown required enum value is refused');
assert.throws(()=>open({...base,id:''}));
assert.throws(()=>open({...base,pinned:'yes'}),'a malformed optional primitive is refused');
assert.throws(()=>open({...base,items:[{id:'',format:'epub'}]}),'a malformed item is refused, not dropped');
assert.equal('added' in open({...base,added:true}),false,'a closed response object ignores an added field (R-09b)');
assert.throws(()=>decode('ShelfRequest',{...base,added:true}),'requests stay closed');
assert.throws(()=>open({...base,required:[{id:'r',format:'djvu'}]}),'dropping below minItems is refused');`
	if err := os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := exec.Command("node", "--experimental-strip-types", filepath.Join(dir, "check.mjs")).CombinedOutput(); err != nil {
		t.Fatalf("two-tier response reading: %v %s", err, raw)
	}
}
