import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../src/index.ts';
import {V1QueuePlayer} from '../src/playback-v1/v1-queue-player.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';
import type {SegmentInput} from '../src/playback-v1/queue.ts';
import type {V1Request, V1Response} from '../src/playback-v1/http.ts';

const mixed: readonly SegmentInput[] = [{source: {items: {ids: ['first', 'second']}}}, {source: {container: {kind: 'album', id: 'release'}}}, {source: {items: {ids: ['last']}}}];
function setup(t: TestContext) {
  const server = new FakePlaybackServer({containerSize: () => 2}), inner = fakeHttp(server), log: V1Request[] = [];
  let serial = 0, pending = 0, intercept = async (_request: V1Request, response: V1Response): Promise<V1Response> => response;
  const http = {send: async (request: V1Request): Promise<V1Response> => { pending++; try { log.push(request); return await intercept(request, await inner.send(request as never) as V1Response); } finally { pending--; } }};
  const api = new V1PlaybackApi(http, {timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `segment-session-${++serial}`});
  const playback = new PlaybackService(api, () => `intent-${++serial}`, {deferRecovery: true});
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}, key: () => `segment-queue-${++serial}`});
  player.connect(); t.after(() => { playback.leave(); player.dispose(); });
  const settle = async () => {
    let count = -1;
    for (let turn = 0; turn < 20; turn++) {
      await new Promise(setImmediate);
      if (pending === 0 && count === log.length) return;
      count = log.length;
    }
    throw new Error('playback fixture did not settle');
  };
  return {server, log, player, playback, settle, intercept: (fn: typeof intercept) => { intercept = fn; }};
}
function gate() { let release!: () => void, enter!: () => void; const started = new Promise<void>(r => { enter = r; }), waiting = new Promise<void>(r => { release = r; }); return {started, release, hold: async () => { enter(); await waiting; }}; }

test('playSegments creates and adopts the exact ordered mixed selection and returns its actual header', async t => {
  const f = setup(t), queue = await f.player.playSegments(mixed);
  assert.equal(queue.id, f.player.getSnapshot().view!.queue.id);
  assert.equal(queue.total, 5);
  assert.equal(f.player.currentSession()!.queue!.queueId, queue.id);
  assert.equal(f.playback.getSnapshot().session!.id, f.player.currentSession()!.id);
  assert.deepEqual(f.player.getSnapshot().view!.queue.entries.map(e => e.hidden ? null : e.itemId), ['first', 'second', 'release-0', 'release-1', 'last']);
  const create = f.log.find(r => r.path === '/v1/queues' && r.method === 'POST')!;
  assert.deepEqual((create.body as {segments: unknown}).segments, mixed);
  assert.equal(f.log.some(r => r.path === '/v1/playback/sessions' && r.method === 'POST'), false, 'the create response supplies the actual session');
  const before = f.log.length;
  await f.player.playItem('first', 0);
  assert.equal(f.log.slice(before).some(r => r.path === '/v1/queues' && r.method === 'POST'), false, 'Replay retains the adopted mixed queue');
  assert.equal(f.player.currentSession()!.queue!.queueId, queue.id);
});

test('playSegments explicit position starts the real selected queue entry separately', async t => {
  const f = setup(t), queue = await f.player.playSegments(mixed, {startSeconds: 31.5});
  const start = f.log.find(r => r.path === '/v1/playback/sessions' && r.method === 'POST')!;
  assert.deepEqual(start.body, {queue: {queueId: queue.id, entryId: queue.current!.entryId}, state: 'playing', startPositionMs: 31500});
  assert.equal(f.playback.getSnapshot().pendingSeek!.positionSeconds, 31.5);
});

test('playSegments clears retry choices from a previous playback even when the first title is the same', async t => {
  const f = setup(t); await f.playback.play('first', 0);
  await f.playback.changePlayback({quality: {mode: 'fixed', id: '720p-high'}, audioTrackId: 'a2'});
  await f.player.playSegments(mixed);
  assert.deepEqual(f.playback.playbackChoices(), {});
  const create = f.log.filter(r => r.path === '/v1/queues' && r.method === 'POST').at(-1)!;
  assert.deepEqual((create.body as {startPlayback: unknown}).startPlayback, {state: 'playing'});
});

