import test from 'node:test';
import assert from 'node:assert/strict';
import {getEventListeners} from 'node:events';
import {
  readPreparation, PreparationProtocolError,
  type PreparationResponse, type PreparationRuntime, type ReadPreparationOptions, type PreparationIntermediateReady,
} from '../src/read-preparation.ts';

const MAX_TIMER = 2_147_483_647;
const READY = {kind: 'ready', values: ['kept exactly']};
const response = (status: number, body?: unknown, headers: Record<string, string> = {}): PreparationResponse => ({status, body, headers});
const pending = (retryAfterMs = 0, headers: Record<string, string> = {}, queryId = 'q-1') =>
  response(202, {kind: 'facet-preparing', state: 'preparing', queryId, retryAfterMs}, headers);

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}

/** Advance only explicitly; no global timer patching, network or real sleeps. */
class Clock implements PreparationRuntime {
  time = Date.parse('Wed, 28 Oct 2026 12:00:00 GMT');
  randomValue = 0;
  next = 0;
  timers = new Map<number, {callback: () => void; ms: number}>();
  delays: number[] = [];
  peak = 0;
  now = () => this.time;
  random = () => this.randomValue;
  setTimeout = (callback: () => void, ms: number) => {
    assert.ok(ms > 0 && ms <= MAX_TIMER);
    const id = ++this.next;
    this.timers.set(id, {callback, ms});
    this.delays.push(ms);
    this.peak = Math.max(this.peak, this.timers.size);
    return id;
  };
  clearTimeout = (id: unknown) => { this.timers.delete(id as number); };
  fire(advanceMs?: number) {
    assert.equal(this.timers.size, 1, 'one live timer per operation');
    const [id, timer] = this.timers.entries().next().value!;
    this.timers.delete(id);
    this.time += advanceMs ?? timer.ms;
    timer.callback();
  }
}

async function flush() { for (let i = 0; i < 32; i++) await Promise.resolve(); }

class DomainApiError extends Error {
  constructor(readonly raw: PreparationResponse) { super(`domain status ${raw.status}`); }
}

type Descriptor = Readonly<{library: string; q: string; at?: string}>;
type Ready = typeof READY;
type Options = ReadPreparationOptions<Descriptor, Ready>;

function fixture(replies: (PreparationResponse | Promise<PreparationResponse>)[] = [response(200, READY)]) {
  const clock = new Clock(), controller = new AbortController();
  const calls: {path: string; method: string; signal?: AbortSignal; singleAttempt?: boolean}[] = [];
  let current = true, readyParses = 0, preparingParses = 0;
  const events: PreparationResponse[] = [], errors: DomainApiError[] = [];
  const descriptor = Object.freeze({library: 'library', q: 'one character', at: 'old-row'});
  const options: Options = {
    api: {requestRaw(path, method, requestOptions = {}) {
      calls.push({path, method, ...requestOptions});
      if (method === 'DELETE') return Promise.resolve(response(204));
      assert.ok(replies.length, 'no unplanned poll');
      return Promise.resolve(replies.shift()!);
    }},
    descriptor,
    paths: {
      initial: d => `/v1/libraries/${d.library}/facets?q=${encodeURIComponent(d.q)}&at=${d.at}`,
      refresh: d => `/v1/libraries/${d.library}/facets?q=${encodeURIComponent(d.q)}`,
      poll: id => `/v1/facet-queries/${encodeURIComponent(id)}`,
      cancel: id => `/v1/facet-queries/${encodeURIComponent(id)}`,
    },
    parseReady(body) {
      readyParses++;
      if (!body || (body as Ready).kind !== 'ready' || !Array.isArray((body as Ready).values)) throw new Error('domain ready envelope');
      return body as Ready;
    },
    parsePreparing(body) {
      preparingParses++;
      const b = body as Record<string, unknown> | undefined;
      if (!b || b.kind !== 'facet-preparing' || b.state !== 'preparing' || 'values' in b || 'total' in b) throw new Error('domain preparing envelope');
      return {queryId: b.queryId as string, retryAfterMs: b.retryAfterMs as number};
    },
    errorFromResponse(raw) { const error = new DomainApiError(raw); errors.push(error); return error; },
    signal: controller.signal,
    isCurrent: () => current,
    onPreparing: (_pending, raw) => { events.push(raw); },
    runtime: clock,
  };
  return {
    options, clock, controller, calls, events, errors, descriptor,
    retire() { current = false; }, readyParses: () => readyParses, preparingParses: () => preparingParses,
    reads: () => calls.filter(c => c.method === 'GET'), deletes: () => calls.filter(c => c.method === 'DELETE'),
  };
}

test('200 returns exact ready value and raw metadata, with no timer, cancel or retained session', async () => {
  const raw = response(200, READY, {etag: 'basis'}), f = fixture([raw]);
  const result = await readPreparation(f.options);
  assert.equal(result.value, READY);
  assert.equal(result.response, raw);
  assert.equal(f.readyParses(), 1);
  assert.equal(f.preparingParses(), 0);
  assert.equal(f.calls.length, 1);
  assert.deepEqual(f.clock.delays, []);
  f.controller.abort();
  assert.equal(f.deletes().length, 0, 'finished operation detached from caller abort');
});

