import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLibraryList} from '../src/library-list.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

test('one library that cannot be read never empties the list', () => {
  resetSkippedParts();
  const movies = {id: 'm', name: 'Movies', kind: 'movie', defaultView: 'discover', futureField: {a: 1}};
  const list = parseLibraryList({futureField: 1, items: [movies, null, {id: 'x'}, {name: 'No id'}, {id: '', name: 'Empty'}, {...movies, name: 'Again'},
    {id: 'p', name: 'Podcasts', kind: 'podcast'}, {id: 'k', name: 'No kind'}]});
  assert.deepEqual(list.items.map(l => [l.id, l.name, l.kind]), [['m', 'Movies', 'movie'], ['p', 'Podcasts', 'podcast'], ['k', 'No kind', '']]);
  assert.equal(list.items[0].defaultView, 'discover');
  assert.equal(skippedParts().filter(p => p.document === 'libraries').reduce((n, p) => n + p.count, 0), 5);
});

test('an answer without a list of libraries is refused', () => {
  for (const raw of [null, 'x', [], {}, {items: {}}, {items: 'none'}]) assert.throws(() => parseLibraryList(raw));
  assert.deepEqual(parseLibraryList({items: []}).items, []);
});

test('the directory carries the fences the shell is keyed on, and only both together', () => {
  const items = [{id: 'movies', name: 'Movies', kind: 'movie'}];
  assert.deepEqual(parseLibraryList({items, viewerFence: 'v', privacyFence: 'p'}).fences, {viewerFence: 'v', privacyFence: 'p'});
  assert.equal(parseLibraryList({items, viewerFence: 'v'}).fences, undefined);
  assert.equal(parseLibraryList({items, viewerFence: '', privacyFence: 'p'}).fences, undefined);
  assert.equal(parseLibraryList({items, viewerFence: 7, privacyFence: 'p'}).fences, undefined);
});
