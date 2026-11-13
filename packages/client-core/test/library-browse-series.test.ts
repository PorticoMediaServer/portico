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

test("a reading library's series card opens the reading series page in every list; an audiobook series keeps its own", async () => {
  // The server names a book or comic library's series' publication (itemShape); an audiobook series names none.
  const {entryFromItem} = await import('../src/items.ts');
  const {homeDocument} = await import('../src/home.ts');
  const {rebuiltContent} = await import('../src/library-content-rebuilt.ts');
  const {readSearch} = await import('../src/search.ts');
  const state = {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false};
  const reading = {id: 'itm_r', kind: 'series', title: 'Saga', libraryId: 'lib_comics', images: {}, state, addedAt: '', publicationType: 'book'};
  const audiobook = {id: 'itm_a', kind: 'series', title: 'Tides', libraryId: 'lib_audio', images: {}, state, addedAt: ''};
  // A magazine's or newspaper's run of issues opens its periodical page, as the library's Magazines and Newspapers tabs do.
  const monthly = {...reading, id: 'itm_m', title: 'Monthly', libraryId: 'lib_books', publicationType: 'magazine'};
  const daily = {...reading, id: 'itm_d', title: 'Daily', libraryId: 'lib_books', publicationType: 'newspaper'};
  const views = (entries: readonly {navigation?: {view: string; entityId?: string; pivot?: string}}[]) => entries.map(entry => entry.navigation);
  const want = [{view: 'series', entityId: 'itm_r'}, {view: 'book_series', entityId: 'itm_a'},
    {view: 'series', entityId: 'itm_m', pivot: 'magazines'}, {view: 'series', entityId: 'itm_d', pivot: 'newspapers'}];
  // The card itself.
  assert.deepEqual(views([reading, audiobook, monthly, daily].map(item => entryFromItem(item as never)!)), want);
  // Home's rows.
  assert.deepEqual(views(homeDocument({rows: [{id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [reading, audiobook, monthly, daily]}]} as never).rows[0]!.entries), want);
  // A library's Discover rows.
  const api = {request: async <T>(path: string): Promise<T> => (path.endsWith('/landing') ? {rows: [{id: 'recently_added', title: 'Recently Added', items: [reading, audiobook, monthly, daily]}]}
    : {id: 'lib_comics', name: 'Comics', kind: 'comics'}) as T};
  const discover = await rebuiltContent(api, {libraryId: 'lib_comics', view: 'discover'}, {viewerId: 'prf', serverId: 'srv'}, 40);
  assert.deepEqual(views(discover.sections[0]!.entries as never), want);
  // Search rows, through the same card.
  const found = await readSearch({request: async <T>(): Promise<T> => ({groups: [{kind: 'book', items: [reading, audiobook, monthly, daily]}]}) as T} as never, {q: 'saga'}, 12) as {groups: {items: {navigation?: {view: string; entityId?: string}}[]}[]};
  assert.deepEqual(views(found.groups[0]!.items), want);
});

test("a re-checked card keeps a magazine's or newspaper's periodical link, and refuses any other pivot", async () => {
  // Saved, catalogue selection, recommendation and relation rows, and browse re-check every card (validateContentEntry).
  const {entryFromItem} = await import('../src/items.ts');
  const {validateContentEntry} = await import('../src/library-content.ts');
  const {parseBrowseResult} = await import('../src/browse.ts');
  const state = {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false};
  const run = (id: string, publicationType: string) => entryFromItem({id, kind: 'series', title: id, libraryId: 'lib_books', images: {}, state, addedAt: '', publicationType} as never)!;
  const monthly = run('itm_m', 'magazine'), daily = run('itm_d', 'newspaper'), atlas = run('itm_a', 'book');
  assert.deepEqual([monthly, daily, atlas].map(card => validateContentEntry(card).navigation),
    [{view: 'series', entityId: 'itm_m', pivot: 'magazines'}, {view: 'series', entityId: 'itm_d', pivot: 'newspapers'}, {view: 'series', entityId: 'itm_a'}]);
  // Through a re-checking path: one browse page.
  const page = parseBrowseResult({pivot: 'series', applied: {query: null, sort: [{field: 'title', direction: 'asc'}]}, entries: [monthly, daily, atlas],
    pageInfo: {start: 0, total: 3, revision: 'r', hasMore: false, nextCursor: ''}});
  assert.deepEqual(page.entries.map(card => card.navigation?.pivot), ['magazines', 'newspapers', undefined]);
  // Nothing else is accepted: another pivot, or a pivot off a series link.
  assert.throws(() => validateContentEntry({...atlas, navigation: {view: 'series', entityId: 'itm_a', pivot: 'albums'}}));
  assert.throws(() => validateContentEntry({...atlas, navigation: {view: 'book_series', entityId: 'itm_a', pivot: 'magazines'}}));
});
