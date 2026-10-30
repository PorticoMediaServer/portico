import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackSessionController, SessionsClient, parseStartPreparation, type PreparingSessionView, type StartOptions, type StartPreparation, type Timers} from '../src/playback-v1/sessions.ts';
import type {MediaPlayer, PlayerSource} from '../src/playback-v1/port.ts';
import {ContractError} from '../src/playback-v1/types.ts';
import {PlaybackApiError, type V1Http, type V1Request} from '../src/playback-v1/http.ts';

// Regression coverage for the actual start wire and supplied clock.
const real = () => ({variant: 'session', id: 'original_session', revision: '1', kind: 'vod', role: 'local', state: 'paused', itemId: 'movie',
  lease: {expiresAt: '2030-01-01T00:00:00Z', reportEveryMs: 10_000},
  presentation: {generation: 1, mode: 'stream', engine: 'native', url: '/v1/media/original.m3u8', startPositionMs: 0,
    quality: {mode: 'original'}, subtitles: [], decision: {video: {action: 'copy', reasons: []}, audio: {action: 'copy', reasons: []}}}});
const reply = (body: unknown, status = 202) => ({status, headers: {}, body});
const settle = async () => {for (let i = 0; i < 12; i++) await Promise.resolve();};
function player() {
  const loads: PlayerSource[] = [];
  const port: MediaPlayer = {load: source => {loads.push(source);}, play() {}, pause() {}, seek() {}, setRate() {}, selectSidecarSubtitle() {},
    observe: () => ({generation: 0, state: 'idle', positionMs: 0, rate: 1}), onEvent: () => () => {}, dispose() {}};
  return {loads, port};
}
function clock() {
  let at = 0, id = 0;
  const jobs = new Map<number, {fn: () => void; ms: number}>(), cleared: number[] = [];
  const timers: Timers = {now: () => at, setTimer: (fn, ms) => {jobs.set(++id, {fn, ms}); return id;},
    clearTimer: timer => {cleared.push(timer as number); jobs.delete(timer as number);}};
  return {timers, jobs, cleared, jump: (ms: number) => {at += ms;}, fire() {
    const job = jobs.entries().next().value;
    assert.ok(job, 'a real pending preparation wait exists');
    jobs.delete(job[0]); at += job[1].ms; job[1].fn();
  }};
}

test('Root67 declared retry-only bounds never fabricate a session', () => {
  for (const retryAfterMs of [1, 500, 10_000]) assert.deepEqual(parseStartPreparation({variant: 'wait', retryAfterMs}), {retryAfterMs});
  for (const retryAfterMs of [undefined, null, 0, -1, 1.5, 10_001, NaN, Infinity, -Infinity, '500'])
    assert.throws(() => parseStartPreparation({variant: 'wait', retryAfterMs}), ContractError);
  for (const field of ['id', 'presentation', 'lease', 'unknown']) for (const value of [null, undefined, 'invented'])
    // R-09b: the tag decides. A wait with members a newer server added is still only a wait.
    assert.equal(parseStartPreparation({variant: 'wait', retryAfterMs: 500, [field]: value}).session, undefined);
  assert.throws(() => parseStartPreparation({retryAfterMs: 500}), ContractError, 'an untagged reply is refused');
  assert.throws(() => parseStartPreparation({variant: 'resumed', retryAfterMs: 500}), ContractError, 'an unknown tag is a start this app cannot follow');
});

test('Root67 genuine session-backed preparation is the official immutable view, not a playable Session', () => {
  const offered = {...real(), retryAfterMs: 500};
  const parsed = parseStartPreparation(offered);
  assert.equal(parsed.session?.id, offered.id);
  assert.equal(parsed.retryAfterMs, 500);
  assert.ok(Object.isFrozen(parsed.session));
  assert.ok(Object.isFrozen(parsed.session?.presentation));
  assert.throws(() => parseStartPreparation(real()), ContractError, '202 requires an explicit bounded wait even for a real session');
  assert.throws(() => parseStartPreparation({...offered, presentation: null}), ContractError);
});

