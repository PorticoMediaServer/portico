import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';
import {schemas} from '../src/schemas.ts';

const item = {id: 'book', libraryId: 'library', title: 'Book', format: 'pdf', sourceId: 'source', inventoryAvailable: true};
const author = {value: 'A & B', source: 'owner'};
const series = {id: 'opaque-library-series', name: 'Dune', number: 2.5, source: 'folder'};
const read = (patch: object = {}) => decode('GetReadingItemsResponse', {items: [{...item, ...patch}]});

test('Reading list attribution is optional and preserves exact unsplit values and decimal numbering', () => {
  assert.doesNotThrow(() => read());
  for (const source of ['owner', 'file-metadata', 'file-name', 'folder']) {
    const value = read({author: {...author, source}, series: {...series, source}}) as {items: Array<{author: typeof author; series: typeof series}>};
    assert.equal(value.items[0].author.value, 'A & B');
    assert.equal(value.items[0].series.number, 2.5);
    assert.equal(value.items[0].series.source, source);
  }
  assert.doesNotThrow(() => read({series: {...series, number: 0}}));
  assert.doesNotThrow(() => read({series: {id: series.id, name: series.name, source: series.source}}));
});

test('present attribution objects require known provenance and keep private evidence out of the wire', () => {
  for (const invalid of [
    {}, {...author, value: ''}, {...author, value: 'x'.repeat(4097)},
    {...author, source: undefined}, {...author, source: null}, {...author, source: 'provider'},
    {...author, value: ['A', 'B']},
  ]) assert.throws(() => read({author: invalid}));
  for (const invalid of [
    {}, {...series, id: ''}, {...series, name: ''}, {...series, source: 'unknown'},
    {...series, number: -1}, {...series, number: '2.5'}, {...series, number: null},
    {...series, number: NaN}, {...series, number: Infinity},
  ]) assert.throws(() => read({series: invalid}));
  // Responses are open: private evidence a server adds is dropped, never surfaced and never fatal.
  const decoded = read({author: {...author, path: '/private/source'}, series: {...series, numberSource: 'file-name', sourceRevision: 'private'}}) as {items: Array<{author: object; series: object}>};
  assert.deepEqual({...decoded.items[0].author}, author);
  assert.deepEqual({...decoded.items[0].series}, series);
});

test('Continue reuses the same item facts while session package metadata keeps its existing scalar contract', () => {
  assert.doesNotThrow(() => decode('GetReadingContinueResponse', {items: [{
    item: {...item, author, series},
    progress: {sourceId: 'source', sourceRevision: 'revision', location: {format: 'pdf', pageIndex: 0, progression: 0.5}, revision: 1, observedAt: 1, stale: false},
  }]}));
  for (const shape of [schemas.GetReadingItemsResponse.properties.items.items,
    schemas.GetReadingContinueResponse.properties.items.items.properties.item]) {
    for (const name of ['author', 'series'] as const) {
      assert.equal(new Set<string>(shape.required).has(name), false);
      assert.equal(shape.properties[name].type, 'object');
      assert.equal('nullable' in shape.properties[name], false);
    }
    assert.equal('numberSource' in shape.properties.series.properties, false);
  }
  assert.equal(schemas.PostReadingSessionsResponse.properties.metadata.properties.author.type, 'string');
});
