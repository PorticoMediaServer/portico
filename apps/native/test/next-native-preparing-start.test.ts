import test from 'node:test';
import assert from 'node:assert/strict';
import {NativePreparingStarts, type NativePreparingOwner} from '../src-next/platform/playback/preparing-start';
import {parseStartPreparation, type PreparingSessionView} from '../../../packages/client-core/src/playback-v1/sessions';
import type {V1Request} from '../../../packages/client-core/src/playback-v1/http';
import {NativePlayback} from '../src-next/platform/playback/native-playback';
import {PlaybackScopeHttp} from '../src-next/platform/playback/scope-http';

// Actual declared real-session202 shape. It is decoded by the canonical strict
// parser, never cast into a playable Session or installed in a player.
const real = (extra = {}) => ({id: 'original_session', revision: '1', kind: 'vod', role: 'local', state: 'paused', itemId: 'movie',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 10_000}, retryAfterMs: 500,
  presentation: {generation: 1, mode: 'stream', engine: 'native', url: '/v1/media/grant-original_session/1/master.m3u8', startPositionMs: 0,
    quality: {mode: 'original'}, subtitles: [], decision: {video: {action: 'copy', reasons: []}, audio: {action: 'copy', reasons: []}}}, ...extra});
const offered = (extra = {}) => {
  const session = parseStartPreparation(real(extra)).session;
  assert.ok(session && 'retryAfterMs' in session, 'the fixture is a real-resource 202');
  return session;
};
const settle = async () => {for (let i = 0; i < 12; i++) await Promise.resolve();};
function fixture() {
  let principal = true, continuation = true, stopped = 0;
  const seen: PreparingSessionView[] = [];
  const owner: NativePreparingOwner = {current: () => principal, stop: async (session, _signal, current, claim) => {
    seen.push(session); if (current() && claim()) stopped++;
  }};
  const manager = new NativePreparingStarts(), registration = manager.attach(() => owner, () => false);
  return {manager, registration, owner, seen, stopped: () => stopped, retire: () => {continuation = false;}, revoke: () => {principal = false;},
    begin: (key = 'original') => registration.begin({itemId: 'movie'}, {}, key, undefined, () => continuation)};
}
const wire = (body: unknown, key = 'original'): V1Request => ({method: 'POST', path: '/v1/playback/sessions', body, headers: {'Idempotency-Key': key}});

test('native original start retains one deep-frozen body/key and the original signal', () => {
  const f = fixture(), signal = new AbortController().signal, target = {queue: {queueId: 'queue', entryId: 'entry'}};
  const start = f.registration.begin(target, {quality: {mode: 'original'}}, 'key', signal, () => true);
  target.queue.entryId = 'changed';
  assert.ok('queue' in start.body);
  assert.equal(start.body.queue.entryId, 'entry');
  assert.equal(Object.isFrozen(start.body.queue), true);
  assert.equal(start.key, 'key'); assert.equal(start.context.signal, signal);
  start.finish();
});

test('retired native view may retire only its exact never-installed real202 once', async () => {
  const f = fixture(), start = f.begin(), session = offered(), cleanup = start.context.capturePreparing!(session)!;
  f.retire(); assert.equal(start.context.isCurrent!(), false);
  await cleanup(); await cleanup(); start.finish();
  assert.equal(f.stopped(), 1); assert.equal(f.seen[0], session);
});

test('late ScopeHTTP202 is available only through the original observed dispatch', async () => {
  const f = fixture(), start = f.begin(), request = wire(start.body);
  f.registration.observe(request); f.retire();
  f.registration.retired(request, {status: 202, headers: {}, body: real()});
  start.finish(); await settle();
  assert.equal(f.stopped(), 1);
});

test('retry-only or malformed late202 creates no cleanup resource', async () => {
  for (const body of [{retryAfterMs: 500}, {retryAfterMs: 500, id: null}, {retryAfterMs: 0}, {retryAfterMs: 10_001}]) {
    const f = fixture(), start = f.begin(), request = wire(start.body);
    f.registration.observe(request); f.retire(); f.registration.retired(request, {status: 202, headers: {}, body});
    start.finish(); await settle(); assert.equal(f.stopped(), 0);
  }
});

test('a matching id is not a dispatch receipt for another body, key, path or registration', async () => {
  for (const changed of ['body', 'key', 'path', 'registration'] as const) {
    const f = fixture(), start = f.begin();
    const request: V1Request = changed === 'body' ? wire({itemId: 'other'}) : changed === 'key' ? wire(start.body, 'other')
      : changed === 'path' ? {...wire(start.body), path: '/v1/queues'} : wire(start.body);
    const observer = changed === 'registration' ? f.manager.attach(() => f.owner, () => false) : f.registration;
    observer.observe(request); f.retire(); f.registration.retired(request, {status: 202, headers: {}, body: real()});
    start.finish(); await settle(); assert.equal(f.stopped(), 0, changed); observer.dispose();
  }
});

test('a session installed before dispatch remains disqualified after forget', async () => {
  const f = fixture(); f.registration.remember('original_session');
  const start = f.begin(); f.registration.forget('original_session');
  const cleanup = start.context.capturePreparing!(offered())!;
  await cleanup(); start.finish(); assert.equal(f.stopped(), 0);
});

test('a later registration of the same id permanently denies the preparing receipt', async () => {
  const f = fixture(), start = f.begin(), cleanup = start.context.capturePreparing!(offered())!;
  f.registration.remember('original_session'); f.registration.forget('original_session');
  await cleanup(); start.finish(); assert.equal(f.stopped(), 0);
});