test('Root67 replays only the original deep wire body, key, signal and callback', async () => {
  const c = clock(), requests: V1Request[] = [], original = new AbortController(), replacement = new AbortController();
  replacement.abort();
  const target = {queue: {queueId: 'queue', entryId: 'entry'}};
  const options = {quality: {mode: 'fixed' as const, id: '720p' as const}, audio: {trackId: 'audio'}, subtitles: {trackId: 'sub'}};
  let originalCallbacks = 0, replacementCallbacks = 0;
  const extra = {signal: original.signal, onPreparing: (_ms: number) => {
    originalCallbacks++;
    target.queue.entryId = 'successor'; options.audio.trackId = 'new-audio'; options.subtitles.trackId = 'new-sub';
    extra.signal = replacement.signal; extra.onPreparing = () => {replacementCallbacks++;};
  }};
  const http: V1Http = {send: async request => {
    requests.push(request);
    return requests.length <= 2 ? reply({variant: 'wait', retryAfterMs: 500}) : reply({...real(), queue: {queueId: 'queue', entryId: 'entry'}}, 201);
  }};
  const pending = new SessionsClient(http, c.timers).start(target, options, 'original_key_0000001', extra);
  await settle(); c.fire(); await settle(); c.fire(); await pending;
  assert.equal(originalCallbacks, 2); assert.equal(replacementCallbacks, 0);
  for (const request of requests) {
    assert.equal(request, requests[0]); assert.equal(request.signal, original.signal);
    assert.equal(request.headers?.['Idempotency-Key'], 'original_key_0000001');
    assert.deepEqual(request.body, {queue: {queueId: 'queue', entryId: 'entry'}, quality: {mode: 'fixed', id: '720p'}, audio: {trackId: 'audio'}, subtitles: {trackId: 'sub'}});
    assert.ok(Object.isFrozen(request.body)); assert.ok(Object.isFrozen((request.body as typeof target).queue));
  }
  assert.equal(c.jobs.size, 0); assert.equal(c.cleared.length, 2);
});

test('Root67 callback retirement before installing a timer has zero continuation and no invented cleanup', async () => {
  const c = clock(), caller = new AbortController(); let calls = 0, abandoned = 0;
  const pending = new SessionsClient({send: async () => {calls++; return reply({variant: 'wait', retryAfterMs: 500});}}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000002', {signal: caller.signal,
      onPreparing: () => caller.abort(), capturePreparing: () => () => {abandoned++;}});
  await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(calls, 1); assert.equal(c.jobs.size, 0); assert.equal(c.cleared.length, 0); assert.equal(abandoned, 0);
});

