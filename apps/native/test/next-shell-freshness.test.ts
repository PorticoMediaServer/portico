import assert from 'node:assert/strict';
import test, {type TestContext} from 'node:test';
import {dependsOn} from '../../../packages/client-core/src/freshness-hub';
import type {ServerEvent} from '../../../packages/client-core/src/playback-v1/types';
import {NativeReadOrder, NativeShellFreshness, nativeQueuedRefresh, type NativeShellFreshnessReads} from '../src-next/shell/freshness';

const settle = async () => {for (let i = 0; i < 12; i++) await Promise.resolve();};
function deferred() {let resolve!: () => void, reject!: (error: unknown) => void; const promise = new Promise<void>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};}
const event = (type: string, id = 'movies', content?: string): ServerEvent => ({id: '1', type, resource: {kind: 'library', id},
  ...(content ? {data: {content, contentOrder: content}} : {})} as ServerEvent);
function fixture(t: TestContext, overrides: Partial<NativeShellFreshnessReads> = {}) {
  t.mock.timers.enable({apis: ['Date', 'setTimeout'], now: 0});
  const calls = {home: 0, libraries: 0, preferences: 0, unread: 0, guide: 0, identity: 0};
  const read = (key: keyof typeof calls) => async () => {calls[key]++;};
  const reads = {home: read('home'), libraries: read('libraries'), preferences: read('preferences'), unread: read('unread'), guide: read('guide'), identity: read('identity')} satisfies Omit<NativeShellFreshnessReads, 'hasHome'>;
  const freshness = new NativeShellFreshness({...reads, hasHome: () => true, ...overrides}, {random: () => 0, gatherMs: 10, settleMs: 0});
  freshness.setHomeOpen(true); t.after(() => freshness.dispose());
  const tick = async (ms = 10) => {t.mock.timers.tick(ms); await settle();};
  return {freshness, calls, tick};
}

test('scan progress reaches its observer without refreshing Home or shell resources', async t => {
  const {freshness, calls, tick} = fixture(t); let scans = 0;
  const off = freshness.hub.watch([dependsOn.event('library.scan.updated')], () => scans++);
  for (let i = 0; i < 100; i++) freshness.hub.accept(event('library.scan.updated'));
  await tick(1000);
  assert.equal(scans, 100);
  assert.deepEqual(calls, {home: 0, libraries: 0, preferences: 0, unread: 0, guide: 0, identity: 0}); off();
});

test('catalog hints refresh the open Home once; directory and notification hints target their own resources', async t => {
  const {freshness, calls, tick} = fixture(t);
  freshness.hub.accept(event('library.changed', 'movies', '1'));
  freshness.hub.accept(event('library.changed', 'movies', '1'));
  await tick(); assert.deepEqual(calls, {home: 1, libraries: 0, preferences: 0, unread: 0, guide: 0, identity: 0});
  freshness.hub.accept(event('libraries.changed', 'directory'));
  await tick(); assert.equal(calls.home, 1, 'content hints retain debt behind the canonical five-second pace'); assert.equal(calls.libraries, 1);
  await tick(4_990); assert.equal(calls.home, 2, 'directory debt is read at the content pace boundary');
  freshness.hub.accept(event('notification.changed', 'inbox'));
  await tick(); assert.equal(calls.unread, 1); assert.equal(calls.home, 2); assert.equal(calls.guide, 0);
});

test('settings.changed for preferences rereads only preferences, ignoring other settings and the obsolete prefix', async t => {
  const {freshness, calls, tick} = fixture(t);
  freshness.hub.accept(event('preferences.changed', 'preferences'));
  freshness.hub.accept(event('settings.changed', 'server'));
  await tick(); assert.equal(calls.preferences, 0);
  freshness.hub.accept(event('settings.changed', 'preferences'));
  await tick(); assert.deepEqual(calls, {home: 0, libraries: 0, preferences: 1, unread: 0, guide: 0, identity: 0});
});

test('covered Home remembers catalog changes and catches up once when shown', async t => {
  const {freshness, calls, tick} = fixture(t); freshness.setHomeOpen(false);
  for (const revision of ['1', '2', '3']) freshness.hub.accept(event('library.changed', 'movies', revision));
  await tick(1000); assert.equal(calls.home, 0);
  freshness.setHomeOpen(true); await tick(); assert.equal(calls.home, 1);
  freshness.setHomeOpen(false); freshness.setHomeOpen(true); await tick(); assert.equal(calls.home, 1, 'reopening an unchanged document does not fetch');
});

test('a hint arriving during a Home read produces a trailing read instead of being dropped', async t => {
  const first = deferred(); let reads = 0;
  const {freshness, tick} = fixture(t, {home: async () => {if (++reads === 1) await first.promise;}});
  freshness.hub.accept(event('library.changed', 'movies', '1')); await tick(); assert.equal(reads, 1);
  for (const revision of ['2', '3', '4']) freshness.hub.accept(event('library.changed', 'movies', revision));
  await tick(100); assert.equal(reads, 1);
  first.resolve(); await settle(); await tick(); assert.equal(reads, 1, 'completion does not bypass the content pace');
  await tick(4_990); assert.equal(reads, 2, 'the paced trailing hint remains owed');
});

