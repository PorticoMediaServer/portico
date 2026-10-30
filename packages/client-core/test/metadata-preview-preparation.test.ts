import assert from 'node:assert/strict';
import test from 'node:test';
import type {HomeApi} from '../src/home.ts';
import {readMetadataPreview, parseMetadataPreviewPreparing} from '../src/home-selection/metadata-preview-preparation.ts';
import {fetchBulkMetadataPreview} from '../src/home-selection/metadata-preview.ts';
import type {PreparationResponse, PreparationRuntime} from '../src/read-preparation.ts';

const tick = async () => {for (let i = 0; i < 25; i++) await Promise.resolve();};
const request = {targets: [{kind: 'item' as const, contentKind: 'movie', id: 'one', libraryId: 'lib'}]};
const path = '/v1/metadata/bulk/preview', poll = `${path}-queries/private-1`;
const pending = (extras = {}): PreparationResponse => ({status: 202, headers: {Location: poll, 'Retry-After': '2'}, body: {kind: 'metadata-preview-preparing', state: 'preparing', queryId: 'private-1', retryAfterMs: 1000, ...extras}});
const ready = (body: unknown = {ready: true}): PreparationResponse => ({status: 200, headers: {}, body});
function clock() {
  let now = 0, sequence = 0; const timers = new Map<number, {at: number; fn(): void}>();
  const runtime: PreparationRuntime = {now: () => now, random: () => 0, setTimeout(fn, delay) {const id = ++sequence; timers.set(id, {at: now + delay, fn}); return id;}, clearTimeout(id) {timers.delete(id as number);}};
  return {runtime, timers, async advance(ms: number) {now += ms; for (const [id, timer] of timers) if (timer.at <= now) {timers.delete(id); timer.fn();} await tick();}};
}
const rawPort = (raw: (path: string, method: string, options?: any) => Promise<PreparationResponse>) => ({request: async () => {throw Error('body port used');}, requestRaw: raw}) as HomeApi;

// The <=200 proof-bearing query path privately converts its selection to CAS
// targets. Those targets and the conversion revision are never its public DTO.
const smallQuery = {selector: {query: {libraryId: 'lib', pivot: 'movies', selectionBasis: 'c'.repeat(64),
  filter: {field: 'year' as const, operator: 'at-least' as const, value: 1900}, sort: [{field: 'title' as const, direction: 'asc' as const}]}},
  expected: {catalogRevision: 'original-query-revision'}} as const;
function smallQueryReady(field: unknown) {
  const spec = {field: 'year', label: 'Year', group: 'numbers', type: 'integer', bulk: true};
  return {serverId: 'server', viewerFence: 'a'.repeat(64), kind: 'item', contentKind: 'movie', total: 70,
    selectionRevision: smallQuery.expected.catalogRevision, schema: [spec], singleItemFields: [spec], excludedFields: [],
    fields: {year: field}, targets: []};
}
const smallMany = {state: 'many', atLeast: 65, values: [], entries: [], locked: 23, truncated: true};

test('small proof-bearing query ready traverses the real POST adapter and parser without exposing private CAS targets', async () => {
  for (const field of [
    {state: 'shared', distinctCount: 1, value: '2020', values: [{value: '2020', count: 70}], entries: [], locked: 70, truncated: false},
    {state: 'mixed', distinctCount: 2, values: [{value: '', count: 30}, {value: '2020', count: 40}], entries: [], locked: 23, truncated: false},
    smallMany,
  ]) {
    const calls: {target: string; method: string; body: unknown}[] = [];
    const response = smallQueryReady(field);
    const api = rawPort(async (target, method, options) => {
      calls.push({target, method, body: options?.body});
      return {status: 200, headers: {'Cache-Control': 'no-store'}, body: response};
    });
    const parsed = await fetchBulkMetadataPreview(api, smallQuery, 'server');
    assert.deepEqual(calls, [{target: path, method: 'POST', body: smallQuery}]);
    assert.deepEqual(parsed.targets, []); assert.ok(Object.isFrozen(parsed.targets));
    assert.equal(parsed.total, 70); assert.equal(parsed.selectionRevision, smallQuery.expected.catalogRevision);
    assert.deepEqual(parsed.fields.year, field);
    if (field.state === 'many') {
      assert.equal(parsed.fields.year!.atLeast, 65);
      assert.equal(Object.hasOwn(parsed.fields.year!, 'distinctCount'), false);
      assert.equal(Object.hasOwn(parsed.fields.year!, 'value'), false);
    }
  }
});
test('small proof-bearing query ready rejects leaked CAS targets, converted revisions and non-saturated many counts', async () => {
  const privateTargets = Array.from({length: 70}, (_, index) => ({kind: 'item', id: String(index), libraryId: 'lib', expectedRevision: 'b'.repeat(64)}));
  const response = smallQueryReady(smallMany);
  for (const body of [
    {...response, targets: privateTargets},
    {...response, selectionRevision: 'private-conversion-revision'},
    {...response, selectionRevision: undefined},
    ...[{atLeast: 64}, {atLeast: 66}, {distinctCount: 65}, {values: [{value: '2020', count: 1}]}, {value: '2020'}, {truncated: false}]
      .map(change => smallQueryReady({...smallMany, ...change})),
  ]) {
    let posts = 0;
    const api = rawPort(async (target, method) => {assert.equal(target, path); assert.equal(method, 'POST'); posts++; return ready(body);});
    await assert.rejects(fetchBulkMetadataPreview(api, smallQuery, 'server'), /invalid-metadata-preview/);
    assert.equal(posts, 1);
  }
});

