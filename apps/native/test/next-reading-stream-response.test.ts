import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../../packages/contracts/src/routes';
import {nativeReadingStreamResponse} from '../src-next/platform/reading-response';
import {nativeServerFetch} from '../src-next/platform/server-connections';
import {NativeModules} from './react-native-stub.mjs';
import {HttpLocalApi} from '../../../packages/client-core/src';

const revision = 'a'.repeat(64), origin = 'http://127.0.0.1:39139';
const headers = {'Content-Type': 'application/pdf', 'Content-Length': '3', 'Accept-Ranges': 'none', ETag: `"${revision}"`};
const encoded = Buffer.from([0, 255, 128]).toString('base64');
const coded = (code: string) => (error: unknown) => typeof error === 'object' && error !== null && 'code' in error && error.code === code;
async function until(check: () => boolean) {for (let n = 0; n < 30 && !check(); n++) await new Promise<void>(resolve => setImmediate(resolve)); assert.ok(check());}

test('native Reading headers and a locked pull reader do not buffer, read ahead or patch stream globals', async () => {
  const responseBefore = globalThis.Response, streamBefore = globalThis.ReadableStream;
  let reads = 0, cancels = 0;
  const response = nativeReadingStreamResponse({status: 200, headers}, {
    async read() {reads++; return reads === 1 ? {done: false, bodyBase64: encoded} : {done: true, bodyBase64: ''};}, cancel() {cancels++;},
  });
  assert.equal(response.status, 200); assert.equal(response.headers.get('Content-Length'), '3'); assert.equal(reads, 0);
  const reader = response.body!.getReader(); assert.throws(() => response.body!.getReader()); assert.equal(reads, 0);
  assert.deepEqual((await reader.read()).value, Uint8Array.of(0, 255, 128)); assert.equal(reads, 1);
  await new Promise(resolve => setImmediate(resolve)); assert.equal(reads, 1);
  assert.equal((await reader.read()).done, true); assert.equal(cancels, 1);
  assert.equal((await reader.read()).done, true); assert.equal(reads, 2); reader.releaseLock();
  assert.equal(globalThis.Response, responseBefore); assert.equal(globalThis.ReadableStream, streamBefore);
});

test('cancellation after headers rejects a stalled pull immediately and discards its late reply', async () => {
  const caller = new AbortController(); let reads = 0, cancels = 0, release!: (value: {done: boolean; bodyBase64: string}) => void;
  const response = nativeReadingStreamResponse({status: 200, headers}, {
    read() {reads++; return new Promise(resolve => {release = resolve;});}, cancel() {cancels++;},
  }, caller.signal), reader = response.body!.getReader();
  const pending = reader.read(); await until(() => reads === 1); caller.abort();
  await assert.rejects(pending, coded('cancelled')); assert.equal(cancels, 1);
  release({done: false, bodyBase64: encoded}); await new Promise(resolve => setImmediate(resolve));
  await assert.rejects(reader.read(), coded('cancelled')); assert.equal(reads, 1);
  await reader.cancel(); assert.equal(cancels, 1); reader.releaseLock();
});

test('one native pull is pending; cancellation permits lock release without pretending EOF', async () => {
  let reads = 0, cancels = 0;
  const response = nativeReadingStreamResponse({status: 200, headers}, {read() {reads++; return new Promise(() => {});}, cancel() {cancels++;}});
  const reader = response.body!.getReader(), pending = reader.read(); await until(() => reads === 1);
  await assert.rejects(reader.read(), coded('invalid_response')); assert.throws(() => reader.releaseLock());
  await reader.cancel(); await assert.rejects(pending, coded('cancelled')); reader.releaseLock();
  const next = response.body!.getReader(); await assert.rejects(next.read(), coded('cancelled')); next.releaseLock();
  assert.equal(reads, 1); assert.equal(cancels, 1);
});

test('invalid chunks and EOF bytes fail closed and cancel the same native request', async () => {
  for (const reply of [{done: false, bodyBase64: 'bad$'}, {done: false, bodyBase64: Buffer.alloc(49153).toString('base64')}, {done: true, bodyBase64: encoded}]) {
    let cancels = 0;
    const response = nativeReadingStreamResponse({status: 200, headers}, {async read() {return reply;}, cancel() {cancels++;}});
    const reader = response.body!.getReader(); await assert.rejects(reader.read(), coded('invalid_response')); reader.releaseLock(); assert.equal(cancels, 1);
  }
});

test('empty native responses preserve 200/204/304 metadata and explicit EOF without inventing bytes', async () => {
  for (const status of [200, 204, 304]) {
    let reads = 0, cancels = 0;
    const response = nativeReadingStreamResponse({status, headers: {'Content-Length': '0'}}, {
      async read() {reads++; return {done: true, bodyBase64: ''};}, cancel() {cancels++;},
    });
    assert.equal(response.status, status); assert.equal(reads, 0);
    const reader = response.body!.getReader(); assert.equal((await reader.read()).done, true);
    assert.equal(reads, 1); assert.equal(cancels, 1); reader.releaseLock();
  }
});