test('202 cannot reach ready parser; one timer and one GET at a time with positive exponential jitter', async () => {
  const f = fixture([pending(), pending(), pending(), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush();
  assert.equal(f.readyParses(), 0);
  assert.equal(f.reads().length, 1);
  for (const [index, delay] of [251, 501, 1001].entries()) {
    assert.equal(f.clock.delays[index], delay, 'random zero still adds positive jitter');
    f.clock.fire();
    await flush();
    assert.equal(f.reads().length, index + 2);
  }
  await done;
  assert.equal(f.events.length, 3);
  assert.equal(f.readyParses(), 1);
  assert.equal(f.clock.peak, 1);
  assert.equal(f.clock.timers.size, 0);
  assert.equal(f.deletes().length, 1, 'ordinary reads release the ready handle by default');
  await flush();
  assert.equal(f.reads().length, 4, 'no timer or extra poll after ready');
});

test('full numeric/date Retry-After and body floors dominate local backoff, with no 30s clamp', async t => {
  for (const example of [
    {headers: {'Retry-After': ' 120 '}, body: 1, expected: 120_001},
    {headers: {'rEtRy-AfTeR': 'Wed, 28 Oct 2026 12:02:00 GMT'}, body: 1, expected: 120_001},
    {headers: {'retry-after': 'Wednesday, 28-Oct-26 12:02:00 GMT'}, body: 1, expected: 120_001},
    {headers: {'retry-after': '1'}, body: 180_000, expected: 180_001},
    {headers: {'retry-after': 'soon'}, body: 60_000, expected: 60_001},
    {headers: {'retry-after': '1.5'}, body: 60_000, expected: 60_001},
    {headers: {'retry-after': 'Wed, 28 Oct 2026 11:59:00 GMT'}, body: 0, expected: 251},
    {headers: {}, body: 90_000, expected: 90_001},
  ]) await t.test(JSON.stringify(example), async () => {
    const f = fixture([pending(example.body, example.headers), response(200, READY)]);
    const done = readPreparation(f.options);
    await flush();
    assert.deepEqual(f.clock.delays, [example.expected]);
    assert.equal(f.reads().length, 1);
    f.clock.fire();
    await done;
    assert.equal(f.reads().length, 2);
  });
});

test('nonzero random adds jitter after the server floor', async () => {
  const f = fixture([pending(60_000), response(200, READY)]);
  f.clock.randomValue = 0.5;
  const done = readPreparation(f.options);
  await flush();
  assert.deepEqual(f.clock.delays, [60_032]);
  f.clock.fire();
  await done;
});

test('long floors are chunked at platform timer maximum without early polling', async () => {
  const f = fixture([pending(2 * MAX_TIMER + 10), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush();
  f.clock.fire(); await flush();
  assert.equal(f.reads().length, 1);
  f.clock.fire(); await flush();
  assert.equal(f.reads().length, 1);
  assert.deepEqual(f.clock.delays, [MAX_TIMER, MAX_TIMER, 11]);
  f.clock.fire();
  await done;
  assert.equal(f.reads().length, 2);
  assert.equal(f.clock.peak, 1);
});

test('early timer wakes re-arm only the remaining floor, with no early GET', async () => {
  const f = fixture([pending(60_000), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush();
  f.clock.fire(1000); await flush();
  assert.equal(f.reads().length, 1);
  assert.deepEqual(f.clock.delays, [60_001, 59_001]);
  f.clock.fire();
  await done;
});

test('clock jumps do not consume unwaited long chunks or shorten a server floor', async () => {
  const f = fixture([pending(MAX_TIMER + 10), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush();
  f.clock.fire(10 * MAX_TIMER); await flush();
  assert.equal(f.reads().length, 1, 'clock leap cannot skip the second timer chunk');
  assert.deepEqual(f.clock.delays, [MAX_TIMER, 11]);
  f.clock.fire(); await done;
  const g = fixture([pending(60_000), response(200, READY)]);
  const other = readPreparation(g.options);
  await flush(); g.clock.fire(-1000); await flush();
  assert.equal(g.reads().length, 1);
  assert.deepEqual(g.clock.delays, [60_001, 60_001]);
  g.clock.fire(); await other;
});

test('one 409 refreshes original descriptor without row tokens; second 409 is typed terminal', async () => {
  const second = response(409, {error: {code: 'query_stale'}});
  const f = fixture([pending(), response(409), second]);
  let refreshedDescriptor: Descriptor | undefined;
  const refresh = f.options.paths.refresh;
  const done = readPreparation({...f.options, paths: {...f.options.paths, refresh: d => { refreshedDescriptor = d; return refresh(d); }}});
  const rejected = assert.rejects(done, error => error === f.errors[0] && (error as DomainApiError).raw === second);
  await flush();
  f.clock.fire();
  await rejected;
  assert.equal(refreshedDescriptor, f.descriptor);
  assert.deepEqual(f.reads().map(c => c.path), [
    '/v1/libraries/library/facets?q=one%20character&at=old-row',
    '/v1/facet-queries/q-1', '/v1/libraries/library/facets?q=one%20character',
  ]);
  assert.equal(f.deletes().length, 1);
  assert.equal(f.readyParses(), 0);
  assert.equal(f.clock.timers.size, 0);
});

test('stale refresh can itself prepare at a new handle, backoff restarts and old tokens are unused', async () => {
  const f = fixture([pending(), response(409), pending(0, {}, 'q-2'), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush(); f.clock.fire(); await flush();
  assert.deepEqual(f.clock.delays, [251, 251]);
  assert.equal(f.deletes()[0].path, '/v1/facet-queries/q-1');
  f.clock.fire();
  await done;
  assert.equal(f.reads().at(-1)?.path, '/v1/facet-queries/q-2');
});

test('429/503, denied, validation, not-found and unexpected success are surfaced as existing typed errors', async t => {
  for (const status of [429, 503, 401, 403, 400, 404, 204, 201, 302]) await t.test(String(status), async () => {
    const raw = response(status, {error: {code: 'original-code'}}, {'retry-after': '120'});
    const f = fixture([raw]);
    await assert.rejects(readPreparation(f.options), error => error === f.errors[0] && (error as DomainApiError).raw === raw);
    assert.equal(f.calls.length, 1);
    assert.equal(f.readyParses(), 0);
    assert.equal(f.preparingParses(), 0);
    assert.deepEqual(f.clock.delays, []);
  });
});

test('a terminal poll failure releases its known handle once without retrying the error', async () => {
  const f = fixture([pending(), response(503)]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, DomainApiError);
  await flush(); f.clock.fire(); await rejected;
  assert.equal(f.reads().length, 2);
  assert.equal(f.deletes().length, 1);
  f.controller.abort();
  assert.equal(f.deletes().length, 1);
});

test('malformed domain pending envelopes and invalid bounded IDs/delays never poll or parse as ready', async t => {
  for (const body of [
    undefined, READY, {state: 'preparing', queryId: 'q', retryAfterMs: 0},
    {kind: 'facet-preparing', state: 'ready', queryId: 'q', retryAfterMs: 0},
    {...pending().body as object, values: []}, {...pending().body as object, total: 0},
    ...['', 42, 'x'.repeat(513), '../escape', '//foreign', '.', '..', 'a%2fb', 'q\n', 'é'].map(queryId => ({kind: 'facet-preparing', state: 'preparing', queryId, retryAfterMs: 0})),
    ...[undefined, -1, NaN, Infinity, 1.5, Number.MAX_SAFE_INTEGER + 1, '1000'].map(retryAfterMs => ({kind: 'facet-preparing', state: 'preparing', queryId: 'q', retryAfterMs})),
  ]) await t.test(JSON.stringify(body) ?? 'undefined', async () => {
    const f = fixture([response(202, body)]);
    await assert.rejects(readPreparation(f.options));
    assert.equal(f.reads().length, 1);
    assert.equal(f.deletes().length, 0, 'invalid handle cannot authorize a DELETE');
    assert.equal(f.readyParses(), 0);
    assert.deepEqual(f.clock.delays, []);
  });
});

test('Location is verified exactly against generated domain poll path, never navigated', async t => {
  for (const location of ['https://foreign.example/v1/facet-queries/q-1', '//foreign.example/q-1', '/v1/other/q-1', '/v1/facet-queries/q-2', '/v1/facet-queries/q-1#fragment', '/v1/facet-queries/q-1?extra=1', '']) {
    await t.test(location || 'empty', async () => {
      const raw = pending(0, {Location: location}), f = fixture([raw]);
      await assert.rejects(readPreparation(f.options), error => error instanceof PreparationProtocolError && error.code === 'unexpected_location' && error.response === raw);
      assert.equal(f.reads().length, 1);
      assert.equal(f.deletes().length, 1);
      assert.equal(f.deletes()[0].path, '/v1/facet-queries/q-1');
      assert.deepEqual(f.clock.delays, []);
    });
  }
  const f = fixture([pending(0, {LOCATION: '/v1/facet-queries/q-1'}), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush(); f.clock.fire(); await done;
});

test('generated callback paths must be server-relative and cannot normalize into other paths', async t => {
  for (const path of ['https://foreign.example/q', '//foreign.example/q', '/\\foreign/q', '/v1/../q', '/v1/%2e%2e/q', '/v1/q#fragment', '/v1/q\n']) {
    await t.test(path, async () => {
      const f = fixture();
      await assert.rejects(readPreparation({...f.options, paths: {...f.options.paths, initial: () => path}}), error => error instanceof PreparationProtocolError && error.code === 'invalid_path');
      assert.equal(f.calls.length, 0);
    });
  }
});

test('unrepresentable numeric server floors fail closed instead of polling early', async () => {
  const raw = pending(0, {'retry-after': '999999999999999999999999999999'}), f = fixture([raw]);
  await assert.rejects(readPreparation(f.options), error => error instanceof PreparationProtocolError && error.code === 'invalid_retry_after' && error.response === raw);
  assert.equal(f.reads().length, 1);
  assert.equal(f.deletes().length, 1);
  assert.equal(f.clock.timers.size, 0);
});

test('pre-abort and pre-supersession prevent all transport and domain work', async t => {
  for (const mode of ['abort', 'supersede']) await t.test(mode, async () => {
    const f = fixture();
    if (mode === 'abort') f.controller.abort(); else f.retire();
    await assert.rejects(readPreparation(f.options), {name: 'AbortError'});
    assert.equal(f.calls.length, 0);
    assert.equal(f.readyParses(), 0);
    assert.equal(f.preparingParses(), 0);
  });
});

test('abort during initial GET returns promptly even when port ignores signal; late ready is discarded', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  assert.equal(f.calls.length, 1);
  f.controller.abort();
  await rejected;
  assert.equal(f.reads()[0].signal?.aborted, true);
  late.resolve(response(200, READY));
  await flush();
  assert.equal(f.readyParses(), 0);
  assert.equal(f.calls.length, 1);
});

test('late initial 202 after abort safely releases once; foreign Location is never a destination', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  f.controller.abort(); await rejected;
  late.resolve(pending(0, {Location: 'https://foreign.example/q'}));
  await flush();
  assert.equal(f.reads().length, 1);
  assert.equal(f.deletes().length, 1);
  assert.equal(f.deletes()[0].path, '/v1/facet-queries/q-1');
  assert.equal(f.deletes()[0].singleAttempt, true);
  assert.equal(f.deletes()[0].signal, undefined, 'DELETE must not inherit aborted read signal');
  assert.equal(f.events.length, 0);
  assert.equal(f.clock.timers.size, 0);
});

test('abort during timer, including a long timer chunk, clears it and never polls again', async t => {
  for (const floor of [0, 3 * MAX_TIMER]) await t.test(String(floor), async () => {
    const f = fixture([pending(floor)]);
    const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
    await flush();
    assert.equal(f.clock.timers.size, 1);
    f.controller.abort(); await rejected; await flush();
    assert.equal(f.clock.timers.size, 0);
    assert.equal(f.reads().length, 1);
    assert.equal(f.deletes().length, 1);
  });
});

test('abort during poll and late same-handle 202 cannot duplicate cancellation', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([pending(), late.promise]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); f.clock.fire(); await flush();
  assert.equal(f.reads().length, 2);
  f.controller.abort(); await rejected;
  late.resolve(pending()); await flush();
  assert.equal(f.deletes().length, 1);
  assert.equal(f.events.length, 1);
  assert.equal(f.clock.timers.size, 0);
});

test('supersession without signal discards late ready/pending and stops at the next timer seam', async t => {
  for (const lateResponse of [response(200, READY), pending()]) await t.test(String(lateResponse.status), async () => {
    const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
    const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
    f.retire(); late.resolve(lateResponse); await rejected; await flush();
    assert.equal(f.readyParses(), 0);
    assert.equal(f.events.length, 0);
    assert.equal(f.deletes().length, lateResponse.status === 202 ? 1 : 0);
  });
  const f = fixture([pending()]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); f.retire(); f.clock.fire(); await rejected;
  assert.equal(f.reads().length, 1);
  assert.equal(f.deletes().length, 1);
});

test('abort at synchronous callback seams never publishes or starts another poll', async t => {
  for (const seam of ['initial', 'refresh', 'poll', 'parsePreparing', 'parseReady', 'onPreparing', 'random', 'now', 'setTimeout', 'errorFromResponse']) {
    await t.test(seam, async () => {
      const f = fixture([seam === 'refresh' ? response(409) : seam === 'parseReady' ? response(200, READY) : seam === 'errorFromResponse' ? response(403) : pending()]);
      const options = {...f.options, paths: {...f.options.paths}, runtime: {
        now: f.clock.now, random: f.clock.random, setTimeout: f.clock.setTimeout, clearTimeout: f.clock.clearTimeout,
      }};
      if (seam === 'initial' || seam === 'refresh' || seam === 'poll') {
        if (seam === 'poll') options.paths.poll = (id, d) => { f.controller.abort(); return f.options.paths.poll(id, d); };
        else options.paths[seam] = d => { f.controller.abort(); return f.options.paths[seam](d); };
      } else if (seam === 'parseReady') {
        options.parseReady = (body, raw) => { const value = f.options.parseReady(body, raw); f.controller.abort(); return value; };
      } else if (seam === 'parsePreparing') {
        options.parsePreparing = (body, raw) => { const value = f.options.parsePreparing(body, raw); f.controller.abort(); return value; };
      } else if (seam === 'onPreparing') options.onPreparing = () => { f.controller.abort(); };
      else if (seam === 'errorFromResponse') options.errorFromResponse = raw => { f.controller.abort(); return f.options.errorFromResponse(raw); };
      else if (seam === 'random') options.runtime.random = () => { f.controller.abort(); return 0; };
      else if (seam === 'now') options.runtime.now = () => { f.controller.abort(); return f.clock.now(); };
      else options.runtime.setTimeout = (callback, ms) => { const id = f.clock.setTimeout(callback, ms); f.controller.abort(); return id; };
      await assert.rejects(readPreparation(options), {name: 'AbortError'});
      await flush();
      assert.ok(f.reads().length <= 1);
      assert.equal(f.clock.timers.size, 0);
      assert.ok(f.deletes().length <= 1);
      if (['parsePreparing', 'poll', 'onPreparing', 'random', 'now', 'setTimeout'].includes(seam)) assert.equal(f.deletes().length, 1);
    });
  }
});

test('cancellation failures, including synchronous throws and hanging responses, never delay abort or replay', async t => {
  for (const mode of ['reject', 'throw', 'hang']) await t.test(mode, async () => {
    const f = fixture([pending()]), port = f.options.api;
    let deletes = 0;
    const done = readPreparation({...f.options, api: {requestRaw(path, method, options) {
      if (method !== 'DELETE') return port.requestRaw(path, method, options);
      deletes++;
      if (mode === 'throw') throw new Error('cancel failed');
      if (mode === 'hang') return new Promise(() => {});
      return Promise.reject(new Error('cancel failed'));
    }}});
    const rejected = assert.rejects(done, {name: 'AbortError'});
    await flush(); f.controller.abort(); await rejected; await flush();
    assert.equal(deletes, 1);
    assert.equal(f.reads().length, 1);
    assert.equal(f.clock.timers.size, 0);
  });
});

test('poll cannot swap private handles and parser/transport failures retain identity', async () => {
  const f = fixture([pending(), pending(0, {}, 'q-2')]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, error => error instanceof PreparationProtocolError && error.code === 'invalid_preparing');
  await flush(); f.clock.fire(); await rejected;
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1']);
  const sentinel = new Error('original parser error'), g = fixture();
  await assert.rejects(readPreparation({...g.options, parseReady: () => { throw sentinel; }}), error => error === sentinel);
  const h = fixture([Promise.reject(sentinel)]);
  await assert.rejects(readPreparation(h.options), error => error === sentinel);
});

test('same helper supports a different generated domain path/envelope without facet assumptions', async () => {
  const f = fixture([response(202, {state: 'preparing', queryId: 'reading_1', retryAfterMs: 0}, {Location: '/v1/reading-queries/reading_1'}), response(200, READY)]);
  const done = readPreparation({...f.options,
    paths: {...f.options.paths, poll: id => `/v1/reading-queries/${id}`, cancel: id => `/v1/reading-queries/${id}`},
    parsePreparing: body => {
      const b = body as {state: string; queryId: string; retryAfterMs: number};
      assert.equal(b.state, 'preparing');
      return {queryId: b.queryId, retryAfterMs: b.retryAfterMs};
    },
  });
  await flush(); f.clock.fire(); await done;
  assert.equal(f.reads()[1].path, '/v1/reading-queries/reading_1');
});

test('many polls keep one timer/listener and bounded local backoff; ready detaches both signals', async () => {
  const f = fixture([...Array.from({length: 100}, () => pending()), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush();
  const internalSignal = f.reads()[0].signal!;
  for (let i = 0; i < 100; i++) {
    assert.equal(getEventListeners(f.controller.signal, 'abort').length, 1);
    assert.equal(getEventListeners(internalSignal, 'abort').length, 1);
    assert.equal(f.clock.timers.size, 1);
    f.clock.fire(); await flush();
  }
  await done;
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
  assert.equal(getEventListeners(internalSignal, 'abort').length, 0);
  assert.equal(f.clock.timers.size, 0);
  assert.equal(f.clock.delays.at(-1), 30_001);
});

test('abort cleans both signals, including a late rejected request', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  const internalSignal = f.reads()[0].signal!;
  f.controller.abort(); await rejected;
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
  assert.equal(getEventListeners(internalSignal, 'abort').length, 0);
  late.reject(new Error('late offline'));
  await flush();
  assert.equal(f.calls.length, 1);
});

test('superseded transport or parser failures are discarded as aborts', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
  const done = readPreparation(f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  f.retire(); late.reject(new Error('late offline')); await rejected;
  const g = fixture();
  await assert.rejects(readPreparation({...g.options, parseReady() { g.retire(); throw new Error('late parse'); }}), {name: 'AbortError'});
});

test('unsafe generated poll/refresh/cancel paths cannot reach transport', async () => {
  const f = fixture([pending()]);
  await assert.rejects(readPreparation({...f.options, paths: {...f.options.paths, poll: () => 'https://foreign.example/q'}}), error => error instanceof PreparationProtocolError && error.code === 'invalid_path');
  assert.equal(f.reads().length, 1);
  assert.equal(f.deletes()[0].path, '/v1/facet-queries/q-1');
  const g = fixture([response(409)]);
  await assert.rejects(readPreparation({...g.options, paths: {...g.options.paths, refresh: () => '//foreign.example/q'}}), error => error instanceof PreparationProtocolError && error.code === 'invalid_path');
  assert.equal(g.calls.length, 1);
  const h = fixture([pending()]);
  const done = readPreparation({...h.options, paths: {...h.options.paths, cancel: () => 'https://foreign.example/q'}});
  const rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); h.controller.abort(); await rejected;
  assert.equal(h.calls.length, 1);
});

test('abort re-entering a cancel callback cannot duplicate DELETE or start stale refresh', async () => {
  const f = fixture([pending(), response(409)]);
  const done = readPreparation({...f.options, paths: {...f.options.paths, cancel: id => {
    f.controller.abort();
    return `/v1/facet-queries/${id}`;
  }}});
  const rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); f.clock.fire(); await rejected;
  assert.equal(f.reads().length, 2);
  assert.equal(f.deletes().length, 1);
});

test('ready default release and explicit release are idempotent; later caller abort does not re-cancel', async () => {
  const f = fixture([pending(), response(200, READY)]);
  const done = readPreparation(f.options);
  await flush(); f.clock.fire();
  const result = await done;
  assert.equal(result.value, READY);
  assert.equal(f.deletes().length, 1);
  result.release(); result.release(); f.controller.abort();
  assert.equal(f.deletes().length, 1);
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
});

test('retained ready lease keeps original signal until later abort, then releases exactly once', async () => {
  const f = fixture([pending(), response(200, READY)]);
  const done = readPreparation({...f.options, retainOnReady: true});
  await flush(); f.clock.fire();
  const result = await done;
  assert.equal(f.deletes().length, 0, 'continuation handle must survive ready publication');
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 1);
  assert.equal(f.clock.timers.size, 0);
  f.controller.abort(); result.release(); result.release();
  assert.equal(f.deletes().length, 1);
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
  assert.equal(f.reads().length, 2);
});

test('explicit ready lease release detaches signal and never cancels a subsequent operation', async () => {
  const f = fixture([pending(), response(200, READY), pending(0, {}, 'q-2')]);
  const first = readPreparation({...f.options, retainOnReady: true});
  await flush(); f.clock.fire();
  const result = await first;
  result.release(); result.release();
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
  const second = readPreparation(f.options), rejected = assert.rejects(second, {name: 'AbortError'});
  await flush(); result.release();
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1']);
  f.controller.abort(); await rejected;
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1', '/v1/facet-queries/q-2']);
});

function readyQueryId(body: unknown): string | undefined {
  const b = body as {kind?: unknown; queryId?: unknown} | undefined;
  if (b?.kind !== 'ready' || typeof b.queryId !== 'string') throw new Error('domain ready handle envelope');
  return b.queryId;
}

test('direct 200 handles use domain extractor and obey default/retained lifetime', async t => {
  for (const retainOnReady of [false, true]) await t.test(String(retainOnReady), async () => {
    const f = fixture([response(200, {...READY, queryId: 'direct-1'})]);
    const result = await readPreparation({...f.options, readyQueryId, retainOnReady});
    assert.equal(f.deletes().length, retainOnReady ? 0 : 1);
    assert.equal(getEventListeners(f.controller.signal, 'abort').length, retainOnReady ? 1 : 0);
    result.release(); f.controller.abort(); result.release();
    assert.equal(f.deletes().length, 1);
    assert.equal(f.deletes()[0].path, '/v1/facet-queries/direct-1');
  });
});

test('late direct 200 after abort releases its handle without ready parsing or publication', async () => {
  const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
  const done = readPreparation({...f.options, readyQueryId, retainOnReady: true});
  const rejected = assert.rejects(done, {name: 'AbortError'});
  f.controller.abort(); await rejected;
  late.resolve(response(200, {...READY, queryId: 'direct-1'})); await flush();
  assert.equal(f.readyParses(), 0);
  assert.equal(f.deletes().length, 1);
  assert.equal(f.deletes()[0].path, '/v1/facet-queries/direct-1');
  assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
});

test('abort during direct-200 extraction/ready parsing and malformed/mismatched ready IDs do not leak interests', async t => {
  for (const seam of ['extract', 'parse']) await t.test(seam, async () => {
    const f = fixture([response(200, {...READY, queryId: 'direct-1'})]);
    const done = readPreparation({...f.options, retainOnReady: true,
      readyQueryId: body => { const id = readyQueryId(body); if (seam === 'extract') f.controller.abort(); return id; },
      parseReady: (body, raw) => { f.controller.abort(); return f.options.parseReady(body, raw); },
    });
    await assert.rejects(done, {name: 'AbortError'});
    assert.equal(f.deletes().length, 1);
    assert.equal(getEventListeners(f.controller.signal, 'abort').length, 0);
  });
  const f = fixture([pending(), response(200, {...READY, queryId: 'different'})]);
  const done = readPreparation({...f.options, readyQueryId}), rejected = assert.rejects(done, error => error instanceof PreparationProtocolError && error.code === 'invalid_ready_handle');
  await flush(); f.clock.fire(); await rejected;
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1']);
  const g = fixture([response(200, {...READY, queryId: '../foreign'})]);
  await assert.rejects(readPreparation({...g.options, readyQueryId}), error => error instanceof PreparationProtocolError && error.code === 'invalid_ready_handle');
  assert.equal(g.deletes().length, 0);
  assert.equal(g.readyParses(), 0);
});

type Intermediate = Readonly<{kind: 'intermediate'; queryId: string; retryAfterMs: number; values: readonly string[]}>;
const intermediateReply = (retryAfterMs = 1000, headers: Record<string, string> = {}, queryId = 'q-1') =>
  response(200, {kind: 'intermediate', queryId, retryAfterMs, values: ['honest lower bound']}, headers);
function parseIntermediate(body: unknown): PreparationIntermediateReady<Intermediate> | undefined {
  const b = body as Intermediate | undefined;
  if (b?.kind !== 'intermediate') return undefined;
  if (!Array.isArray(b.values) || b.values.some(v => typeof v !== 'string')) throw new Error('domain intermediate envelope');
  return {pending: {queryId: b.queryId, retryAfterMs: b.retryAfterMs}, value: b};
}

test('optional intermediate 200 publishes transient metadata and keeps polling to final ready', async () => {
  const first = intermediateReply(60_000), second = intermediateReply(90_000);
  const f = fixture([first, second, response(200, READY)]), seen: PreparationResponse[] = [];
  let finished = false;
  const done = readPreparation({...f.options, parseIntermediateReady: parseIntermediate,
    onIntermediateReady(value, raw) { assert.equal(value.kind, 'intermediate'); seen.push(raw); }});
  void done.then(() => { finished = true; });
  await flush();
  assert.deepEqual(seen, [first]); assert.equal(finished, false); assert.equal(f.readyParses(), 0);
  assert.equal(f.events.length, 0, 'an intermediate is not a bare preparing event');
  assert.deepEqual(f.clock.delays, [60_001]);
  f.clock.fire(); await flush();
  assert.deepEqual(seen, [first, second]); assert.equal(finished, false);
  assert.deepEqual(f.clock.delays, [60_001, 90_001]);
  f.clock.fire(); const result = await done;
  assert.equal(result.value, READY); assert.equal(f.readyParses(), 1);
  assert.equal(f.reads().length, 3); assert.equal(f.deletes().length, 1); assert.equal(f.clock.timers.size, 0);
});

test('200 remains ordinary ready without an intermediate adapter and when adapter declines it', async () => {
  const f = fixture([response(200, READY)]);
  const result = await readPreparation({...f.options, parseIntermediateReady: parseIntermediate});
  assert.equal(result.value, READY); assert.equal(f.calls.length, 1);
  const g = fixture([intermediateReply()]);
  await assert.rejects(readPreparation(g.options), /domain ready envelope/);
  assert.equal(g.calls.length, 1, 'default 200 semantics are unchanged');
});

test('202, intermediate 200, and final 200 preserve one stable handle and retained ready lifetime', async () => {
  const f = fixture([pending(1), intermediateReply(1), response(200, {...READY, queryId: 'q-1'})]);
  let intermediates = 0;
  const done = readPreparation({...f.options, retainOnReady: true, parseIntermediateReady: parseIntermediate,
    readyQueryId: body => (body as {queryId?: string}).queryId, onIntermediateReady: () => { intermediates++; }});
  await flush(); f.clock.fire(); await flush();
  assert.equal(intermediates, 1); assert.equal(f.events.length, 1); assert.equal(f.deletes().length, 0);
  assert.deepEqual(f.clock.delays, [251, 501]);
  f.clock.fire(); const result = await done;
  assert.equal(f.deletes().length, 0);
  f.controller.abort(); result.release();
  assert.equal(f.deletes().length, 1);
});

test('intermediate Retry-After numeric/date/body floors and long chunks are validated before publication', async t => {
  for (const example of [
    {body: 1, headers: {'Retry-After': '120'}, delay: 120_001},
    {body: 1, headers: {'Retry-After': 'Wed, 28 Oct 2026 12:02:00 GMT'}, delay: 120_001},
    {body: 180_000, headers: {'Retry-After': '1'}, delay: 180_001},
    {body: MAX_TIMER + 10, headers: {}, delay: MAX_TIMER},
  ]) await t.test(JSON.stringify(example), async () => {
    const f = fixture([intermediateReply(example.body, example.headers), response(200, READY)]);
    let callbacks = 0;
    const done = readPreparation({...f.options, parseIntermediateReady: parseIntermediate, onIntermediateReady: () => { callbacks++; }});
    await flush(); assert.equal(callbacks, 1); assert.equal(f.clock.delays[0], example.delay);
    f.clock.fire(); await flush();
    if (example.body > MAX_TIMER) { assert.equal(f.reads().length, 1); assert.equal(f.clock.delays[1], 11); f.clock.fire(); }
    await done; assert.equal(f.clock.peak, 1);
  });
});

test('invalid intermediate pending/Location/server floor never publishes or parses as ready', async t => {
  for (const [name, raw] of [
    ['foreign Location', intermediateReply(1, {Location: 'https://foreign.example/q-1'})],
    ['wrong generated Location', intermediateReply(1, {Location: '/v1/facet-queries/other'})],
    ['unrepresentable floor', intermediateReply(1, {'Retry-After': '999999999999999999999999999'})],
    ['invalid ID', intermediateReply(1, {}, '../escape')],
    ['invalid delay', intermediateReply(-1)],
  ] as const) await t.test(name, async () => {
    const f = fixture([raw]); let callbacks = 0;
    await assert.rejects(readPreparation({...f.options, parseIntermediateReady: parseIntermediate, onIntermediateReady: () => { callbacks++; }}), PreparationProtocolError);
    assert.equal(callbacks, 0); assert.equal(f.readyParses(), 0); assert.equal(f.reads().length, 1); assert.equal(f.clock.timers.size, 0);
  });
});

test('intermediate cannot swap an existing pending handle, or final ready replace its handle', async t => {
  for (const final of [false, true]) await t.test(String(final), async () => {
    const f = fixture([final ? intermediateReply(1) : pending(1), final ? response(200, {...READY, queryId: 'q-2'}) : intermediateReply(1, {}, 'q-2')]);
    let callbacks = 0;
    const done = readPreparation({...f.options, parseIntermediateReady: parseIntermediate,
      readyQueryId: body => (body as {queryId?: string}).queryId, onIntermediateReady: () => { callbacks++; }});
    const rejected = assert.rejects(done, PreparationProtocolError);
    await flush(); f.clock.fire(); await rejected;
    assert.equal(callbacks, final ? 1 : 0);
    assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1']);
    assert.equal(f.clock.timers.size, 0);
  });
});

test('abort/supersession at intermediate parser/callback/wait seams clears timer and cancels once', async t => {
  for (const seam of ['parse-abort', 'parse-supersede', 'callback-abort', 'wait-abort']) await t.test(seam, async () => {
    const f = fixture([intermediateReply(60_000)]); let callbacks = 0;
    const done = readPreparation({...f.options,
      parseIntermediateReady: body => {
        const intermediate = parseIntermediate(body);
        if (seam === 'parse-abort') f.controller.abort();
        if (seam === 'parse-supersede') f.retire();
        return intermediate;
      }, onIntermediateReady: () => { callbacks++; if (seam === 'callback-abort') f.controller.abort(); },
    });
    const rejected = assert.rejects(done, {name: 'AbortError'});
    await flush(); if (seam === 'wait-abort') f.controller.abort(); await rejected;
    assert.equal(callbacks, seam.startsWith('parse') ? 0 : 1);
    assert.equal(f.deletes().length, 1); assert.equal(f.reads().length, 1); assert.equal(f.clock.timers.size, 0);
  });
});

test('intermediate callback failure retains error identity and releases the private handle', async () => {
  const f = fixture([intermediateReply()]), sentinel = new Error('publication failed');
  await assert.rejects(readPreparation({...f.options, parseIntermediateReady: parseIntermediate, onIntermediateReady: () => { throw sentinel; }}), error => error === sentinel);
  assert.equal(f.deletes().length, 1); assert.equal(f.clock.timers.size, 0);
});

test('late intermediate 200 after abort safely cancels without publication, with or without a minimal extractor', async t => {
  for (const extractor of [false, true]) await t.test(String(extractor), async () => {
    const late = deferred<PreparationResponse>(), f = fixture([late.promise]);
    let parses = 0, callbacks = 0;
    const done = readPreparation({...f.options,
      parseIntermediateReady: body => { parses++; return parseIntermediate(body); },
      ...(extractor ? {readyQueryId: (body: unknown) => (body as Intermediate).queryId} : {}),
      onIntermediateReady: () => { callbacks++; },
    });
    const rejected = assert.rejects(done, {name: 'AbortError'});
    f.controller.abort(); await rejected;
    late.resolve(intermediateReply(1, {Location: 'https://foreign.example/q-1'})); await flush();
    assert.equal(callbacks, 0); assert.equal(f.readyParses(), 0); assert.equal(parses, extractor ? 0 : 1);
    assert.equal(f.deletes().length, 1); assert.equal(f.deletes()[0].path, '/v1/facet-queries/q-1');
  });
});

test('stale intermediate refreshes once and refusal remains a typed terminal error', async () => {
  const refused = response(503, {error: {code: 'capacity'}});
  const f = fixture([intermediateReply(1), response(409), intermediateReply(1, {}, 'q-2'), refused]);
  let callbacks = 0;
  const done = readPreparation({...f.options, parseIntermediateReady: parseIntermediate, onIntermediateReady: () => { callbacks++; }});
  const rejected = assert.rejects(done, error => error === f.errors[0] && (error as DomainApiError).raw === refused);
  await flush(); f.clock.fire(); await flush();
  assert.equal(callbacks, 2); assert.deepEqual(f.clock.delays, [251, 251]);
  f.clock.fire(); await rejected;
  assert.equal(f.reads().length, 4);
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1', '/v1/facet-queries/q-2']);
});

test('minimal 200-union handle extraction precedes intermediate row validation and cleans parser failure', async () => {
  const raw = intermediateReply(), f = fixture([raw]), sentinel = new Error('malformed intermediate rows');
  let callbacks = 0;
  await assert.rejects(readPreparation({...f.options,
    readyQueryId: body => (body as Intermediate).queryId,
    parseIntermediateReady: (): PreparationIntermediateReady<Intermediate> => { throw sentinel; },
    onIntermediateReady: () => { callbacks++; },
  }), error => error === sentinel);
  assert.equal(callbacks, 0); assert.equal(f.readyParses(), 0); assert.equal(f.reads().length, 1);
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/facet-queries/q-1']);
});
