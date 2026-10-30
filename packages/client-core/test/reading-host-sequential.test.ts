import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi, type Viewer} from '../src/index.ts';
import {ReadingError} from '../src/reading.ts';
import {createReadingHost, connectReadingHost} from '../src/reading-host.ts';
import {createReadingPublication} from '../../reader-core/src/index.ts';
import {parsePreferenceSnapshot} from '../src/preferences.ts';
import {routePath} from '../../contracts/src/index.ts';

const revision = 'a'.repeat(64), channel = 'captured_sequential_channel';
const preferences = parsePreferenceSnapshot(JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences);
const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const coded = (code: string) => (error: unknown) => error instanceof ReadingError && error.code === code;
async function until(check: () => boolean) {for (let n = 0; n < 30 && !check(); n++) await new Promise<void>(resolve => setImmediate(resolve)); assert.ok(check());}

async function fixture(options: {size?: number; sequential?: boolean; mode?: 'stall' | 'short' | 'oversize' | 'headers'} = {}) {
  const size = options.size ?? (2 << 20) + 17, sequential = options.sequential ?? true;
  let opens = 0, pulls = 0, cancels = 0, sentBytes = 0, current = true;
  const pendingHeaders: ((response: Response) => void)[] = []; let lastChunk: Uint8Array | undefined;
  const calls: {method: string; headers: Headers}[] = [], messages: string[] = [];
  const pdfHeaders = {'Content-Type': 'application/pdf', 'Content-Length': String(size), 'Accept-Ranges': sequential ? 'none' : 'bytes', ETag: `"${revision}"`};
  const response = () => new Response(new ReadableStream<Uint8Array>({
    pull(controller) {
      pulls++;
      if (options.mode === 'stall') return new Promise<void>(() => {});
      if (sentBytes >= (options.mode === 'short' ? size - 1 : size)) {controller.close(); return;}
      const length = options.mode === 'oversize' ? (1 << 20) + 1 : Math.min(1 << 20, (options.mode === 'short' ? size - 1 : size) - sentBytes);
      lastChunk = Uint8Array.from({length}, (_, index) => (sentBytes + index) % 251); sentBytes += length;
      controller.enqueue(lastChunk);
    }, cancel() {cancels++;},
  }, {highWaterMark: 0}), {headers: pdfHeaders});
  const api = new HttpLocalApi('http://127.0.0.1:39137', 'throwaway-sequential-token', async (url, init = {}) => {
    const path = new URL(String(url)).pathname, method = init.method ?? 'GET', headers = new Headers(init.headers);
    calls.push({method, headers}); assert.equal(headers.get('Authorization'), 'Bearer throwaway-sequential-token');
    if (path === routePath('post_reading_sessions')) return new Response(JSON.stringify({sessionId: 'session', itemId: 'item', sourceId: 'source', revision, size, format: 'pdf', sequential, metadata: {title: 'Catalogue title'}}), {headers: {'Content-Type': 'application/json', ETag: `"${revision}"`}});
    assert.equal(headers.get('If-Match'), `"${revision}"`);
    if (method === 'DELETE') return new Response(null, {status: 204});
    assert.equal(path, routePath('get_reading_sessions_sessionId_source', {sessionId: 'session'}));
    if (method === 'HEAD') return new Response(null, {headers: pdfHeaders});
    assert.equal(method, 'GET'); assert.equal(headers.get('Range'), null); opens++;
    if (options.mode === 'headers') return new Promise<Response>(resolve => {pendingHeaders.push(resolve);});
    return response();
  });
  const client = api.reading({viewer, isCurrent: () => current}), session = await client.open({itemId: 'item', sourceId: 'source'});
  let input: ((message: string) => void) | undefined;
  const host = createReadingHost({session, channel, preferences: () => preferences, initialProgress: {progress: null}, current: () => current,
    send(message) {messages.push(message); input?.(message);}, onBack() {}, onSettings() {}});
  const connection = connectReadingHost({send: host.receive, subscribe(listener) {input = listener; return () => {input = undefined;};}}), guest = await connection.ready;
  return {api, client, session, guest, host, size, calls, messages,
    get opens() {return opens;}, get pulls() {return pulls;}, get cancels() {return cancels;}, get lastChunk() {return lastChunk;},
    releaseHeaders(index = 0) {assert.ok(pendingHeaders[index]); pendingHeaders[index]!(response());},
    retire() {current = false; api.setAccessToken('replacement-test-token');},
    async dispose() {guest.dispose(); host.dispose(); await session.close().catch(() => {}); client.dispose();},
  };
}

