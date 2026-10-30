import test from 'node:test';
import assert from 'node:assert/strict';
import {getEventListeners} from 'node:events';
import {
  FacetReadSession, FacetWindowError, SIMPLE_FACET_PAGE_BYTES,
  parseSimpleFacetPage, parseSimpleFacetPreparing, parseSimpleFacetFallbackPage, simpleFacetPath,
  type SimpleFacetRead, type FacetReadSessionOptions,
} from '../src/facet-windows.ts';
import type {PreparationResponse, PreparationRuntime} from '../src/read-preparation.ts';
import {routePaths} from '../../contracts/src/route-paths.ts';

const row = (value = 'Drama', label = value) => ({value, label, count: 5, countIsLowerBound: false});
function page(queryId = 'query_1', over: Record<string, unknown> = {}) {
  return {kind: 'simple-keyset', field: 'genre', revision: 'basis_1', total: 3, totalIsLowerBound: false,
    values: [row()], window: {queryId, basis: 'basis_1', limit: 2, returned: 1, next: 'next_1', reload: 'reload_1'}, ...over};
}
const response = (status: number, body?: unknown, headers: Record<string, string> = {}): PreparationResponse => ({status, body, headers});
const preparing = (queryId = 'query_1', retryAfterMs = 1000) => ({kind: 'facet-preparing', state: 'preparing', queryId, retryAfterMs});
const read: SimpleFacetRead = {libraryId: 'movies', field: 'genre', q: 'D', limit: 2};
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));

class Clock implements PreparationRuntime {
  time = Date.parse('Wed, 28 Oct 2026 12:00:00 GMT');
  now = () => this.time;
  random = () => 0;
  next = 0;
  timers = new Map<number, {callback: () => void; ms: number}>();
  delays: number[] = [];
  setTimeout = (callback: () => void, ms: number) => {
    assert.equal(this.timers.size, 0, 'one timer per operation');
    const id = ++this.next;
    this.timers.set(id, {callback, ms}); this.delays.push(ms); return id;
  };
  clearTimeout = (id: unknown) => { this.timers.delete(id as number); };
  fire() {
    assert.equal(this.timers.size, 1);
    const [id, timer] = this.timers.entries().next().value!;
    this.timers.delete(id); this.time += timer.ms; timer.callback();
  }
}
async function flush() { for (let i = 0; i < 40; i++) await Promise.resolve(); }
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>(yes => { resolve = yes; });
  return {promise, resolve};
}
class TypedError extends Error { constructor(readonly raw: PreparationResponse) { super(`status ${raw.status}`); } }
function fixture(replies: (PreparationResponse | Promise<PreparationResponse>)[] = [response(200, page())], over: Partial<FacetReadSessionOptions> = {}) {
  const clock = new Clock(), external = new AbortController();
  const calls: {path: string; method: string; signal?: AbortSignal; singleAttempt?: boolean}[] = [];
  const pendingEvents: unknown[] = [], errors: TypedError[] = [];
  let current = true;
  const options: FacetReadSessionOptions = {
    api: {requestRaw(path, method, options = {}) {
      calls.push({path, method, ...options});
      if (method === 'DELETE') return Promise.resolve(response(204));
      assert.ok(replies.length, 'no eager or extra page request');
      return Promise.resolve(replies.shift()!);
    }},
    // Simulated generated adapters; production caller supplies get_facet_query/cancel_facet_query.
    paths: {poll: id => `/v1/facet-queries/${id}`, cancel: id => `/v1/facet-queries/${id}`},
    retainReadyHandle: true, signal: external.signal, isCurrent: () => current, runtime: clock,
    errorFromResponse: raw => { const error = new TypedError(raw); errors.push(error); return error; },
    onPreparing: pending => { pendingEvents.push(pending); }, ...over,
  };
  const session = new FacetReadSession(options);
  return {session, options, clock, external, calls, errors, pendingEvents,
    supersede: () => { current = false; },
    gets: () => calls.filter(c => c.method === 'GET'), deletes: () => calls.filter(c => c.method === 'DELETE')};
}

test('exact parser preserves count order, empty labels, scalar spelling and safe maximum counts', () => {
  const raw = page('query_1', {values: [row('Mixed CASE', ''), {...row('é\n', '  É\t'), count: Number.MAX_SAFE_INTEGER}],
    total: Number.MAX_SAFE_INTEGER, window: {...page().window, returned: 2}});
  const parsed = parseSimpleFacetPage(raw, {field: 'genre', limit: 2});
  assert.deepEqual(parsed, raw);
  assert.ok(Object.isFrozen(parsed) && Object.isFrozen(parsed.values) && Object.isFrozen(parsed.values[0]) && Object.isFrozen(parsed.window));
  assert.equal(parsed.values[0].label, '', 'no fallback to value');
  raw.values[0].value = 'mutated';
  assert.equal(parsed.values[0].value, 'Mixed CASE');
});

