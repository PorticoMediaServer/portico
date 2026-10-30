import assert from 'node:assert/strict';
import test from 'node:test';
import {setImmediate as settle} from 'node:timers/promises';
import type {HttpLocalApi, LocalSession} from '../../packages/client-core/src/index.ts';
import {V1ChannelControl, type OriginalChannelStart} from '../../packages/client-core/src/playback-v1/channel-control.ts';
import {parseStartPreparation, type Timers} from '../../packages/client-core/src/playback-v1/sessions.ts';
import type {V1Request} from '../../packages/client-core/src/playback-v1/http.ts';
import {ownedWebChannels} from '../src/platform/playback/channel-start-binding.ts';
import {createWebPreparingStarts} from '../src/platform/playback/start-owner.ts';

// Authored against the real Web binding and canonical SDK. Only the original
// fake API and documented decoders are used; no network, browser, tuner,
// actual timer or server preparation job.
// The accepted channel 201 shape: an active linear session observed after the
// producer's buffer exists, with its own canonical linear media URL
// (`/v1/media/linear/<id>/<generation>/<token>/index.m3u8`).
const real = (id = 'original_channel') => ({variant: 'session', id, revision: '1', kind: 'live', role: 'local', state: 'playing', channelId: 'live:source:station',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'stream', engine: 'native', url: `/v1/media/linear/${id}/1/original-grant/index.m3u8`, startPositionMs: 0,
    quality: {mode: 'original'}, subtitles: [], decision: {},
    linear: {name: 'Station', originMs: 0, windowStartMs: 0, windowEndMs: 120_000, liveEdgeMs: 60_000, state: 'active'}}});
const response = (body: unknown, status = 202) => ({body, status, headers: {}});
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(yes => {resolve = yes;});return {promise, resolve};}
function fixture() {
  let principal = true, view = true;
  const session: LocalSession = {accessToken: 'synthetic', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z',
    tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'device', installationId: 'installation',
    viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}};
  const route = {pin: {serverId: 'server', publicKey: 'pin'}, getSnapshot: () => ({phase: 'ready'})};
  const calls: {method: string; path: string; signal?: AbortSignal}[] = [], reads: Promise<unknown>[] = [], stops: Promise<unknown>[] = [];
  const api = {baseUrl: 'https://original.invalid', getRouteConnection: () => route,
    requestRaw: async (path: string, method: string, options: {signal?: AbortSignal}) => {
      calls.push({path, method, signal: options.signal});
      return response(method === 'GET' ? await (reads.shift() ?? Promise.resolve(real())) : await (stops.shift() ?? Promise.resolve(undefined)), method === 'GET' ? 200 : 204);
    }} as unknown as HttpLocalApi;
  const owner = createWebPreparingStarts(api, session, () => session, () => principal)!, reg = owner.attach();
  const original = (key = 'channel-key'): OriginalChannelStart => Object.freeze({body: Object.freeze({channelId: 'live:source:station', channelGeneration: 'original-guide', state: 'playing' as const}),
    key, signal: new AbortController().signal, isCurrent: () => view});
  return {owner, reg, original, calls, reads, stops, route, retireView: () => {view = false;}, retirePrincipal: () => {principal = false;}, current: () => view};
}

test('genuine active channel 201 receipt owns only the original channel read and once-only bounded stop', async () => {
  const f = fixture(), start = f.reg.beginChannel(f.original(), f.current);
  try {
    f.retireView();assert.equal(start.isCurrent(), false);
    const cleanup = Promise.resolve(start.retireUninstalled(Object.freeze({id: 'original_channel', generation: 1})));
    await cleanup;
    await start.retireUninstalled(Object.freeze({id: 'original_channel', generation: 1}));start.finish();
    assert.deepEqual(f.calls.map(call => [call.method, call.path]), [['GET', '/v1/playback/sessions/original_channel'], ['DELETE', '/v1/playback/sessions/original_channel']]);
    assert.equal(f.calls[0]!.signal, f.calls[1]!.signal);
  } finally {f.reg.dispose();f.owner.dispose();}
});

