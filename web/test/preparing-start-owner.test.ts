import assert from 'node:assert/strict';
import test from 'node:test';
import {setImmediate as settle} from 'node:timers/promises';
import type {HttpLocalApi, LocalSession} from '../../packages/client-core/src/index.ts';
import {SessionsClient, parseStartPreparation, type StartOptions, type Timers} from '../../packages/client-core/src/playback-v1/sessions.ts';
import type {V1Request} from '../../packages/client-core/src/playback-v1/http.ts';
import {parseSession} from '../../packages/client-core/src/playback-v1/types.ts';
import {WebPreparingStarts} from '../src/platform/playback/preparing-start.ts';
import {createWebPreparingStarts} from '../src/platform/playback/start-owner.ts';
import {WebPlayback} from '../src/platform/playback/web-playback.ts';

// Authored UNRUN against the real Web binding and canonical public SDK. No
// network, session storage, browser, device, engine or actual timer is used.
const real = () => ({variant: 'session', id: 'original_session', revision: '1', kind: 'vod', role: 'local', state: 'paused', itemId: 'movie',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'stream', engine: 'native', url: '/v1/media/grant-original_session/1/master.m3u8', startPositionMs: 0,
    quality: {mode: 'original'}, subtitles: [], decision: {video: {action: 'copy', reasons: []}, audio: {action: 'copy', reasons: []}}}});
const preparing = () => parseStartPreparation({...real(), retryAfterMs: 500}).session!;
const response = (body: unknown, status = 202) => ({status, headers: {}, body});
function deferred<T>() {let resolve!: (value: T) => void;
  const promise = new Promise<T>(yes => {resolve = yes;});return {promise, resolve};}
function clock() {
  let id = 0, at = 0;
  const jobs = new Map<number, {fn: () => void; ms: number}>();
  const timers: Timers = {now: () => at, setTimer: (fn, ms) => {jobs.set(++id, {fn, ms});return id;}, clearTimer: timer => {jobs.delete(timer as number);}};
  return {jobs, timers, fire() {const job = jobs.entries().next().value;assert.ok(job);jobs.delete(job[0]);at += job[1].ms;job[1].fn();}};
}
function fixture() {
  let principal = true, opening = {live: true};
  const stops: {id: string; generation: number; signal: AbortSignal}[] = [], calls: V1Request[] = [], c = clock();
  const owner = new WebPreparingStarts({isCurrent: () => principal, stop: async (session, signal, uninstalled) => {
    assert.ok(uninstalled());stops.push({id: session.id, generation: session.presentation.generation, signal});
  }});
  const replies: Promise<ReturnType<typeof response>>[] = [];
  const web = new WebPlayback({preparingStarts: owner, captureStartCurrent: () => {const original = opening;return () => original.live;},
    http: {send: async request => {calls.push(request);return await (replies.shift() ?? Promise.resolve(response({...real(), retryAfterMs: 500})));}},
    mediaUrl: path => path, timers: c.timers});
  return {owner, web, stops, calls, replies, c, retireView: () => {opening.live = false;opening = {live: true};}, retirePrincipal: () => {principal = false;}};
}

