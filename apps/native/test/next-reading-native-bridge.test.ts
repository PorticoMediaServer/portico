import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, createReadingHost, connectReadingHost} from '../../../packages/client-core/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {nativeReadingSettingsSection} from '../src-next/screens/settings/reading-preferences';
import {nativeServerFetch} from '../src-next/platform/server-connections';
import {NativeModules} from './react-native-stub.mjs';

const rows = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows);
const fields = rows.map(row => ({key: row.key!, type: typeof row.control.default === 'boolean' ? 'boolean' : typeof row.control.default === 'number' ? 'integer' : 'string',
  default: row.control.default, scopes: ['profile-server'], group: 'reading', labelKey: `preferences.${row.key}`,
  ...(row.control.options ? {allowedValues: row.control.options.map(option => option.value)} : {})}));
const preferences = parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'fixture', registry: {revision: 'fixture', fields},
  documents: [{scope: 'profile-server', revision: 1, activeRevision: 1, digest: 'a'.repeat(64), values: {}},
    {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'b'.repeat(64), values: {}}],
  effective: Object.fromEntries(fields.map(field => [field.key, field.default])),
  effectiveSource: Object.fromEntries(fields.map(field => [field.key, 'default'])), clampedFields: []});
const bytes = Uint8Array.from({length: 2 * 49152 + 17}, (_, i) => i % 251), revision = 'a'.repeat(64);
const coded = (code: string) => (error: unknown) => typeof error === 'object' && error !== null && 'code' in error && error.code === code;
async function until(check: () => boolean) {for (let n = 0; n < 50 && !check(); n++) await new Promise<void>(resolve => setImmediate(resolve)); assert.ok(check());}
type Reply = {done: boolean; bodyBase64: string};

/** Only the native method boundary is simulated. Facade, Response adapter and
 * trusted host/guest protocol below are the actual MAIN implementations. */
async function fixture(t: TestContext) {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
  const before = {request: runtime.networkRequest, open: runtime.networkStreamOpen, read: runtime.networkStreamRead, cancel: runtime.cancelQueueRequest, bytes: runtime.networkBytesRequest, id: runtime.playbackRequestId};
  let dispose: (() => Promise<void>) | undefined;
  t.after(async () => {try {await dispose?.();} finally {
    runtime.networkRequest = before.request; runtime.networkStreamOpen = before.open; runtime.networkStreamRead = before.read;
    runtime.cancelQueueRequest = before.cancel; runtime.networkBytesRequest = before.bytes; runtime.playbackRequestId = before.id;
  }});
  let requestId = 0; runtime.playbackRequestId = () => `native-bridge-${++requestId}`;
  const authority = new AbortController();
  let current = true, nativeReads = 0, mode: 'exact' | 'stall' | 'truncated' = 'exact';
  let late: ((reply: Reply) => void) | undefined;
  const streams = new Map<string, number>(), cancelled = new Set<string>(), calls: string[] = [], packets: string[] = [];
  const headers = {'Content-Type': 'application/pdf', 'Content-Length': String(bytes.length), 'Accept-Ranges': 'none', ETag: `"${revision}"`};
  runtime.networkRequest = async (_id: string, _url: string, method: string, requestHeaders: Record<string, string>) => {
    calls.push(method); assert.equal(new Headers(requestHeaders).get('Authorization'), 'Bearer native-bridge-test-token');
    const data = method === 'POST' ? {sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size: bytes.length, format: 'pdf', sequential: true, metadata: {title: 'Stored title'}} : {progress: null};
    return {status: method === 'DELETE' ? 204 : 200, headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}, body: method === 'DELETE' ? '' : JSON.stringify(data)};
  };
  runtime.networkBytesRequest = async (_id: string, _url: string, method: string) => {assert.equal(method, 'HEAD'); return {status: 200, headers, bodyBase64: ''};};
  runtime.networkStreamOpen = async (id: string, _url: string, requestHeaders: Record<string, string>) => {
    assert.equal(new Headers(requestHeaders).get('Authorization'), 'Bearer native-bridge-test-token');
    assert.equal(new Headers(requestHeaders).get('If-Match'), `"${revision}"`); assert.equal(new Headers(requestHeaders).has('Range'), false);
    assert.equal(streams.has(id), false); streams.set(id, 0); return {status: 200, headers};
  };
  runtime.networkStreamRead = async (id: string): Promise<Reply> => {
    assert.ok(streams.has(id)); assert.equal(cancelled.has(id), false); nativeReads++;
    if (mode === 'stall') return new Promise(resolve => {late = resolve;});
    const offset = streams.get(id)!, limit = mode === 'truncated' ? bytes.length - 17 : bytes.length;
    if (offset === limit) return {done: true, bodyBase64: ''};
    const end = Math.min(offset + 49152, limit); streams.set(id, end);
    return {done: false, bodyBase64: Buffer.from(bytes.subarray(offset, end)).toString('base64')};
  };
  runtime.cancelQueueRequest = (id: string) => {assert.ok(streams.has(id)); assert.equal(cancelled.has(id), false); cancelled.add(id);};
  const api = new HttpLocalApi('http://127.0.0.1:39143', 'native-bridge-test-token', nativeServerFetch('server'));
  const client = api.reading({viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'member'}, isCurrent: () => current, signal: authority.signal});
  dispose = async () => {client.dispose();};
  const session = await client.open({itemId: 'item', sourceId: 'source'});
  dispose = async () => {try {if (current) await session.close();} finally {client.dispose();}};
  let input: ((message: string) => void) | undefined;
  const host = createReadingHost({session, channel: 'native_bridge_channel_123', preferences: () => preferences, initialProgress: await session.progress(), current: () => current,
    send(message) {packets.push(message); input?.(message);}, onBack() {}, onSettings() {}});
  const connection = connectReadingHost({send: message => host.receive(message), subscribe(listener) {input = listener; return () => {input = undefined;};}});
  dispose = async () => {
    connection.dispose(); host.dispose();
    try {if (current) await session.close();} finally {client.dispose();}
  };
  const guest = await connection.ready;
  return {guest, packets, calls, streams, cancelled, get reads() {return nativeReads;}, setMode(value: typeof mode) {mode = value;},
    releaseLate() {late?.({done: false, bodyBase64: Buffer.from(bytes.subarray(0, 49152)).toString('base64')});},
    retire(reason?: unknown) {current = false; authority.abort(reason); host.dispose();}};
}

