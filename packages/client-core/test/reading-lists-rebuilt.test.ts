import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type Viewer} from '../src/index.ts';

// A Read library's lists on the rebuilt server: its titles, Recently added, Continue reading and a
// series' volumes, read from the item list and the landing and Home rows.
const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const json = (value: unknown) => new Response(JSON.stringify(value), {headers: {'Content-Type': 'application/json'}});
const wire = (id: string, kind: string, extra: Record<string, unknown> = {}) => ({id, kind, title: 'Title ' + id, libraryId: 'lib_books', addedAt: '2026-11-12T00:00:00Z',
  images: {}, state: {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}, ...extra});
function server(answer: (url: URL) => unknown) {
  const calls: URL[] = [];
  const api = new HttpLocalApi('https://server.example', 'token', async input => {const url = new URL(String(input)); calls.push(url); return json(answer(url));});
  return {calls, reading: api.reading({viewer, isCurrent: () => true})};
}

test('a library lists every book, comic and issue in it, by title, a page at a time', async () => {
  const {calls, reading} = server(() => ({items: [wire('itm_a', 'book', {images: {poster: '/v1/images/abc123'}, state: {watched: true}}), wire('itm_s', 'series'), wire('itm_c', 'issue')],
    nextCursor: 'next'}));
  const page = await reading.list({libraryId: 'lib_books', limit: 40, cursor: 'here'});
  assert.equal(calls[0].pathname, '/v1/items');
  assert.deepEqual(Object.fromEntries(calls[0].searchParams), {library: 'lib_books', kind: 'book,comic,issue', limit: '40', cursor: 'here'});
  assert.deepEqual(page.items.map(item => item.id), ['itm_a', 'itm_c'], 'a series is not a title to read');
  assert.equal(page.items[0].coverUrl, '/v1/images/abc123');
  assert.equal(page.items[0].readingMark?.read, true);
  assert.equal(page.nextCursor, 'next');
});

test('Discover shows what was added most recently, and its row goes on from its own cursor', async () => {
  const {calls, reading} = server(url => ({items: [wire(url.searchParams.has('cursor') ? 'itm_b' : 'itm_a', 'book')], ...(url.searchParams.has('cursor') ? {} : {nextCursor: 'more'})}));
  const discover = await reading.discover({libraryId: 'lib_books', limit: 20, shortReadsLimit: 12});
  assert.equal(calls[0].searchParams.get('sort'), 'added'); assert.equal(calls[0].searchParams.get('order'), 'desc');
  assert.deepEqual(discover.rows.map(row => [row.key, row.items.map(item => item.id), row.nextCursor]), [['recent', ['itm_a'], 'more']]);
  const next = await reading.discoverRow('recent', {libraryId: 'lib_books', cursor: 'more', limit: 20});
  assert.equal(calls[1].searchParams.get('cursor'), 'more');
  assert.deepEqual(next.row.items.map(item => item.id), ['itm_b']); assert.equal(next.row.nextCursor, undefined);
  assert.deepEqual((await reading.discoverRow('series', {libraryId: 'lib_books'})).row.items, []);
  assert.equal(calls.length, 2, 'a row with no read asks for nothing');
});