test('legal oversized scalars are lossless; parser does not truncate, drop or invent a cap', () => {
  const scalar = '  藝術\n'.repeat(SIMPLE_FACET_PAGE_BYTES);
  const parsed = parseSimpleFacetPage(page('query_1', {values: [row(scalar, scalar)]}));
  assert.equal(parsed.values.length, 1);
  assert.equal(parsed.values[0].value, scalar);
  assert.equal(parsed.values[0].label, scalar);
});

test('empty exact window is distinct from preparing, and optional Reading identity remains exact', () => {
  const empty = page('query_1', {total: 0, values: [], window: {queryId: 'query_1', basis: 'basis_1', limit: 2, returned: 0}});
  assert.deepEqual(parseSimpleFacetPage(empty), empty);
  const seriesId = 'x'.repeat(43);
  assert.equal(parseSimpleFacetPage(page('query_1', {values: [{...row(), seriesId}]})).values[0].seriesId, seriesId);
});

test('legacy, lower-bound, malformed or inconsistent ready responses reject the entire page', async t => {
  const cases: [string, (raw: ReturnType<typeof page>) => void][] = [
    ['legacy kind', r => { delete (r as Partial<typeof r>).kind; }],
    ['preparing kind', r => { r.kind = 'facet-preparing'; }],
    ['wrong field', r => { r.field = 'actor'; }],
    ['lower total', r => { r.totalIsLowerBound = true; }],
    ['unsafe total', r => { r.total = Number.MAX_SAFE_INTEGER + 1; }],
    ['negative total', r => { r.total = -1; }],
    ['fraction total', r => { r.total = 1.5; }],
    ['too few total', r => { r.total = 0; }],
    ['missing revision', r => { r.revision = ''; }],
    ['revision/basis mismatch', r => { r.window.basis = 'basis_2'; }],
    ['returned mismatch', r => { r.window.returned = 0; }],
    ['returned > limit', r => { r.window.limit = 0; }],
    ['limit too large', r => { r.window.limit = 201; }],
    ['fraction limit', r => { r.window.limit = 1.5; }],
    ['missing reload', r => { delete (r.window as Partial<typeof r.window>).reload; }],
    ['token too long', r => { r.window.next = 'x'.repeat(513); }],
    ['foreign token', r => { r.window.next = 'https://foreign.example'; }],
    ['non-url token alphabet', r => { r.window.queryId = 'a.b'; }],
    ['missing exact row flag', r => { delete (r.values[0] as Partial<typeof r.values[0]>).countIsLowerBound; }],
    ['row lower bound', r => { r.values[0].countIsLowerBound = true; }],
    ['zero row count', r => { r.values[0].count = 0; }],
    ['unsafe row count', r => { r.values[0].count = Number.MAX_SAFE_INTEGER + 1; }],
    ['empty value', r => { r.values[0].value = ''; }],
    ['duplicate value', r => { r.values.push(row()); r.window.returned = 2; }],
  ];
  for (const [name, change] of cases) await t.test(name, () => {
    const raw = clone(page()); change(raw);
    assert.throws(() => parseSimpleFacetPage(raw), error => error instanceof FacetWindowError && error.code === 'invalid_facet_window');
  });
  for (const bad of [null, [], {}, preparing(), {...page(), values: null}, {...page(), window: null},
    {...page(), values: [null]}, {...page(), values: [{...row(), label: null}]},
    {...page(), values: [{...row(), seriesId: 'wrong'}]},
    {...page(), window: {...page().window, next: null}}, {...page(), state: 'preparing'},
    {...page(), total: '3'}, {...page(), window: {...page().window, queryId: 12}},
    {...page(), values: [], window: {...page().window, returned: 0}},
  ]) assert.throws(() => parseSimpleFacetPage(bad));
  assert.throws(() => parseSimpleFacetPage(page(), {field: 'studio'}));
  assert.throws(() => parseSimpleFacetPage(page(), {limit: 1}));
});

test('row and token bounds are exact, without deduplication or omissions', () => {
  const values = Array.from({length: 200}, (_, i) => row(String(i)));
  const raw = page('x'.repeat(512), {total: 201, values, window: {...page().window, queryId: 'x'.repeat(512), limit: 200, returned: 200, next: 'x'.repeat(512)}});
  assert.equal(parseSimpleFacetPage(raw).values.length, 200);
  assert.throws(() => parseSimpleFacetPage({...raw, values: [...values, row('201')]}));
});

