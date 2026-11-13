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
const reference = (kind: 'live-source' | 'library-channel', channelId: string) => ({kind, sourceId: 'server', channelId, generation: '1', name: 'Harbour News', logoUrl: '/v1/images/0123456789abcdef'});

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
    assert.equal(view.playbackId, 'live_1');assert.equal(view.linear.name, 'Harbour News');assert.equal(view.linear.media.logoUrl, '/v1/images/0123456789abcdef');
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
    // The server's time is pinned at the window's live moment, so the join target does not move with the real clock.
    {plays: p => !(p.mode === 'live' && p.route === 'original'), serverNow: () => Date.parse('2026-11-12T20:05:00Z')});
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  try {
    const view = f.views.at(-1)!;
    assert.equal(view.linear.media.mode, 'hls');assert.equal(view.linear.media.clock, 'dates');assert.deepEqual(f.errors, []);
    // Read by program dates from the stretch's oldest moment (20:00): joined at live (20:05), never at the oldest moment.
    assert.equal(view.linear.desired.seek?.positionUs, String(300_000 * 1000));
    assert.equal(view.linear.media.originMs, Date.parse('2026-11-12T20:00:00Z'));
  } finally {f.c.close();}
});

test('every tuner busy: the server\'s own words say what is using them', async () => {
  const f = control(() => ({status: 409, body: {error: {code: 'no_tuner', message: 'Every tuner is busy: A recording, Another viewer.', retry: false}}}));
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  assert.deepEqual(f.errors, [['Every tuner is busy: A recording, Another viewer.', true]]);
});

test('a Library Channel starts where the server says and seeks within its programme', async () => {
  const now = Date.parse('2026-11-12T20:10:00Z');
  const f = control(r => r.method === 'POST' ? {status: 201, body: title('2026-11-12T20:00:00Z', '2026-11-12T21:00:00Z')} : r.method === 'PATCH' ? {status: 200, body: title('2026-11-12T20:00:00Z', '2026-11-12T21:00:00Z')} : {status: 204},
    {serverNow: () => now});
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    const first = f.views.at(-1)!;
    assert.deepEqual([first.linear.media.mode, first.linear.media.clock, first.linear.desired.seek?.positionUs, first.linear.media.originMs], ['direct', 'media', '600000000', Date.parse('2026-11-12T20:00:00Z')]);
    f.c.seek(120);await wait(5);
    const patch = f.sent.find(r => r.method === 'PATCH')!;
    assert.deepEqual((patch.body as {seek: {positionMs: number}}).seek.positionMs, 120_000);
    assert.equal(f.views.at(-1)!.linear.desired.seek?.positionUs, '120000000');
  } finally {f.c.close();}
});

// The programme's end (11 rules 27 and 109, as the lead ruled): a viewer at live changes programme with the clock; one behind
// live keeps the programme they are in to its own end, then goes on at the start of the programme now on.
function programmeEnd(options: {lapsed?: boolean} = {}) {
  const start = Date.parse('2026-11-12T20:00:00Z'), end = start + 30 * 60_000;
  let clock = end - 150, starts = 0;
  const ses = (n: number, patch: Record<string, unknown> = {}) => ({...title(new Date(start + (n - 1) * 30 * 60_000).toISOString(), new Date(end + (n - 1) * 30 * 60_000).toISOString(), 'ses_' + n), ...patch});
  const f = control(r => {
    if (r.method === 'POST') {starts++; return {status: 201, body: ses(starts, {presentation: {...title('', '').presentation, startPositionMs: Math.max(0, clock - start - (starts - 1) * 30 * 60_000)}})};}
    if (r.method === 'PATCH') return options.lapsed && r.path.endsWith('ses_1') && (r.body as {state?: string}).state === 'playing' ? {status: 404, body: {error: {code: 'not_found'}}} : {status: 200, body: ses(starts, {state: (r.body as {state?: string}).state ?? 'playing'})};
    return {status: 204};
  }, {serverNow: () => clock});
  return {...f, start, end, setClock: (value: number) => {clock = value;}, starts: () => starts};
}

test('a viewer at live changes programme with the clock', async () => {
  const f = programmeEnd();
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    await f.c.observe((f.end - 150 - f.start) / 1000);
    f.setClock(f.end + 500);await wait(400);
    assert.equal(f.starts(), 2, 'tuned again for the next programme');
    assert.ok(f.sent.some(r => r.method === 'DELETE' && r.path === '/v1/playback/sessions/ses_1'));
    assert.equal(f.views.at(-1)!.playbackId, 'ses_2');
    assert.ok(!f.sent.some(r => r.method === 'PATCH' && r.path.endsWith('ses_2')), 'joined at live, not moved to the start');
  } finally {f.c.close();}
});

// Review desk A, #2957: a person pauses ten minutes before a film ends and comes back later: the film is still there.
test('a paused Library Channel is not thrown into the next programme when its programme ends on the clock', async () => {
  const f = programmeEnd();
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    f.c.pause();
    await wait(20);
    f.setClock(f.end + 500);
    await wait(400);
    assert.deepEqual(f.sent.filter(r => r.method === 'DELETE').map(r => r.path), [], 'the paused session is not stopped');
    assert.equal(f.starts(), 1, 'the channel is not tuned again under a paused viewer');
  } finally {f.c.close();}
});