test('Continue reading is the library landing row, or Home\'s, each title with the place it was left at', async () => {
  const position = (place: Record<string, unknown>) => ({state: {watched: false, position: JSON.stringify(place), lastPlayedAt: '2026-11-12T01:00:00Z'}});
  const {calls, reading} = server(url => ({rows: [{id: url.pathname === '/v1/home' ? 'continue_reading' : 'continue', title: 'Continue', shape: 'poster', items: [
    wire('itm_epub', 'book', position({format: 'epub', cfi: 'epubcfi(/6/4!/4,/80,/82/1:34)', section: 'OEBPS/c1.xhtml', progression: 0.53, fingerprint: '0123456789abcdef'})),
    wire('itm_comic', 'issue', position({format: 'cbz', pageIndex: 2, progression: 0.5})),
    wire('itm_none', 'book'),
  ]}]}));
  const library = await reading.continue({libraryId: 'lib_books'});
  assert.equal(calls[0].pathname, '/v1/libraries/lib_books/landing');
  assert.deepEqual(library.items.map(row => [row.item.id, row.item.format, row.progress.location.progression]), [['itm_epub', 'epub', 0.53], ['itm_comic', 'cbz', 0.5]],
    'a title with no place is not in progress');
  assert.equal(library.items[0].progress.location.cfi, 'epubcfi(/6/4!/4,/80,/82/1:34)');
  assert.equal(library.items[1].progress.location.pageIndex, 2);
  assert.equal(library.items[0].progress.observedAt, Date.parse('2026-11-12T01:00:00Z'));
  const everywhere = await reading.continue();
  assert.equal(calls[1].pathname, '/v1/home');
  assert.equal(everywhere.items.length, 2);
});

test('a series names itself and lists its volumes in volume order', async () => {
  const {calls, reading} = server(url => url.pathname === '/v1/items/itm_series'
    ? wire('itm_series', 'series', {title: 'Harbour Tales', counts: {children: 3, leaves: 3, unwatchedLeaves: 2}})
    : {items: [wire('itm_1', 'issue'), wire('itm_2', 'issue')], nextCursor: 'more'});
  const page = await reading.series('lib_books', 'itm_series', {limit: 30});
  assert.deepEqual(Object.fromEntries(calls[0].searchParams), {series: 'itm_series', sort: 'order', limit: '30'});
  assert.deepEqual(page.items.map(item => item.id), ['itm_1', 'itm_2']);
  assert.equal(calls[1].pathname, '/v1/items/itm_series');
  assert.deepEqual(page.overview, {id: 'itm_series', title: 'Harbour Tales', total: 3, completed: 1});
  assert.equal(page.nextCursor, 'more');
});

test('Discover\'s New issues are the library\'s newest magazine issues by issue date, the row going on from its own cursor', async () => {
  const {calls, reading} = server(url => url.searchParams.get('publication') === 'magazine'
    ? {items: [wire('itm_dec', 'book', {releaseDate: '1929-12', series: {id: 'itm_amazing', kind: 'series', title: 'Amazing Stories'}}), wire('itm_undated', 'book')], ...(url.searchParams.has('cursor') ? {} : {nextCursor: 'more'})}
    : {items: [wire('itm_a', 'book')]});
  const discover = await reading.discover({libraryId: 'lib_books', limit: 20, newIssuesLimit: 12});
  const asked = calls.find(url => url.searchParams.get('publication') === 'magazine')!;
  assert.deepEqual([asked.searchParams.get('sort'), asked.searchParams.get('order'), asked.searchParams.get('limit')], ['release', 'desc', '12']);
  assert.deepEqual(discover.newIssueCards.map(card => [card.item.id, card.issueDate, card.item.series?.name]), [['itm_dec', '1929-12', 'Amazing Stories']], 'an issue with no date is not an issue');
  assert.equal(discover.rows.find(row => row.key === 'new-issues')?.nextCursor, 'more');
  const next = await reading.discoverRow('new-issues', {libraryId: 'lib_books', cursor: 'more', limit: 12});
  assert.equal(calls.at(-1)!.searchParams.get('cursor'), 'more'); assert.equal(next.newIssueCards.length, 1); assert.equal(next.row.nextCursor, undefined);
});

test('Discover keeps Recently added when its optional New issues read fails', async () => {
  const {reading} = server(url => {
    if (url.searchParams.get('publication') === 'magazine') throw new Error('Issues unavailable');
    return {items: [wire('itm_recent', 'book')], nextCursor: 'more'};
  });
  const discover = await reading.discover({libraryId: 'lib_books', newIssuesLimit: 12});
  assert.deepEqual(discover.rows.map(row => [row.key, row.items.map(item => item.id), row.nextCursor]), [['recent', ['itm_recent'], 'more']]);
  assert.deepEqual(discover.newIssueCards, []);
});