test('preparing parser validates its domain, private ID and positive JSON-safe retry milliseconds', () => {
  assert.deepEqual(parseSimpleFacetPreparing(preparing()), preparing());
  assert.equal(parseSimpleFacetPreparing(preparing('x'.repeat(512), Number.MAX_SAFE_INTEGER)).queryId.length, 512);
  for (const bad of [null, {}, page(), {...preparing(), kind: 'reading-preparing'}, {...preparing(), state: 'ready'},
    {...preparing(), values: []}, {...preparing(), total: 0},
    ...['', 'a.b', '../path', 'x'.repeat(513), 3].map(queryId => ({...preparing(), queryId})),
    ...[0, -1, 1.5, Infinity, Number.MAX_SAFE_INTEGER + 1, '1000'].map(retryAfterMs => ({...preparing(), retryAfterMs})),
  ]) assert.throws(() => parseSimpleFacetPreparing(bad));
});

test('SIMPLE generated path has exact field/q/limit and one opaque anchor, never offset/context', () => {
  const url = new URL(simpleFacetPath({...read, libraryId: 'Library / ID', q: '  É & + ', before: 'opaque_1'}), 'https://local.invalid');
  assert.equal(url.pathname, '/v1/libraries/Library%20%2F%20ID/facets');
  assert.deepEqual([...url.searchParams], [['field', 'genre'], ['q', '  É & + '], ['limit', '2'], ['before', 'opaque_1']]);
  assert.equal(new URL(simpleFacetPath({libraryId: 'movies', field: 'year'}), url).searchParams.get('limit'), '200');
  for (const q of ['x'.repeat(100), 'é'.repeat(50), '😀'.repeat(25), '', 'A']) assert.equal(new URL(simpleFacetPath({...read, q}), url).searchParams.get('q'), q);
  for (const bad of [{...read, q: 'é'.repeat(51)}, {...read, q: '😀'.repeat(26)}, {...read, q: null}, {...read, q: '\ud800'}, {...read, q: '\udc00'},
    {...read, offset: 0}, {...read, offset: 1}, {...read, pivot: 'movies'}, {...read, query: {}}, {...read, libraryId: '*'},
    {...read, field: 'actor'}, {...read, after: 'a', before: 'b'}, {...read, at: ''}, {...read, after: 'x'.repeat(513)},
    {...read, limit: null}, {...read, limit: 201}, {...read, limit: 0},
  ]) assert.throws(() => simpleFacetPath(bad as SimpleFacetRead));
});

test('one direct 200 retains one page/lease; cancel and retire release exactly once and clear rows/tokens', async () => {
  const f = fixture();
  const result = await f.session.read(read);
  assert.equal(f.session.page, result);
  assert.equal(f.session.window, result.window);
  assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 0);
  f.session.cancel(); f.session.cancel();
  assert.equal(f.session.page, undefined); assert.equal(f.session.window, undefined);
  assert.equal(f.deletes().length, 1);
  f.session.retire(); f.external.abort();
  assert.equal(f.deletes().length, 1);
  assert.equal(getEventListeners(f.external.signal, 'abort').length, 0);
  await assert.rejects(f.session.read(read), {name: 'AbortError'});
});

test('explicit one-shot policy releases success and refuses continuation', async () => {
  const f = fixture(undefined, {retainReadyHandle: false}), perRead = new AbortController();
  await f.session.read(read, perRead.signal);
  assert.equal(f.deletes().length, 1);
  assert.equal(f.session.window, undefined);
  await assert.rejects(f.session.next(), error => error instanceof FacetWindowError && error.code === 'facet_continuation_unavailable');
  perRead.abort();
  assert.equal(f.session.page, undefined);
  assert.equal(f.deletes().length, 1);
  f.session.retire();
});

test('202 remains preparing until one scheduled poll, honors full Retry-After and preserves raw metadata', async () => {
  const raw = response(202, preparing(), {Location: '/v1/facet-queries/query_1', 'Retry-After': '120'});
  const f = fixture([raw, response(200, page())]);
  let seen: PreparationResponse | undefined;
  const options = {...f.options, onPreparing: (pending: unknown, meta: PreparationResponse) => { f.pendingEvents.push(pending); seen = meta; }};
  f.session.retire();
  const session = new FacetReadSession(options);
  const done = session.read(read);
  await flush();
  assert.equal(session.page, undefined);
  assert.deepEqual(f.pendingEvents, [preparing()]);
  assert.equal(seen, raw);
  assert.deepEqual(f.clock.delays, [120_001]);
  assert.equal(f.gets().length, 1);
  f.clock.fire();
  const result = await done;
  assert.equal(result.kind, 'simple-keyset');
  assert.equal(f.gets()[1].path, '/v1/facet-queries/query_1');
  assert.equal(f.deletes().length, 0);
  session.retire(); assert.equal(f.deletes().length, 1);
});

