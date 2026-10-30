import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';
import {schemas} from '../src/schemas.ts';

test('genre tiles preserve counted genres without art and bound the shared art projection', () => {
  const genre = {value: 'Drama', label: 'Drama', count: 1, covers: []};
  const page = {queryId: 'private-query', revision: 'revision', genres: [genre]};
  assert.doesNotThrow(() => decode('GetSearchGenresResponse', page));
  const art = {kind: 'item', id: 'item', path: '/metadata/item/artwork'};
  assert.doesNotThrow(() => decode('GetSearchGenresResponse', {...page, genres: [{...genre, backdrop: art, covers: [art, {...art, kind: 'show'}]}]}));
  for (const changed of [{...genre, count: 0}, {...genre, covers: [art, art, art]}, {...genre, backdrop: {...art, kind: 'episode'}}]) {
    assert.throws(() => decode('GetSearchGenresResponse', {...page, genres: [changed]}));
  }
  const ready = schemas.GetSearchGenresResponse.anyOf.find(branch => 'genres' in branch.properties && !('kind' in branch.properties));
  assert.ok(ready && 'genres' in ready.properties);
  const backdrop = ready.properties.genres.items.properties.backdrop;
  assert.equal(backdrop.type, 'object');
  assert.notEqual('nullable' in backdrop && backdrop.nullable, true);
  assert.deepEqual(Object.keys(backdrop.properties).sort(), ['id', 'kind', 'path']);
});
