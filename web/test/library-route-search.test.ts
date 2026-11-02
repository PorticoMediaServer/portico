import test from 'node:test';
import assert from 'node:assert/strict';
import {parseLibraryScreenSearch} from '../src/screens/browse/route-search.ts';

test('Library links retain actual category and compound server predicates without forwarding raw keys', () => {
  const query = {variant:'group',all: [{variant:'predicate',field: 'genre', operator: 'contains-any', value: ['actual genre']}]};
  const sort = [{field: 'added', direction: 'desc'}, {field: 'title', direction: 'asc'}];
  assert.deepEqual(parseLibraryScreenSearch({view: 'browse', entityId: 'opaque/ID', category: 'genre:actual', pivot: 'movies', query, sort, unowned: true}),
    {view: 'browse', entityId: 'opaque/ID', category: 'genre:actual', pivot: 'movies', query, sort});
});
test('Malformed library predicates never silently broaden into the whole library', () => {
  for (const raw of [{query: {field: 'genre'}}, {sort: []}, {sort: [{field: 'title', direction: 'sideways'}]},
    {view: 'invented'}, {category: 'x\n'}, {entityId: ['opaque']}]) assert.throws(() => parseLibraryScreenSearch(raw));
  assert.deepEqual(parseLibraryScreenSearch({}), {});
});
test('Recently aired is its own addressable tab, drawn selected, and opens with no sort in its address (LB-2)', async () => {
  const {libraryTabs} = await import('../../packages/client-core/src/presentation/library-tabs.ts');
  const {libraryTabAddress, selectedLibraryTab} = await import('../src/screens/browse/route-search.ts');
  const tabs = libraryTabs('tv'), aired = tabs.find(tab => tab.id === 'aired')!, shows = tabs.find(tab => tab.id === 'browse')!;
  assert.deepEqual(libraryTabAddress(tabs, aired), {view: 'browse', pivot: 'shows', tab: 'aired'});
  assert.deepEqual(libraryTabAddress(tabs, shows), {view: 'browse', pivot: 'shows'});
  const parsed = parseLibraryScreenSearch(libraryTabAddress(tabs, aired));
  assert.equal(selectedLibraryTab(tabs, parsed, 'discover')?.id, 'aired');
  // A changed order on that tab keeps the tab; a sort alone never moves the underline.
  assert.equal(selectedLibraryTab(tabs, {...parsed, sort: [{field: 'title', direction: 'asc'}]}, 'discover')?.id, 'aired');
  assert.equal(selectedLibraryTab(tabs, {view: 'browse', pivot: 'shows', sort: [{field: 'latestAired', direction: 'desc'}]}, 'discover')?.id, 'browse');
  // A tab id that does not read the addressed pivot is ignored, not trusted.
  assert.equal(selectedLibraryTab(tabs, {view: 'browse', pivot: 'collections', tab: 'aired'}, 'discover')?.id, 'collections');
  assert.equal(selectedLibraryTab(libraryTabs('movie'), {}, 'discover')?.id, 'discover');
  assert.throws(() => parseLibraryScreenSearch({tab: 'x\n'}));
});
