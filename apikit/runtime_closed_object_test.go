package apikit

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

type closedTransportMessage struct{}

func (closedTransportMessage) JSONSchema() map[string]any {
	closed := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	text := map[string]any{"type": "string", "minLength": 1}
	return map[string]any{"oneOf": []any{
		closed(map[string]any{"variant": map[string]any{"type": "string", "const": "code"}, "code": text, "state": text}, "variant", "code", "state"),
		closed(map[string]any{"variant": map[string]any{"type": "string", "const": "owner"}, "transactionId": text, "ownerStart": closed(map[string]any{"payload": text, "signature": text}, "payload", "signature")}, "variant", "transactionId", "ownerStart"),
	}}
}

type startPreparationTransportMessage struct{}

func (startPreparationTransportMessage) JSONSchema() map[string]any {
	return map[string]any{"anyOf": []any{
		map[string]any{
			"type": "object", "required": []string{"variant", "id", "revision", "presentation"},
			"properties": map[string]any{
				"variant":  map[string]any{"type": "string", "const": "session"},
				"id":       map[string]any{"type": "string", "minLength": 1},
				"revision": map[string]any{"type": "string", "minLength": 1},
				"presentation": map[string]any{"type": "object", "required": []string{"generation"}, "properties": map[string]any{
					"generation": map[string]any{"type": "integer", "minimum": 1},
				}},
			},
		},
		map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"variant", "retryAfterMs"},
			"properties": map[string]any{"variant": map[string]any{"type": "string", "const": "wait"}, "retryAfterMs": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}},
		},
	}}
}

func TestGeneratedStartWaitIsChosenByItsTag(t *testing.T) {
	artifacts, err := Generate([]Definition{{Metadata: Metadata{Audience: Clients, ID: "start_wait", Method: "POST", Path: "/v1/start-wait", Summary: "Preparation union fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 202}, Request: reflect.TypeFor[struct{}](), Response: reflect.TypeFor[startPreparationTransportMessage]()}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, raw := range map[string][]byte{"schemas.ts": artifacts.Schemas, "runtime.ts": artifacts.Runtime, "package.json": []byte(`{"type":"module"}`)} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `import assert from 'node:assert/strict';import {decode} from './runtime.ts';
const read=input=>decode('StartWaitResponse',input);
for(const retryAfterMs of [1,500,10000])assert.equal(read({variant:'wait',retryAfterMs}).retryAfterMs,retryAfterMs);
const session={variant:'session',id:'real-session',revision:'1',presentation:{generation:1}};
assert.equal(read(session).id,session.id);
// R-09b: the server's tag decides the variant; an added field never moves a reply between them.
assert.equal(read({...session,retryAfterMs:500,future:{nested:true}}).id,'real-session');
assert.equal('future' in read({...session,future:1}),false,'unknown fields are not passed through');
assert.deepEqual({...read({variant:'resumed',id:'x'})},{variant:'resumed',unknownVariant:true});
for(const bad of [
 {retryAfterMs:500},{id:'real-session',revision:'1',presentation:{generation:1}},{variant:1,retryAfterMs:500},
 {variant:'session',id:''},{variant:'session',id:'incomplete-session'},{variant:'session',id:'s',revision:'1',presentation:{}},
 {variant:'wait',retryAfterMs:0},{variant:'wait',retryAfterMs:-1},{variant:'wait',retryAfterMs:10001},{variant:'wait',retryAfterMs:1.5},
 {variant:'wait',retryAfterMs:NaN},{variant:'wait',retryAfterMs:Infinity},{variant:'wait',retryAfterMs:'500'},{variant:'wait',retryAfterMs:null},{variant:'wait'},null,[]
])assert.throws(()=>read(bad));`
	if err := os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := exec.Command("node", "--experimental-strip-types", filepath.Join(dir, "check.mjs")).CombinedOutput(); err != nil {
		t.Fatalf("generated preparation union: %v %s", err, raw)
	}
}

func TestGeneratedRuntimeReadsClosedResponsesOpenAndRequestsExact(t *testing.T) {
	artifacts, err := Generate([]Definition{{Metadata: Metadata{Audience: Clients, ID: "closed", Method: "GET", Path: "/v1/closed", Summary: "Closed transport fixture", Access: Public, Requires: ScopeOpen, Lane: Default, Cost: Constant, Status: 200}, Request: reflect.TypeFor[closedTransportMessage](), Response: reflect.TypeFor[closedTransportMessage]()}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, raw := range map[string][]byte{"schemas.ts": artifacts.Schemas, "runtime.ts": artifacts.Runtime, "package.json": []byte(`{"type":"module"}`)} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `import assert from 'node:assert/strict';import {decode} from './runtime.ts';
const normal={variant:'code',code:'code',state:'state'},owner={variant:'owner',transactionId:'transaction',ownerStart:{payload:'proof',signature:'signature'}};
assert.equal(decode('ClosedResponse',normal).code,'code');assert.equal(decode('ClosedResponse',owner).ownerStart.payload,'proof');
// R-09b: responses are open at every depth; requests stay exactly closed.
assert.equal('extra' in decode('ClosedResponse',{...normal,extra:1}),false);
assert.equal('unknown' in decode('ClosedResponse',{...owner,ownerStart:{...owner.ownerStart,unknown:1}}).ownerStart,false);
assert.equal('code' in decode('ClosedResponse',{...owner,code:'code'}),false,'the owner tag keeps the owner variant');
for(const bad of [{...owner,ownerStart:null},{variant:'owner',transactionId:'transaction'},{variant:'code',state:'state'},{code:'code',state:'state'}])assert.throws(()=>decode('ClosedResponse',bad));
for(const bad of [{...normal,extra:1},{...owner,ownerStart:{...owner.ownerStart,unknown:1}},{...owner,code:'code'}])assert.throws(()=>decode('ClosedRequest',bad));
assert.equal(decode('ClosedRequest',normal).code,'code');`
	if err := os.WriteFile(filepath.Join(dir, "check.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := exec.Command("node", "--experimental-strip-types", filepath.Join(dir, "check.mjs")).CombinedOutput(); err != nil {
		t.Fatalf("generated closed-object decoder: %v %s", err, raw)
	}
}
