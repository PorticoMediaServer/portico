import test from 'node:test';
import assert from 'node:assert/strict';
import {LibraryContentService, libraryContentPages} from '../src/library-content.ts';
import {withQueries} from './fixtures/viewer-queries.ts';

const scope = {viewerId: JSON.stringify(['hosted', 'account', 'profile']), serverId: 'server'};
const film = (id: string) => ({id, kind: 'movie', title: `Film ${id}`, images: {poster: `/v1/images/${id}`}});

function service(answers: (path: string) => unknown, paths: string[] = []) {
  return new LibraryContentService(withQueries({scope, api: {request: async <T>(path: string) => { paths.push(path); return answers(path) as T; }}}));
}

test('a library\'s Discover is its landing page: each of its rows is a rail, in the server\'s order, with the server\'s own titles', async () => {
  const paths: string[] = [];
  const nav = service(path => path === '/v1/libraries/lib_a/landing'
    ? {rows: [{id: 'continue', title: 'Continue Watching', shape: 'landscape', items: [film('m1')]},
      {id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [film('m2'), film('m3'), {id: 'x', kind: 'something-new', title: 'Not a card'}]},
      {id: 'mood:cozy', title: 'Cozy stories for you', shape: 'poster', items: []}]}
    : {id: 'lib_a', name: 'Films', kind: 'movies'}, paths);
  await nav.select({libraryId: 'lib_a', view: 'discover'});
  const snap = nav.getSnapshot();
  assert.equal(snap.phase, 'ready');
  assert.deepEqual([...paths].sort(), ['/v1/libraries/lib_a', '/v1/libraries/lib_a/landing']);
  // A row with nothing the app can draw has no rail; a card of an unknown kind is left out alone.
  assert.deepEqual(snap.sections.map(section => [section.id, section.type, section.heading.fallback, section.entries.map(entry => entry.id)]),
    [['continue', 'rail', 'Continue Watching', ['m1']], ['recently_added', 'rail', 'Recently Added', ['m2', 'm3']]]);
  // The layout reads the library's kind in the app's words.
  assert.equal(snap.projection?.scope.libraryKind, 'movie');
  assert.equal(snap.projection?.scope.view, 'discover');
  assert.equal(snap.sections[0]!.entries[0]!.libraryId, 'lib_a');
  nav.dispose();
});

test('an audiobooks library\'s Discover says its kind, so it draws square cards; an empty landing is an empty Discover, not a failure', async () => {
  const nav = service(path => path.endsWith('/landing') ? {rows: []} : {id: 'lib_b', name: 'Audiobooks', kind: 'audiobooks'});
  await nav.select({libraryId: 'lib_b', view: 'discover'});
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(nav.getSnapshot().projection?.scope.libraryKind, 'audiobook');
  assert.deepEqual(nav.getSnapshot().sections, []);
  nav.dispose();
});

const track = (id: string, disc: number, n: number) => ({id, kind: 'track', title: `Song ${id}`, index: n, parentIndex: disc, durationMs: 180_000});

function holderApi(paths: string[], children: Record<string, unknown>, titles: Record<string, unknown>) {
  return async (path: string) => {
    paths.push(path);
    const u = new URL(path, 'https://server.test');
    if (u.pathname === '/v1/items') return children[u.searchParams.get('parent') ?? u.searchParams.get('collection') ?? ''];
    if (u.pathname.startsWith('/v1/libraries/')) return {id: 'lib_m', name: 'Music', kind: 'music'};
    return titles[decodeURIComponent(u.pathname.split('/')[3]!)];
  };
}

test('an album page is its title and its songs in disc and track order, read from the rebuilt server', async () => {
  const paths: string[] = [];
  const read = holderApi(paths, {alb_1: {items: [track('t1', 1, 1), track('t2', 1, 2), track('t3', 2, 1)], total: 3}},
    {alb_1: {id: 'alb_1', kind: 'album', title: 'Album One', libraryId: 'lib_m', year: 2001}});
  const nav = service(read);
  await nav.select({libraryId: 'lib_m', view: 'album', entityId: 'alb_1'});
  const snap = nav.getSnapshot();
  assert.equal(snap.phase, 'ready');
  assert.equal(snap.projection?.entity?.kind, 'album');
  assert.equal(snap.projection?.entity?.id, 'alb_1');
  assert.equal(snap.projection?.entity?.playback, undefined, 'the page\'s own title never plays from its header');
  assert.equal(snap.projection?.scope.libraryKind, 'music');
  assert.deepEqual(snap.sections.map(section => [section.id, section.type, section.entries.map(e => [e.id, e.discNumber, e.trackNumber])]),
    [['tracks', 'list', [['t1', 1, 1], ['t2', 1, 2], ['t3', 2, 1]]]]);
  const list = new URL(paths.find(path => path.startsWith('/v1/items?'))!, 'https://server.test');
  assert.equal(list.searchParams.get('parent'), 'alb_1');
  assert.equal(list.searchParams.get('sort'), 'order');
  assert.ok(!paths.some(path => path.includes('/content')));
  nav.dispose();
});