test('late real success facts are read under original channel custody, never converted to a Session', async () => {
  const f = fixture(), original = f.original(), start = f.reg.beginChannel(original, f.current);
  try {
    f.retireView();await start.retireUninstalled(Object.freeze({id: 'original_channel', generation: 1}));start.finish();
    assert.deepEqual(f.calls.map(call => call.method), ['GET', 'DELETE']);
    assert.ok(Object.isFrozen(original.body));assert.equal(original.body.channelGeneration, 'original-guide');
  } finally {f.reg.dispose();f.owner.dispose();}
});

test('a changed actual generation, channel, kind or ownership refuses cleanup', async () => {
  for (const changed of [
    {...real(), presentation: {...real().presentation, generation: 2}}, {...real(), channelId: 'live:other:station'},
    {...real(), role: 'receiver'}, {...real(), kind: 'vod'}, {...real(), state: 'ended'},
    {...real(), queue: {queueId: 'queue', entryId: 'entry'}}, {...real(), itemId: 'movie'},
  ]) {
    const f = fixture(), start = f.reg.beginChannel(f.original(), f.current);f.reads.push(Promise.resolve(changed));
    try {await start.retireUninstalled({id: 'original_channel', generation: 1});start.finish();assert.deepEqual(f.calls.map(call => call.method), ['GET']);}
    finally {f.reg.dispose();f.owner.dispose();}
  }
});

test('a first actual 201 receipt can never be replaced by late facts for another id or generation', async () => {
  const f = fixture(), start = f.reg.beginChannel(f.original(), f.current);
  try {
    const cleanup = Promise.resolve(start.retireUninstalled(Object.freeze({id: 'original_channel', generation: 1})));
    assert.equal(start.retireUninstalled(Object.freeze({id: 'other', generation: 1})), undefined);
    assert.equal(start.retireUninstalled(Object.freeze({id: 'original_channel', generation: 2})), undefined);
    await cleanup;start.finish();assert.deepEqual(f.calls.map(call => call.method), ['GET', 'DELETE']);
  } finally {f.reg.dispose();f.owner.dispose();}
});

test('an installed then forgotten channel permanently denies its older original receipt', async () => {
  const f = fixture(), next = f.owner.attach(), start = f.reg.beginChannel(f.original(), f.current);
  try {
    next.remember('original_channel');next.forget('original_channel');
    await start.retireUninstalled(Object.freeze({id: 'original_channel', generation: 1}));start.finish();assert.equal(f.calls.length, 0);
  } finally {next.dispose();f.reg.dispose();f.owner.dispose();}
});

test('installation while the original channel GET is held aborts that cleanup before stop', async () => {
  const f = fixture(), read = deferred<unknown>(), next = f.owner.attach();f.reads.push(read.promise);
  const start = f.reg.beginChannel(f.original(), f.current);
  try {
    const pending = Promise.resolve(start.retireUninstalled({id: 'original_channel', generation: 1}));
    const rejected = assert.rejects(pending, {name: 'AbortError'});await settle();next.remember('original_channel');read.resolve(real());await rejected;start.finish();
    assert.deepEqual(f.calls.map(call => call.method), ['GET']);assert.equal(f.calls[0]!.signal?.aborted, true);
  } finally {next.dispose();f.reg.dispose();f.owner.dispose();}
});

test('original principal or pin retirement refuses even a real late channel resource', async () => {
  for (const retire of [(f: ReturnType<typeof fixture>) => f.retirePrincipal(), (f: ReturnType<typeof fixture>) => {f.route.pin.publicKey = 'replacement';}]) {
    const f = fixture(), start = f.reg.beginChannel(f.original(), f.current);
    try {retire(f);await start.retireUninstalled({id: 'original_channel', generation: 1});start.finish();assert.equal(f.calls.length, 0);}
    finally {f.reg.dispose();f.owner.dispose();}
  }
});