test('Root67 waiting abort clears its timer and removes its own abort listener', async () => {
  const c = clock(), caller = new AbortController(), signal = caller.signal;
  let listeners = 0, calls = 0;
  const add = signal.addEventListener.bind(signal), remove = signal.removeEventListener.bind(signal);
  signal.addEventListener = ((type: string, fn: EventListenerOrEventListenerObject, options?: AddEventListenerOptions | boolean) => {
    if (type === 'abort') listeners++; add(type, fn, options);
  }) as typeof signal.addEventListener;
  signal.removeEventListener = ((type: string, fn: EventListenerOrEventListenerObject, options?: EventListenerOptions | boolean) => {
    if (type === 'abort') listeners--; remove(type, fn, options);
  }) as typeof signal.removeEventListener;
  const pending = new SessionsClient({send: async () => {calls++; return reply({variant: 'wait', retryAfterMs: 500});}}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000003', {signal});
  await settle(); assert.equal(listeners, 1); assert.equal(c.jobs.size, 1);
  caller.abort(); await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(listeners, 0); assert.equal(c.jobs.size, 0); assert.equal(c.cleared.length, 1); assert.equal(calls, 1);
});

test('Root67 an original currentness guard cannot borrow a later opening after waiting', async () => {
  const c = clock(); let current = true, calls = 0;
  const pending = new SessionsClient({send: async () => {calls++; return reply({variant: 'wait', retryAfterMs: 500});}}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000004', {isCurrent: () => current});
  await settle(); current = false; c.fire();
  await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(calls, 1); assert.equal(c.jobs.size, 0);
});

test('Root67 invalid timing cannot poison the budget or fall back to a header', async () => {
  for (const body of [{retryAfterMs: NaN}, {}, {retryAfterMs: 0}, {retryAfterMs: 500, id: null}]) {
    const c = clock(); let calls = 0, preparing = 0;
    const http: V1Http = {send: async () => {calls++; return {...reply(body), headers: {'Retry-After': '1'}};}};
    await assert.rejects(new SessionsClient(http, c.timers).start({itemId: 'movie'}, {}, 'original_key_0000005', {onPreparing: () => {preparing++;}}), ContractError);
    assert.equal(calls, 1); assert.equal(c.jobs.size, 0); assert.equal(preparing, 0);
  }
});

test('Root69 a valid 202 keeps retrying beyond the retired 120000ms client budget', async () => {
  const c = clock(); let calls = 0; const notices: StartPreparation[] = [];
  const pending = new SessionsClient({send: async () => {calls++; return calls <= 15 ? reply({variant: 'wait', retryAfterMs: 10_000}) : reply(real(), 201);}}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000006', {onPreparing: (_ms, notice) => {notices.push(notice);}});
  for (let i = 0; i < 15; i++) {await settle(); assert.equal(c.jobs.size, 1); c.fire();}
  const session = await pending;
  assert.equal(session.id, 'original_session'); assert.equal(calls, 16); assert.equal(c.jobs.size, 0);
  assert.equal(notices.length, 15);
  for (const notice of notices) {assert.deepEqual(notice, {messageId: 'player.preparing.slowStart', retryAfterMs: 10_000}); assert.ok(Object.isFrozen(notice));}
});

test('Root69 slow I/O and repeated real 202s keep one original request until success', async () => {
  const c = clock(); let calls = 0; const requests: V1Request[] = [];
  const http: V1Http = {send: async request => {requests.push(request); calls++; c.jump(60_000); return calls <= 3 ? reply({...real(), retryAfterMs: 500}) : reply(real(), 201);}};
  const pending = new SessionsClient(http, c.timers).start({itemId: 'movie'}, {}, 'original_key_0000007');
  for (let i = 0; i < 3; i++) {await settle(); c.fire();}
  const session = await pending;
  assert.equal(session.id, 'original_session'); assert.equal(calls, 4); assert.equal(c.jobs.size, 0);
  for (const request of requests) {
    assert.equal(request, requests[0]); assert.equal(request.headers?.['Idempotency-Key'], 'original_key_0000007');
    assert.deepEqual(request.body, {itemId: 'movie'});
  }
});

test('Root67 capacity refusal is never preparation POST polling', async () => {
  const c = clock(); let calls = 0;
  const http: V1Http = {send: async () => {calls++; return reply({error: {code: 'stream_limit_reached', retry: 'same_request'}}, 429);}};
  await assert.rejects(new SessionsClient(http, c.timers).start({itemId: 'movie'}, {}, 'original_key_0000008'),
    error => error instanceof PlaybackApiError && error.status === 429);
  assert.equal(calls, 1); assert.equal(c.jobs.size, 0);
});

test('Root67 genuine abandoned 202 reaches only the captured original host cleanup', async () => {
  const c = clock(), caller = new AbortController(), abandoned: PreparingSessionView[] = [];
  let calls = 0;
  const pending = new SessionsClient({send: async () => {calls++; return reply({...real(), retryAfterMs: 500});}}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000009', {signal: caller.signal, onPreparing: () => caller.abort(),
      capturePreparing: held => () => {abandoned.push(held);}});
  await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(abandoned.length, 1); assert.equal(abandoned[0].id, 'original_session');
  assert.equal(calls, 1, 'the wire client does not invent DELETE ownership'); assert.equal(c.jobs.size, 0);
});

test('Root67 a changed session or mismatched queue cannot be adopted as this preparation', async () => {
  const c = clock(), abandoned: PreparingSessionView[] = []; let calls = 0;
  const http: V1Http = {send: async () => ++calls === 1 ? reply({...real(), retryAfterMs: 500}) : reply({...real(), id: 'successor'}, 201)};
  const pending = new SessionsClient(http, c.timers).start({itemId: 'movie'}, {}, 'original_key_0000010', {capturePreparing: held => () => {abandoned.push(held);}});
  await settle(); c.fire(); await assert.rejects(pending, ContractError);
  assert.deepEqual(abandoned.map(value => value.id), ['original_session']);
  await assert.rejects(new SessionsClient({send: async () => reply({...real(), queue: {queueId: 'other', entryId: 'entry'}, retryAfterMs: 500})}, c.timers)
    .start({queue: {queueId: 'queue', entryId: 'entry'}}, {}, 'original_key_0000011'), ContractError);
});

test('Root67 callback failure preserves its identity and original real-resource cleanup', async () => {
  const failure = new Error('original callback failed'), abandoned: string[] = [], c = clock();
  await assert.rejects(new SessionsClient({send: async () => reply({...real(), retryAfterMs: 500})}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000012', {onPreparing: () => {throw failure;},
      capturePreparing: held => () => {abandoned.push(held.id); throw new Error('cleanup failed');}}), error => error === failure);
  assert.deepEqual(abandoned, ['original_session']); assert.equal(c.jobs.size, 0);
});

test('Root67 a late real success still reaches the existing controller cleanup path', async () => {
  const caller = new AbortController();
  const http: V1Http = {send: async () => {caller.abort(); return reply(real(), 201);}};
  const session = await new SessionsClient(http).start({itemId: 'movie'}, {}, 'original_key_0000013', {signal: caller.signal});
  assert.equal(session.id, 'original_session', 'a genuine late 201 is not thrown away before its host can abandon it');
});

test('Root67 the original preparation receipt cannot reacquire a successor known-absence registration', async () => {
  const caller = new AbortController(), c = clock(); let registration = {}, installed = false, deleted = 0, captures = 0;
  await assert.rejects(new SessionsClient({send: async () => reply({...real(), retryAfterMs: 500})}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000014', {
      capturePreparing: held => {
        captures++; assert.equal(held.id, 'original_session');
        const original = registration;
        return () => {if (registration === original && !installed) deleted++;};
      }, onPreparing: () => {
        installed = true; registration = {}; // A successor registers even this same id.
        installed = false; registration = {}; // Forgetting it cannot revive the old receipt.
        caller.abort();
      }, signal: caller.signal}), {name: 'AbortError'});
  assert.equal(captures, 1); assert.equal(deleted, 0); assert.equal(c.jobs.size, 0);
});