test('an installation during the original read aborts cleanup before its stop claim', async () => {
  let finish!: () => void, stopped = 0, signal: AbortSignal | undefined;
  const manager = new NativePreparingStarts();
  const registration = manager.attach(() => ({current: () => true, stop: async (_session, scoped, current, claim) => {
    signal = scoped; await new Promise<void>(resolve => {finish = resolve;}); if (current() && claim()) stopped++;
  }}), () => false);
  const start = registration.begin({itemId: 'movie'}, {}, 'key', undefined, () => true), pending = start.context.capturePreparing!(offered())!();
  registration.remember('original_session'); registration.forget('original_session');
  assert.equal(signal?.aborted, true); finish(); await pending; start.finish(); assert.equal(stopped, 0);
});

test('the private stop claim prevents local adoption until bounded settlement', async () => {
  let finish!: () => void, claimed = false;
  const manager = new NativePreparingStarts();
  const registration = manager.attach(() => ({current: () => true, stop: async (_session, _signal, _current, claim) => {
    claimed = claim(); await new Promise<void>(resolve => {finish = resolve;});
  }}), () => false);
  const start = registration.begin({itemId: 'movie'}, {}, 'key', undefined, () => true), pending = start.context.capturePreparing!(offered())!();
  assert.equal(claimed, true); assert.equal(registration.remember('original_session'), false);
  start.finish(); finish(); await pending;
  assert.equal(registration.remember('original_session'), true);
});

test('changed original principal refuses a captured resource instead of borrowing a successor', async () => {
  const f = fixture(), start = f.begin(), cleanup = start.context.capturePreparing!(offered())!;
  f.revoke(); await cleanup(); start.finish(); assert.equal(f.stopped(), 0);
});

test('wrong target, version, generation or receiver transfer cannot create a native receipt', () => {
  for (const [target, options, session] of [
    [{itemId: 'movie'}, {}, offered({itemId: 'other'})],
    [{queue: {queueId: 'queue', entryId: 'entry'}}, {}, offered({queue: {queueId: 'queue', entryId: 'other'}})],
    [{itemId: 'movie'}, {versionId: 'original'}, offered({versionId: 'other'})],
    [{itemId: 'movie'}, {}, {...offered(), presentation: {...offered().presentation, generation: 0}}],
    [{transferId: 'receiver'}, {}, offered()],
  ] as const) {
    const f = fixture(), start = f.registration.begin(target, options, 'key', undefined, () => true);
    assert.equal(start.context.capturePreparing!(session), undefined); start.finish();
  }
});

test('the original key is reserved before an options getter can reenter begin', () => {
  const f = fixture(); let refused = false;
  const options = {get state(): 'playing' {
    assert.throws(() => f.begin(), error => {refused = (error as Error).name === 'AbortError'; return refused;}); return 'playing';
  }};
  const start = f.registration.begin({itemId: 'movie'}, options, 'original', undefined, () => true);
  assert.equal(refused, true); start.finish();
});

test('actual native transport uses its purpose receipt on a real202 wait cancellation without installation', async () => {
  const f = fixture(), life = new AbortController(); let otherCapture = 0;
  const path = new NativePlayback({preparingStarts: f.registration, http: {async send() {return {status: 202, headers: {}, body: real()};}}});
  try {
    await assert.rejects(path.transport.start({itemId: 'movie'}, {}, 'original', 10, life.signal,
      {onPreparing: () => life.abort(), capturePreparing: () => {otherCapture++; return undefined;}}));
    await settle();
    assert.equal(f.stopped(), 1); assert.equal(otherCapture, 0);
    assert.equal(path.controller.getSnapshot().session, undefined);
    assert.equal(path.session('original_session'), undefined);
  } finally {path.dispose();}
});

test('actual native ScopeHTTP late real202 reaches only the captured uninstalled cleanup owner', async () => {
  const f = fixture(), life = new AbortController(); let finish!: (value: {status: number; headers: {}; body: unknown}) => void;
  const scoped = new PlaybackScopeHttp({send: () => new Promise(resolve => {finish = resolve;})}, () => true,
    (request, response) => f.registration.retired(request, response));
  const path = new NativePlayback({preparingStarts: f.registration, http: {send: request => {
    f.registration.observe(request); return scoped.send(request);
  }}});
  try {
    const pending = path.transport.start({itemId: 'movie'}, {}, 'original', 10, life.signal);
    life.abort(); finish({status: 202, headers: {}, body: real()});
    await assert.rejects(pending); await settle();
    assert.equal(f.stopped(), 1); assert.equal(path.session('original_session'), undefined);
  } finally {path.dispose(); scoped.dispose();}
});

test('a real-resource202 without original owner proof refuses continuation and never borrows cleanup authority', () => {
  const registration = new NativePreparingStarts().attach(() => undefined, () => false);
  const start = registration.begin({itemId: 'movie'}, {}, 'unbound', undefined, () => true);
  assert.throws(() => start.context.capturePreparing!(offered()), {name: 'AbortError'});
  start.finish(); registration.dispose();
});

test('a reentrant key producer cannot dispatch an older native start over its successor', async () => {
  const requests: unknown[] = []; let successor: Promise<unknown> | undefined, entered = false;
  const path = new NativePlayback({key: () => {
    if (!entered) {entered = true; successor = path.transport.start({itemId: 'successor'}, {}, 'new-key', 10);}
    return 'old-key';
  }, http: {async send(request) {requests.push(request.body);return {status: 201, headers: {}, body: {...real(), itemId: 'successor', retryAfterMs: undefined}};}}});
  try {
    await assert.rejects(path.transport.start({itemId: 'movie'}, {}, undefined, 10), {name: 'AbortError'});
    await successor;
    assert.deepEqual(requests, [{itemId: 'successor'}]);
  } finally {path.dispose();}
});
