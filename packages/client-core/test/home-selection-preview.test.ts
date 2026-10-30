import assert from 'node:assert/strict';
import test from 'node:test';
import {parseBulkMetadataPreview} from '../src/home-selection/metadata-preview.ts';

const targets = Array.from({length: 70}, (_, index) => ({kind: 'item' as const, contentKind: 'movie', id: String(index), libraryId: 'library'}));
const field = {state: 'mixed', distinctCount: 2, values: [{value: '', count: 30}, {value: '2020', count: 40}], entries: [], locked: 23, truncated: false};
const many = {state: 'many', atLeast: 65, values: [], entries: [{value: 'Entry', count: 17}], locked: 23, truncated: true};
function fixture(value: unknown = field) {
  const schema = [{field: 'year', label: 'Year', group: 'numbers', type: 'integer', bulk: true}];
  const excluded = [{field: 'title', label: 'Title', group: 'general', type: 'text', bulk: false}];
  return {serverId: 'server', viewerFence: 'a'.repeat(64), kind: 'item', contentKind: 'movie', total: 70, schema,
    singleItemFields: [...schema, ...excluded], excludedFields: excluded, fields: {year: value},
    targets: targets.map(target => ({...target, expectedRevision: 'b'.repeat(64)}))};
}
const parse = (value: unknown) => parseBulkMetadataPreview(fixture(value), {targets}, 'server').fields.year!;

test('shared and mixed preserve exact counts, empty values, locks and explicit CAS order', () => {
  const parsed = parseBulkMetadataPreview(fixture(), {targets}, 'server');
  assert.deepEqual(parsed.excludedFields.map(field => [field.field, field.label]), [['title', 'Title']]);
  assert.equal(parsed.fields.year!.distinctCount, 2);
  assert.equal(parsed.fields.year!.locked, 23);
  assert.deepEqual(parsed.fields.year!.values, field.values);
  assert.deepEqual(parsed.targets.map(target => target.id), targets.map(target => target.id));
  assert.equal(parsed.targets[0]!.expectedRevision, 'b'.repeat(64));
  const shared = parse({state: 'shared', distinctCount: 1, value: '', values: [{value: '', count: 70}], entries: [], locked: 70, truncated: false});
  assert.equal(shared.value, ''); assert.equal(shared.locked, 70);
  assert.ok(Object.isFrozen(parsed.fields.year!.values));
});
test('many exposes only the K+1 lower bound, bounded exact entries and exact lock count', () => {
  const parsed = parse(many);
  assert.equal(parsed.state, 'many'); assert.equal(parsed.atLeast, 65);
  assert.equal(Object.hasOwn(parsed, 'distinctCount'), false); assert.equal(Object.hasOwn(parsed, 'value'), false);
  assert.deepEqual(parsed.values, []); assert.deepEqual(parsed.entries, many.entries); assert.equal(parsed.locked, 23);
  const entries = Array.from({length: 64}, (_, index) => ({value: String(index), count: 70}));
  assert.equal(parse({...many, entries}).entries.length, 64);
  assert.throws(() => parse({...many, entries: [...entries, {value: 'overflow', count: 1}]}));
});
test('lower bounds reject contradictory exact state, partial examples and invalid cardinalities', () => {
  for (const change of [{atLeast: undefined}, {atLeast: 64}, {atLeast: 66}, {atLeast: 65.5}, {atLeast: '65'}, {atLeast: Infinity},
    {distinctCount: 65}, {distinctCount: 0}, {value: ''}, {values: [{value: 'sample', count: 1}]}, {truncated: false},
    {locked: 71}, {locked: -1}, {locked: 1.5}, {entries: [{value: 'sample', count: 71}]},
    {entries: [{value: 'same', count: 1}, {value: 'same', count: 2}]}]) {
    assert.throws(() => parse({...many, ...change}), /invalid-metadata-preview/, JSON.stringify(change));
  }
  assert.throws(() => parseBulkMetadataPreview({...fixture(many), total: 64}, {targets: targets.slice(0, 64)}, 'server'));
});
test('exact mixed counts require all variants even when list entries are truncated', () => {
  assert.equal(parse({...field, truncated: true}).distinctCount, 2);
  const variants = Array.from({length: 64}, (_, index) => ({value: String(index), count: index === 0 ? 7 : 1}));
  assert.equal(parse({...field, distinctCount: 64, values: variants}).distinctCount, 64);
  for (const change of [{atLeast: 65}, {distinctCount: 65}, {distinctCount: 70}, {distinctCount: 1}, {distinctCount: undefined},
    {values: []}, {values: [{value: '2020', count: 40}], truncated: true}, {value: '2020'}]) {
    assert.throws(() => parse({...field, ...change}), /invalid-metadata-preview/);
  }
});
test('query previews preserve the original revision and cannot return a target census', () => {
  const request = {selector: {query: {libraryId: 'library', pivot: 'movies', sort: []}}, expected: {catalogRevision: 'original'}} as const;
  const raw = {...fixture(many), targets: [], selectionRevision: 'original'};
  assert.equal(parseBulkMetadataPreview(raw, request, 'server').selectionRevision, 'original');
  for (const change of [{selectionRevision: 'new'}, {targets: fixture().targets}, {serverId: 'other'}]) {
    assert.throws(() => parseBulkMetadataPreview({...raw, ...change}, request, 'server'));
  }
});