test('playSegments checks context after create and abandons its late session before any adoption', async t => {
  const f = setup(t), delayed = gate(); let current = true, sessionId = '';
  f.intercept(async (request, response) => {
    if (request.path === '/v1/queues' && request.method === 'POST') { sessionId = (response.body as {session: {id: string}}).session.id; await delayed.hold(); }
    return response;
  });
  const playing = f.player.playSegments(mixed, {isCurrent: () => current});
  const rejected = assert.rejects(playing, {name: 'AbortError'});
  await delayed.started; current = false; delayed.release(); await rejected;
  assert.equal(f.playback.getSnapshot().session, undefined);
  assert.equal(f.server.session(sessionId)!.ended, true);
});

test('playSegments rechecks context after queue observers immediately before media adoption', async t => {
  const f = setup(t); let current = true;
  const off = f.player.subscribe(() => { if (f.player.getSnapshot().view?.current) current = false; }); t.after(off);
  await assert.rejects(f.player.playSegments(mixed, {isCurrent: () => current}), {name: 'AbortError'});
  assert.equal(f.playback.getSnapshot().session, undefined);
  const stopped = f.log.filter(r => r.method === 'DELETE' && r.path.startsWith('/v1/playback/sessions/'));
  assert.equal(stopped.length, 1);
  assert.equal(f.server.session(decodeURIComponent(stopped[0]!.path.split('/').at(-1)!))!.ended, true);
});

test('playSegments rejects a create response for a different queue before media adoption', async t => {
  const f = setup(t);
  f.intercept(async (request, response) => {
    if (request.path !== '/v1/queues' || request.method !== 'POST') return response;
    const body = response.body as {session: {queue: object}};
    return {...response, body: {...body, session: {...body.session, queue: {...body.session.queue, queueId: 'other-queue'}}}};
  });
  await assert.rejects(f.player.playSegments(mixed), /queue playback session/);
  assert.equal(f.playback.getSnapshot().session, undefined);
});

for (const startSeconds of [undefined, 5]) test(`playSegments aborts a late ${startSeconds === undefined ? 'create' : 'start'} without replacing a newer Play`, async t => {
  const f = setup(t), delayed = gate(), abort = new AbortController(); let sessionId = '', once = true;
  f.intercept(async (request, response) => {
    const target = startSeconds === undefined ? '/v1/queues' : '/v1/playback/sessions';
    if (once && request.method === 'POST' && request.path === target) { once = false; sessionId = (startSeconds === undefined ? (response.body as {session: {id: string}}).session : response.body as {id: string}).id; await delayed.hold(); }
    return response;
  });
  const pending = f.player.playSegments(mixed, {startSeconds, signal: abort.signal});
  const rejected = assert.rejects(pending, {name: 'AbortError'});
  await delayed.started; abort.abort(); await f.player.playItem('replacement', 8);
  const accepted = f.playback.getSnapshot().session!.id;
  delayed.release(); await rejected;
  assert.equal(f.playback.getSnapshot().session!.id, accepted);
  assert.equal(f.playback.getSnapshot().itemId, 'replacement');
  assert.equal(f.server.session(sessionId)!.ended, true);
});

test('playSegments rejects an invalid or stale selection before any request', async t => {
  const f = setup(t), abort = new AbortController(); abort.abort();
  await f.settle();
  assert.deepEqual(f.log.map(r => [r.method, r.path]), [['GET', '/v1/listening/preferences']], 'connect loads real listening preferences before the operation');
  const before = f.log.length;
  await assert.rejects(f.player.playSegments([]));
  await assert.rejects(f.player.playSegments(mixed, {startSeconds: -1}));
  await assert.rejects(f.player.playSegments(mixed, {isCurrent: () => false}));
  await assert.rejects(f.player.playSegments(mixed, {signal: abort.signal}));
  assert.equal(f.log.length, before);
  assert.deepEqual(f.log.slice(before), [], 'invalid or stale selection dispatches no request of any kind');
});
