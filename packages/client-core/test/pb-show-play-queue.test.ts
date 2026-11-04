/**
 * PB-1, MU-1, MU-2. A title page's Play (and a song row, and a book's Resume) plays one run of what
 * the item belongs to: the queue is that container anchored on the pressed item, so the Episodes
 * drawer, Next, autoplay and the next part are jumps inside it. The container comes from the
 * server (`PlaybackOptions.container`) through `playItem`'s `from`.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import {parseOptions} from '../src/playback-v1/types.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {V1Http, V1Request} from '../src/playback-v1/http.ts';

async function fixture(kind = 'episode', musicDefaults?: () => {shuffle: boolean; repeat: 'off' | 'one' | 'all'}) {
  const server = new FakePlaybackServer({itemKind: () => kind, containerSize: () => 3});
  const inner = fakeHttp(server), calls: V1Request[] = [];
  const http: V1Http = {send: async request => {calls.push(request as V1Request); return inner.send(request as never) as never;}};
  let serial = 0;
  const api = new V1PlaybackApi(http, {key: () => `api-${++serial}`});
  const playback = new PlaybackService(api, () => `run-${++serial}`);
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}, key: () => `key-${++serial}`, ...(musicDefaults ? {musicDefaults} : {})});
  const stop = player.connect();
  const creates = () => calls.filter(call => call.method === 'POST' && call.path === routePath('create_queue')).map(call => (call.body as {segments: unknown[]}).segments);
  return {server, calls, player, playback, stop, creates};
}

test('PB-1: Play on an episode queues its show anchored on it, so the Episodes drawer and Next reach the rest', async () => {
  const f = await fixture();
  try {
    await f.player.playItem('show-0', undefined, undefined, undefined, undefined, undefined, {from: {container: {kind: 'show', id: 'show'}}});
    assert.deepEqual(f.creates(), [[{source: {container: {kind: 'show', id: 'show'}}, anchor: {itemId: 'show-0'}}]]);
    assert.equal(f.player.currentSession()?.itemId, 'show-0');
    assert.equal(f.player.getSnapshot().view?.next.available, true, 'Next and autoplay have a successor');
    // The drawer's next row is an entry of this run: a jump, not a 404.
    await f.player.selectEpisode('show-1');
    assert.equal(f.player.currentSession()?.itemId, 'show-1');
    assert.equal(f.creates().length, 1, 'the same run, no new queue');
  } finally {f.stop(); f.player.dispose();}
});

test('W1-A1: a special the show run leaves out joins the run as the next entry and plays, in the same playback', async () => {
  const f = await fixture();
  try {
    await f.player.playItem('show-0', undefined, undefined, undefined, undefined, undefined, {from: {container: {kind: 'show', id: 'show'}}});
    const run = f.playback.getSnapshot().playbackRun;
    await f.player.selectEpisode('special-1');
    assert.equal(f.player.currentSession()?.itemId, 'special-1', 'the special plays');
    assert.equal(f.creates().length, 1, 'no new queue');
    assert.equal(f.playback.getSnapshot().playbackRun, run, 'the same run, so its choices carry on');
    assert.ok(f.calls.some(call => call.method === 'POST' && call.path.endsWith('/segments') && (call.body as {placement?: string}).placement === 'next'), 'added as the next entry');
    // An ordinary queued pick never adds anything.
    const segments = f.calls.filter(call => call.path.endsWith('/segments')).length;
    await f.player.selectEpisode('show-1');
    assert.equal(f.calls.filter(call => call.path.endsWith('/segments')).length, segments);
    // Only the drawer's pick joins the run: other jumps keep refusing an item the queue doesn't hold.
    await assert.rejects(f.player.selectQueuedItem('special-2'), (error: {status?: number}) => error.status === 404);
  } finally {f.stop(); f.player.dispose();}
});

test('PB-1: an explicit start position and version still travel with the anchored queue', async () => {
  const f = await fixture();
  try {
    await f.player.playItem('show-1', 42, undefined, undefined, undefined, undefined, {from: {container: {kind: 'show', id: 'show'}}});
    assert.deepEqual(f.creates(), [[{source: {container: {kind: 'show', id: 'show'}}, anchor: {itemId: 'show-1'}}]]);
    const start = f.calls.find(call => call.method === 'POST' && call.path === routePath('start_playback_session'));
    assert.equal((start?.body as {startPositionMs?: number}).startPositionMs, 42_000);
    assert.equal(f.player.currentSession()?.itemId, 'show-1');
  } finally {f.stop(); f.player.dispose();}
});

test('MU-1: a pressed song starts its album with the music defaults, the song first', async () => {
  const f = await fixture('song', () => ({shuffle: true, repeat: 'all'}));
  try {
    await f.player.playItem('album-2', undefined, undefined, undefined, undefined, undefined, {from: {container: {kind: 'album', id: 'album'}}});
    assert.deepEqual(f.creates(), [[{source: {container: {kind: 'album', id: 'album'}}, anchor: {itemId: 'album-2'}, order: {mode: 'shuffle'}}]]);
    assert.equal(f.player.currentSession()?.itemId, 'album-2');
    assert.ok(f.calls.some(call => call.method === 'PATCH' && (call.body as {repeat?: string}).repeat === 'all'), 'repeat default applied');
  } finally {f.stop(); f.player.dispose();}
});

test('a title that stands alone is still one entry', async () => {
  const f = await fixture('movie');
  try {
    await f.player.playItem('m1');
    assert.deepEqual(f.creates(), [[{source: {items: {ids: ['m1']}}}]]);
  } finally {f.stop(); f.player.dispose();}
});

test('playback options carry the server container; unknown kinds are dropped', () => {
  const base = {itemId: 'e1', kind: 'episode', versions: [], chapters: [], markers: [], qualities: [], alternatives: []};
  assert.deepEqual(parseOptions({...base, container: {kind: 'show', id: 's1'}}).container, {kind: 'show', id: 's1'});
  assert.deepEqual(parseOptions({...base, container: {kind: 'book', id: 'b1'}}).container, {kind: 'book', id: 'b1'});
  assert.equal(parseOptions({...base, container: {kind: 'playlist', id: 'p1'}}).container, undefined);
  assert.equal(parseOptions(base).container, undefined);
});