test('HTTP-date floor and long timer chunks are inherited without real sleeps', async () => {
  const floor = 2_147_483_647 + 10;
  const f = fixture([response(202, preparing('query_1', floor)), response(200, page())]);
  const done = f.session.read(read);
  await flush(); f.clock.fire(); await flush();
  assert.equal(f.gets().length, 1);
  assert.deepEqual(f.clock.delays, [2_147_483_647, 11]);
  f.clock.fire(); await done; f.session.retire();
  const g = fixture([response(202, preparing(), {'Retry-After': 'Wed, 28 Oct 2026 12:02:00 GMT'}), response(200, page())]);
  const other = g.session.read(read); await flush();
  assert.deepEqual(g.clock.delays, [120_001]);
  g.clock.fire(); await other; g.session.retire();
});

test('explicit continuation keeps old handle alive until token is consumed, then drops old lease/page', async () => {
  const ack = deferred<PreparationResponse>();
  const f = fixture([response(200, page()), ack.promise, response(200, page('query_2', {values: [row('Comedy')]}))]);
  const first = await f.session.read(read);
  const next = f.session.next();
  await flush();
  assert.equal(f.session.page, undefined, 'no old ready array retained during replacement');
  assert.equal(f.deletes().length, 0, 'token still needs predecessor handle');
  assert.equal(new URL(f.gets()[1].path, 'https://local.invalid').searchParams.get('after'), first.window.next);
  ack.resolve(response(202, preparing('query_2'))); await flush();
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1']);
  f.clock.fire();
  const second = await next;
  assert.equal(f.session.page, second); assert.equal(f.session.window?.queryId, 'query_2');
  assert.equal(f.gets().length, 3, 'no eager drain');
  f.session.retire();
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1', '/v1/facet-queries/query_2']);
});

test('next/previous/reload are explicit generated row-anchor reads with no history or offsets', async () => {
  const withPrevious = (id: string) => page(id, {window: {...page(id).window, previous: 'previous_1'}});
  const f = fixture([response(200, withPrevious('query_1')), response(200, withPrevious('query_2')),
    response(200, withPrevious('query_3')), response(200, withPrevious('query_4'))]);
  await f.session.read(read); await f.session.next(); await f.session.previous(); await f.session.reload();
  const queries = f.gets().map(c => new URL(c.path, 'https://local.invalid').searchParams);
  assert.equal(queries[1].get('after'), 'next_1');
  assert.equal(queries[2].get('before'), 'previous_1');
  assert.equal(queries[3].get('at'), 'reload_1');
  for (const query of queries) { assert.equal(query.get('offset'), null); assert.equal(query.get('q'), 'D'); }
  assert.equal(f.session.window?.queryId, 'query_4');
  assert.equal(f.deletes().length, 3);
  f.session.retire();
});

test('replace and reanchor clear old leases before GET and never reuse row tokens', async () => {
  const f = fixture([response(200, page()), response(200, page('query_2')), response(200, page('query_3'))]);
  await f.session.read(read);
  await f.session.replace({...read, q: 'x'});
  assert.deepEqual(f.calls.slice(0, 3).map(c => c.method), ['GET', 'DELETE', 'GET']);
  await f.session.reanchor();
  assert.equal(f.deletes().length, 2);
  const query = new URL(f.gets().at(-1)!.path, 'https://local.invalid').searchParams;
  assert.equal(query.get('q'), 'x'); assert.equal(query.has('after') || query.has('before') || query.has('at'), false);
  f.session.retire();
});

test('409 reanchors once at a newly accepted basis; second stale remains typed and leaves no lease', async () => {
  const newer = page('query_2', {revision: 'basis_2', window: {...page('query_2').window, basis: 'basis_2'}});
  const f = fixture([response(200, page()), response(409), response(200, newer)]);
  await f.session.read(read); await f.session.next();
  assert.equal(f.session.window?.basis, 'basis_2');
  const query = new URL(f.gets()[2].path, 'https://local.invalid').searchParams;
  assert.equal(query.has('after'), false); assert.equal(query.get('q'), 'D');
  f.session.retire();
  const stale = response(409, {error: {code: 'stale_continuation'}}), g = fixture([response(409), stale]);
  await assert.rejects(g.session.read({...read, after: 'old_row'}), error => error === g.errors[0] && (error as TypedError).raw === stale);
  assert.equal(g.gets().length, 2); assert.equal(g.session.page, undefined); assert.equal(g.session.window, undefined);
  g.session.retire();
});

test('continuation cannot silently mix basis, and mismatched field/limit responses never publish', async () => {
  const f = fixture([response(200, page()), response(200, page('query_2', {revision: 'basis_2', window: {...page('query_2').window, basis: 'basis_2'}}))]);
  await f.session.read(read);
  await assert.rejects(f.session.next(), error => error instanceof FacetWindowError && error.code === 'stale_continuation');
  assert.equal(f.session.page, undefined); assert.equal(f.deletes().length, 2); f.session.retire();
  for (const wrong of [{field: 'studio'}, {window: {...page().window, limit: 3}}]) {
    const g = fixture([response(200, page('query_1', wrong))]);
    await assert.rejects(g.session.read(read), FacetWindowError);
    assert.equal(g.session.page, undefined); g.session.retire();
  }
});