test('current genuine 202 then wait abort spends only the original uninstalled receipt once', async () => {
  const f = fixture(), caller = new AbortController();
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60, caller.signal);
    const rejected = assert.rejects(pending, {name: 'AbortError'});
    await settle();assert.equal(f.c.jobs.size, 1);caller.abort();await rejected;await settle();
    assert.equal(f.stops.length, 1);assert.equal(f.stops[0]!.id, 'original_session');assert.equal(f.stops[0]!.generation, 1);
    assert.notEqual(f.stops[0]!.signal, caller.signal);assert.equal(f.stops[0]!.signal.aborted, false);
    assert.equal(f.c.jobs.size, 0);assert.equal(f.web.session('original_session'), undefined);assert.equal(f.calls.length, 1);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('late actual 202 after stack disposal still belongs to the captured principal, never the successor view', async () => {
  const f = fixture(), held = deferred<ReturnType<typeof response>>();f.replies.push(held.promise);
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60);
    const rejected = assert.rejects(pending, {name: 'AbortError'});
    f.retireView();f.web.dispose();held.resolve(response({...real(), retryAfterMs: 500}));await rejected;await settle();
    assert.equal(f.stops.length, 1);assert.equal(f.c.jobs.size, 0);assert.equal(f.web.session('original_session'), undefined);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('retiring original principal while a real reply is held authorizes no cleanup', async () => {
  const f = fixture(), held = deferred<ReturnType<typeof response>>();f.replies.push(held.promise);
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60);
    const rejected = assert.rejects(pending, {name: 'AbortError'});
    f.retirePrincipal();held.resolve(response({...real(), retryAfterMs: 500}));await rejected;await settle();
    assert.equal(f.stops.length, 0);assert.equal(f.c.jobs.size, 0);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('retry-only abort carries no resource and does not manufacture cleanup', async () => {
  const f = fixture(), caller = new AbortController();f.replies.push(Promise.resolve(response({variant: 'wait', retryAfterMs: 500})));
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60, caller.signal);
    const rejected = assert.rejects(pending, {name: 'AbortError'});await settle();caller.abort();await rejected;await settle();
    assert.equal(f.stops.length, 0);assert.equal(f.c.jobs.size, 0);assert.equal(f.web.session('original_session'), undefined);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('the queue sixth context receives only the canonical preparation notice and keeps its original callbacks', async () => {
  const f = fixture(), caller = new AbortController(), notices: string[] = [];
  const context = {isCurrent: () => true, onPreparing: (_wait: number, notice: {messageId: string}) => {notices.push(notice.messageId);}};
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60, caller.signal, context);
    const rejected = assert.rejects(pending, {name: 'AbortError'});
    context.onPreparing = () => {assert.fail('successor callback');};await settle();
    assert.deepEqual(notices, ['player.preparing.slowStart']);caller.abort();await rejected;await settle();
    assert.equal(f.stops.length, 1);assert.equal(f.c.jobs.size, 0);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('the Web wrapper keeps the same request past 120 seconds of genuine server preparation', async () => {
  const f = fixture();let notices = 0;
  for (let index = 0; index < 13; index++) f.replies.push(Promise.resolve(response({variant: 'wait', retryAfterMs: 10_000})));
  f.replies.push(Promise.resolve(response(real(), 201)));
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60, undefined,
      {isCurrent: () => true, onPreparing: () => {notices++;}});
    for (let index = 0; index < 13; index++) {await settle();f.c.fire();}
    assert.equal((await pending).session.id, 'original_session');
    assert.equal(notices, 13);assert.equal(f.calls.length, 14);assert.equal(f.stops.length, 0);
    assert.ok(f.calls.every(call => call.headers?.['Idempotency-Key'] === 'original-key'));
    assert.ok(f.calls.every(call => call.body === f.calls[0]!.body));assert.equal(f.c.jobs.size, 0);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('retiring only the original queue caller ends continuation without publishing to its successor', async () => {
  const f = fixture();let live = true, notices = 0;
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60, undefined,
      {isCurrent: () => live, onPreparing: () => {notices++;}});
    const rejected = assert.rejects(pending, {name: 'AbortError'});await settle();live = false;f.c.fire();await rejected;await settle();
    assert.equal(notices, 1);assert.equal(f.calls.length, 1);assert.equal(f.stops.length, 1);assert.equal(f.c.jobs.size, 0);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('a successor stack registration spends the old receipt even if that registration is later forgotten', async () => {
  const f = fixture(), caller = new AbortController(), successor = f.owner.attach();
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60, caller.signal);
    const rejected = assert.rejects(pending, {name: 'AbortError'});await settle();
    successor.remember('original_session');successor.forget('original_session');caller.abort();await rejected;await settle();
    assert.equal(f.stops.length, 0);
  } finally {successor.dispose();f.web.dispose();f.owner.dispose();}
});

test('registration before the held 202 arrives also permanently refuses original cleanup', async () => {
  const f = fixture(), held = deferred<ReturnType<typeof response>>(), successor = f.owner.attach();f.replies.push(held.promise);
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60);
    const rejected = assert.rejects(pending, {name: 'AbortError'});
    successor.remember('original_session');successor.forget('original_session');f.retireView();
    held.resolve(response({...real(), retryAfterMs: 500}));await rejected;await settle();assert.equal(f.stops.length, 0);
  } finally {successor.dispose();f.web.dispose();f.owner.dispose();}
});

test('an installed occurrence stays protected even after its bounded known entry was forgotten', async () => {
  let stops = 0;
  const owner = new WebPreparingStarts({isCurrent: () => true, stop: async () => {stops++;}}), installed = owner.attach(id => id === 'original_session'), reg = owner.attach();
  try {
    installed.remember('original_session');installed.forget('original_session');
    const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true);
    await start.context.capturePreparing!(preparing())!();start.finish();assert.equal(stops, 0);
  } finally {installed.dispose();reg.dispose();owner.dispose();}
});

test('a synchronous reentrant caller cannot claim the original key before request capture finishes', () => {
  const owner = new WebPreparingStarts({isCurrent: () => true, stop: async () => {assert.fail('no cleanup');}}), reg = owner.attach();
  try {
    const target = {get itemId() {
      assert.throws(() => reg.begin({itemId: 'successor'}, {}, 'original-key', undefined, () => true), {name: 'AbortError'});
      return 'movie';
    }};
    const start = reg.begin(target, {}, 'original-key', undefined, () => true);assert.equal('itemId' in start.body && start.body.itemId, 'movie');start.finish();
  } finally {reg.dispose();owner.dispose();}
});

test('a registration caused by a currentness callback is refused before cleanup dispatch', async () => {
  let armed = false, stops = 0;
  let reg!: ReturnType<WebPreparingStarts['attach']>;
  const owner = new WebPreparingStarts({isCurrent: () => {
    if (armed) {armed = false;reg.remember('original_session');reg.forget('original_session');}return true;
  }, stop: async () => {stops++;}});
  reg = owner.attach();
  try {
    const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true), cleanup = start.context.capturePreparing!(preparing())!;
    armed = true;await cleanup();start.finish();assert.equal(stops, 0);
  } finally {reg.dispose();owner.dispose();}
});

test('original nested body/key stay frozen across preparation, while returned 201 is remembered normally', async () => {
  const f = fixture(), options: StartOptions = {audio: {trackId: 'track-a'}, subtitles: {trackId: 'sub-a', delivery: 'sidecar'}};
  f.replies.push(Promise.resolve(response({...real(), retryAfterMs: 500})), Promise.resolve(response(real(), 201)));
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, options, 'original-key', 60);await settle();
    (options.audio as {trackId: string}).trackId = 'track-b';(options.subtitles as {trackId: string}).trackId = 'sub-b';
    f.c.fire();const accepted = await pending;
    assert.deepEqual(f.calls.map(call => call.body), [f.calls[0]!.body, f.calls[0]!.body]);
    assert.deepEqual(f.calls[0]!.body, {itemId: 'movie', audio: {trackId: 'track-a'}, subtitles: {trackId: 'sub-a', delivery: 'sidecar'}});
    assert.ok(Object.isFrozen((f.calls[0]!.body as StartOptions).audio));
    assert.ok(f.calls.every(call => call.headers?.['Idempotency-Key'] === 'original-key'));
    assert.equal(f.web.session(accepted.session.id), accepted.session);assert.equal(f.stops.length, 0);assert.equal(f.c.jobs.size, 0);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('late actual 201 still reaches the original core owner instead of being turned into preparation cleanup', async () => {
  const f = fixture(), held = deferred<ReturnType<typeof response>>();f.replies.push(held.promise);
  try {
    const pending = f.web.transport.start({itemId: 'movie'}, {}, 'original-key', 60);
    f.retireView();f.web.dispose();held.resolve(response(real(), 201));const accepted = await pending;
    assert.equal(accepted.session.id, 'original_session');assert.equal(f.stops.length, 0);assert.equal(f.calls.length, 1);
  } finally {f.web.dispose();f.owner.dispose();}
});

test('once-only cleanup settlement does not delay or renew the failed start', async () => {
  const release = deferred<void>(), caller = new AbortController(), c = clock();let stops = 0;
  const owner = new WebPreparingStarts({isCurrent: () => true, stop: async () => {stops++;await release.promise;}}), reg = owner.attach();
  const original = reg.begin({itemId: 'movie'}, {}, 'original-key', caller.signal, () => true);
  const pending = new SessionsClient({send: async () => response({...real(), retryAfterMs: 500})}, c.timers).start(original.body, {}, original.key, original.context);
  const rejected = assert.rejects(pending, {name: 'AbortError'});await settle();caller.abort();await rejected;original.finish();
  assert.equal(stops, 1);assert.equal(c.jobs.size, 0);release.resolve();await settle();reg.dispose();owner.dispose();
});

test('transfer, receiver, wrong target/version/title do not acquire a local preparation receipt', () => {
  const owner = new WebPreparingStarts({isCurrent: () => true, stop: async () => {assert.fail('no cleanup');}}), reg = owner.attach();
  try {
    for (const [target, options, offered] of [
      [{transferId: 'transfer'}, {}, preparing()],
      [{itemId: 'movie'}, {}, {...preparing(), role: 'receiver'}],
      [{itemId: 'other'}, {}, preparing()],
      [{itemId: 'movie'}, {versionId: 'version'}, preparing()],
      [{itemId: 'movie'}, {versionId: 'version', discTitle: {id: 'title', revision: '1'}}, {...preparing(), versionId: 'version'}],
    ] as const) {
      const start = reg.begin(target, options, 'original-key', undefined, () => true);
      assert.equal(start.context.capturePreparing!(offered), undefined);start.finish();
    }
  } finally {reg.dispose();owner.dispose();}
});

function principalFixture() {
  let now: LocalSession = {accessToken: 'synthetic', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z',
    tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'device', installationId: 'installation',
    viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}};
  let current = true, phase = 'ready', route = {pin: {serverId: 'server', publicKey: 'original-pin'}, getSnapshot: () => ({phase})};
  const calls: {path: string; method: string; signal?: AbortSignal}[] = [], reads: Promise<unknown>[] = [], deletes: Promise<unknown>[] = [];
  const api = {baseUrl: 'https://original.invalid', getRouteConnection: () => route,
    requestRaw: async (path: string, method: string, options: {signal?: AbortSignal}) => {
      calls.push({path, method, signal: options.signal});
      return response(method === 'GET' ? await (reads.shift() ?? Promise.resolve(real())) : await (deletes.shift() ?? Promise.resolve(undefined)), method === 'GET' ? 200 : 204);
    }} as unknown as HttpLocalApi;
  const original = now;
  return {api, original, calls, reads, deletes, read: () => now, current: () => current, retire: () => {current = false;},
    change: (patch: Partial<LocalSession>) => {now = {...now, ...patch};},
    pin: () => {route.pin.publicKey = 'successor-pin';}, replaceRoute: () => {route = {...route};}, unready: () => {phase = 'recovering';}};
}
test('actual host reads only the captured id/generation before its bounded original-API stop', async () => {
  const f = principalFixture(), owner = createWebPreparingStarts(f.api, f.original, f.read, f.current)!, reg = owner.attach();
  try {
    const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true), cleanup = start.context.capturePreparing!(preparing())!;
    await cleanup();await cleanup();start.finish();
    assert.deepEqual(f.calls.map(call => [call.method, call.path]), [['GET', '/v1/playback/sessions/original_session'], ['DELETE', '/v1/playback/sessions/original_session']]);
    assert.equal(f.calls[0]!.signal, f.calls[1]!.signal);
  } finally {reg.dispose();owner.dispose();}
});
test('changed generation or resource read is refused without adopting or stopping it', async () => {
  for (const changed of [{...real(), presentation: {...real().presentation, generation: 2}}, {...real(), itemId: 'other'}, {...real(), role: 'receiver'}]) {
    const f = principalFixture();f.reads.push(Promise.resolve(changed));
    const owner = createWebPreparingStarts(f.api, f.original, f.read, f.current)!, reg = owner.attach();
    try {
      const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true);
      await start.context.capturePreparing!(preparing())!();start.finish();assert.deepEqual(f.calls.map(call => call.method), ['GET']);
    } finally {reg.dispose();owner.dispose();}
  }
});
test('family/device/installation/viewer/horizon/pin/route/origin replacement denies cleanup', async () => {
  for (const change of [
    (f: ReturnType<typeof principalFixture>) => f.change({sessionFamilyId: 'new-family'}),
    (f: ReturnType<typeof principalFixture>) => f.change({deviceId: 'new-device'}),
    (f: ReturnType<typeof principalFixture>) => f.change({installationId: 'new-installation'}),
    (f: ReturnType<typeof principalFixture>) => f.change({viewer: {...f.original.viewer, profileId: 'new-profile'}}),
    (f: ReturnType<typeof principalFixture>) => f.change({authorizationHorizon: '2099-01-03T00:00:00Z'}),
    (f: ReturnType<typeof principalFixture>) => f.pin(), (f: ReturnType<typeof principalFixture>) => f.replaceRoute(),
    (f: ReturnType<typeof principalFixture>) => f.unready(), (f: ReturnType<typeof principalFixture>) => f.retire(),
    (f: ReturnType<typeof principalFixture>) => {Object.defineProperty(f.api, 'baseUrl', {value: 'https://successor.invalid'});},
  ]) {
    const f = principalFixture(), owner = createWebPreparingStarts(f.api, f.original, f.read, f.current)!, reg = owner.attach();
    try {
      const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true), cleanup = start.context.capturePreparing!(preparing())!;
      change(f);await cleanup();start.finish();assert.equal(f.calls.length, 0);
    } finally {reg.dispose();owner.dispose();}
  }
});
test('same-family credential refresh preserves the original bounded authority', async () => {
  const f = principalFixture(), owner = createWebPreparingStarts(f.api, f.original, f.read, f.current)!, reg = owner.attach();
  try {
    const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true), cleanup = start.context.capturePreparing!(preparing())!;
    f.change({accessToken: 'refreshed-synthetic', tokenGeneration: '2'});await cleanup();start.finish();
    assert.deepEqual(f.calls.map(call => call.method), ['GET', 'DELETE']);
  } finally {reg.dispose();owner.dispose();}
});