test('Root67 synchronous timer completion still clears the installed handle', async () => {
  const cleared: unknown[] = []; let calls = 0;
  const timers: Timers = {now: () => 0, setTimer: fn => {fn(); return 'own_timer';}, clearTimer: timer => {cleared.push(timer);}};
  const session = await new SessionsClient({send: async () => ++calls === 1 ? reply({variant: 'wait', retryAfterMs: 500}) : reply(real(), 201)}, timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000015');
  assert.equal(session.id, 'original_session'); assert.deepEqual(cleared, ['own_timer']); assert.equal(calls, 2);
});

test('Root67 pre-aborted original lifetime sends no request', async () => {
  const caller = new AbortController(); caller.abort(); let calls = 0;
  await assert.rejects(new SessionsClient({send: async () => {calls++; return reply(real(), 201);}})
    .start({itemId: 'movie'}, {}, 'original_key_0000016', {signal: caller.signal}), {name: 'AbortError'});
  assert.equal(calls, 0);
});

test('Root67 cleanup settlement cannot extend a failed start deadline', async () => {
  const caller = new AbortController(); let cleanup = 0;
  await assert.rejects(new SessionsClient({send: async () => reply({...real(), retryAfterMs: 500})})
    .start({itemId: 'movie'}, {}, 'original_key_0000017', {signal: caller.signal, onPreparing: () => caller.abort(),
      capturePreparing: () => () => {cleanup++; return new Promise<void>(() => {});}}), {name: 'AbortError'});
  assert.equal(cleanup, 1);
});

test('Root67 controller claims before a request getter can start a successor', async () => {
  const c = clock(), engine = player(), sent: string[] = [];
  let successor: ReturnType<PlaybackSessionController['start']> | undefined;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000018',
    http: {send: async request => {const itemId = (request.body as {itemId: string}).itemId; sent.push(itemId); return reply({...real(), itemId}, 201);}}});
  const options: StartOptions = {get state() {successor = controller.start({itemId: 'successor'}); return 'playing';}};
  try {
    assert.equal(await controller.start({itemId: 'original'}, options), undefined);
    await successor;
    assert.deepEqual(sent, ['successor']);
    assert.equal(controller.getSnapshot().session?.itemId, 'successor');
  } finally {controller.dispose();}
});

