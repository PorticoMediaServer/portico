import test from 'node:test';
import assert from 'node:assert/strict';

// The rebuilt server's answer for an album's tracks, as the TV lane's Android TV emulator received it.
const track = (n: number) => ({id: `itm_t${n}`, kind: 'track', title: `Walk Track ${n}`, year: 2022, parentId: 'alb_1', rootId: 'art_1', index: n, parentIndex: 1,
  durationMs: 40000, addedAt: '2026-11-12T00:00:00Z', images: {}, state: {}, libraryId: 'lib_m'});

async function album(answerTracks: (url: URL) => unknown = url => url.searchParams.get('cursor') === 'c2'
  ? {items: [track(2), track(3)], total: 3} : {items: [track(1)], nextCursor: 'c2', total: 3}) {
  const {NativeTVMusicController} = await import('../src-next/screens/destinations/music/model.ts');
  const seen: string[] = [];
  const answer = (path: string, method = 'GET') => {
    seen.push(`${method} ${path}`);
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/items' && url.searchParams.get('parent') === 'alb_1') return answerTracks(url);
    if (url.pathname === '/v1/items/alb_1') return {id: 'alb_1', kind: 'album', title: 'Walk Album', libraryId: 'lib_m', images: {}, parent: {id: 'art_1', kind: 'artist', title: 'Walker'}};
    if (url.pathname === '/v1/libraries/lib_m') return {id: 'lib_m', name: 'Music', kind: 'music', views: [{kind: 'artist'}, {kind: 'album'}, {kind: 'track'}], sorts: [], filters: []};
    throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
  };
  let active = true;
  const controller = new NativeTVMusicController({entry: {id: 'alb_1', libraryId: 'lib_m', kind: 'album', title: 'Walk Album'},
    api: {request: async (path: string, method?: string) => answer(path, method)}, similarScope: {},
    lifetime: {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => active}, requestId: async () => 'op'} as never);
  return {controller, seen, close: () => {active = false; controller.dispose();}};
}

test('an album opens with its tracks from the rebuilt server, in album order, and pages on', async () => {
  const f = await album();
  await f.controller.load();
  const state = f.controller.getSnapshot();
  assert.equal(state.phase, 'ready', String((state.error as Error | undefined)?.message));
  assert.deepEqual(state.songs.map(song => [song.id, song.kind]), [['itm_t1', 'song']]);
  assert.equal(state.songTotal, 3);
  assert.ok(f.seen.includes('GET /v1/items?parent=alb_1&sort=order&limit=200&total=true'), f.seen.join(' | '));
  assert.ok(!f.seen.some(request => request.startsWith('POST')), 'no old-server browse query is sent');
  await f.controller.nextSongs(state.songPage);
  assert.deepEqual(f.controller.getSnapshot().songs.map(song => song.id), ['itm_t1', 'itm_t2', 'itm_t3']);
  assert.ok(f.seen.includes('GET /v1/items?parent=alb_1&sort=order&cursor=c2&limit=200&total=true'), f.seen.join(' | '));
  f.close();
});

test('a whole-album action gets every track, following the pages', async () => {
  const f = await album();
  await f.controller.load();
  assert.deepEqual(await f.controller.albumMembership(), ['itm_t1', 'itm_t2', 'itm_t3']);
  f.close();
});

test('a whole-album action refuses a list that changed between its pages', async () => {
  const f = await album(url => url.searchParams.get('cursor') === 'c2' ? {items: [track(2)], total: 2} : {items: [track(1)], nextCursor: 'c2', total: 3});
  await f.controller.load();
  await assert.rejects(f.controller.albumMembership(), {code: 'album_membership_changed'});
  f.close();
});

test('an answer that is not a list of tracks is the songs read failing, not a crash', async () => {
  const f = await album(() => ({tracks: []}));
  await f.controller.load();
  const state = f.controller.getSnapshot();
  assert.equal(state.phase, 'error');
  assert.equal((state.error as {code?: string}).code, 'music_album_songs_unavailable');
  f.close();
});
