import test from 'node:test';
import assert from 'node:assert/strict';
import {HomeSelectionDestinations} from '../src/home-selection/destinations.ts';
import type {HomeSelectionPicker} from '../src/home-selection/types.ts';
import {pickerResultSucceeded} from '../src/selection-outcomes.ts';

const target = {mode: 'query', total: 50000, sourceTotal: 50000, selector: {query: {libraryId: 'library', pivot: 'albums',
  filter: {variant: 'predicate', field: 'genre', operator: 'equals', value: 'Rock'}}},
  summary: {count: 50000, types: [{kind: 'album', count: 50000}], every: {playlist: true, collection: true}, library: {kind: 'single', libraryId: 'library'}},
  refinement: {kind: null, excludedItems: []}, expected: {catalogRevision: 'reading-1'}};
const list = (id = 'list', kind = 'playlist', count = 0) => ({id, kind, name: 'Long-view list', owner: {id: 'owner', name: 'Owner'}, role: 'owner', count, covers: [], revision: '2',
  createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z'});
type Call = {path: string; method: string; body?: any; key?: string};
function fixture({failed = 0, running = false, result = true, kind = 'playlist'} = {}) {
  const calls: Call[] = [], listeners = new Set<(event: unknown) => void>(), cache = new Map<string, unknown>();
  let state = running ? 'running' : failed ? 'failed' : 'done', count = kind === 'collection' ? 122000 : 3000, failStart = false, hidden = false;
  const answer = () => ({id: 'job', kind: 'add_view_to_list', state, progress: {done: 70000, total: 70000},
    error: 'Added 999999. Human wording must never be parsed.', ...(result ? {result: hidden ? {added: 0, alreadyPresent: 0, failed: 0} : {added: kind === 'collection' ? 1000 : 120000, alreadyPresent: kind === 'collection' ? 69000 : 0, failed}} : {})});
  const request = async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    if (path === '/v1/lists') return {items: [list('list', kind, count)]} as T;
    if (path.startsWith('/v1/lists?')) return {items: [list('list', kind, count)]} as T;
    if (path === '/v1/lists/list') return list('list', kind, count) as T;
    if (path === '/v1/jobs/job' && method === 'DELETE') {state = 'cancelled'; queueMicrotask(() => {for (const listener of listeners) listener({resource: {id: 'job'}});}); return undefined as T;}
    if (path === '/v1/jobs/job') return answer() as T;
    if (path.startsWith('/v1/jobs/job/failures')) {
      const start = Number(new URL(path, 'http://test').searchParams.get('cursor') ?? 0), size = Math.min(50, failed - start);
      return {items: Array.from({length: Math.max(0, size)}, (_, n) => ({itemId: `source-${start + n}`, title: `Album ${start + n}`, error: {code: 'not_allowed', message: 'Not allowed.'}})),
        ...(start + size < failed ? {nextCursor: String(start + size)} : {})} as T;
    }
    throw new Error('Unexpected ' + method + ' ' + path);
  };
  const api = {request, requestRaw: async (path: string, method: 'POST', options: {headers?: Readonly<Record<string, string>>; body?: unknown} = {}) => {
    const key = options.headers?.['Idempotency-Key'] ?? ''; assert.ok(key);
    calls.push({path, method, body: options.body, key});
    if (cache.has(key)) return {status: 200, headers: {}, body: cache.get(key)};
    if (path === '/v1/jobs') {const job = answer();count += job.result?.added ?? 0;cache.set(key, job);if (failStart) {failStart = false;throw new Error('Lost response');}return {status: 202, headers: {}, body: job};}
    if (path === '/v1/lists') {count = 0;const made = list('list', kind); cache.set(key, made); return {status: 201, headers: {}, body: made};}
    throw new Error('Unexpected write ' + path);
  }};
  let snapshot: HomeSelectionPicker | undefined, current = true, onChange: ((value: HomeSelectionPicker) => void) | undefined;
  const signal = new AbortController();
  const picker = new HomeSelectionDestinations({api, events: {on: (_type: string, listener: (event: unknown) => void) => {listeners.add(listener); return () => listeners.delete(listener);}},
    requestId: async () => 'operation'} as never, target as never, kind as never, signal.signal, () => current, value => {snapshot = value; onChange?.(value);});
  return {picker, calls, snapshot: () => snapshot!, setChange: (listener: (value: HomeSelectionPicker) => void) => {onChange = listener;},
    loseStartAnswer: () => {failStart = true;}, hideSources: () => {hidden = true;state = 'done';}, retire: () => {current = false; signal.abort(); picker.dispose();}};
}

test('a whole-view addition is one keyed server job; counts come only from its structured result', async () => {
  const f = fixture();
  await f.picker.load(); await f.picker.choose('list');
  const receipt = f.snapshot().receipt!;
  assert.deepEqual([receipt.added, receipt.alreadyPresent, receipt.failed], [120000, 0, 0]);
  assert.equal(f.snapshot().phase, 'added');
  assert.deepEqual(f.calls.filter(call => call.method === 'POST'), [{path: '/v1/jobs', method: 'POST', key: 'operation',
    body: {kind: 'add_view_to_list', target: {filter: 'library=library&kind=album&genre=Rock'}, params: {list: 'list'}}}]);
  assert.equal(f.calls.some(call => call.path.startsWith('/v1/items')), false);
});

test('creating a list for a whole view creates it empty, then submits the same complete view', async () => {
  const f = fixture(); await f.picker.load(); await f.picker.create('New list');
  const writes = f.calls.filter(call => call.method === 'POST');
  assert.deepEqual(writes[0].body, {kind: 'playlist', name: 'New list'});
  assert.equal(writes[1].path, '/v1/jobs'); assert.equal(writes[1].key, 'operation.view');
  assert.equal(f.snapshot().receipt?.added, 120000);
});

test('Cancel stops the job and retains committed receipt counts without claiming full success', async () => {
  const f = fixture({running: true}); let asked = false;
  f.setChange(value => {if (value.phase === 'adding' && value.jobs.length && !asked) {asked = true; queueMicrotask(() => {void f.picker.cancel();});}});
  await f.picker.load(); await f.picker.choose('list');
  const receipt = f.snapshot().receipt!;
  assert.equal(receipt.cancelled, true); assert.equal(receipt.added, 120000); assert.equal(f.snapshot().phase, 'partial');
  assert.equal(f.calls.filter(call => call.method === 'DELETE').length, 1);
  assert.equal(pickerResultSucceeded({inserted: receipt.added, failed: 0, failures: [], complete: true, cancelled: receipt.cancelled}), false);
});

test('failed source titles page in bounded windows without reading or re-adding the view', async () => {
  const f = fixture({failed: 52}); await f.picker.load(); await f.picker.choose('list');
  assert.equal(f.snapshot().receipt!.failures.length, 50); assert.equal(f.snapshot().next, true);
  assert.equal(f.snapshot().receipt!.failureEntries[0].kind, 'album', 'failure titles are source containers, not invented expanded leaves');
  await f.picker.page(); assert.equal(f.snapshot().receipt!.failures.length, 2); assert.equal(f.snapshot().previous, true);
  await f.picker.page(true); assert.equal(f.snapshot().receipt!.failures.length, 50);
  await f.picker.retry();
  assert.deepEqual([f.snapshot().receipt!.added, f.snapshot().receipt!.failed], [120000, 52]);
  assert.equal(f.calls.filter(call => call.path === '/v1/jobs').length, 1, 'checking the result never repeats the successful whole view');
  assert.equal(f.calls.some(call => call.path.endsWith('/entries') || call.path.startsWith('/v1/items')), false);
});

test('a human note without structured counts cannot become a successful receipt', async () => {
  const f = fixture({result: false}); await f.picker.load(); await f.picker.choose('list');
  assert.equal(f.snapshot().phase, 'uncertain'); assert.equal(f.snapshot().receipt, null);
  assert.equal(f.snapshot().error?.code, 'list-job-result-unavailable');
});

test('collection already-present counts are read as numbers, not inferred from list size', async () => {
  const f = fixture({kind: 'collection'}); await f.picker.load(); await f.picker.choose('list');
  assert.deepEqual([f.snapshot().receipt!.added, f.snapshot().receipt!.alreadyPresent], [1000, 69000]);
});

test('an unconfirmed job start retries its original key rather than making a second addition', async () => {
  const f = fixture(); await f.picker.load(); f.loseStartAnswer(); await f.picker.choose('list');
  assert.equal(f.snapshot().phase, 'uncertain'); await f.picker.retry();
  const starts = f.calls.filter(call => call.path === '/v1/jobs');
  assert.equal(starts.length, 2); assert.equal(starts[0].key, starts[1].key);
  assert.equal(f.snapshot().receipt?.added, 120000);
});

test('a fresh result uses the current reader counts and drops failure titles that no longer contribute', async () => {
  const f = fixture({failed: 2}); await f.picker.load(); await f.picker.choose('list');
  f.hideSources(); await f.picker.retry();
  const receipt = f.snapshot().receipt!;
  assert.deepEqual([receipt.added, receipt.alreadyPresent, receipt.failed, receipt.failureEntries.length], [0, 0, 0, 0]);
});

test('leaving the picker stops observation and never cancels an already running durable job', async () => {
  const f = fixture({running: true}); let publications = 0;
  f.setChange(value => {publications++;if (value.jobs.length) queueMicrotask(f.retire);});
  await f.picker.load(); await f.picker.choose('list'); const before = publications;
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(publications, before); assert.equal(f.calls.some(call => call.method === 'DELETE'), false);
});