test('Root67 controller keeps the original paused intent through preparation publication', async () => {
  const c = clock(), engine = player(), options: {state: 'paused' | 'playing'} = {state: 'paused'};
  let calls = 0;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000019',
    http: {send: async () => ++calls === 1 ? reply({variant: 'wait', retryAfterMs: 500}) : reply(real(), 201)}});
  const off = controller.subscribe(() => {if (controller.getSnapshot().phase === 'preparing') options.state = 'playing';});
  try {
    const pending = controller.start({itemId: 'movie'}, options);
    await settle(); c.fire(); await pending;
    assert.equal(options.state, 'playing', 'the subscriber did mutate its own later intent');
    assert.equal(engine.loads[0].autoplay, false, 'the original paused start owns acceptance');
  } finally {off(); controller.dispose();}
});

test('Root67 a retired controller opening cannot publish over an installed session', async () => {
  const c = clock(), engine = player(); let calls = 0, publications = 0;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000020',
    http: {send: async () => {calls++; return reply(real(), 201);}}});
  try {
    await controller.start({itemId: 'movie'});
    const installed = controller.getSnapshot(), off = controller.subscribe(() => {publications++;});
    try {
      assert.equal(await controller.start({itemId: 'retired'}, {}, undefined, {isCurrent: () => false}), undefined);
      assert.equal(controller.getSnapshot(), installed); assert.equal(publications, 0); assert.equal(calls, 1);
    } finally {off();}
  } finally {controller.dispose();}
});

test('Root67 currentness callbacks cannot abort and then authorize their original request', async () => {
  const caller = new AbortController(); let calls = 0;
  await assert.rejects(new SessionsClient({send: async () => {calls++; return reply(real(), 201);}})
    .start({itemId: 'movie'}, {}, 'original_key_0000021', {signal: caller.signal, isCurrent: () => {caller.abort(); return true;}}), {name: 'AbortError'});
  assert.equal(calls, 0);
});

test('Root67 currentness reentrancy immediately before dispatch keeps the successor', async () => {
  const c = clock(), engine = player(), sent: string[] = [];
  let checks = 0, successor: ReturnType<PlaybackSessionController['start']> | undefined;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000022',
    http: {send: async request => {const itemId = (request.body as {itemId: string}).itemId; sent.push(itemId); return reply({...real(), itemId}, 201);}}});
  try {
    await assert.rejects(controller.start({itemId: 'original'}, {}, undefined, {isCurrent: () => {
      if (++checks === 3) successor = controller.start({itemId: 'successor'});
      return true;
    }}), {name: 'AbortError'});
    await successor;
    assert.deepEqual(sent, ['successor']); assert.equal(controller.getSnapshot().session?.itemId, 'successor');
  } finally {controller.dispose();}
});

test('Root67 an opening retired during a real 201 reaches cleanup instead of adoption', async () => {
  const c = clock(), engine = player(), methods: string[] = []; let current = true;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000023',
    http: {send: async request => {
      methods.push(request.method);
      if (request.method === 'DELETE') return {status: 204, headers: {}};
      current = false; return reply(real(), 201);
    }}});
  try {
    assert.equal(await controller.start({itemId: 'movie'}, {}, undefined, {isCurrent: () => current}), undefined);
    assert.deepEqual(methods, ['POST', 'DELETE']); assert.equal(engine.loads.length, 0);
    assert.equal(controller.getSnapshot().session, undefined);
  } finally {controller.dispose();}
});