test('same-basis continuation total is immutable, and changed totals require explicit stale refresh', async () => {
  const f = fixture([response(200, page()), response(200, page('query_2', {total: 4}))]);
  await f.session.read(read);
  await assert.rejects(f.session.next(), error => error instanceof FacetWindowError && error.code === 'stale_continuation');
  assert.equal(f.session.page, undefined); assert.equal(f.deletes().length, 2); f.session.retire();
});

test('legal oversized ready row returns losslessly but is not cached; compact lease still supports navigation', async () => {
  const scalar = 'é😀'.repeat(SIMPLE_FACET_PAGE_BYTES);
  const f = fixture([response(200, page('query_1', {values: [row(scalar, scalar)]})), response(200, page('query_2'))]);
  const result = await f.session.read(read);
  assert.equal(result.values[0].value, scalar); assert.equal(result.values[0].label, scalar);
  assert.equal(f.session.page, undefined, 'no exceptional ready array in session cache');
  assert.equal(f.session.window?.queryId, 'query_1');
  await f.session.next(); assert.equal(f.session.page?.window.queryId, 'query_2');
  f.session.retire();
});

test('429/503, denied and legacy 200 are failures, never ready/empty or automatically retried', async t => {
  for (const status of [429, 503, 401, 403, 404, 400]) await t.test(String(status), async () => {
    const raw = response(status, {error: {code: 'original'}}, {'Retry-After': '120'}), f = fixture([raw]);
    await assert.rejects(f.session.read(read), error => error === f.errors[0] && (error as TypedError).raw === raw);
    assert.equal(f.session.page, undefined); assert.equal(f.gets().length, 1); assert.equal(f.clock.delays.length, 0);
    f.session.retire();
  });
  const f = fixture([response(200, {field: 'genre', values: [], total: 0, totalIsLowerBound: false})]);
  await assert.rejects(f.session.read(read), FacetWindowError); assert.equal(f.session.page, undefined); f.session.retire();
});

test('malformed pending and foreign Location do not poll or turn into empty; known handle cancellation stays local', async () => {
  const f = fixture([response(202, {...preparing(), values: []})]);
  await assert.rejects(f.session.read(read), FacetWindowError);
  assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 0); f.session.retire();
  const g = fixture([response(202, preparing(), {Location: 'https://foreign.example/query_1'})]);
  await assert.rejects(g.session.read(read));
  assert.equal(g.gets().length, 1); assert.equal(g.deletes()[0].path, '/v1/facet-queries/query_1'); g.session.retire();
  const h = fixture([response(202, preparing())], {paths: {poll: id => `/v1/wrong/${id}`, cancel: id => `/v1/facet-queries/${id}`}});
  await assert.rejects(h.session.read(read), FacetWindowError);
  assert.equal(h.gets().length, 1); assert.equal(h.deletes()[0].path, '/v1/facet-queries/query_1'); h.session.retire();
});

test('replacement aborts previous request and discards/cancels late ready without overwriting the new page', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise, response(200, page('query_2'))]);
  const old = f.session.read(read), rejected = assert.rejects(old, {name: 'AbortError'});
  const newer = await f.session.replace({...read, q: 'new'}); await rejected;
  assert.equal(f.gets()[0].signal?.aborted, true);
  late.resolve(response(200, page())); await flush();
  assert.equal(f.session.page, newer); assert.equal(f.session.window?.queryId, 'query_2');
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1']);
  f.session.retire(); assert.equal(f.deletes().length, 2);
});

test('retirement during preparation clears timer, cancels once and never polls', async () => {
  const f = fixture([response(202, preparing())]);
  const done = f.session.read(read), rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); f.session.retire(); await rejected; await flush();
  assert.equal(f.clock.timers.size, 0); assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 1);
  assert.equal(f.session.page, undefined); assert.equal(f.session.window, undefined);
});

test('external abort, per-read abort and isCurrent loss clear ready lease/rows on every lifecycle', async t => {
  for (const mode of ['external', 'per-read', 'isCurrent']) await t.test(mode, async () => {
    const f = fixture(), controller = new AbortController();
    await f.session.read(read, controller.signal);
    if (mode === 'external') f.external.abort(); else if (mode === 'per-read') controller.abort(); else f.supersede();
    assert.equal(f.session.page, undefined); assert.equal(f.session.window, undefined); assert.equal(f.deletes().length, 1);
    assert.equal(getEventListeners(controller.signal, 'abort').length, 0);
    f.session.retire(); assert.equal(getEventListeners(f.external.signal, 'abort').length, 0);
  });
});

test('abort while continuation response is late releases predecessor and new handle once each', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([response(200, page()), late.promise]);
  await f.session.read(read);
  const done = f.session.next(), rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); f.external.abort(); await rejected;
  late.resolve(response(202, preparing('query_2'))); await flush();
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1', '/v1/facet-queries/query_2']);
  assert.equal(f.clock.timers.size, 0); assert.equal(f.session.page, undefined);
});

