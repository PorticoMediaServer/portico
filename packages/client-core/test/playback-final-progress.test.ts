import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/http.ts';
import type {LocalSession} from '../src/index.ts';
import type {OutboxEntry} from '../src/outbox.ts';
import {outboxBody, maxEditAgeMs} from '../src/outbox-routes.ts';
import {localV1Http} from '../src/playback-v1/local-http.ts';
import {V1PlaybackApi} from '../src/playback-v1/legacy-bridge.ts';
import {PlaybackSessionController, SessionsClient} from '../src/playback-v1/sessions.ts';
import type {MediaPlayer, PlayerEvent} from '../src/playback-v1/port.ts';
import {parseSession} from '../src/playback-v1/types.ts';

const viewer = {accessToken: 'token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z',
  tokenGeneration: '1', sessionFamilyId: 'family', viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'member'}} as LocalSession;
const session = {id: 'pbs_play', kind: 'vod', state: 'playing', itemId: 'itm_film', versionId: 'med_film', progressToken: 'receipt-for-this-play',
  lease: {reportEveryMs: 10_000}, presentation: {generation: 1, mode: 'stream', url: '/v1/media/play/master.m3u8', startPositionMs: 0,
    engine: 'native', route: 'converted', quality: {mode: 'original'}, subtitles: [], decision: {}}};
const settle = async () => {for (let n = 0; n < 20; n++) await new Promise(setImmediate);};

// PB-01: an old offline stop must keep its observation age, so it cannot overwrite a newer device.
// The same receipt must survive app restart, since the server may already have forgotten the session.
for (const owner of ['controller', 'native bridge'] as const) test(`${owner} keeps the final receipt, position and played time through outbox restart`, async t => {
  let now = 1_000_000;
  t.mock.method(Date, 'now', () => now);
  const timers = {now: () => now, setTimer: () => 0, clearTimer: () => {}};
  const kept = new Map<string, OutboxEntry>();
  const store = {list: async () => [...kept.values()], put: async (entry: OutboxEntry) => {kept.set(entry.id, structuredClone(entry));},
    delete: async (id: string) => {kept.delete(id);}};
  const sent: unknown[] = [];
  let online = false, finishReport: ((response: Response) => void) | undefined;
  const api = new HttpLocalApi('https://server.test', 'token', async (_input, init) => {
    if (init?.method === 'POST') return Response.json(session, {status: 201});
    if (init?.method === 'PUT') return new Promise(resolve => {finishReport = resolve;});
    assert.equal(init?.method, 'DELETE');
    sent.push(JSON.parse(init!.body as string));
    if (!online) throw new TypeError('offline');
    return new Response(null, {status: 204});
  });
  api.setOutboxSession(viewer);
  let outbox = api.configureOutbox(store);
  const http = localV1Http(api);
  let controller: PlaybackSessionController | undefined, bridge: V1PlaybackApi | undefined;
  try {
    let stopping: Promise<void>;
    if (owner === 'controller') {
      let event: ((e: PlayerEvent) => void) | undefined;
      const player: MediaPlayer = {load() {}, play() {}, pause() {}, seek() {}, setRate() {}, selectSidecarSubtitle() {}, dispose() {},
        observe: () => ({generation: 1, state: 'playing', positionMs: 600_000, rate: 1}), onEvent(fn) {event = fn; return () => {event = undefined;};}};
      controller = new PlaybackSessionController({http, player, timers});
      assert.equal((await controller.start({itemId: 'itm_film'}))?.progressToken, session.progressToken);
      event!({type: 'time', observation: player.observe()});
      now += 3000;
      stopping = controller.stop();
      // Its final heartbeat is slow; that wait belongs to the final observation's age too.
      now += 2000;
      finishReport!(Response.json({error: {code: 'busy'}}, {status: 503}));
    } else {
      bridge = new V1PlaybackApi(http, {timers, now: () => now});
      // Queue commit and session reads share this parser and adoption path.
      bridge.adopt(parseSession({...session, kind: 'audio'}), 7_200);
      await bridge.progressPlayback(session.id, {generation: 1, sequence: 1, positionSeconds: 600, state: 'playing'});
      now += 3000;
      stopping = bridge.stopPlayback(session.id);
    }
    await stopping;
    const age = owner === 'controller' ? 5000 : 3000;
    const final = {progressToken: session.progressToken, positionMs: 600_000, madeAgoMs: age, playClockMs: 3000};
    assert.deepEqual(sent, [final]);
    assert.equal(kept.size, 1);
    assert.deepEqual([...kept.values()][0]!.body, final, 'the durable body retains the original receipt and observation');
    outbox.dispose();
    controller?.dispose();
    now += 180_000; // Beyond the server's session lease, then restart the app's outbox.
    online = true;
    outbox = api.configureOutbox(store);
    await settle();
    assert.deepEqual(sent, [final, {...final, madeAgoMs: age + 180_000}]);
    assert.equal(kept.size, 0, '204 acknowledges the final observation even after the session is gone');
  } finally {controller?.dispose(); bridge?.forget(session.id); outbox.dispose();}
});

test('outbox replay ages only receipt-bearing stops, without changing or repeatedly aging their stored body', () => {
  const body = {progressToken: 'original-play', positionMs: 630_000, madeAgoMs: 500, playClockMs: 30_000};
  const entry = {method: 'DELETE', path: '/v1/playback/sessions/pbs_play', createdAt: 1000, body};
  assert.deepEqual(outboxBody(entry, 2000), {...body, madeAgoMs: 1500});
  assert.deepEqual(outboxBody(entry, 4000), {...body, madeAgoMs: 3500});
  assert.equal(body.madeAgoMs, 500);
  assert.deepEqual(outboxBody(entry, 500), body, 'a clock moving backward cannot make an observation newer');
  assert.deepEqual(outboxBody(entry, maxEditAgeMs * 2), {...body, madeAgoMs: maxEditAgeMs});
  const legacy = {positionMs: 630_000};
  assert.deepEqual(outboxBody({...entry, body: legacy}, 4000), legacy);
});

test('stops without a receipt keep the immediate legacy and Live TV request shape', async () => {
  const sent: unknown[] = [];
  const client = new SessionsClient({send: async request => {sent.push(request.body); return {status: 204, headers: {}};}});
  await client.stop('pbs_legacy', 1234.5);
  await client.stop('pbs_live');
  assert.deepEqual(sent, [{positionMs: 1235}, undefined]);
  assert.equal(parseSession({...session, progressToken: undefined}).progressToken, undefined);
});
