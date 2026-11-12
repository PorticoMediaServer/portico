import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type Viewer} from '../../../packages/client-core/src';
import {routePath} from '../../../packages/contracts/src/routes';
import {nativeReadingPolicy, nativeReadingResponse} from '../src-next/platform/reading-response';
import {nativeServerFetch} from '../src-next/platform/server-connections';
import {NativeModules} from './react-native-stub.mjs';

const origin = 'http://127.0.0.1:39129', revision = 'a'.repeat(64);
const viewer: Viewer = {accountId: 'a', profileId: 'p', serverId: 's', authority: 'local', role: 'member'};
test('canonical byte operations alone select exact bounds and other Reading controls retain JSON', () => {
  for (const [path, method, limit] of [
    [routePath('head_reading_sessions_sessionId_source', {sessionId: 'session'}), 'HEAD', 65536],
    [routePath('get_reading_sessions_sessionId_source', {sessionId: 'session'}), 'GET', 65536],
    [routePath('get_reading_sessions_sessionId_assets', {sessionId: 'session'}), 'GET', 33554432],
    [routePath('get_reading_sessions_sessionId_pages_pageIndex', {sessionId: 'session', pageIndex: 0}), 'GET', 16777216],
  ] as const) {
    assert.equal(nativeReadingPolicy(origin + path, method, method === 'GET' && path.endsWith('/source') ? {Range: 'bytes=0-9'} : undefined)?.binaryLimit, limit);
    assert.equal(nativeReadingPolicy(origin + path, 'PATCH'), undefined);
  }
  assert.deepEqual(nativeReadingPolicy(origin + routePath('post_reading_sessions'), 'POST'), {binaryLimit: undefined});
  assert.equal(nativeReadingPolicy(origin + '/v1/items/other', 'GET'), undefined);
  assert.deepEqual(nativeReadingPolicy(origin + routePath('get_reading_sessions_sessionId_source', {sessionId: 'session'}), 'GET'), {sequential: true});
});
// The rebuilt server's book bytes come from the media address its book route gave. On a phone each one must
// cross the native byte exchange: through the ordinary JSON request a PDF, an EPUB part or a comic page is lost.
test('a book’s bytes at its media address cross the native byte exchange with their limits, and nothing else under /v1/media does', () => {
  const media = origin + '/v1/media/g_1.a-b';
  assert.equal(nativeReadingPolicy(media + '/source', 'HEAD')?.binaryLimit, 65536);
  assert.equal(nativeReadingPolicy(media + '/source', 'GET', {Range: 'bytes=0-9'})?.binaryLimit, 65536);
  assert.equal(nativeReadingPolicy(media + '/source?save=1', 'GET')?.binaryLimit, 64 * 1024 * 1024);
  assert.equal(nativeReadingPolicy(media + '/parts/OEBPS/c%201.xhtml', 'GET')?.binaryLimit, 32 * 1024 * 1024);
  assert.equal(nativeReadingPolicy(media + '/pages/12?w=800', 'GET')?.binaryLimit, 16 * 1024 * 1024);
  for (const [path, method] of [['/source', 'PUT'], ['/pages/x', 'GET'], ['/file/0', 'GET'], ['/subtitles/1', 'GET']] as const) assert.equal(nativeReadingPolicy(media + path, method), undefined);
});
test('actual captured facade consumes non-UTF8 PDF bytes through native byte exchange and local body reader', async t => {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
  const before = {request: runtime.networkRequest, bytes: runtime.networkBytesRequest, Response: globalThis.Response};
  t.after(() => {runtime.networkRequest = before.request; runtime.networkBytesRequest = before.bytes; globalThis.Response = before.Response;});
  const data = Uint8Array.from({length: 100003}, (_, index) => index % 256), calls: string[] = [], fingerprint = '0123456789abcdef';
  // JSON comes back as the phone's fetch (Hermes) gives it: text, in a response with no body stream.
  globalThis.Response = class extends before.Response { get body() { return null; } };
  runtime.networkRequest = async (_id: string, url: string, method: string, headers: Record<string, string>) => {
    const path = new URL(url).pathname; calls.push(method + ' ' + path); assert.equal(new Headers(headers).get('Authorization'), 'Bearer throwaway-test-token');
    const value = path === '/v1/items/item/book'
      ? {itemId: 'item', version: 'source', versions: [{id: 'source', format: 'pdf'}], format: 'pdf', fingerprint, size: data.length, mediaUrl: '/v1/media/g1'}
      : {id: 'item', kind: 'book', title: 'Stored title', state: {}};
    return {status: 200, headers: {'Content-Type': 'application/json'}, body: JSON.stringify(value)};
  };
  runtime.networkBytesRequest = async (_id: string, url: string, method: string, headers: Record<string, string>, limit: number) => {
    calls.push(method + ' ' + new URL(url).pathname); assert.equal(limit, 65536); const h = new Headers(headers);
    assert.equal(h.get('Authorization'), 'Bearer throwaway-test-token');
    assert.equal(new URL(url).pathname, '/v1/media/g1/source');
    const range = /^bytes=(\d+)-(\d+)$/.exec(h.get('Range') ?? '');
    const body = method === 'HEAD' ? new Uint8Array() : data.slice(Number(range![1]), Number(range![2]) + 1);
    return {status: method === 'HEAD' ? 200 : 206, headers: {'Content-Type': 'application/pdf', 'Accept-Ranges': 'bytes',
      'Content-Length': String(method === 'HEAD' ? data.length : body.length), ...(range ? {'Content-Range': `bytes ${range[1]}-${range[2]}/${data.length}`} : {})}, bodyBase64: Buffer.from(body).toString('base64')};
  };
  const api = new HttpLocalApi(origin, 'throwaway-test-token', nativeServerFetch('s'));
  const client = api.reading({viewer, isCurrent: () => true});
  assert.equal(calls.length, 0);
  const session = await client.open({itemId: 'item', sourceId: 'source'});
  assert.equal(session.manifest.metadata.title, 'Stored title');
  await session.headPDF();
  const bytes = await session.readRange({start: 200, endExclusive: 65000});
  assert.deepEqual(bytes, data.slice(200, 65000)); assert.ok(bytes.includes(255));
  assert.equal((await session.progress()).progress, null);
  await session.close(); client.dispose();
  assert.deepEqual(calls.filter(call => call.includes('/v1/media/')), ['HEAD /v1/media/g1/source', 'GET /v1/media/g1/source']);
});
test('local response rejects bad base64/overflow and cancellation before reading, without a global patch', async () => {
  const responseBefore = globalThis.Response, streamsBefore = globalThis.ReadableStream;
  const raw = {status: 200, headers: {}, bodyBase64: Buffer.from([0, 255, 128]).toString('base64')};
  const caller = new AbortController(), response = nativeReadingResponse(raw, 3, caller.signal);
  const reader = response.body!.getReader(); assert.deepEqual((await reader.read()).value, Uint8Array.of(0, 255, 128));
  caller.abort(); await assert.rejects(reader.read(), /cancelled/i); reader.releaseLock();
  assert.throws(() => nativeReadingResponse(raw, 2));
  assert.throws(() => nativeReadingResponse({...raw, bodyBase64: '???'}, 3));
  assert.throws(() => nativeReadingResponse(raw, 3, caller.signal));
  assert.equal(globalThis.Response, responseBefore); assert.equal(globalThis.ReadableStream, streamsBefore);
});