test('pre-abort/current loss prevent reads; preparing callback can retire or replace without late publication', async () => {
  const f = fixture(); f.external.abort(); await assert.rejects(f.session.read(read), {name: 'AbortError'}); assert.equal(f.calls.length, 0);
  const g = fixture(); g.supersede(); await assert.rejects(g.session.read(read), {name: 'AbortError'}); assert.equal(g.calls.length, 0);
  const h = fixture([response(202, preparing())]); h.session.retire();
  let session!: FacetReadSession;
  session = new FacetReadSession({...h.options, onPreparing: () => session.retire()});
  await assert.rejects(session.read(read), {name: 'AbortError'});
  assert.equal(h.deletes().length, 1); assert.equal(h.clock.timers.size, 0);
});

test('captured q/field/limit cannot change under a pending response or stale reanchor', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise, response(200, page('query_2'))]);
  const input = {...read};
  const done = f.session.read(input);
  input.q = 'replaced'; input.limit = 200; input.field = 'studio';
  late.resolve(response(409)); await done;
  const query = new URL(f.gets()[1].path, 'https://local.invalid').searchParams;
  assert.deepEqual([...query], [['field', 'genre'], ['q', 'D'], ['limit', '2']]);
  assert.equal(f.session.page?.field, 'genre'); f.session.retire();
});

test('an unavailable edge performs no GET and keeps current interest until explicitly retired', async () => {
  const f = fixture(); await f.session.read(read);
  await assert.rejects(f.session.previous(), error => error instanceof FacetWindowError && error.code === 'facet_continuation_unavailable');
  assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 0); assert.equal(f.session.window?.queryId, 'query_1');
  f.session.retire(); assert.equal(f.deletes().length, 1);
});

function fallback(queryId = 'query_1', over: Record<string, unknown> = {}) {
  return {kind: 'facet-fallback', field: 'genre', total: 1, totalIsLowerBound: true,
    values: [{...row(), countIsLowerBound: true}], preparation: preparing(queryId), ...over};
}

test('strict fallback parser preserves explicit lower bounds, optional revision and legal long scalars', () => {
  const scalar = '  é😀\n'.repeat(SIMPLE_FACET_PAGE_BYTES);
  const raw = fallback('query_1', {revision: 'staging_1', total: Number.MAX_SAFE_INTEGER,
    values: [{...row(scalar, ''), count: Number.MAX_SAFE_INTEGER, countIsLowerBound: true}]});
  const parsed = parseSimpleFacetFallbackPage(raw, {field: 'genre', limit: 2});
  assert.deepEqual(parsed, raw); assert.equal(parsed.values[0].value, scalar); assert.equal(parsed.values[0].label, '');
  assert.ok(Object.isFrozen(parsed) && Object.isFrozen(parsed.values) && Object.isFrozen(parsed.values[0]) && Object.isFrozen(parsed.preparation));
  assert.equal('window' in parsed, false);
  assert.throws(() => parseSimpleFacetPage(parsed), FacetWindowError);
  const zero = fallback('query_1', {values: [{...row(), count: 0, countIsLowerBound: true}]});
  assert.equal(parseSimpleFacetFallbackPage(zero).values[0].count, 0, 'zero is a valid nonnegative lower bound');
});

test('fallback parser rejects exact flags, anchors, malformed preparation, unsafe counts and oversized row sets', async t => {
  const bads: [string, unknown][] = [
    ['legacy', {field: 'genre', total: 0, values: []}], ['exact', page()], ['pending', preparing()],
    ['missing total flag', {...fallback(), totalIsLowerBound: undefined}],
    ['false total flag', {...fallback(), totalIsLowerBound: false}],
    ['row exact flag', {...fallback(), values: [row()]}],
    ['row missing flag', {...fallback(), values: [{value: 'Drama', label: 'Drama', count: 1}]}],
    ['window', {...fallback(), window: page().window}], ['next', {...fallback(), next: 'opaque'}],
    ['after', {...fallback(), after: 'opaque'}], ['bad kind', {...fallback(), kind: 'simple-keyset'}],
    ['wrong field', {...fallback(), field: 'actor'}], ['bad revision', {...fallback(), revision: null}],
    ['missing preparation', {...fallback(), preparation: undefined}],
    ['malformed preparation', {...fallback(), preparation: {...preparing(), state: 'ready'}}],
    ['foreign ID', {...fallback(), preparation: preparing('https://foreign.example')}],
    ['unsafe total', {...fallback(), total: Number.MAX_SAFE_INTEGER + 1}],
    ['fraction total', {...fallback(), total: 1.5}], ['inconsistent total', {...fallback(), total: 0}],
    ['unsafe row count', {...fallback(), values: [{...row(), count: Number.MAX_SAFE_INTEGER + 1, countIsLowerBound: true}]}],
    ['negative row count', {...fallback(), values: [{...row(), count: -1, countIsLowerBound: true}]}],
    ['empty row value', {...fallback(), values: [{...row(''), countIsLowerBound: true}]}],
    ['duplicate rows', {...fallback(), total: 2, values: [{...row(), countIsLowerBound: true}, {...row(), countIsLowerBound: true}]}],
    ['too many rows', {...fallback(), total: 201, values: Array.from({length: 201}, (_, i) => ({...row(String(i)), countIsLowerBound: true}))}],
  ];
  for (const [name, raw] of bads) await t.test(name, () => {
    assert.throws(() => parseSimpleFacetFallbackPage(raw), error => error instanceof FacetWindowError && error.code === 'invalid_facet_window');
  });
  assert.throws(() => parseSimpleFacetFallbackPage(fallback(), {field: 'studio'}));
  assert.throws(() => parseSimpleFacetFallbackPage(fallback('query_1', {total: 2, values: [
    {...row('A'), countIsLowerBound: true}, {...row('B'), countIsLowerBound: true},
  ]}), {limit: 1}));
});

