import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
import {schemas} from '../src/schemas.ts';

// A browse reply is a union: a ready page carries `applied`, a preparing reply carries
// only its poll state. Read the relative predicate from the actual generated ready
// branch, and assert the preparing branch separately so a shape change cannot quietly
// move the predicate out of the published page response.
type ResponseShape = (typeof schemas)['BrowseLibraryResponse']['anyOf'][number];
type ReadyShape = Extract<ResponseShape, {properties: {applied: unknown}}>;
type PreparingShape = Exclude<ResponseShape, ReadyShape>;
const responseShapes = schemas.BrowseLibraryResponse.anyOf;
const readyShapes = responseShapes.filter((shape): shape is ReadyShape => 'applied' in shape.properties);
const preparingShapes = responseShapes.filter((shape): shape is PreparingShape => !('applied' in shape.properties));
assert.equal(readyShapes.length, 1, 'the contract has exactly one ready page branch');
assert.equal(preparingShapes.length, 1, 'the contract has exactly one preparing branch');
assert.deepEqual(Object.keys(preparingShapes[0].properties).sort(), ['kind', 'queryId', 'retryAfterMs', 'state', 'variant'], 'the preparing branch carries only its poll state');

// Use the actual generated browse node schema in the production runtime.
const nodeSchema = readyShapes[0].properties.applied.properties.query;
async function decoder() {
 const template=readFileSync(new URL('../src/runtime.ts',import.meta.url),'utf8');
 const source=template.replace("import { schemas } from './schemas.ts';",`const schemas = ${JSON.stringify({BrowseNode:nodeSchema})};`);
 return await import('data:text/javascript;base64,'+Buffer.from(stripTypeScriptTypes(source)).toString('base64'));
}

test('published relative browse predicate advertises date fields and whole day limits', async () => {
 type NodeShape = (typeof nodeSchema)['oneOf'][number];
 type RelativeShape = Extract<NodeShape, {properties: {operator: {enum: readonly ['within-last-days']}}}>;
 const relative=nodeSchema.oneOf.find((shape): shape is RelativeShape => (shape.properties.operator.enum as readonly string[]).includes('within-last-days'));
 assert.ok(relative, 'the published node schema carries the relative operator');
 assert.deepEqual([...relative.properties.field.enum].sort(),['dateAdded','lastPlayedAt','releaseDate']);
 assert.deepEqual(relative.properties.value,{type:'integer',minimum:1,maximum:36500});
 const api=await decoder();
 for(const field of relative.properties.field.enum)for(const value of [1,30,36500]) {
  const node={variant:'relative',field,operator:'within-last-days',value};assert.deepEqual({...api.decode('BrowseNode',node)},node);
 }
 for(const value of [null,'30',true,[],{},0,-1,36501,1.5,NaN,Infinity])assert.throws(()=>api.decode('BrowseNode',{variant:'relative',field:'dateAdded',operator:'within-last-days',value}));
 assert.throws(()=>api.decode('BrowseNode',{variant:'relative',field:'dateAdded',operator:'within-last-days'}));
 assert.throws(()=>api.decode('BrowseNode',{variant:'relative',field:'year',operator:'within-last-days',value:30}));
 assert.throws(()=>api.decode('BrowseNode',{variant:'relative',field:'dateAdded',operator:'within-last-days',value:30,at:'2026-10-24T00:00:00Z'}));
 assert.throws(()=>api.decode('BrowseNode',{variant:'relative',field:'dateAdded',operator:'within-last-days',value:30,all:[]}));
 assert.doesNotThrow(()=>api.decode('BrowseNode',{variant:'predicate',field:'dateAdded',operator:'at-least',value:'2026-09-23'}));
 assert.doesNotThrow(()=>api.decode('BrowseNode',{variant:'group',all:[{variant:'predicate',field:'dateAdded',operator:'at-least',value:'2026-09-23'}]}));
});
