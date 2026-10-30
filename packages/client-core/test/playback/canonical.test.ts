import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { canonicalJSON, canonicalUTF8 } from '../../src/playback/canonical.ts';

// Unmodified PR-01 draft3 corpus; independent public schema author's expected bytes/digests.
const bytes=readFileSync(new URL('./pr01-canonical-fixtures.json',import.meta.url));
assert.equal(createHash('sha256').update(bytes).digest('hex'),'99730529de8a572c2f36606ed4c8cbfe1eef7343d3ac90538dca82860571dea1');
const corpus=JSON.parse(bytes.toString('utf8'));
for (const fixture of corpus.fixtures) test('public canonical bytes: '+fixture.name,()=>{
  assert.equal(canonicalJSON(fixture.semanticEnvelope),fixture.canonicalUTF8);
  assert.equal(createHash('sha256').update(canonicalUTF8(fixture.semanticEnvelope)).digest('hex'),fixture.sha256);
  assert.equal(canonicalJSON(JSON.parse(fixture.rawInput)),fixture.canonicalUTF8);
});
test('encoding rejects non-JSON runtime values rather than silently coercing',()=>{
  for(const value of [undefined,NaN,Infinity,1.5,2147483648,1n,new Date(),{a:undefined},'\ud800','\udc00',Array(1)])
    assert.throws(()=>canonicalJSON(value));
  const cyclic:unknown[]=[];cyclic.push(cyclic);assert.throws(()=>canonicalJSON(cyclic));
  assert.throws(()=>canonicalJSON(Object.defineProperty({},'a',{enumerable:true,get(){throw new Error('accessor executed');}})),/accessors/);
});
test('body byte bound uses UTF8 length, not JS character count',()=>{
  assert.equal(canonicalUTF8('a'.repeat(65534)).length,65536);
  assert.throws(()=>canonicalUTF8('a'.repeat(65535)),/body limit/);
  assert.throws(()=>canonicalUTF8('😀'.repeat(16384)),/body limit/);
});
// Raw parser/schema rejection cases in the corpus belong to the validation boundary, not this encoder.
