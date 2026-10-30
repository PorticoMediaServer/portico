package apikit

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type constrainedMessage struct{}

func (constrainedMessage) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "required": []string{"label", "enabled", "tier", "count", "items", "choice", "nullable", "raw"}, "properties": map[string]any{
			"label":     map[string]any{"type": "string", "minLength": 2, "maxLength": 6, "pattern": "^[A-Z][a-z]+$"},
			"enabled":   map[string]any{"type": "boolean", "const": true},
			"reading":   map[string]any{"type": "boolean", "enum": []bool{false}},
			"cardShape": map[string]any{"type": "string", "enum": []string{"poster", "square", "portrait"}},
			"tier":      map[string]any{"type": "string", "enum": []string{"owner", "member"}},
			"count":     map[string]any{"type": "integer", "minimum": 1, "maximum": 5},
			"rate":      map[string]any{"type": "integer", "minimum": 0, "multipleOf": 1_000_000},
			"fraction":  map[string]any{"type": "number", "multipleOf": 0.1},
			"tiny":      map[string]any{"type": "number", "multipleOf": 1e-7},
			"items":     map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": map[string]any{"type": "string"}},
			"choice":    map[string]any{"oneOf": []any{map[string]any{"type": "string", "const": "selected"}, map[string]any{"type": "null"}}},
			"nullable":  map[string]any{"type": "string", "nullable": true}, "raw": map[string]any{},
		}}
}
func TestGeneratedRuntimeEnforcesConstraintsAndPreservesNullableUnions(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Fatal("node is required for actual generated decoder verification")
	}
	artifacts, e := Generate([]Definition{{Metadata: Metadata{Audience: Clients, ID: "constraint", Method: "GET", Path: "/v1/constraint", Summary: "Constraint fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[constrainedMessage]()}})
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	for name, raw := range map[string][]byte{"schemas.ts": artifacts.Schemas, "runtime.ts": artifacts.Runtime, "package.json": []byte(`{"type":"module"}`)} {
		if e = os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	script := `import assert from 'node:assert/strict';import {decode} from './runtime.ts';
const good={reading:false,cardShape:'poster',label:'Owner',enabled:true,tier:'owner',count:1,items:['a'],choice:'selected',nullable:null,raw:{extension:[1,null]}};
assert.equal(decode('ConstraintResponse',good).nullable,null);assert.equal(decode('ConstraintResponse',{...good,choice:null}).choice,null);
// Lane 2.05c exact capability regression: reading enum[false], cardShape enum.
for(const bad of [{...good,reading:true},{...good,cardShape:'unknown-shape'},{...good,label:'A'},{...good,label:'Toolongname'},{...good,label:'owner'},{...good,enabled:false},{...good,tier:'admin'},{...good,count:0},{...good,count:6},{...good,items:[]},{...good,items:['a','b','c']},{...good,choice:'other'},{...good,label:undefined}])assert.throws(()=>decode('ConstraintResponse',bad));
assert.equal(decode('ConstraintResponse',{...good,nullable:'value',raw:[null,1]}).nullable,'value');
for(const rate of [0,1000000,1000000000])assert.equal(decode('ConstraintResponse',{...good,rate}).rate,rate);
for(const rate of [1,999999,1000001,1500000])assert.throws(()=>decode('ConstraintResponse',{...good,rate}));
for(const fraction of [0,0.3,-0.3,1000])assert.equal(decode('ConstraintResponse',{...good,fraction}).fraction,fraction);
for(const fraction of [0.15,0.30000000000000004])assert.throws(()=>decode('ConstraintResponse',{...good,fraction}));
assert.equal(decode('ConstraintResponse',{...good,tiny:3e-7}).tiny,3e-7);
assert.throws(()=>decode('ConstraintResponse',{...good,tiny:1.5e-7}));`
	if e = os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("node", "--experimental-strip-types", filepath.Join(dir, "check.mjs"))
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("generated decoder: %v %s", e, raw)
	}
}