test('actual native fetch selects canonical no-Range Reading stream and retains abort ownership after metadata', async t => {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
  const before = {open: runtime.networkStreamOpen, read: runtime.networkStreamRead, cancel: runtime.cancelQueueRequest, bytes: runtime.networkBytesRequest};
  t.after(() => {runtime.networkStreamOpen = before.open; runtime.networkStreamRead = before.read; runtime.cancelQueueRequest = before.cancel; runtime.networkBytesRequest = before.bytes;});
  let id: unknown, reads = 0, cancels = 0;
  runtime.networkStreamOpen = async (identifier: string, url: string, requestHeaders: Record<string, string>) => {
    id = identifier; assert.equal(url, origin + routePath('get_reading_sessions_sessionId_source', {sessionId: 'session'}));
    assert.equal(new Headers(requestHeaders).get('Authorization'), 'Bearer throwaway-stream-token'); assert.equal(new Headers(requestHeaders).get('Range'), null);
    return {status: 200, headers};
  };
  runtime.networkStreamRead = (identifier: string) => {assert.equal(identifier, id); reads++; return new Promise(() => {});};
  runtime.cancelQueueRequest = (identifier: string) => {assert.equal(identifier, id); cancels++;};
  runtime.networkBytesRequest = () => {throw new Error('sequential PDF must not use completed bytes');};
  const caller = new AbortController(), response = await nativeServerFetch('server')(origin + routePath('get_reading_sessions_sessionId_source', {sessionId: 'session'}), {
    signal: caller.signal, headers: {Authorization: 'Bearer throwaway-stream-token', 'If-Match': `"${revision}"`},
  });
  assert.equal(reads, 0); const reader = response.body!.getReader(), pending = reader.read(); await until(() => reads === 1);
  caller.abort(); await assert.rejects(pending, coded('cancelled')); assert.equal(cancels, 1); reader.releaseLock();
});

test('canonical captured facade accepts first-byte metadata and native pulls without a global ReadableStream constructor', async t => {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
  const before = {request: runtime.networkRequest, open: runtime.networkStreamOpen, read: runtime.networkStreamRead, cancel: runtime.cancelQueueRequest, bytes: runtime.networkBytesRequest};
  t.after(() => {runtime.networkRequest = before.request; runtime.networkStreamOpen = before.open; runtime.networkStreamRead = before.read; runtime.cancelQueueRequest = before.cancel; runtime.networkBytesRequest = before.bytes;});
  let reads = 0, opens = 0, cancels = 0, streamId: string | undefined;
  let firstByte!: () => void;
  runtime.networkRequest = async (_id: string, _url: string, method: string) => ({status: method === 'DELETE' ? 204 : 200,
    headers: {'Content-Type': 'application/json', ETag: `"${revision}"`},
    body: method === 'DELETE' ? '' : JSON.stringify({sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size: 3, format: 'pdf', sequential: true, metadata: {title: 'Stored title'}})});
  runtime.networkBytesRequest = async (_id: string, _url: string, method: string) => {assert.equal(method, 'HEAD'); return {status: 200, headers, bodyBase64: ''};};
  runtime.networkStreamOpen = (identifier: string, _url: string, requestHeaders: Record<string, string>) => {
    opens++; streamId = identifier;
    assert.equal(new Headers(requestHeaders).get('Authorization'), 'Bearer canonical-native-test-token');
    assert.equal(new Headers(requestHeaders).get('If-Match'), `"${revision}"`); assert.equal(new Headers(requestHeaders).has('Range'), false);
    // Models §44's allowed delay; the native driver still owns its first bytes.
    return new Promise(resolve => {firstByte = () => resolve({status: 200, headers});});
  };
  runtime.networkStreamRead = async (identifier: string) => {assert.equal(identifier, streamId); return ++reads === 1 ? {done: false, bodyBase64: encoded} : {done: true, bodyBase64: ''};};
  runtime.cancelQueueRequest = (identifier: string) => {assert.equal(identifier, streamId); cancels++;};
  const api = new HttpLocalApi(origin, 'canonical-native-test-token', nativeServerFetch('server'));
  const client = api.reading({viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}, isCurrent: () => true});
  const session = await client.open({itemId: 'item', sourceId: 'source'}); await session.headPDF();
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'ReadableStream'); assert.ok(descriptor);
  try {
    Object.defineProperty(globalThis, 'ReadableStream', {configurable: true, writable: true, value: undefined});
    const opening = session.openSequential(); await until(() => opens === 1); assert.equal(reads, 0);
    firstByte(); const stream = await opening; assert.equal(reads, 0);
    const reader = stream.getReader(); assert.deepEqual((await reader.read()).value, Uint8Array.of(0, 255, 128));
    assert.equal((await reader.read()).done, true); assert.equal(cancels, 1); reader.releaseLock();
    assert.equal(globalThis.ReadableStream, undefined, 'production did not install a constructor');
  } finally {Object.defineProperty(globalThis, 'ReadableStream', descriptor); await session.close(); client.dispose();}
});