function controlFixture() {
  const f = fixture(), caller = new AbortController(), calls: V1Request[] = [], jobs = new Map<unknown, () => void>();
  const timers: Timers = {setTimer(fn) {const id = {};jobs.set(id, fn);return id;}, clearTimer(id) {jobs.delete(id);}};
  const replies: Promise<ReturnType<typeof response>>[] = [];
  const control = new V1ChannelControl({key: () => 'actual-channel-key', timers, http: {send: async request => {
    calls.push(request);
    if (request.method === 'DELETE') return {status: 204, headers: {}};
    return await (replies.shift() ?? Promise.resolve(response({variant: 'wait', retryAfterMs: 500})));
  }}});
  const installed: string[] = [];
  const bound = ownedWebChannels(control, f.reg, () => f.current);
  const start = (onError: (message: string, terminal: boolean) => void = () => {assert.fail('no successor error');}) =>
    bound.open({kind: 'live-source', sourceId: 'source', channelId: 'station', generation: 'original-guide'},
      {state: value => {installed.push(value.playbackId);}, error: onError},
      {signal: caller.signal, isCurrent: f.current});
  return {...f, cleanupCalls: f.calls, control, caller, calls, jobs, replies, installed, bound, start};
}

test('actual channel SDK retry-only 202 pending wait then wait-abort spends no resource', async () => {
  const f = controlFixture();
  try {const pending = f.start();await settle();assert.equal(f.jobs.size, 1);f.caller.abort();await pending;await settle();
    assert.deepEqual(f.calls.map(call => call.method), ['POST']);assert.equal(f.jobs.size, 0);assert.equal(f.installed.length, 0);
    assert.equal(f.cleanupCalls.length, 0, 'a retry-only preparation owns no resource to retire');
    assert.deepEqual(f.calls[0]!.body, {channelId: 'live:source:station', channelGeneration: 'original-guide', state: 'playing'});
    assert.deepEqual(f.calls[0]!.headers, {'Idempotency-Key': 'actual-channel-key'});
  } finally {f.control.close();f.reg.dispose();f.owner.dispose();}
});

test('actual channel SDK retry-only abort has no resource or cleanup', async () => {
  const f = controlFixture();
  try {const pending = f.start();await settle();f.caller.abort();await pending;await settle();assert.equal(f.jobs.size, 0);assert.equal(f.calls.length, 1);assert.equal(f.cleanupCalls.length, 0);}
  finally {f.control.close();f.reg.dispose();f.owner.dispose();}
});

test('an impossible live 202 is refused by the strict start decoder without install, timer or cleanup', async () => {
  const f = controlFixture(), refused: {message: string; terminal: boolean}[] = [];
  f.replies.push(Promise.resolve(response({...real(), retryAfterMs: 500})));
  try {
    assert.throws(() => parseStartPreparation({...real(), retryAfterMs: 500}), {name: 'ContractError'});
    await f.start((message, terminal) => {refused.push({message, terminal});});await settle();
    assert.deepEqual(f.calls.map(call => call.method), ['POST']);assert.equal(f.installed.length, 0);
    assert.equal(f.jobs.size, 0);assert.equal(f.cleanupCalls.length, 0);
    assert.equal(refused.length, 1);assert.equal(refused[0]!.terminal, true);assert.ok(refused[0]!.message.length > 0);
  } finally {f.control.close();f.reg.dispose();f.owner.dispose();}
});

test('actual channel SDK late201 after caller abort uses only its original host GET and DELETE', async () => {
  const f = controlFixture(), held = deferred<ReturnType<typeof response>>();f.replies.push(held.promise);
  try {const pending = f.start();await settle();f.caller.abort();held.resolve(response(real(), 201));await pending;await settle();
    assert.deepEqual(f.calls.map(call => call.method), ['POST']);assert.equal(f.jobs.size, 0);assert.equal(f.installed.length, 0);
    assert.deepEqual(f.cleanupCalls.map(call => call.method), ['GET', 'DELETE']);assert.equal(f.cleanupCalls.length, 2, 'one bounded cleanup for the one actual receipt');
    assert.deepEqual(f.calls[0]!.body, {channelId: 'live:source:station', channelGeneration: 'original-guide', state: 'playing'});
    assert.deepEqual(f.calls[0]!.headers, {'Idempotency-Key': 'actual-channel-key'});
  } finally {f.control.close();f.reg.dispose();f.owner.dispose();}
});