test('Root69 a terminal reply is never replayed as preparation', async () => {
  for (const status of [400, 404, 500]) {
    const c = clock(); let calls = 0;
    const http: V1Http = {send: async () => {calls++; return {status, headers: {}, body: {error: {code: 'terminal'}}};}};
    await assert.rejects(new SessionsClient(http, c.timers).start({itemId: 'movie'}, {}, 'original_key_0000024'));
    assert.equal(calls, 1, `status ${status}`); assert.equal(c.jobs.size, 0);
  }
});

test('Root69 caller retirement during a past-budget wait clears its timer and sends nothing new', async () => {
  const c = clock(), caller = new AbortController(); let calls = 0;
  const pending = new SessionsClient({send: async () => {calls++; return reply({variant: 'wait', retryAfterMs: 10_000});}}, c.timers)
    .start({itemId: 'movie'}, {}, 'original_key_0000025', {signal: caller.signal});
  for (let i = 0; i < 13; i++) {await settle(); c.fire();}
  await settle(); assert.equal(c.jobs.size, 1);
  caller.abort(); await assert.rejects(pending, {name: 'AbortError'});
  assert.equal(calls, 14); assert.equal(c.jobs.size, 0); assert.equal(c.cleared.length, 14);
});

test('Root69 ordinary item, channel and queue starts replay only their own original body', async () => {
  const queue = {queueId: 'queue', entryId: 'entry'};
  const cases: {target: {itemId: string} | {channelId: string} | {queue: typeof queue}; offered: Record<string, unknown>}[] = [
    {target: {itemId: 'movie'}, offered: real()},
    {target: {channelId: 'channel'}, offered: {...real(), kind: 'channel', itemId: 'channel'}},
    {target: {queue}, offered: {...real(), itemId: 'entry-item', queue}}
  ];
  for (const {target, offered} of cases) {
    const c = clock(), requests: V1Request[] = [];
    const http: V1Http = {send: async request => {requests.push(request); return requests.length <= 2 ? reply({variant: 'wait', retryAfterMs: 1_000}) : reply(offered, 201);}};
    const pending = new SessionsClient(http, c.timers).start(target, {}, 'original_key_0000026');
    await settle(); c.fire(); await settle(); c.fire();
    const session = await pending;
    assert.equal(session.id, 'original_session'); assert.equal(requests.length, 3);
    for (const request of requests) {assert.equal(request, requests[0]); assert.deepEqual(request.body, target);}
    if ('itemId' in target) {assert.equal(session.kind, 'vod'); assert.equal(session.itemId, 'movie');}
    if ('channelId' in target) {assert.equal(session.kind, 'channel'); assert.equal(session.itemId, 'channel');}
    if ('queue' in target) {assert.deepEqual(session.queue, target.queue);}
  }
});

test('Root69 a real-session 202 past the old budget keeps one original ownership receipt', async () => {
  const c = clock(); let calls = 0, captures = 0, abandoned = 0;
  const http: V1Http = {send: async () => {calls++; return calls <= 13 ? reply({...real(), retryAfterMs: 10_000}) : reply(real(), 201);}};
  const pending = new SessionsClient(http, c.timers).start({itemId: 'movie'}, {}, 'original_key_0000027',
    {capturePreparing: held => {captures++; assert.equal(held.id, 'original_session'); return () => {abandoned++;};}});
  for (let i = 0; i < 13; i++) {await settle(); c.fire();}
  const session = await pending;
  assert.equal(session.id, 'original_session'); assert.equal(calls, 14); assert.equal(captures, 1); assert.equal(abandoned, 0);
});

