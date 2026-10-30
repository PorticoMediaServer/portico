import test from 'node:test';
import assert from 'node:assert/strict';
import {SelectionActionTransferQueue, type BrowserSelectionActionApi} from '../src/screens/browse/selection-action-transfers.ts';
import {routePath} from '../../packages/contracts/src/route-paths.ts';

const artifact = {kind: 'prepared', ref: 'version', sha256: 'a'.repeat(64), bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'film.mp4'};
function preparation(id = 'prep') {return {
  id, itemId: 'item-' + id, libraryId: 'library', profileId: 'profile', quality: 'original', origin: 'item', batchId: 'original-batch',
  title: {kind: 'movie', title: 'Film', onServer: true}, state: 'ready', reason: '', artifact,
  progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaMs: null}, actions: ['remove'], revision: 1,
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z', readyAt: '2026-01-01T00:00:00Z', expiresAt: '2030-01-01T00:00:00Z',
};}
function grant(id = 'prep') {return {preparationId: id, itemId: 'item-' + id, token: 'opaque-grant', url: routePath('get_download_artifact', {grant: 'opaque-grant'}),
  issuedAt: '2026-01-01T00:00:00Z', expiresAt: '2030-01-01T00:00:00Z', renewAfterMs: 60_000, renewed: false, artifact};}
function source() {
  const listeners = new Set<() => void>(), lifetime = new AbortController(); let current = true;
  return {isCurrent: () => current, signal: lifetime.signal, subscribe: (fn: () => void) => {listeners.add(fn);return () => {listeners.delete(fn);};},
    retire: () => {current = false;for (const fn of [...listeners]) fn();}, listeners};
}
function harness(handler: (path: string, method?: string, body?: unknown, signal?: AbortSignal) => unknown | Promise<unknown>) {
  const requests: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[] = [], handoffs: {url: string; filename: string}[] = [];
  let ids = 0;
  const api: BrowserSelectionActionApi = {mediaUrl: path => 'https://media.example.invalid' + path,
    async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) {requests.push({path, method, body, signal});return await handler(path, method, body, signal) as T;}};
  const queue = new SelectionActionTransferQueue({api, requestId: async () => 'grant-operation-' + (++ids), handoff: (url, filename) => {handoffs.push({url, filename});}});
  return {queue, requests, handoffs, ids: () => ids};
}
const flush = async () => {for (let i = 0; i < 20; i++) await Promise.resolve();};

test('browser query transfer keeps one bounded page and hands off an actual scoped preparation without re-submitting media', async () => {
  const h = harness((path, method) => method === 'POST' ? grant('p0') : {closed:true,items: Array.from({length: 20}, (_, n) => preparation('p' + n)), nextCursor: 'page2'}), live = source();
  await h.queue.open('operation', 'device', live);
  const parsed = new URL(h.requests[0].path, 'https://example.invalid');
  assert.deepEqual(Object.fromEntries(parsed.searchParams), {operationId: 'operation', deviceId: 'device', limit: '20'});
  assert.equal(h.queue.getTransfers().items.length, 20);
  await h.queue.nextPage(); assert.equal(h.requests.length, 1);
  await h.queue.transferNext();
  assert.deepEqual(h.handoffs, [{url: 'https://media.example.invalid' + routePath('get_download_artifact', {grant: 'opaque-grant'}), filename: 'film.mp4'}]);
  assert.equal(h.queue.getTransfers().handedOff, 1); assert.equal(h.queue.getTransfers().items.length, 19);
  assert.equal(h.requests[1].path, routePath('create_download_grant', {id: 'p0'}));
  assert.deepEqual(h.requests[1].body, {operationId: 'grant-operation-1'});
  assert.ok(h.requests.every(r => r.path !== routePath('create_download_preparations')));
  h.queue.dispose(); assert.equal(live.listeners.size, 0);
});
test('browser query transfer retries an ambiguous grant with the same immutable operation id', async () => {
  let posts = 0;
  const h = harness((_path, method) => {
    if (method === 'POST') {if (++posts === 1) throw new Error('lost response'); return grant();}
    return {closed:true,items: [preparation()], nextCursor: ''};
  });
  await h.queue.open('operation', 'device', source());
  await h.queue.transferNext(); assert.equal(h.queue.getTransfers().phase, 'error');
  await h.queue.transferNext();
  assert.equal(h.ids(), 1); assert.deepEqual(h.requests[1].body, h.requests[2].body);
  assert.equal(h.handoffs.length, 1); assert.equal(h.queue.getTransfers().phase, 'complete'); h.queue.dispose();
});
test('browser query transfer never hands off a changed artifact or foreign claim', async () => {
  for (const bad of [{...grant(), artifact: {...artifact, sha256: 'b'.repeat(64)}}, {...grant(), preparationId: 'foreign'}]) {
    const h = harness((_path, method) => method === 'POST' ? bad : {closed:true,items: [preparation()], nextCursor: ''});
    await h.queue.open('operation', 'device', source()); await h.queue.transferNext();
    assert.equal(h.handoffs.length, 0); assert.equal(h.queue.getTransfers().phase, 'error');
    assert.equal(h.queue.getTransfers().items.length, 1); h.queue.dispose();
  }
});
test('browser query transfer source retirement aborts inventory and ignores late exposure', async () => {
  let resolve!: (v: unknown) => void;
  const h = harness(() => new Promise(r => {resolve = r;})), live = source();
  const pending = h.queue.open('operation', 'device', live); await flush(); live.retire();
  assert.equal(h.requests[0].signal?.aborted, true); resolve({closed:true,items: [preparation()], nextCursor: ''}); await pending;
  assert.equal(h.queue.getTransfers().phase, 'idle'); assert.equal(h.queue.getTransfers().items.length, 0);
  assert.equal(live.listeners.size, 0); assert.equal(h.handoffs.length, 0); h.queue.dispose();
});
test('browser query transfer waits for the actual queued preparation and validates its refreshed identity', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const h = harness((path, method) => method === 'POST' ? grant() : path === routePath('get_download_preparation', {id: 'prep'})
    ? preparation() : {closed:true,items: [{...preparation(), state: 'queued'}], nextCursor: ''});
  await h.queue.open('operation', 'device', source());
  const pending = h.queue.transferNext(); await flush(); assert.equal(h.requests.length, 1);
  t.mock.timers.tick(1500); await pending;
  assert.equal(h.requests[1].path, routePath('get_download_preparation', {id: 'prep'}));
  assert.equal(h.handoffs.length, 1); h.queue.dispose();
});
test('browser query transfer page retry preserves the exact next cursor and never rescans consumed claims', async () => {
  let page2 = 0;
  const h = harness(path => {
    const cursor = new URL(path, 'https://example.invalid').searchParams.get('cursor');
    if (cursor === 'page2') {if (++page2 === 1) throw new Error('network');return {closed:true,items: [preparation('second')], nextCursor: ''};}
    return {closed:true,items: [preparation()], nextCursor: 'page2'};
  });
  await h.queue.open('operation', 'device', source()); h.queue.skipNext(); await h.queue.nextPage();
  assert.equal(h.queue.getTransfers().phase, 'error'); await h.queue.retryInventory();
  assert.equal(h.queue.getTransfers().items[0].id, 'second');
  assert.equal(new URL(h.requests[1].path, 'https://example.invalid').searchParams.get('cursor'), 'page2');
  assert.equal(h.requests[1].path, h.requests[2].path); h.queue.dispose();
});