test('actual captured sequential facade crosses the bridge into the public publication with demand-only bounded owned chunks and EOF', async t => {
  const f = await fixture(); t.after(() => f.dispose());
  const publication = await createReadingPublication(f.guest.session); t.after(() => publication.dispose());
  assert.equal(publication.source.read, undefined); assert.ok(publication.source.sequential); assert.equal(f.opens, 0);
  const stream = await publication.source.sequential.open(new AbortController().signal), reader = stream.getReader();
  t.after(() => reader.cancel().catch(() => {})); assert.equal(f.opens, 1); assert.equal(f.pulls, 0);
  let total = 0;
  for (;;) {
    const before: number = f.pulls, next = await reader.read();
    assert.equal(f.pulls, before + 1);
    if (next.done) break;
    assert.ok(next.value.length <= 1 << 20); assert.equal(next.value[0], total % 251);
    total += next.value.length; const original = f.lastChunk![0]; next.value[0] = 255;
    assert.equal(f.lastChunk![0], original, 'engine bytes do not alias the captured source');
    await new Promise(resolve => setImmediate(resolve)); assert.equal(f.pulls, before + 1, 'no read ahead between consumer pulls');
  }
  reader.releaseLock(); assert.equal(total, f.size); assert.equal(f.cancels, 0);
  const chunks = f.messages.map(message => JSON.parse(message)).filter(packet => packet.value?.bytes);
  assert.ok(chunks.length > 40); assert.ok(chunks.every(packet => packet.value.bytes.length <= 65536));
  assert.ok(f.messages.every(message => !message.includes('throwaway-sequential-token') && !message.includes('127.0.0.1')));
});

test('one sequential stream stays busy until cancellation, then a genuine new open recovers without range fallback', async t => {
  const f = await fixture({size: 64 << 20}); t.after(() => f.dispose());
  const stream = await f.guest.session.openSequential!(), reader = stream.getReader();
  assert.equal(f.pulls, 0); await assert.rejects(f.guest.session.openSequential!(), coded('sequential_busy')); assert.equal(f.opens, 1);
  await reader.read(); f.host.receive(JSON.stringify({v: 1, channel: 'another_channel_name', kind: 'sequential-cancel', stream: 1}));
  assert.equal(f.cancels, 0); await reader.cancel(); reader.releaseLock(); await until(() => f.cancels === 1);
  const next = await f.guest.session.openSequential!(); assert.equal(f.opens, 2); assert.equal(f.pulls, 1);
  await next.cancel(); await until(() => f.cancels === 2);
  assert.equal(f.calls.filter(call => call.headers.has('Range')).length, 0);
});

test('caller abort after headers interrupts a stalled pull while preserving the captured book', async t => {
  const f = await fixture({mode: 'stall'}); t.after(() => f.dispose());
  const caller = new AbortController(), stream = await f.guest.session.openSequential!(caller.signal), reader = stream.getReader();
  const pending = reader.read(); await until(() => f.pulls === 1); caller.abort();
  await assert.rejects(pending, coded('cancelled')); await until(() => f.cancels === 1);
  assert.equal(f.session.signal.aborted, false); reader.releaseLock();
});

test('cancel before headers cancels a late response without pulling it or closing a replacement stream', async t => {
  const f = await fixture({mode: 'headers'}); t.after(() => f.dispose());
  const caller = new AbortController(), opening = f.guest.session.openSequential!(caller.signal);
  await until(() => f.opens === 1); caller.abort(); await assert.rejects(opening, coded('cancelled'));
  await new Promise(resolve => setImmediate(resolve));
  const replacement = f.guest.session.openSequential!(); await until(() => f.opens === 2);
  f.releaseHeaders(1); const stream = await replacement;
  f.releaseHeaders(0); await until(() => f.cancels === 1); assert.equal(f.pulls, 0); assert.equal(f.session.signal.aborted, false);
  const reader = stream.getReader(); assert.equal((await reader.read()).done, false);
  await reader.cancel(); reader.releaseLock(); await until(() => f.cancels === 2);
});

test('authority retirement cancels the live upstream and guest without new-viewer I/O', async t => {
  const f = await fixture({mode: 'stall'}); t.after(() => f.dispose());
  const stream = await f.guest.session.openSequential!(), reader = stream.getReader(), pending = reader.read();
  await until(() => f.pulls === 1); const before = f.calls.length; f.retire();
  await assert.rejects(pending, coded('cancelled')); await until(() => f.cancels === 1);
  await assert.rejects(f.guest.session.openSequential!(), coded('cancelled')); assert.equal(f.calls.length, before); reader.releaseLock();
});

test('short EOF and oversized upstream chunks fail instead of completing or buffering', async t => {
  for (const mode of ['short', 'oversize'] as const) await t.test(mode, async t => {
    const f = await fixture({size: (1 << 20) + 17, mode}); t.after(() => f.dispose());
    const stream = await f.guest.session.openSequential!(), reader = stream.getReader();
    await assert.rejects(async () => {for (;;) {if ((await reader.read()).done) return;}}, coded('response_size'));
    assert.equal(f.guest.session.signal.aborted, true); reader.releaseLock();
  });
});

test('range access on a sequential publication and sequential access on a ranged publication fail before source I/O', async t => {
  const seq = await fixture(); t.after(() => seq.dispose());
  await assert.rejects(seq.guest.session.readRange({start: 0, endExclusive: 10}), coded('invalid_format')); assert.equal(seq.opens, 0);
  const range = await fixture({sequential: false}); t.after(() => range.dispose());
  await assert.rejects(range.guest.session.openSequential!(), coded('invalid_format')); assert.equal(range.opens, 0);
});