/** An artist's page on the rebuilt server: its albums by `parent=`, its songs by `artist=` (#2956). */
function artistApi(paths: string[], popular: () => unknown) {
  return async (path: string) => {
    paths.push(path);
    const u = new URL(path, 'https://server.test'), query = u.searchParams;
    if (u.pathname === '/v1/items' && query.get('parent') === 'art_1') return {items: [{id: 'alb_2', kind: 'album', title: 'Later', year: 2010}, {id: 'alb_1', kind: 'album', title: 'Earlier', year: 2001}], total: 2};
    if (u.pathname === '/v1/items' && query.get('sort') === 'popular') return popular();
    if (u.pathname === '/v1/items' && query.get('artist') === 'art_1')
      return query.get('cursor') === 'c2' ? {items: [track('t3', 1, 3)], total: 3} : {items: [track('t1', 1, 1), track('t2', 1, 2)], total: 3, nextCursor: 'c2'};
    if (u.pathname.startsWith('/v1/libraries/')) return {id: 'lib_m', name: 'Music', kind: 'music'};
    return {id: 'art_1', kind: 'artist', title: 'The Band', libraryId: 'lib_m'};
  };
}
const ids = (snap: ReturnType<LibraryContentService['getSnapshot']>) => snap.sections.map(section => [section.id, section.entries.map(e => e.id)]);

test('an artist page shows its albums newest first, its most played songs, and every song crediting it', async () => {
  const paths: string[] = [];
  const nav = service(artistApi(paths, () => ({items: [track('t2', 1, 2)], total: 1})));
  await nav.select({libraryId: 'lib_m', view: 'artist', entityId: 'art_1'});
  const snap = nav.getSnapshot();
  assert.equal(snap.phase, 'ready');
  assert.equal(snap.projection?.entity?.kind, 'artist');
  assert.deepEqual(ids(snap), [['releases', ['alb_2', 'alb_1']], ['popularTracks', ['t2']], ['songs', ['t1', 't2']]]);
  const asked = (test: (q: URLSearchParams) => boolean) => paths.map(path => new URL(path, 'https://server.test')).find(u => u.pathname === '/v1/items' && test(u.searchParams))?.searchParams;
  const releases = asked(q => q.get('parent') === 'art_1')!, popular = asked(q => q.get('sort') === 'popular')!, songs = asked(q => q.get('sort') === 'title')!;
  assert.deepEqual([releases.get('kind'), releases.get('sort'), releases.get('order')], ['album', 'year', 'desc']);
  assert.deepEqual([popular.get('artist'), popular.get('kind'), popular.get('limit')], ['art_1', 'track', '10']);
  assert.deepEqual([songs.get('artist'), songs.get('kind'), songs.get('order')], ['art_1', 'track', 'asc']);
  // Popular is one fixed rail; every song pages on by its own cursor.
  assert.equal(snap.sections.find(section => section.id === 'popularTracks')?.nextCursor, '');
  await nav.next('songs');
  assert.deepEqual(nav.getSnapshot().appended.find(section => section.id === 'songs')?.entries.map(e => e.id), ['t1', 't2', 't3']);
  assert.equal(new URL(paths.at(-1)!, 'https://server.test').searchParams.get('cursor'), 'c2');
  nav.dispose();
});

test('an artist nobody has played has no Popular row, and the page opens when Popular cannot be read', async () => {
  for (const popular of [() => ({items: [], total: 0}), () => { throw Object.assign(new Error('unavailable'), {status: 503}); }]) {
    const nav = service(artistApi([], popular));
    await nav.select({libraryId: 'lib_m', view: 'artist', entityId: 'art_1'});
    assert.equal(nav.getSnapshot().phase, 'ready');
    assert.deepEqual(ids(nav.getSnapshot()), [['releases', ['alb_2', 'alb_1']], ['songs', ['t1', 't2']]]);
    nav.dispose();
  }
});

