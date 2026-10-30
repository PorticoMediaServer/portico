import test from 'node:test';
import assert from 'node:assert/strict';
import {createDownloadRequest, getDownloadRequest, isDownloadRequestTerminal, parseDownloadRequest, parseDownloadRequestList, pollDownloadRequest} from '../src/download-requests.ts';
import {resetSkippedParts, skippedParts} from '../src/tolerant.ts';

const sha = 'b'.repeat(64);
function preparation(id: string): any {
  return {
    id: 'prep-' + id, itemId: id, libraryId: 'lib', profileId: 'profile', quality: 'original', origin: 'container',
    title: {kind: 'episode', title: 'Episode ' + id, onServer: true},
    batchId: 'batch', state: 'ready', reason: '',
    artifact: {kind: 'prepared', ref: 'version', sha256: sha, bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: id + '.mp4'},
    progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaSeconds: null},
    actions: ['remove'], revision: 1,
    createdAt: '2026-09-16T10:00:00Z', updatedAt: '2026-09-16T10:05:00Z', readyAt: '2026-09-16T10:05:00Z', expiresAt: '2026-10-16T10:05:00Z',
  };
}

function request(over: Record<string, unknown> = {}): any {
  return {
    requestId: 'req-1', total: 0, totalKnown: false, state: 'capturing',
    ready: 0, preparing: 0, queued: 0, failed: 0, paused: 0, items: [], nextCursor: '', ...over,
  };
}

function fakeApi(routes: Record<string, (body?: unknown) => unknown>): {request<T>(path: string, method?: string, body?: unknown): Promise<T>; calls: {path: string; method?: string; body?: unknown}[]} {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  return {
    calls,
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
      calls.push({path, method, body});
      const route = routes[method + ' ' + path.split('?')[0]];
      if (!route) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
      return route(body) as T;
    },
  };
}

test('a download request starts capturing without a known total', () => {
  const parsed = parseDownloadRequest(request());
  assert.equal(parsed.totalKnown, false);
  assert.equal(isDownloadRequestTerminal(parsed.state), false);
  assert.equal(isDownloadRequestTerminal(parseDownloadRequest(request({state: 'complete', total: 3, totalKnown: true, ready: 3})).state), true);
});

test('requests validate the target, device, quality and policy', async () => {
  const api = fakeApi({'POST /v1/downloads/requests': () => request()});
  const created = await createDownloadRequest(api, {operationId: 'op-1', target: {kind: 'season', id: 's-1'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'next', keepNext: 3}});
  assert.equal(created.requestId, 'req-1');
  assert.deepEqual(api.calls[0].body, {operationId: 'op-1', target: {kind: 'season', id: 's-1'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'next', keepNext: 3}});
  await assert.rejects(createDownloadRequest(api, {operationId: 'op-1', target: {kind: 'show', id: 's'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'all', keepNext: 2}}));
  await assert.rejects(createDownloadRequest(api, {operationId: 'bad id!', target: {kind: 'show', id: 's'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'all'}}));
  await assert.rejects(createDownloadRequest(api, {operationId: 'op-1', target: {kind: 'episode' as any, id: 'e'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'all'}}));
});

test('a reused operationId with a different body fails as idempotency_key_reused', async () => {
  const api = fakeApi({'POST /v1/downloads/requests': () => { throw Object.assign(new Error('reused'), {status: 422, code: 'idempotency_key_reused'}); }});
  await assert.rejects(
    createDownloadRequest(api, {operationId: 'op-1', target: {kind: 'show', id: 's'}, deviceId: 'dev-1', quality: '720p-high', policy: {episodes: 'unwatched'}}),
    (e: any) => e.code === 'idempotency_key_reused',
  );
});

test('reads page members and refuse another request id', async () => {
  const api = fakeApi({'GET /v1/downloads/requests/req-1': () => request({state: 'preparing', total: 2, totalKnown: true, ready: 1, queued: 1, items: [{itemId: 'e1', preparation: preparation('e1')}, {itemId: 'e2', reason: 'storage_full'}]})});
  const view = await getDownloadRequest(api, 'req-1', {limit: 100});
  assert.equal(view.items.length, 2);
  assert.equal(view.items[0].preparation?.itemId, 'e1');
  assert.equal(view.items[1].reason, 'storage_full');
  const wrong = fakeApi({'GET /v1/downloads/requests/req-1': () => request({requestId: 'req-2', state: 'preparing', total: 0, totalKnown: true})});
  await assert.rejects(getDownloadRequest(wrong, 'req-1'));
});

test('polling ends complete and reports a failed capture without effects', async () => {
  let polls = 0;
  const api = fakeApi({'GET /v1/downloads/requests/req-1': () => (++polls < 2 ? request({state: 'capturing'}) : request({state: 'complete', total: 1, totalKnown: true, ready: 1, items: [{itemId: 'e1', preparation: preparation('e1')}]}))});
  const done = await pollDownloadRequest(api, 'req-1', {intervalMs: 500, timeoutMs: 10000});
  assert.equal(done.state, 'complete');
  const failed = fakeApi({'GET /v1/downloads/requests/req-9': () => request({requestId: 'req-9', state: 'failed', total: 0, totalKnown: true, errorCode: 'authority_revoked'})});
  const gone = await pollDownloadRequest(failed, 'req-9', {intervalMs: 500});
  assert.equal(gone.errorCode, 'authority_revoked');
});