test('Root69 the controller exposes real preparation before any session exists', async () => {
  const c = clock(), engine = player(); let calls = 0;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000028',
    http: {send: async () => ++calls === 1 ? reply({variant: 'wait', retryAfterMs: 500}) : reply(real(), 201)}});
  try {
    const pending = controller.start({itemId: 'movie'});
    await settle();
    let snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'preparing');
    assert.deepEqual(snapshot.preparation, {messageId: 'player.preparing.slowStart', retryAfterMs: 500});
    assert.ok(Object.isFrozen(snapshot.preparation));
    assert.equal(snapshot.session, undefined); assert.equal(engine.loads.length, 0);
    c.fire(); await pending;
    snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'active'); assert.equal(snapshot.preparation, undefined);
    assert.equal(engine.loads.length, 1);
  } finally {controller.dispose();}
});

test('Root69 repeated accepted retries keep preparation current until a terminal reply clears it', async () => {
  const c = clock(), engine = player(); let calls = 0;
  const http: V1Http = {send: async () => ++calls === 1 ? reply({variant: 'wait', retryAfterMs: 500}) : calls === 2 ? reply({variant: 'wait', retryAfterMs: 2_000}) : {status: 400, headers: {}, body: {error: {code: 'terminal'}}}};
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000029', http});
  try {
    const pending = controller.start({itemId: 'movie'});
    await settle();
    assert.equal(controller.getSnapshot().preparation?.retryAfterMs, 500);
    c.fire(); await settle();
    assert.equal(controller.getSnapshot().preparation?.retryAfterMs, 2_000);
    c.fire(); await assert.rejects(pending);
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.phase, 'error'); assert.equal(snapshot.preparation, undefined);
  } finally {controller.dispose();}
});

test('Root69 detaching a waiting start clears its preparation', async () => {
  const c = clock(), engine = player();
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000030',
    http: {send: async () => reply({variant: 'wait', retryAfterMs: 500})}});
  try {
    const pending = controller.start({itemId: 'movie'});
    await settle();
    assert.equal(controller.getSnapshot().phase, 'preparing');
    controller.detach();
    assert.equal(controller.getSnapshot().phase, 'idle');
    assert.equal(controller.getSnapshot().preparation, undefined);
    c.fire(); await assert.rejects(pending, {name: 'AbortError'});
    assert.equal(controller.getSnapshot().phase, 'idle'); assert.equal(controller.getSnapshot().preparation, undefined);
  } finally {controller.dispose();}
});

test('Root69 a retired older start cannot publish preparation over its successor', async () => {
  const c = clock(), engine = player(); let calls = 0;
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => `original_key_00000${31 + calls}`,
    http: {send: async request => {calls++; const itemId = (request.body as {itemId: string}).itemId;
      return itemId === 'old' ? reply({variant: 'wait', retryAfterMs: 500}) : reply({...real(), itemId: 'new'}, 201);}}});
  try {
    const oldStart = controller.start({itemId: 'old'});
    await settle();
    assert.equal(controller.getSnapshot().preparation?.retryAfterMs, 500);
    await controller.start({itemId: 'new'});
    assert.equal(controller.getSnapshot().session?.itemId, 'new');
    assert.equal(controller.getSnapshot().preparation, undefined);
    c.fire();
    await assert.rejects(oldStart, {name: 'AbortError'});
    assert.equal(controller.getSnapshot().session?.itemId, 'new');
    assert.equal(controller.getSnapshot().preparation, undefined);
  } finally {controller.dispose();}
});

test('Root69 the controller forwards each preparation notice to its original caller', async () => {
  const c = clock(), engine = player(); let calls = 0; const waits: number[] = [], notices: StartPreparation[] = [];
  const controller = new PlaybackSessionController({timers: c.timers, player: engine.port, key: () => 'original_key_0000032',
    http: {send: async () => ++calls === 1 ? reply({variant: 'wait', retryAfterMs: 500}) : reply(real(), 201)}});
  try {
    const pending = controller.start({itemId: 'movie'}, {}, undefined, {onPreparing: (wait, notice) => {waits.push(wait); notices.push(notice);}});
    await settle(); c.fire(); await pending;
    assert.deepEqual(waits, [500]);
    assert.deepEqual(notices, [{messageId: 'player.preparing.slowStart', retryAfterMs: 500}]);
    assert.ok(Object.isFrozen(notices[0]));
  } finally {controller.dispose();}
});