test('browser transfer requires closed admission proof and preserves sparse advancing inventory', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let closed = false;
  const h = harness(path => ({closed, items: closed ? [] : [preparation('not-yet-stable')], nextCursor: path.includes('cursor=page2') ? '' : 'page2'}));
  const pending = h.queue.open('operation', 'device', source()); await flush();
  assert.equal(h.queue.getTransfers().phase, 'waiting'); assert.equal(h.queue.getTransfers().nextCursor, '');
  assert.equal(h.queue.getTransfers().items.length, 0); assert.equal(h.queue.getTransfers().closed, false);
  await h.queue.transferNext(); assert.equal(h.handoffs.length, 0);
  closed = true; t.mock.timers.tick(1500); await pending;
  assert.equal(h.queue.getTransfers().phase, 'ready'); assert.equal(h.queue.getTransfers().nextCursor, 'page2');
  assert.equal(h.queue.getTransfers().closed, true);
  // A closed sparse page is not EOF until its advancing cursor is exhausted.
  await h.queue.nextPage(); assert.equal(h.queue.getTransfers().phase, 'complete');
  assert.equal(h.requests[0].path, h.requests[1].path); h.queue.dispose();
});
test('browser transfer missing header is explicitly unavailable, never a successful empty drain', async () => {
  const h = harness(() => {throw Object.assign(new Error('missing header'), {status: 404, code: 'not_found'});});
  await h.queue.open('operation', 'device', source());
  assert.equal(h.queue.getTransfers().phase, 'error'); assert.equal(h.queue.getTransfers().errorCode, 'not_found');
  assert.equal(h.queue.getTransfers().closed, false); assert.equal(h.handoffs.length, 0); h.queue.dispose();
});
test('browser transfer cannot reopen an already closed fixed cursor drain', async () => {
  const h = harness(path => new URL(path, 'https://example.invalid').searchParams.has('cursor')
    ? {closed: false, items: [], nextCursor: ''} : {closed: true, items: [preparation()], nextCursor: 'page2'});
  await h.queue.open('operation', 'device', source()); h.queue.skipNext(); await h.queue.nextPage();
  assert.equal(h.queue.getTransfers().phase, 'error'); assert.equal(h.queue.getTransfers().closed, true);
  assert.equal(h.queue.getTransfers().nextCursor, 'page2'); h.queue.dispose();
});
test('browser transfer source retirement cancels header waiting and its scheduled poll', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const h = harness(() => ({closed: false, items: [], nextCursor: 'active-page'})), live = source();
  const pending = h.queue.open('operation', 'device', live); await flush();
  assert.equal(h.queue.getTransfers().phase, 'waiting'); live.retire(); await pending;
  t.mock.timers.tick(5000); await flush();
  assert.equal(h.requests.length, 1); assert.equal(h.queue.getTransfers().phase, 'idle');
  assert.equal(live.listeners.size, 0); h.queue.dispose();
});