test('pending/fallback/exact share one handle; fallback is callback-only and read awaits exact', async () => {
  const raw = response(200, fallback(), {Location: '/v1/facet-queries/query_1', 'Retry-After': '120'});
  const f = fixture([response(202, preparing()), raw, response(200, page())]);
  f.session.retire();
  const seen: unknown[] = [];
  let rawSeen: PreparationResponse | undefined, finished = false;
  const session = new FacetReadSession({...f.options, onFallback: (value, meta) => { seen.push(value); rawSeen = meta; }});
  const done = session.read(read); void done.then(() => { finished = true; });
  await flush(); assert.equal(seen.length, 0); f.clock.fire(); await flush();
  assert.equal(seen.length, 1); assert.deepEqual(seen[0], fallback()); assert.equal(rawSeen, raw);
  assert.equal(finished, false); assert.equal(session.page, undefined); assert.equal(session.window, undefined);
  assert.equal(f.pendingEvents.length, 1); assert.deepEqual(f.clock.delays, [1001, 120_001]);
  await assert.rejects(session.next(), error => error instanceof FacetWindowError && error.code === 'facet_continuation_unavailable');
  assert.equal(f.gets().length, 2, 'fallback exposes no eager continuation');
  f.clock.fire(); const exact = await done;
  assert.equal(exact.kind, 'simple-keyset'); assert.equal(session.page, exact); assert.equal(session.window?.queryId, 'query_1');
  assert.equal(f.deletes().length, 0); session.retire(); assert.equal(f.deletes().length, 1);
});

test('empty and oversized fallback panels remain transient, explicitly lower-bound and still preparing', async t => {
  for (const large of [false, true]) await t.test(large ? 'oversized' : 'empty', async () => {
    const scalar = '漢字😀'.repeat(SIMPLE_FACET_PAGE_BYTES);
    const raw = fallback('query_1', large ? {values: [{...row(scalar, scalar), countIsLowerBound: true}]} : {values: [], total: 0});
    const f = fixture([response(200, raw), response(200, page())]); f.session.retire();
    let callbacks = 0;
    const session = new FacetReadSession({...f.options, onFallback(value) {
      callbacks++; assert.equal(value.totalIsLowerBound, true); assert.equal(value.preparation.state, 'preparing');
      if (large) { assert.equal(value.values[0].value, scalar); assert.equal(value.values[0].label, scalar); }
      else assert.deepEqual(value.values, []);
    }});
    const done = session.read(read); await flush();
    assert.equal(callbacks, 1); assert.equal(session.page, undefined); assert.equal(session.window, undefined);
    assert.equal(f.gets().length, 1); f.clock.fire(); await done; session.retire();
  });
});

test('repeated fallbacks publish independently with no cached rows/history or early exact settlement', async () => {
  const f = fixture([response(200, fallback()), response(200, fallback('query_1', {revision: 'staging_2', total: 2})), response(200, page())]);
  f.session.retire(); let callbacks = 0;
  const session = new FacetReadSession({...f.options, onFallback() { callbacks++; assert.equal(session.page, undefined); assert.equal(session.window, undefined); }});
  const done = session.read(read); await flush(); f.clock.fire(); await flush();
  assert.equal(callbacks, 2); assert.equal(f.gets().length, 2); assert.equal(f.deletes().length, 0);
  f.clock.fire(); await done; assert.equal(session.page?.kind, 'simple-keyset'); session.retire();
});