test('Root69 an adapter decode failure keeps the original 202 cleanup and preserves the rejection', async () => {
  const c = clock(); let calls = 0, abandoned = 0;
  const failure = new Error('adapter decode failed');
  const http: V1Http = {send: async () => ++calls === 1 ? reply({...real(), retryAfterMs: 500}) : reply(real(), 201)};
  const pending = new SessionsClient(http, c.timers).startResponse({itemId: 'movie'}, {}, 'original_key_0000033',
    {capturePreparing: () => () => {abandoned++;}}, () => {throw failure;});
  const rejected = assert.rejects(pending, error => error === failure);
  await settle(); c.fire(); await rejected;
  assert.equal(calls, 2); assert.equal(abandoned, 1, 'the original real-202 receipt cleans up exactly once');
  assert.equal(c.jobs.size, 0);
});

test('Root69 an adapter decode success keeps the exact original identity and its decoded view', async () => {
  const c = clock(); let calls = 0;
  const decoded = {linear: 'projection'};
  const http: V1Http = {send: async () => ++calls === 1 ? reply({...real(), retryAfterMs: 500}) : reply(real(), 201)};
  const pending = new SessionsClient(http, c.timers).startResponse({itemId: 'movie'}, {}, 'original_key_0000034', {}, () => decoded);
  await settle(); c.fire();
  const result = await pending;
  assert.deepEqual(result, {id: 'original_session', generation: 1, body: decoded});
  assert.equal(calls, 2); assert.equal(c.jobs.size, 0);
});

// R-09 (O-159): a 1.0 client reads a "1.1" start. An unknown quality mode, an unknown
// optional stream action and a subtitle file of an unknown time domain are left out of the
// 202 preparation view; the 201 session maps unknown kind and state to `other`. A required
// presentation mode outside the vocabulary still fails closed.
test('R-09: a newer start with unknown enum values is still read', async () => {
  const {parseSession} = await import('../src/playback-v1/types.ts');
  const {decodeOpen} = await import('../../contracts/src/runtime.ts');
  const newer = () => {
    const body: any = {...real(), retryAfterMs: 500};
    body.presentation = {...body.presentation, quality: {mode: 'hi-res'},
      decision: {...body.presentation.decision, subtitles: {action: 'render', reasons: []}},
      subtitles: [{trackId: 'future', format: 'webvtt', url: '/v1/sub.vtt', offsetMs: 0, timeDomain: 'presentation-relative'}]};
    return body;
  };
  const parsed = parseStartPreparation(newer());
  assert.equal(parsed.session?.id, 'original_session');
  assert.equal((parsed.session?.presentation as any).quality?.mode, undefined);
  assert.deepEqual((parsed.session?.presentation as any).subtitles, []);
  const session = parseSession({...real(), kind: 'hologram', state: 'warming'});
  assert.equal(session.kind, 'other');
  assert.equal(session.state, 'other');
  const read = decodeOpen('GetPlaybackSessionResponse', {...real(), playbackInfo: {basis: 'future_basis'}}) as any;
  assert.equal(read.playbackInfo?.basis, undefined);
  const required: any = newer();
  required.presentation.mode = 'teleport';
  assert.throws(() => parseStartPreparation(required), ContractError);
});

// R-09b: a newer server of the same major adds members at the top and inside nested objects of
// the tagged start reply; the 1.0 client still reads the session it was given.
test('R-09b: a tagged start with members a newer server added is still read', () => {
  const body: any = {...real(), retryAfterMs: 500, addedAtTop: {since: '1.1'}};
  body.presentation = {...body.presentation, addedNested: true, decision: {...body.presentation.decision, addedDeeper: [1]}};
  body.lease = {...body.lease, addedInLease: 'x'};
  const parsed = parseStartPreparation(body);
  assert.equal(parsed.session?.id, 'original_session');
  assert.equal(parsed.retryAfterMs, 500);
  assert.equal(parseStartPreparation({variant: 'wait', retryAfterMs: 700, addedAtTop: true}).retryAfterMs, 700);
});