test('the public pre-install claim registers a genuine accepted channel before service publication', async () => {
  const f = controlFixture();
  // The genuine active 201 carries media, so the canonical go-live join lands
  // before the projection publishes; the registration precedes both.
  f.replies.push(Promise.resolve(response(real(), 201)), Promise.resolve(response(real(), 200)));
  try {await f.start();await settle();assert.deepEqual(f.installed, ['original_channel']);
    // The successful resource is remembered before an older receipt can try
    // to acquire and retire this same id, even without media attachment yet.
    const later = f.reg.beginChannel(f.original('later-key'), f.current);
    await later.retireUninstalled({id: 'original_channel', generation: 1});later.finish();assert.equal(f.cleanupCalls.length, 0);
    f.control.close();await settle();
    const stops = f.calls.filter(call => call.method === 'DELETE');
    assert.equal(stops.length, 1, 'the installed channel retires exactly once');
    assert.equal(stops[0]!.path, '/v1/playback/sessions/original_channel');
    assert.deepEqual(stops[0]!.body, {generation: 1}, 'normal teardown retains the exact installed generation');
  } finally {f.control.close();f.reg.dispose();f.owner.dispose();}
});

test('a cleanup-claimed resource refuses canonical install before any state or go-live PATCH', async () => {
  const f = controlFixture(), stopping = deferred<unknown>();f.stops.push(stopping.promise);
  const original = f.reg.beginChannel(f.original('cleanup-key'), f.current);
  try {
    const cleanup = Promise.resolve(original.retireUninstalled({id: 'original_channel', generation: 1}));await settle();
    assert.deepEqual(f.cleanupCalls.map(call => call.method), ['GET', 'DELETE']);
    f.replies.push(Promise.resolve(response(real(), 201)));await f.start();assert.equal(f.installed.length, 0);
    assert.deepEqual(f.calls.map(call => call.method), ['POST']);
    stopping.resolve(undefined);await cleanup;original.finish();
  } finally {stopping.resolve(undefined);f.control.close();f.reg.dispose();f.owner.dispose();}
});

test('a real installed channel denies earlier custody before its joined media need publish', async () => {
  const f = fixture(), earlier = f.reg.beginChannel(f.original('earlier-key'), f.current), current = f.reg.beginChannel(f.original('new-key'), f.current);
  try {
    assert.equal(current.claimInstall(Object.freeze({id: 'original_channel', generation: 1})), true);
    await earlier.retireUninstalled(Object.freeze({id: 'original_channel', generation: 1}));
    earlier.finish();current.finish();assert.equal(f.calls.length, 0);
  } finally {f.reg.dispose();f.owner.dispose();}
});

test('an ownerless Web binding truthfully refuses late cleanup and never invokes legacy raw DELETE', async () => {
  const held = deferred<ReturnType<typeof response>>(), caller = new AbortController(), methods: string[] = [];
  const control = new V1ChannelControl({key: () => 'ownerless-key', http: {send: async request => {methods.push(request.method);return held.promise;}}});
  const bound = ownedWebChannels(control, undefined, () => () => !caller.signal.aborted);
  try {
    const pending = bound.open({kind: 'live-source', sourceId: 'source', channelId: 'station', generation: 'original-guide'},
      {state: () => assert.fail('retired state'), error: () => assert.fail('retired error')}, {signal: caller.signal});
    await settle();caller.abort();held.resolve(response(real(), 201));await pending;await settle();assert.deepEqual(methods, ['POST']);
  } finally {control.close();}
});

test('E-web-25: claimInstall accepts a Live start answered at generation 0 and refuses negative or non-integer generations', () => {
  const f = fixture();
  try {
    for (const generation of [-1, 0.5, Number.NaN]) {
      const start = f.reg.beginChannel(f.original(`bad-${generation}`), f.current);
      assert.equal(start.claimInstall(Object.freeze({id: 'original_channel', generation})), false, `generation ${generation} is refused`);
      start.finish();
    }
    const start = f.reg.beginChannel(f.original('gen0-key'), f.current);
    assert.equal(start.claimInstall(Object.freeze({id: 'original_channel', generation: 0})), true, 'generation 0 installs');
    start.finish();
  } finally {f.reg.dispose();f.owner.dispose();}
});