test('large proof-bearing query ready retains its exact total and lower bound without a target census', async () => {
  const response = {...smallQueryReady(smallMany), total: 201};
  const api = rawPort(async (target, method, options) => {
    assert.equal(target, path); assert.equal(method, 'POST'); assert.deepEqual(options?.body, smallQuery);
    return ready(response);
  });
  const parsed = await fetchBulkMetadataPreview(api, smallQuery, 'server');
  assert.equal(parsed.total, 201); assert.equal(parsed.selectionRevision, smallQuery.expected.catalogRevision);
  assert.deepEqual(parsed.targets, []); assert.equal(parsed.fields.year!.state, 'many');
  assert.equal(parsed.fields.year!.atLeast, 65); assert.equal(Object.hasOwn(parsed.fields.year!, 'distinctCount'), false);
});

test('metadata preparation validates its exact domain, retry floor and safe handle', () => {
  assert.deepEqual(parseMetadataPreviewPreparing(pending().body), {queryId: 'private-1', retryAfterMs: 1000});
  for (const extras of [{kind: 'reading-browse-preparing'}, {state: 'ready'}, {retryAfterMs: 0}, {retryAfterMs: '1000'}, {queryId: '../bad'}, {queryId: 'x'.repeat(129)}, {queryId: '..'}]) {
    assert.throws(() => parseMetadataPreviewPreparing(pending(extras).body), {code: 'invalid_preparing'});
  }
});
test('metadata POST/poll uses actual status and headers, immutable input and no partial ready publication', async () => {
  const time = clock(), calls: any[] = [], statuses: number[] = []; let parsed = 0;
  const mutable = structuredClone(request);
  const api = rawPort(async (target, method, options) => {calls.push({target, method, options}); return method === 'POST' ? pending() : ready();});
  const work = readMetadataPreview(api, mutable, (body, captured) => {parsed++; assert.equal(captured.targets![0]!.id, 'one'); return body;},
    {runtime: time.runtime, onPreparing: () => statuses.push(202)});
  mutable.targets[0]!.id = 'changed';
  await tick(); assert.equal(parsed, 0); assert.deepEqual(statuses, [202]); assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].options.body.targets[0].id, 'one'); assert.ok(Object.isFrozen(calls[0].options.body.targets));
  await time.advance(2000); assert.equal(calls.length, 1); await time.advance(1);
  assert.deepEqual(await work, {ready: true}); assert.equal(parsed, 1);
  assert.deepEqual(calls.map(call => [call.target, call.method]), [[path, 'POST'], [poll, 'GET'], [poll, 'DELETE']]);
  assert.equal(calls[2].options.singleAttempt, true); assert.equal(calls[2].options.signal, undefined);
});
test('aborting preparing releases demand and prevents polling/publication', async () => {
  const time = clock(), calls: string[] = [], abort = new AbortController(); let parsed = false;
  const api = rawPort(async (target, method) => {calls.push(`${method} ${target}`); return method === 'POST' ? pending() : ready();});
  const work = readMetadataPreview(api, request, () => {parsed = true;}, {signal: abort.signal, runtime: time.runtime});
  await tick(); abort.abort(); await assert.rejects(work, {name: 'AbortError'}); await tick();
  assert.equal(parsed, false); assert.deepEqual(calls, [`POST ${path}`, `DELETE ${poll}`]); assert.equal(time.timers.size, 0);
});
test('late initial pending handle is cancelled after retirement even when transport ignores abort', async () => {
  let finish!: (response: PreparationResponse) => void; const calls: string[] = [], abort = new AbortController();
  const api = rawPort(async (target, method) => {calls.push(`${method} ${target}`); return method === 'POST' ? new Promise(resolve => {finish = resolve;}) : ready();});
  const work = readMetadataPreview(api, request, () => {throw Error('published');}, {signal: abort.signal});
  await tick(); abort.abort(); await assert.rejects(work, {name: 'AbortError'}); finish(pending()); await tick();
  assert.deepEqual(calls, [`POST ${path}`, `DELETE ${poll}`]);
});
test('terminal typed capacity errors are surfaced without polling or retry', async () => {
  let calls = 0;
  const response = {status: 503, headers: {'Retry-After': '60'}, body: {error: {code: 'rate_limited', retry: 'same_request'}}};
  await assert.rejects(readMetadataPreview(rawPort(async () => {calls++; return response;}), request, () => {throw 0;}, {}),
    (error: any) => error.status === 503 && error.code === 'rate_limited' && error.retryable && error.response === response);
  assert.equal(calls, 1);
});
test('raw status does not turn a successful status with pending body into ready', async () => {
  await assert.rejects(readMetadataPreview(rawPort(async () => ({...pending(), status: 200})), request, body => {
    if ((body as any).state === 'preparing') throw Error('invalid ready'); return body;
  }, {}), /invalid ready/);
});
test('body-only compatibility accepts only the explicit typed pending envelope', async () => {
  const time = clock(), calls: string[] = [];
  const api: HomeApi = {request: async <T>(target: string, method = 'GET') => {calls.push(`${method} ${target}`); return (method === 'POST' ? pending().body : {ready: true}) as T;}};
  const work = readMetadataPreview(api, request, body => body, {runtime: time.runtime}); await tick(); await time.advance(1001);
  assert.deepEqual(await work, {ready: true}); assert.deepEqual(calls, [`POST ${path}`, `GET ${poll}`, `DELETE ${poll}`]);
  await assert.rejects(readMetadataPreview({request: async <T>() => ({...pending().body as object, kind: 'browse-selection-preparing'}) as T}, request, body => body, {}), {code: 'invalid_preparing'});
});

