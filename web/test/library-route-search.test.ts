import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLibraryScreenSearch} from '../src/screens/browse/route-search.ts';

test('Library links retain actual category and compound server predicates without forwarding raw keys', () => {
  const query = {all: [{field: 'genre', operator: 'contains-any', value: ['actual genre']}]};
  const sort = [{field: 'added', direction: 'desc'}, {field: 'title', direction: 'asc'}];
  assert.deepEqual(parseLibraryScreenSearch({view: 'browse', entityId: 'opaque/ID', category: 'genre:actual', pivot: 'movies', query, sort, unowned: true}),
    {view: 'browse', entityId: 'opaque/ID', category: 'genre:actual', pivot: 'movies', query, sort});
});
test('Malformed library predicates never silently broaden into the whole library', () => {
  for (const raw of [{query: {field: 'genre'}}, {sort: []}, {sort: [{field: 'title', direction: 'sideways'}]},
    {view: 'invented'}, {category: 'x\n'}, {entityId: ['opaque']}]) assert.throws(() => parseLibraryScreenSearch(raw));
  assert.deepEqual(parseLibraryScreenSearch({}), {});
});