test('a device lists only its own requests, and duplicate rows are a contract break', () => {
  const summary = {requestId: 'req-1', state: 'preparing', target: {kind: 'season', id: 's-1'}, title: 'Season 1', quality: 'original', createdAt: '2026-09-16T10:00:00Z'};
  const listed = parseDownloadRequestList({requests: [summary], nextCursor: '1758016800000.req-1'});
  assert.equal(listed.requests[0].requestId, 'req-1');
  assert.equal(listed.requests[0].target.kind, 'season');
  assert.equal(listed.requests[0].title, 'Season 1');
  assert.equal(listed.nextCursor, '1758016800000.req-1');
  assert.equal(parseDownloadRequestList({requests: []}).nextCursor, '');
  // A page holds at most fifty; more is a contract break, not a silent truncation.
  assert.throws(() => parseDownloadRequestList({requests: Array.from({length: 51}, (_, i) => ({...summary, requestId: 'req-' + i}))}));
  // The same request cannot appear twice in the list a device reconciles against.
  assert.throws(() => parseDownloadRequestList({requests: [summary, {...summary}]}));
  assert.throws(() => parseDownloadRequestList({requests: [{...summary, state: 'lost'}]}));
  assert.throws(() => parseDownloadRequestList({requests: [{...summary, target: {kind: 'episode', id: 'e-1'}}]}));
});

test('a repeated member is read once, and an error code that cannot be read is left out', () => {
  assert.deepEqual(parseDownloadRequest(request({items: [{itemId: 'a', reason: 'storage_full'}, {itemId: 'a'}, {itemId: 'b'}]})).items, [{itemId: 'a', reason: 'storage_full'}, {itemId: 'b'}]);
  assert.equal('errorCode' in parseDownloadRequest(request({state: 'preparing', errorCode: 'selection_changed'})), false);
  const failed = parseDownloadRequest(request({state: 'failed', total: 0, totalKnown: true, errorCode: 'nope'}));
  assert.equal(failed.state, 'failed');
  assert.equal('errorCode' in failed, false);
  assert.equal(parseDownloadRequest(request({state: 'failed', total: 0, totalKnown: true, errorCode: 'authority_revoked'})).errorCode, 'authority_revoked');
});

test('one unreadable member leaves the rest of a request, and an unreadable preparation leaves its member', () => {
  resetSkippedParts();
  const parsed = parseDownloadRequest(request({
    state: 'preparing', total: 6, totalKnown: true, ready: 1, queued: 5, nextCursor: 'after-e6', estimate: 'new',
    items: [
      {itemId: 'e1', preparation: preparation('e1'), badge: 'new'},
      {itemId: 'e2', preparation: {...preparation('e2'), state: 'verifying'}},
      {itemId: 'e3', preparation: {...preparation('e3'), artifact: {...preparation('e3').artifact, sha256: 'not-a-digest'}}},
      {itemId: 'bad id!', preparation: preparation('e4')},
      'not a member',
      {itemId: 'e5', reason: 7},
      {itemId: 'e6', reason: 'storage_full'},
    ],
  }));
  assert.deepEqual(parsed.items.map(i => i.itemId), ['e1', 'e2', 'e3', 'e5', 'e6']);
  assert.equal(parsed.items[0].preparation?.id, 'prep-e1');
  assert.equal('badge' in parsed.items[0], false);
  assert.deepEqual(parsed.items.slice(1, 4), [{itemId: 'e2'}, {itemId: 'e3'}, {itemId: 'e5'}]);
  assert.equal(parsed.items[4].reason, 'storage_full');
  assert.deepEqual([parsed.total, parsed.ready, parsed.queued, parsed.nextCursor], [6, 1, 5, 'after-e6']);
  assert.deepEqual(skippedParts().map(p => p.document + ':' + p.part + ':' + p.count).sort(), ['download-request:item.preparation:2', 'download-request:item.reason:1', 'download-request:items:2']);
});

test('a request keeps its identity, state, counts, cursor and bounds exact', async () => {
  for (const over of [
    {requestId: 'bad id!'},
    {state: 'archived'},
    {total: -1},
    {totalKnown: 'yes'},
    {ready: '3'},
    {paused: 1.5},
    {nextCursor: 4},
    {items: {0: {itemId: 'a'}}},
    {items: Array.from({length: 101}, (_, i) => ({itemId: 'e' + i}))},
  ]) assert.throws(() => parseDownloadRequest(request(over)), `accepted ${JSON.stringify(Object.keys(over))}`);
  assert.throws(() => parseDownloadRequest([request()]));
  // What the client sends stays exact: a policy with a field the server does not take is never sent.
  const api = fakeApi({'POST /v1/downloads/requests': () => request()});
  await assert.rejects(createDownloadRequest(api, {operationId: 'op-1', target: {kind: 'show', id: 's'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'all', keepLast: 2} as any}));
  await assert.rejects(createDownloadRequest(api, {operationId: 'op-1', target: {kind: 'show', id: 's'}, deviceId: 'dev-1', quality: 'original', policy: {episodes: 'some'} as any}));
  assert.equal(api.calls.length, 0);
});

test('quality defaults r25: omitted container quality is Original and explicit choices are preserved',async()=>{
 const api=fakeApi({'POST /v1/downloads/requests':()=>request()});
 const input={operationId:'default-op',target:{kind:'album' as const,id:'album'},deviceId:'dev',policy:{episodes:'all' as const}};
 await createDownloadRequest(api,input);
 assert.equal((api.calls.at(-1)!.body as any).quality,'original');
 for(const quality of ['original','automatic','720p-high','1080p-low']) {
  await createDownloadRequest(api,{...input,quality});
  assert.equal((api.calls.at(-1)!.body as any).quality,quality);
 }
 const n=api.calls.length;
 for(const quality of ['',null,42,'auto','1080p','bogus'])await assert.rejects(createDownloadRequest(api,{...input,quality} as any));
 assert.equal(api.calls.length,n);
});