test('a collection\'s grid is opened at any position with the whole count, from the rebuilt server', async () => {
  const paths: string[] = [];
  const films = Array.from({length: 5}, (_, n) => film(`f${n}`));
  const read = async (path: string) => {
    paths.push(path);
    const u = new URL(path, 'https://server.test');
    if (u.pathname === '/v1/items') { const at = Number(u.searchParams.get('at') ?? 0); return {items: films.slice(at, at + Number(u.searchParams.get('limit'))), total: 5}; }
    if (u.pathname.startsWith('/v1/libraries/')) return {id: 'lib_a', name: 'Films', kind: 'movies'};
    return {id: 'col_1', kind: 'collection', title: 'The Trilogy', libraryId: 'lib_a'};
  };
  const pages = libraryContentPages({api: {request: async <T>(path: string) => read(path) as T}, scope, route: {libraryId: 'lib_a', view: 'collection', entityId: 'col_1'}, pageSize: 2});
  const second = await pages(2, 2, new AbortController().signal);
  assert.deepEqual(second.items.map(e => e.id), ['f2', 'f3']);
  assert.equal(second.total, 5);
  const list = new URL(paths.find(path => path.startsWith('/v1/items?'))!, 'https://server.test');
  assert.deepEqual([list.searchParams.get('collection'), list.searchParams.get('at'), list.searchParams.get('limit'), list.searchParams.get('total')], ['col_1', '2', '2', 'true']);
  // A collection opened through the service draws the same titles.
  const nav = service(read);
  await nav.select({libraryId: 'lib_a', view: 'collection', entityId: 'col_1'});
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(nav.getSnapshot().sections[0]!.totalCount, 5);
  nav.dispose();
});

test('a paged section reads its next page by its cursor, and carries the server\'s total and next cursor', async () => {
  const paths: string[] = [];
  const read = async (path: string) => {
    paths.push(path);
    const u = new URL(path, 'https://server.test');
    if (u.pathname === '/v1/items') return u.searchParams.get('cursor') === 'c2'
      ? {items: [track('t3', 1, 3)], total: 3}
      : {items: [track('t1', 1, 1), track('t2', 1, 2)], total: 3, nextCursor: 'c2'};
    if (u.pathname.startsWith('/v1/libraries/')) return {id: 'lib_m', name: 'Music', kind: 'music'};
    return {id: 'alb_1', kind: 'album', title: 'Long Album', libraryId: 'lib_m', parent: {id: 'art_1', kind: 'artist', title: 'The Band'}};
  };
  const nav = service(read);
  await nav.select({libraryId: 'lib_m', view: 'album', entityId: 'alb_1'});
  assert.equal(nav.getSnapshot().sections[0]!.totalCount, 3);
  assert.equal(nav.getSnapshot().sections[0]!.nextCursor, 'tracks~c2', 'the cursor names its section');
  // The album names its artist, for the page's "by" link.
  assert.deepEqual(nav.getSnapshot().projection?.entity?.artist, {id: 'art_1', name: 'The Band'});
  await nav.next('tracks');
  assert.equal(new URL(paths.filter(path => path.startsWith('/v1/items?')).at(-1)!, 'https://server.test').searchParams.get('cursor'), 'c2');
  assert.deepEqual(nav.getSnapshot().appended[0]!.entries.map(e => e.id), ['t1', 't2', 't3'], 'the next page is the second page, not the first again');
  nav.dispose();
});

test('Discover survives the library read failing: the rows still show, in the default layout', async () => {
  const nav = service(path => {
    if (path.endsWith('/landing')) return {rows: [{id: 'recently_added', title: 'Recently Added', shape: 'poster', items: [film('m1')]}]};
    throw Object.assign(new Error('gone'), {status: 500});
  });
  await nav.select({libraryId: 'lib_a', view: 'discover'});
  assert.equal(nav.getSnapshot().phase, 'ready');
  assert.equal(nav.getSnapshot().projection?.scope.libraryKind, '');
  assert.deepEqual(nav.getSnapshot().sections.map(section => section.id), ['recently_added']);
  nav.dispose();
});

