import test from 'node:test';
import assert from 'node:assert/strict';

// The rebuilt server's answers for an artist's page: its albums (parent=), and its songs (artist=, #2916).
const track = (n: number) => ({id: `itm_t${n}`, kind: 'track', title: `Song ${n}`, parentId: 'alb_1', rootId: 'art_1', index: n, parentIndex: 1,
  durationMs: 40000, addedAt: '2026-11-12T00:00:00Z', images: {}, state: {}, libraryId: 'lib_m'});
const albumItem = (id: string, year: number) => ({id, kind: 'album', title: `Album ${year}`, year, parentId: 'art_1', addedAt: '2026-11-12T00:00:00Z',
  images: {poster: `/v1/images/${id}`}, state: {}, libraryId: 'lib_m', counts: {children: 2, leaves: 2, unwatchedLeaves: 2}});

type Answers = Readonly<{popular?: unknown; songs?: (url: URL) => unknown}>;
async function artist(answers: Answers = {}) {
  const {NativeTVMusicController} = await import('../src-next/screens/destinations/music/model.ts');
  const seen: string[] = [];
  const answer = (path: string, method = 'GET') => {
    seen.push(`${method} ${path}`);
    const url = new URL(path, 'https://server.test'), query = url.searchParams;
    if (url.pathname === '/v1/items' && query.get('parent') === 'art_1' && query.get('kind') === 'album')
      return query.get('cursor') === 'r2' ? {items: [albumItem('alb_0', 2010)], total: 3} : {items: [albumItem('alb_2', 2024), albumItem('alb_1', 2020)], nextCursor: 'r2', total: 3};
    if (url.pathname === '/v1/items' && query.get('artist') === 'art_1' && query.get('sort') === 'popular') return answers.popular ?? {items: [track(2), track(1)], total: 2};
    if (url.pathname === '/v1/items' && query.get('artist') === 'art_1') return answers.songs?.(url) ?? (query.get('cursor') === 's2'
      ? {items: [track(3)], total: 3} : {items: [track(1), track(2)], nextCursor: 's2', total: 3});
    if (url.pathname === '/v1/items/art_1') return {id: 'art_1', kind: 'artist', title: 'Walker', libraryId: 'lib_m', images: {}, state: {}, addedAt: '2026-11-12T00:00:00Z',
      counts: {children: 3, leaves: 6, unwatchedLeaves: 6}};
    if (url.pathname === '/v1/libraries/lib_m') return {id: 'lib_m', name: 'Music', kind: 'music', views: [{kind: 'artist'}, {kind: 'album'}, {kind: 'track'}], sorts: [], filters: []};
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  };
  let active = true;
  const controller = new NativeTVMusicController({entry: {id: 'art_1', libraryId: 'lib_m', kind: 'artist', title: 'Walker'},
    api: {request: async (path: string, method?: string) => answer(path, method)}, similarScope: {},
    lifetime: {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => active}, requestId: async () => 'op'} as never);
  return {controller, seen, close: () => {active = false; controller.dispose();}};
}

test('an artist opens with its newest albums and its most played songs from the rebuilt server', async () => {
  const f = await artist();
  await f.controller.load();
  const state = f.controller.getSnapshot();
  assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
  assert.deepEqual(state.albums.map(album => [album.id, album.year, album.artist?.name, album.posterUrl]),
    [['alb_2', 2024, 'Walker', '/v1/images/alb_2'], ['alb_1', 2020, 'Walker', '/v1/images/alb_1']]);
  assert.deepEqual(state.songs.map(song => [song.id, song.kind]), [['itm_t2', 'song'], ['itm_t1', 'song']]);
  assert.ok(f.seen.includes('GET /v1/items?parent=art_1&kind=album&sort=year&order=desc&limit=100&total=true'), f.seen.join(' | '));
  assert.ok(f.seen.includes('GET /v1/items?artist=art_1&kind=track&sort=popular&limit=10&total=true'), f.seen.join(' | '));
  assert.ok(!f.seen.some(request => request.startsWith('POST') || request.includes('/v1/artists/')), 'no old-server read is sent');
  assert.equal(state.songTotal, 6, "the artist's song count is its own, not the popular row's");
  await f.controller.nextReleases(state.releases);
  assert.deepEqual(f.controller.getSnapshot().albums.map(album => album.id), ['alb_2', 'alb_1', 'alb_0']);
  f.close();
});

test('an artist nobody has played still opens, with no popular songs', async () => {
  const f = await artist({popular: {items: [], total: 0}});
  await f.controller.load();
  const state = f.controller.getSnapshot();
  assert.equal(state.phase, 'ready');
  assert.deepEqual(state.songs, []);
  assert.equal(state.albums.length, 2);
  f.close();
});

test("All songs lists every song crediting the artist by name, and pages on", async () => {
  const f = await artist();
  await f.controller.load();
  await f.controller.loadAllSongs();
  const all = f.controller.getSnapshot().allSongs;
  assert.equal(all.phase, 'ready');
  assert.deepEqual(all.songs.map(song => song.id), ['itm_t1', 'itm_t2']);
  assert.equal(all.total, 3);
  assert.ok(f.seen.includes('GET /v1/items?artist=art_1&kind=track&sort=title&order=asc&limit=200&total=true'), f.seen.join(' | '));
  await f.controller.nextAllSongs(all);
  assert.deepEqual(f.controller.getSnapshot().allSongs.songs.map(song => song.id), ['itm_t1', 'itm_t2', 'itm_t3']);
  f.close();
});
