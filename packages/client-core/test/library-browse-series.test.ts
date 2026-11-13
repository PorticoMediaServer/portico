import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryBrowseCapabilities, libraryItemQuery} from '../src/library-browse.ts';

test('an audiobook library\'s Series tab lists its series, each opening its volumes', () => {
  const library = {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}], sorts: [{id: 'title', order: 'asc'}], filters: []} as never;
  const caps = libraryBrowseCapabilities(library, 'series') as {pivots: {id: string}[]; resolvedPivot: {entityKinds: string[]}};
  assert.deepEqual(caps.pivots.map(pivot => pivot.id), ['books', 'series', 'authors']);
  assert.deepEqual(caps.resolvedPivot.entityKinds, ['series']);
  assert.deepEqual(libraryItemQuery(library, 'series', [], [{field: 'title', direction: 'asc'}]), {library: 'lib_a', kind: 'series', sort: 'title', order: 'asc'});
});

test('an audiobook library\'s Authors tab is its people list by name, each author opening their page', async () => {
  const {BrowseSession} = await import('../src/browse-session.ts');
  const library = {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}], sorts: [{id: 'added', order: 'desc'}, {id: 'title', order: 'asc'}], filters: [{id: 'genre'}]} as never;
  const caps = libraryBrowseCapabilities(library, 'authors') as {pivots: {id: string}[]; resolvedPivot: {sorts: string[]; filters: string[]; defaultSort: unknown[]}};
  assert.deepEqual(caps.pivots.map(pivot => pivot.id), ['books', 'series', 'authors']);
  assert.deepEqual([caps.resolvedPivot.sorts, caps.resolvedPivot.filters, caps.resolvedPivot.defaultSort], [['title'], [], [{field: 'title', direction: 'asc'}]]);
  // A film library has no Authors tab.
  assert.ok(!(libraryBrowseCapabilities({...(library as object), kind: 'movies', views: [{kind: 'movie'}]} as never, 'authors') as {pivots: {id: string}[]}).pivots.some(pivot => pivot.id === 'authors'));
  const asked: string[] = [];
  const person = (n: number) => ({id: `per_${n}`, name: `Author ${n}`, images: {}, titleCount: n});
  const session = new BrowseSession({query: {libraryId: 'lib_a', pivot: 'authors', capabilities: {} as never, nodes: [], sorts: [{field: 'title', direction: 'asc'}]},
    current: () => true, item: entry => entry, request: async (path: string) => {
      asked.push(path);
      const query = new URL(path, 'http://server').searchParams;
      const at = query.get('cursor') ? Number(query.get('cursor')) : 0;
      return {items: [person(at + 1), person(at + 2)], ...(at + 2 < 6 ? {nextCursor: String(at + 2)} : {}), ...(query.get('total') ? {total: 6} : {})};
    }});
  const none = new AbortController().signal;
  const first = await session.fetchPage(0, 2, none);
  assert.deepEqual(first.items.map(entry => [entry.id, entry.kind, entry.title, entry.navigation]), [
    ['per_1', 'author', 'Author 1', {view: 'author', entityId: 'per_1'}], ['per_2', 'author', 'Author 2', {view: 'author', entityId: 'per_2'}]]);
  assert.equal(first.total, 6);
  assert.match(asked[0]!, /^\/v1\/people\?library=lib_a&role=author&limit=2&total=true$/);
  // The next page follows the cursor; a position further on is read on to.
  assert.deepEqual((await session.fetchPage(2, 2, none)).items.map(entry => entry.id), ['per_3', 'per_4']);
  assert.match(asked.at(-1)!, /cursor=2/);
  const far = new BrowseSession({query: {libraryId: 'lib_a', pivot: 'authors', capabilities: {} as never, nodes: [], sorts: []}, current: () => true, item: entry => entry,
    request: async (path: string) => {const query = new URL(path, 'http://server').searchParams, at = query.get('cursor') ? Number(query.get('cursor')) : 0;
      return {items: [person(at + 1), person(at + 2)], ...(at + 2 < 6 ? {nextCursor: String(at + 2)} : {})};}});
  assert.deepEqual((await far.fetchPage(4, 2, none)).items.map(entry => entry.id), ['per_5', 'per_6']);
});

// The lead's rulings (13 Nov): a Books library's Series lists its book series only, and a reading
// series is a 2:3 book cover, where an audiobook series stays square.
test("a Books library's Series lists book series only; comics and audiobooks list all theirs", async () => {
  const {libraryItemQuery} = await import('../src/library-browse.ts');
  assert.equal(libraryItemQuery({id: 'lib', views: [{kind: 'book'}, {kind: 'series'}, {kind: 'magazine'}]} as never, 'series', [], []).publication, 'book');
  assert.equal(libraryItemQuery({id: 'lib', kind: 'book'}, 'series', [], []).publication, 'book', 'the browse grid names the library by its kind');
  assert.equal(libraryItemQuery({id: 'lib', views: [{kind: 'comic'}, {kind: 'series'}]} as never, 'series', [], []).publication, undefined);
  assert.equal(libraryItemQuery({id: 'lib', views: [{kind: 'audiobook'}, {kind: 'series'}]} as never, 'series', [], []).publication, undefined);
});

test('a reading series is a 2:3 cover; an audiobook series stays square', async () => {
  const {shapeFor} = await import('../src/presentation/card-shape.ts');
  assert.equal(shapeFor('book_series', 'grid', 'book'), 'poster');
  assert.equal(shapeFor('book_series', 'grid', 'comic'), 'poster');
  assert.equal(shapeFor('book_series', 'grid', 'audiobook'), 'square');
  assert.equal(shapeFor('book_series', 'grid'), 'square');
});

test("a reading library's series card opens the reading series page; an audiobook series keeps its own", async () => {
  const {readingSeries} = await import('../src/browse-session.ts');
  const series = {id: 'itm_s', kind: 'book_series', title: 'S', libraryId: 'lib', navigation: {view: 'book_series', entityId: 'itm_s'}} as never;
  const book = {id: 'itm_b', kind: 'ebook', title: 'B', libraryId: 'lib', navigation: {view: 'item', entityId: 'itm_b'}} as never;
  for (const kind of ['book', 'comic']) {
    const [s, b] = readingSeries([series, book], kind) as {navigation?: {view: string; entityId?: string}}[];
    assert.deepEqual(s!.navigation, {view: 'series', entityId: 'itm_s'}, kind);
    assert.deepEqual(b!.navigation, {view: 'item', entityId: 'itm_b'}, 'other cards are left as they are');
  }
  assert.deepEqual((readingSeries([series], 'audiobook')[0] as {navigation?: unknown}).navigation, {view: 'book_series', entityId: 'itm_s'});
});