test('metadata stale handle restarts only once with the original captured POST and expected revision', async () => {
  const time = clock(), calls: any[] = []; let posts = 0;
  const query = {selector: {query: {libraryId: 'lib', pivot: 'movies', sort: []}}, expected: {catalogRevision: 'original'}} as const;
  const stale: PreparationResponse = {status: 409, headers: {}, body: {error: {code: 'stale_continuation'}}};
  const api = rawPort(async (target, method, options) => {
    calls.push({target, method, body: options?.body});
    if (method === 'DELETE') return {status: 204, headers: {}};
    if (method === 'POST') return ++posts === 1 ? {...pending(), headers: {}} : stale;
    return stale;
  });
  const work = readMetadataPreview(api, query, body => body, {runtime: time.runtime}); const rejected = assert.rejects(work, (error: any) => error.status === 409 && error.code === 'stale_continuation');
  await tick(); await time.advance(1001); await rejected;
  assert.equal(posts, 2); assert.deepEqual(calls.filter(call => call.method === 'POST').map(call => call.body), [query, query]);
  assert.equal(calls.filter(call => call.method === 'DELETE').length, 1);
});
test('metadata pending handle or Location substitution is rejected and releases only the owned handle', async () => {
  for (const location of [true, false]) {
    const time = clock(), calls: string[] = []; let first = true;
    const api = rawPort(async (target, method) => {
      calls.push(`${method} ${target}`);
      if (method === 'DELETE') return {status: 204, headers: {}};
      if (first) {first = false; return {...pending(), headers: location ? {Location: '/unrelated'} : {}};}
      return {...pending({queryId: 'another'}), headers: {}};
    });
    const work = readMetadataPreview(api, request, body => body, {runtime: time.runtime});
    const rejected = assert.rejects(work, {code: location ? 'unexpected_location' : 'invalid_preparing'});
    await tick(); if (!location) await time.advance(1001); await rejected;
    assert.equal(calls.filter(call => call === `DELETE ${poll}`).length, 1);
    assert.ok(!calls.some(call => call.includes('another') || call.includes('/unrelated')));
  }
});
