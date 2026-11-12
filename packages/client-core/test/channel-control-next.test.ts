import test from 'node:test';
import assert from 'node:assert/strict';
import {setTimeout as wait} from 'node:timers/promises';
import {V1ChannelControl, CHANNEL_NOT_PLAYABLE_HERE, type ChannelView} from '../src/playback-v1/channel-control.ts';
import type {V1Request} from '../src/playback-v1/http.ts';

// Watching a channel on the rebuilt server (definition 11, rules 22-29): POST /v1/playback/sessions {channel}, the session
// presented as the channel view the player reads.
const live = (patch: Record<string, unknown> = {}) => ({id: 'live_1', kind: 'live', state: 'playing', itemId: '', versionId: '', channel: {id: 'chn_1'},
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 0, mode: 'live', url: '/v1/live/streams/grant', startPositionMs: 0, engine: 'copy', route: 'original', quality: {}, subtitles: [], decision: {}},
  live: {oldestAt: '2026-11-12T20:00:00Z', liveAt: '2026-11-12T20:05:00Z'}, ...patch});
const title = (startsAt: string, endsAt: string, id = 'ses_1') => ({id, kind: 'vod', state: 'playing', itemId: 'itm_1', versionId: 'ver_1', channel: {id: 'chn_lib', startsAt, endsAt},
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'direct', url: '/v1/media/file', startPositionMs: 600_000, engine: 'native', route: 'direct', quality: {}, subtitles: [], decision: {}}});
const reference = (kind: 'live-source' | 'library-channel', channelId: string) => ({kind, sourceId: 'server', channelId, generation: '1', name: 'Harbour News'});

function control(answer: (request: V1Request) => {status: number; body?: unknown}, options: {plays?: (p: {mode: string; route: string}) => boolean; serverNow?: () => number} = {}) {
  const sent: V1Request[] = [];
  let n = 0;
  const c = new V1ChannelControl({key: () => `key-${++n}`, pollMs: 60_000, ...options, http: {send: async request => {sent.push(request); const a = answer(request); return {status: a.status, headers: {}, body: a.body};}}});
  const views: ChannelView[] = [], errors: [string, boolean][] = [];
  return {c, sent, views, errors, sink: {state: (v: ChannelView) => views.push(v), error: (m: string, t: boolean) => errors.push([m, t])}};
}

test('a live channel starts with only its id, and the engine joins the source\'s own stream directly from live', async () => {
  const f = control(r => r.method === 'POST' ? {status: 201, body: live()} : {status: 204});
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  try {
    assert.deepEqual(f.sent[0]!.body, {channel: 'chn_1', state: 'playing'});
    const view = f.views.at(-1)!;
    assert.equal(view.playbackId, 'live_1');assert.equal(view.linear.name, 'Harbour News');
    assert.deepEqual([view.linear.media.streamUrl, view.linear.media.mode, view.linear.media.bufferGeneration, view.linear.desired.seek?.positionUs], ['/v1/live/streams/grant', 'direct', '1', '0']);
    assert.deepEqual(f.errors, []);
  } finally {f.c.close();}
});

test('a stream this device cannot play is said plainly and its session is stopped at once, giving the tuner back', async () => {
  const f = control(r => r.method === 'POST' ? {status: 201, body: live()} : {status: 204}, {plays: p => !(p.mode === 'live' && p.route === 'original')});
  await f.c.open(reference('live-source', 'chn_1'), f.sink);await wait(5);
  assert.deepEqual(f.errors, [[CHANNEL_NOT_PLAYABLE_HERE, true]]);
  assert.deepEqual(f.views, []);
  assert.deepEqual(f.sent.map(r => [r.method, r.path]), [['POST', '/v1/playback/sessions'], ['DELETE', '/v1/playback/sessions/live_1']]);
});

test('a live HLS stream the server converted is played as HLS', async () => {
  const f = control(r => r.method === 'POST' ? {status: 201, body: live({presentation: {...live().presentation, url: '/v1/live/streams/grant/index.m3u8', route: 'repackaged', container: 'mpegts_hls'}})} : {status: 204},
    {plays: p => !(p.mode === 'live' && p.route === 'original')});
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  try {assert.equal(f.views.at(-1)?.linear.media.mode, 'hls');assert.equal(f.views.at(-1)?.linear.media.clock, 'dates');assert.deepEqual(f.errors, []);} finally {f.c.close();}
});

test('every tuner busy: the server\'s own words say what is using them', async () => {
  const f = control(() => ({status: 409, body: {error: {code: 'no_tuner', message: 'Every tuner is busy: A recording, Another viewer.', retry: false}}}));
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  assert.deepEqual(f.errors, [['Every tuner is busy: A recording, Another viewer.', true]]);
});

test('a Library Channel starts where the server says, seeks within its programme, and is tuned again when the programme ends', async () => {
  const now = Date.parse('2026-11-12T20:10:00Z');
  const ends = new Date(now + 60).toISOString();
  let posts = 0;
  // The first programme ends a moment from now; the next one an hour later.
  const endOf = (n: number) => n === 1 ? ends : new Date(now + 3_600_000).toISOString();
  const f = control(r => r.method === 'POST' ? {status: 201, body: title('2026-11-12T20:00:00Z', endOf(++posts), `ses_${posts}`)} : r.method === 'PATCH' ? {status: 200, body: title('2026-11-12T20:00:00Z', endOf(posts), `ses_${posts}`)} : {status: 204},
    {serverNow: () => now});
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    const first = f.views.at(-1)!;
    assert.deepEqual([first.linear.media.mode, first.linear.media.clock, first.linear.desired.seek?.positionUs, first.linear.media.originMs], ['direct', 'media', '600000000', Date.parse('2026-11-12T20:00:00Z')]);
    f.c.seek(120);await wait(5);
    const patch = f.sent.find(r => r.method === 'PATCH')!;
    assert.deepEqual((patch.body as {seek: {positionMs: number}}).seek.positionMs, 120_000);
    assert.equal(f.views.at(-1)!.linear.desired.seek?.positionUs, '120000000');
    await wait(150);
    assert.equal(posts, 2, 'the channel was tuned again for its next programme');
    assert.ok(f.sent.some(r => r.method === 'DELETE' && r.path === '/v1/playback/sessions/ses_1'), 'the finished programme\'s session was stopped');
    assert.equal(f.views.at(-1)!.playbackId, 'ses_2');
  } finally {f.c.close();}
});

test('Go to live on a live source joins its stream again from live', async () => {
  const f = control(r => r.method === 'POST' ? {status: 201, body: live()} : r.method === 'PATCH' ? {status: 200, body: live()} : {status: 204});
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  try {
    const before = f.views.at(-1)!.linear.media.bufferGeneration;
    f.c.goLive();await wait(5);
    assert.notEqual(f.views.at(-1)!.linear.media.bufferGeneration, before);
    assert.deepEqual(f.sent.filter(r => r.method === 'PATCH').map(r => r.body), [{state: 'playing'}]);
  } finally {f.c.close();}
});