test('foreign fallback Location, mismatched pending handle and malformed lower bounds never publish', async t => {
  for (const mode of ['location', 'handle', 'flags']) await t.test(mode, async () => {
    const raw = mode === 'location' ? response(200, fallback(), {Location: 'https://foreign.example/query_1'})
      : mode === 'handle' ? response(200, fallback('query_2')) : response(200, {...fallback(), totalIsLowerBound: false});
    const f = fixture([response(202, preparing()), raw]); f.session.retire(); let callbacks = 0;
    const session = new FacetReadSession({...f.options, onFallback: () => { callbacks++; }});
    const done = session.read(read), rejected = assert.rejects(done);
    await flush(); f.clock.fire(); await rejected;
    assert.equal(callbacks, 0); assert.equal(session.page, undefined); assert.equal(session.window, undefined);
    assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1']); session.retire();
  });
});

test('fallback callback retirement/throw releases once and prevents further polls', async t => {
  for (const mode of ['retire', 'throw']) await t.test(mode, async () => {
    const f = fixture([response(200, fallback())]); f.session.retire();
    const sentinel = new Error('fallback publication failed');
    const session = new FacetReadSession({...f.options, onFallback: () => { if (mode === 'throw') throw sentinel; session.retire(); }});
    await assert.rejects(session.read(read), error => mode === 'throw' ? error === sentinel : (error as Error).name === 'AbortError');
    assert.equal(f.deletes().length, 1); assert.equal(f.clock.timers.size, 0); assert.equal(f.gets().length, 1);
    assert.equal(session.page, undefined); session.retire();
  });
});

test('late fallback after replacement cleans its handle without parsing/publishing rows or overwriting exact', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise, response(200, page('query_2'))]); f.session.retire();
  let callbacks = 0;
  const session = new FacetReadSession({...f.options, onFallback: () => { callbacks++; }});
  const old = session.read(read), rejected = assert.rejects(old, {name: 'AbortError'});
  const exact = await session.replace({...read, q: 'new'}); await rejected;
  // Minimal kind/field/preparation validation safely releases the late interest even if
  // its rows would fail the full parser. No fallback-row parser/publication is needed.
  late.resolve(response(200, fallback('query_1', {values: 'unreadable'}))); await flush();
  assert.equal(callbacks, 0); assert.equal(session.page, exact); assert.equal(session.window?.queryId, 'query_2');
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1']);
  session.retire(); assert.equal(f.deletes().length, 2);
});

test('fallback stale refresh is one-shot and does not carry fallback rows/tokens into a new exact basis', async () => {
  const newer = page('query_2', {revision: 'basis_2', window: {...page('query_2').window, basis: 'basis_2'}});
  const f = fixture([response(200, fallback()), response(409), response(200, newer)]); f.session.retire();
  let callbacks = 0;
  const session = new FacetReadSession({...f.options, onFallback: () => { callbacks++; }});
  const done = session.read({...read, after: 'old_anchor'}); await flush(); f.clock.fire(); const exact = await done;
  assert.equal(callbacks, 1); assert.equal(exact.window.basis, 'basis_2');
  const query = new URL(f.gets().at(-1)!.path, 'https://local.invalid').searchParams;
  assert.equal(query.has('after'), false); assert.equal(query.get('q'), 'D');
  assert.equal(session.page, exact); assert.equal(f.deletes().length, 1); session.retire();
});

test('one-shot fallback still polls final exact and releases only when exact arrives', async () => {
  const f = fixture([response(200, fallback()), response(200, page())], {retainReadyHandle: false});
  const done = f.session.read(read); await flush();
  assert.equal(f.deletes().length, 0); assert.equal(f.session.page, undefined);
  f.clock.fire(); const exact = await done;
  assert.equal(exact.kind, 'simple-keyset'); assert.equal(f.deletes().length, 1); assert.equal(f.session.window, undefined);
  f.session.retire(); assert.equal(f.deletes().length, 1);
});

test('default poll/cancel uses generated route IDs and fails closed while apigen is still pending', async () => {
  const f = fixture([response(202, preparing()), response(200, page())], {paths: undefined});
  const done = f.session.read(read);
  if (!('get_facet_query' in routePaths) || !('cancel_facet_query' in routePaths)) {
    await assert.rejects(done, /Unknown API operation/);
    assert.equal(f.gets().length, 1); assert.equal(f.clock.timers.size, 0);
  } else {
    await flush(); f.clock.fire(); await done;
    assert.equal(f.gets()[1].path, '/v1/facet-queries/query_1'); f.session.retire();
    assert.equal(f.deletes()[0].path, '/v1/facet-queries/query_1');
  }
  f.session.retire();
});

test('malformed initial fallback rows release the minimally validated preparation interest', async () => {
  const raw = response(200, {...fallback(), values: [row()]}), f = fixture([raw]);
  await assert.rejects(f.session.read(read), error => error instanceof FacetWindowError && error.response === raw);
  assert.equal(f.session.page, undefined); assert.equal(f.session.window, undefined);
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/query_1']);
  assert.equal(f.gets().length, 1); assert.equal(f.clock.timers.size, 0); f.session.retire();
});