// A phone's fetch (Hermes) gives a response with no body stream. A Read library's lists are JSON
// read as text there, and a retired sign-in asks for nothing more.
test('Discover and a series\' volumes read on a phone, and a retired sign-in sends nothing', async t => {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
  const before = {request: runtime.networkRequest, Response: globalThis.Response};
  t.after(() => {runtime.networkRequest = before.request; globalThis.Response = before.Response;});
  globalThis.Response = class extends before.Response { get body() { return null; } };
  const wire = (id: string) => ({id, kind: 'issue', title: 'Volume ' + id, libraryId: 'comics', addedAt: '2026-11-12T00:00:00Z', images: {},
    state: {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}});
  const seen: URL[] = []; let current = true;
  runtime.networkRequest = async (_id: string, url: string, method: string, headers: Record<string, string>) => {
    const at = new URL(url); seen.push(at);
    assert.equal(method, 'GET'); assert.equal(at.pathname, '/v1/items');
    assert.equal(new Headers(headers).get('Authorization'), 'Bearer throwaway-test-token');
    assert.equal(nativeReadingPolicy(url, method), undefined, 'a list is ordinary JSON, not book bytes');
    return {status: 200, headers: {'Content-Type': 'application/json'}, body: JSON.stringify(at.searchParams.has('cursor') ? {items: []} : {items: [wire('one')], nextCursor: 'more'})};
  };
  const api = new HttpLocalApi(origin, 'throwaway-test-token', nativeServerFetch('s'));
  const client = api.reading({viewer, isCurrent: () => current});
  try {
    const discover = await client.discover({libraryId: 'comics', limit: 20, shortReadsLimit: 12});
    assert.deepEqual(discover.rows.map(row => [row.key, row.items.map(item => item.id), row.nextCursor]), [['recent', ['one'], 'more']]);
    const series = await client.series('comics', 'itm_series', {limit: 30});
    assert.deepEqual(series.items.map(item => item.id), ['one']);
    assert.deepEqual((await client.series('comics', 'itm_series', {limit: 30, cursor: series.nextCursor})).items, []);
    assert.equal(seen[1].searchParams.get('series'), 'itm_series'); assert.equal(seen[2].searchParams.get('cursor'), 'more');
    current = false;
    await assert.rejects(client.series('comics', 'itm_series', {limit: 30}), error => error instanceof ReadingError && error.code === 'cancelled');
    assert.equal(seen.length, 3, 'a retired sign-in sends nothing more');
  } finally {client.dispose();}
});