test('an audiobook library\'s Continue is the screen\'s resume shelf', async () => {
  const nav = service(path => path.endsWith('/landing')
    ? {rows: [{id: 'continue', title: 'Continue Listening', shape: 'poster', items: [{id: 'b1', kind: 'audiobook', title: 'A Book'}]}]}
    : {id: 'lib_b', name: 'Audiobooks', kind: 'audiobooks'});
  await nav.select({libraryId: 'lib_b', view: 'discover'});
  assert.deepEqual(nav.getSnapshot().sections.map(section => section.id), ['continue_listening']);
  nav.dispose();
});

test('a later page that comes back with no titles keeps the titles already shown, and ends the list', async () => {
  const read = async (path: string) => {
    const u = new URL(path, 'https://server.test');
    if (u.pathname === '/v1/items') return u.searchParams.get('cursor') === 'c2' ? {items: [], total: 2} : {items: [track('t1', 1, 1), track('t2', 1, 2)], total: 3, nextCursor: 'c2'};
    if (u.pathname.startsWith('/v1/libraries/')) return {id: 'lib_m', name: 'Music', kind: 'music'};
    return {id: 'alb_1', kind: 'album', title: 'Album', libraryId: 'lib_m'};
  };
  const nav = service(read);
  await nav.select({libraryId: 'lib_m', view: 'album', entityId: 'alb_1'});
  await nav.next('tracks');
  const shown = nav.getSnapshot().appended.find(section => section.id === 'tracks')!;
  assert.deepEqual(shown.entries.map(e => e.id), ['t1', 't2']);
  assert.equal(shown.nextCursor, '');
  nav.dispose();
});

/** An audiobook library's series and author pages on the rebuilt server. */
function bookApi(person: Record<string, unknown> = {id: 'per_1', name: 'Ada Adams', biography: 'Writes about the sea.', images: {portrait: '/v1/images/ada'}}) {
  const paths: string[] = [];
  const read = async (path: string) => {
    paths.push(path);
    const u = new URL(path, 'https://server.test'), query = u.searchParams;
    const book = (id: string, index?: number) => ({id, kind: 'audiobook', title: `Book ${id}`, ...(index ? {index} : {})});
    if (u.pathname === '/v1/items' && query.get('series') === 'ser_1') return {items: [book('b1', 1), book('b2', 2)], total: 2};
    if (u.pathname === '/v1/items' && query.get('author') === 'per_1') return {items: [book('b9')], total: 1};
    if (u.pathname === '/v1/items/ser_1') return {id: 'ser_1', kind: 'series', title: 'Tides', libraryId: 'lib_a'};
    if (u.pathname === '/v1/people/per_1') return person;
    return {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks'};
  };
  return {paths, read};
}

test('a series page is its volumes in volume order; an author page is the person and their books in this library', async () => {
  const {paths, read} = bookApi();
  const nav = service(read);
  await nav.select({libraryId: 'lib_a', view: 'book_series', entityId: 'ser_1'});
  assert.deepEqual([nav.getSnapshot().projection?.entity?.kind, nav.getSnapshot().projection?.entity?.title], ['book_series', 'Tides']);
  assert.deepEqual(ids(nav.getSnapshot()), [['books', ['b1', 'b2']]]);
  assert.ok(paths.some(path => path.startsWith('/v1/items?series=ser_1&sort=order')), paths.join(' | '));
  await nav.select({libraryId: 'lib_a', view: 'author', entityId: 'per_1'});
  const author = nav.getSnapshot().projection?.entity;
  assert.deepEqual([author?.kind, author?.title, author?.overview, author?.posterUrl], ['author', 'Ada Adams', 'Writes about the sea.', '/v1/images/ada']);
  assert.deepEqual(ids(nav.getSnapshot()), [['books', ['b9']]]);
  assert.ok(paths.some(path => path.startsWith('/v1/items?library=lib_a&author=per_1')), paths.join(' | '));
  nav.dispose();
});

test('an author page whose person read names someone else has no header, never theirs', async () => {
  const nav = service(bookApi({id: 'per_2', name: 'Someone Else', images: {}}).read);
  await nav.select({libraryId: 'lib_a', view: 'author', entityId: 'per_1'});
  assert.equal(nav.getSnapshot().projection?.entity, undefined);
  nav.dispose();
});