test('missing device/installation/pin and expired authority never fabricate a preparing cleanup owner', () => {
  for (const missing of ['deviceId', 'installationId'] as const) {
    const f = principalFixture();assert.equal(createWebPreparingStarts(f.api, {...f.original, [missing]: undefined}, f.read, f.current), undefined);
  }
  const f = principalFixture();
  assert.equal(createWebPreparingStarts(f.api, {...f.original, authorizationHorizon: '2000-01-01T00:00:00Z'}, f.read, f.current), undefined);
  const pin = f.api.getRouteConnection()!.pin as {publicKey: string};pin.publicKey = '';
  assert.equal(createWebPreparingStarts(f.api, f.original, f.read, f.current), undefined);
});
test('installation during the exact read aborts its cleanup and cannot stop the installed session', async () => {
  const f = principalFixture(), read = deferred<unknown>();f.reads.push(read.promise);
  const owner = createWebPreparingStarts(f.api, f.original, f.read, f.current)!, reg = owner.attach(), successor = owner.attach();
  try {
    const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true), cleanup = start.context.capturePreparing!(preparing())!;
    const pending = Promise.resolve(cleanup()), rejected = assert.rejects(pending, {name: 'AbortError'});await settle();
    successor.remember('original_session');assert.equal(f.calls[0]!.signal?.aborted, true);read.resolve(real());await rejected;start.finish();
    assert.deepEqual(f.calls.map(call => call.method), ['GET']);
  } finally {successor.dispose();reg.dispose();owner.dispose();}
});