test('canonical native facade crosses the trusted sequential bridge with demand-only 48KiB pulls and exact EOF', async t => {
  const f = await fixture(t); await f.guest.session.headPDF();
  const stream = await f.guest.session.openSequential!(); assert.equal(f.reads, 0);
  const reader = stream.getReader(); let offset = 0;
  while (true) {
    const next = await reader.read(); if (next.done) break;
    assert.ok(next.value.length <= 49152); assert.deepEqual(next.value, bytes.subarray(offset, offset + next.value.length)); offset += next.value.length;
    const reads: number = f.reads; await new Promise(resolve => setImmediate(resolve)); assert.equal(f.reads, reads, 'no upstream read without guest demand');
  }
  reader.releaseLock(); assert.equal(offset, bytes.length); assert.equal(f.reads, 4); assert.equal(f.cancelled.size, 1);
  assert.ok(f.packets.every(packet => !packet.includes('native-bridge-test-token') && !packet.includes('127.0.0.1') && !packet.includes('Authorization')));
});

test('guest child cancellation reaches the same native request after metadata and a late pull cannot contaminate a new stream', async t => {
  const f = await fixture(t); f.setMode('stall'); const signal = new AbortController();
  const reader = (await f.guest.session.openSequential!(signal.signal)).getReader();
  const pending = assert.rejects(reader.read(), coded('cancelled')); await until(() => f.reads === 1);
  signal.abort(); await pending; await until(() => f.cancelled.size === 1); reader.releaseLock();
  f.releaseLate(); await new Promise(resolve => setImmediate(resolve)); assert.equal(f.guest.session.signal.aborted, false);
  f.setMode('exact'); const replacement = (await f.guest.session.openSequential!()).getReader();
  assert.deepEqual((await replacement.read()).value, bytes.subarray(0, 49152)); await replacement.cancel(); replacement.releaseLock();
  await until(() => f.cancelled.size === 2); assert.equal(f.streams.size, 2);
});

// The canonical host maps deliberate retirement/AbortError to cancellation;
// typed Reading failures retain their original source/permission classification.
for (const retirement of [{reason: undefined, code: 'cancelled'}, {reason: new ReadingError('source_changed'), code: 'source_changed'}])
test(`authority retirement preserves ${retirement.code}, aborts the guest pull and never cleans up as replacement authority`, async t => {
  const f = await fixture(t); f.setMode('stall'); const reader = (await f.guest.session.openSequential!()).getReader();
  const pending = assert.rejects(reader.read(), coded(retirement.code)); await until(() => f.reads === 1);
  const calls = f.calls.length; f.retire(retirement.reason); await pending; await until(() => f.cancelled.size === 1);
  assert.equal(f.guest.session.signal.aborted, true); f.releaseLate(); await new Promise(resolve => setImmediate(resolve));
  assert.equal(f.calls.length, calls); reader.releaseLock();
});

test('truncated native EOF stays a size error through facade and trusted guest bridge', async t => {
  const f = await fixture(t); f.setMode('truncated'); const reader = (await f.guest.session.openSequential!()).getReader();
  assert.equal((await reader.read()).value?.length, 49152); assert.equal((await reader.read()).value?.length, 49152);
  await assert.rejects(reader.read(), coded('response_size')); reader.releaseLock(); await until(() => f.cancelled.size === 1);
});