test('a rewound viewer watches the programme to its own end, then goes on at the start of the programme now on', async () => {
  const f = programmeEnd();
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    await f.c.observe(600);
    f.setClock(f.end + 500);await wait(400);
    assert.equal(f.starts(), 1, 'the programme they are in keeps playing past its end on the clock');
    f.setClock(f.end + 5 * 60_000);
    await f.c.observe((f.end - f.start) / 1000);await wait(20);
    assert.equal(f.starts(), 2, 'at its end, the channel is tuned again');
    const view = f.views.at(-1)!;
    assert.deepEqual([view.playbackId, view.linear.desired.seek?.positionUs], ['ses_2', '0'], 'placed at the start of the programme now on');
    // The same channel tuned again is a new stream: the engine joins it rather than staying on the old one (which the server has stopped).
    const before = f.views.filter(v => v.playbackId === 'ses_1').at(-1)!;
    assert.notEqual(view.linear.media.bufferGeneration, before.linear.media.bufferGeneration, 'a new attachment');
    const placed = f.sent.find(r => r.method === 'PATCH' && r.path === '/v1/playback/sessions/ses_2')!;
    assert.deepEqual([(placed.body as {state: string}).state, (placed.body as {seek: {positionMs: number}}).seek.positionMs], ['playing', 0]);
  } finally {f.c.close();}
});

test('a viewer away past the end of a paused programme comes back at the start of the programme now on', async () => {
  const f = programmeEnd({lapsed: true});
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    f.c.pause();await wait(20);
    f.setClock(f.end + 20 * 60_000);await wait(400);
    assert.equal(f.starts(), 1, 'nothing changes while they are away');
    f.c.resume();await wait(50);
    assert.equal(f.starts(), 2, 'the lapsed session is replaced by a new tune');
    assert.equal(f.views.at(-1)!.linear.desired.seek?.positionUs, '0');
    assert.deepEqual((f.sent.filter(r => r.method === 'POST').at(-1)!.body as {state: string}).state, 'playing');
  } finally {f.c.close();}
});

test('Go to live behind a programme that has ended on the clock goes to live in the programme now on', async () => {
  const f = programmeEnd();
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  try {
    f.c.pause();await wait(20);
    f.setClock(f.end + 60_000);await wait(400);
    f.c.goLive();await wait(50);
    assert.equal(f.starts(), 2);
    assert.ok(!f.sent.some(r => r.method === 'PATCH' && r.path.endsWith('ses_2')), 'live, not the start');
    assert.equal(f.views.at(-1)!.linear.desired.seek?.positionUs, String(60_000 * 1000));
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

// Review desk A, #2957: a server converting as many streams as its owner allows refuses the start with 429
// conversion_capacity and its own sentence. That is not a connection fault, and the person should read the server's words.
test('a start refused because the server is converting all it may says so in the server\'s words', async () => {
  const sentence = 'This server is converting as many streams as it is allowed. Try again when one finishes.';
  const f = control(() => ({status: 429, body: {error: {code: 'conversion_capacity', message: sentence, retry: false}}}));
  await f.c.open(reference('library-channel', 'chn_lib'), f.sink);
  assert.deepEqual(f.errors.map(([m]) => m), [sentence]);
});

// Review desk A, #2957 at ff393475f. A person rewinds a converted live channel, watches for twenty minutes and presses Go
// to live (11 rule 26): live is NOW, twenty minutes past where it was when the session was last read.
test('Go to live on a converted live channel goes to live now, not to where live was when the session was read', async () => {
  const oldest = Date.parse('2026-11-12T20:00:00Z'), liveAtStart = Date.parse('2026-11-12T20:05:00Z');
  const hls = () => live({presentation: {...live().presentation, url: '/v1/live/streams/grant/index.m3u8', route: 'converted', container: 'fmp4_hls'},
    live: {oldestAt: new Date(oldest).toISOString(), liveAt: new Date(liveAtStart).toISOString()}});
  let clock = liveAtStart;
  const f = control(r => r.method === 'POST' ? {status: 201, body: hls()} : r.method === 'PATCH' ? {status: 200, body: hls()} : {status: 204}, {serverNow: () => clock});
  await f.c.open(reference('live-source', 'chn_1'), f.sink);
  try {
    assert.equal(f.views.at(-1)!.linear.desired.seek?.positionUs, String((liveAtStart - oldest) * 1000), 'it joins at live');
    clock = liveAtStart + 20 * 60_000;
    f.c.goLive();
    await wait(20);
    const target = Number(f.views.at(-1)!.linear.desired.seek?.positionUs) / 1000;
    assert.ok(target >= clock - oldest - 5_000, `Go to live asked for ${Math.round((clock - oldest - target) / 60_000)} minutes behind live`);
  } finally {f.c.close();}
});