test('background, covering and retirement cancel owned reads and keep only valid pending work', async t => {
  const waiting = deferred(), signals: AbortSignal[] = [];
  const {freshness, tick} = fixture(t, {home: async ({signal}) => {signals.push(signal); await waiting.promise;}});
  freshness.setActive(false); freshness.hub.accept(event('library.changed', 'movies', '1'));
  await tick(1000); assert.equal(signals.length, 0);
  freshness.setActive(true); await tick(); assert.equal(signals.length, 1);
  freshness.setHomeOpen(false); assert.equal(signals[0].aborted, true);
  freshness.setHomeOpen(true); await tick(); assert.equal(signals.length, 2);
  freshness.dispose(); assert.equal(signals[1].aborted, true);
  waiting.resolve(); await settle(); await tick(1000); assert.equal(signals.length, 2);
});

test('auxiliary startup reads run independently of Home and preference admission', async t => {
  const {freshness, calls, tick} = fixture(t);
  freshness.loadAuxiliary(); await tick();
  assert.deepEqual(calls, {home: 0, libraries: 0, preferences: 0, unread: 1, guide: 1, identity: 0});
});

test('resync refreshes each shell resource once without refreshing a covered Home', async t => {
  const {freshness, calls, tick} = fixture(t); freshness.setHomeOpen(false);
  freshness.hub.resync(); await tick();
  assert.deepEqual(calls, {home: 0, libraries: 1, preferences: 1, unread: 1, guide: 1, identity: 1});
  freshness.setHomeOpen(true); await tick(); assert.equal(calls.home, 1);
});

test('explicit refresh preserves overlapping requests, even when the earlier read fails', async () => {
  for (const fail of [false, true]) {
    const first = deferred(); let calls = 0;
    const refresh = nativeQueuedRefresh(async () => {if (++calls === 1) await first.promise;}, () => true);
    const pending = refresh(); assert.equal(refresh(), pending); assert.equal(refresh(), pending);
    if (fail) first.reject(new Error('transient')); else first.resolve();
    await pending; assert.equal(calls, 2);
    await refresh(); assert.equal(calls, 3);
  }
});

test('explicit refresh reports the final failure and does not retry a retired opening', async () => {
  const failure = new Error('failed');
  await assert.rejects(nativeQueuedRefresh(async () => {throw failure;}, () => true)(), failure);
  const first = deferred(); let current = true, calls = 0;
  const refresh = nativeQueuedRefresh(async () => {calls++; await first.promise;}, () => current);
  const pending = refresh(); refresh(); current = false; first.reject(failure);
  await assert.rejects(pending, failure); assert.equal(calls, 1);
});

test('a refresh at the completion microtask boundary starts a new read', async () => {
  const first = deferred(), second = deferred(); let calls = 0;
  const refresh = nativeQueuedRefresh(() => ++calls === 1 ? first.promise : second.promise, () => true);
  const pending = refresh();
  let trailing: Promise<void> | undefined;
  const afterRead = first.promise.then(() => {trailing = refresh();});
  first.resolve(); await afterRead; await pending;
  assert.equal(calls, 2); assert.notEqual(trailing, pending);
  second.resolve(); await trailing;
});

test('a synchronously failed refresh releases its slot for the next request', async () => {
  const failure = new Error('synchronous failure'); let calls = 0;
  const refresh = nativeQueuedRefresh(() => {if (++calls === 1) throw failure; return Promise.resolve();}, () => true);
  await assert.rejects(refresh(), failure);
  await refresh(); assert.equal(calls, 2);
});

test('a delayed full refresh cannot replace a newer published resource answer', async () => {
  const identity = deferred(), order = new NativeReadOrder(); let shown = 'original';
  const fullTicket = order.begin();
  const full = (async () => {const olderAnswer = 'older'; await identity.promise; return order.apply(fullTicket, () => {shown = olderAnswer;});})();
  const resourceTicket = order.begin();
  assert.equal(order.apply(resourceTicket, () => {shown = 'newer';}), true);
  identity.resolve(); assert.equal(await full, false); assert.equal(shown, 'newer');
});

test('a cancelled newer read does not suppress an older success or another resource', () => {
  const library = new NativeReadOrder(), guide = new NativeReadOrder(); let shown = 'original', hasGuide = false;
  const older = library.begin(), cancelled = library.begin(), guideRead = guide.begin();
  assert.ok(cancelled > older);
  assert.equal(library.apply(older, () => {shown = 'successful';}), true);
  assert.equal(library.apply(older, () => {shown = 'duplicate';}), false);
  assert.equal(guide.apply(guideRead, () => {hasGuide = true;}), true);
  assert.equal(shown, 'successful'); assert.equal(hasGuide, true);
});