test('after exact read, the original stop claim refuses a successor installation until its bounded settlement', async () => {
  const f = principalFixture(), stopped = deferred<unknown>();f.deletes.push(stopped.promise);
  const owner = createWebPreparingStarts(f.api, f.original, f.read, f.current)!, reg = owner.attach(), successor = owner.attach();
  const web = new WebPlayback({preparingStarts: owner, http: {send: async () => {assert.fail('adoption performs no transport');}}, mediaUrl: path => path});
  try {
    const start = reg.begin({itemId: 'movie'}, {}, 'original-key', undefined, () => true), cleanup = start.context.capturePreparing!(preparing())!;
    const pending = Promise.resolve(cleanup());await settle();
    assert.deepEqual(f.calls.map(call => call.method), ['GET', 'DELETE']);
    assert.equal(successor.remember('original_session'), false, 'a new local owner must not install a resource already claimed for retirement');
    assert.throws(() => web.transport.adopt(parseSession(real()), 60), {name: 'AbortError'});
    assert.equal(web.session('original_session'), undefined);
    assert.equal(f.calls[1]!.signal?.aborted, false, 'an adoption attempt cannot unsend the original stop');
    stopped.resolve(undefined);await pending;start.finish();
    assert.equal(successor.remember('different_session'), true);
  } finally {web.dispose();successor.dispose();reg.dispose();owner.dispose();}
});

test('WEB002 web preparing observation forwards canonical flights, installed IDs and late cleanup', async () => {
  const held = deferred<void>();let stopping = false;
  const owner = new WebPreparingStarts({isCurrent: () => true, stop: async () => {stopping = true;await held.promise;}});
  const changes: boolean[] = [], off = owner.subscribePending(() => changes.push(owner.pending())), reg = owner.attach();
  assert.equal(owner.pending(), false);
  const start = reg.begin({itemId: 'movie'}, {}, 'web002', undefined, () => true);
  assert.equal(owner.pending(), true);
  const cleanup = start.context.capturePreparing!(preparing())!;
  const pending = cleanup();assert.equal(stopping, true);start.finish();reg.dispose();
  assert.equal(owner.pending(), true);assert.deepEqual(changes, [true]);
  held.resolve();await pending;assert.equal(owner.pending(), false);assert.deepEqual(changes, [true, false]);
  const next = owner.attach();next.remember('installed');assert.equal(owner.pending(), true);
  next.forget('installed');assert.equal(owner.pending(), false);next.dispose();off();owner.dispose();
  assert.deepEqual(changes, [true, false, true, false]);
});
