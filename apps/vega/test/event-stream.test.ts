/**
 * The server's event streams on Vega (Watch Together, and what a phone sends the television):
 * the runtime's `openNetworkStream`, `readNetworkStream` and `cancelNetworkStream`, with the
 * bounds and the rejection codes the Apple and Android runtimes have.
 */
import test from 'node:test';
import assert from 'node:assert/strict';

/** A stand-in for the device's XMLHttpRequest that the test drives by hand. */
class FakeRequest {
  static made: FakeRequest[] = [];
  readyState = 0; status = 0; responseText = ''; responseURL = ''; responseType = ''; withCredentials = true; timeout = 0;
  method = ''; url = ''; sent = false; aborted = false;
  headers: Record<string, string> = {};
  onreadystatechange: (() => void) | null = null;
  onprogress: (() => void) | null = null;
  onerror: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  constructor() { FakeRequest.made.push(this); }
  open(method: string, url: string) { this.method = method; this.url = url; }
  setRequestHeader(name: string, value: string) { this.headers[name] = value; }
  getAllResponseHeaders() { return 'Content-Type: text/event-stream\r\nCache-Control: no-store\r\n'; }
  send() { this.sent = true; }
  abort() { this.aborted = true; }
  // What the server does:
  begin(status = 200, from = this.url) { this.status = status; this.responseURL = from; this.readyState = 2; this.onreadystatechange?.(); }
  // Kepler's real __didReceiveResponse order, before its first LOADING/progress event.
  sdkHeaders(status = 200, from = this.url) { this.status = status; this.responseURL = ''; this.readyState = 2; this.onreadystatechange?.(); this.responseURL = from; }
  write(text: string) { this.responseText += text; this.readyState = 3; this.onprogress?.(); }
  finish() { this.readyState = 4; this.onreadystatechange?.(); }
  fail() { this.onerror?.(); }
}
(globalThis as {XMLHttpRequest?: unknown}).XMLHttpRequest = FakeRequest;

const {cancelNetworkStream, openNetworkStream, openStreamCount, readNetworkStream} = await import('../src/bridge/event-stream.ts');

const events = 'http://192.168.1.20:32500/v1/events';
const last = () => FakeRequest.made[FakeRequest.made.length - 1];
const text = (data: string | undefined) => Buffer.from(data ?? '', 'base64').toString('utf8');
const code = (error: unknown) => (error as {code?: string}).code;

test('SDK headers-before-URL ordering publishes neither headers nor pieces before validation', async () => {
  const opening = openNetworkStream('sdk-order', events, {});
  const request = last();
  let settled = false;
  void opening.then(() => { settled = true; }, () => { settled = true; });
  request.sdkHeaders();
  await Promise.resolve();
  assert.equal(settled, false);
  assert.equal(request.aborted, false);
  await assert.rejects(readNetworkStream('sdk-order'), error => code(error) === 'event_local');
  request.write('data: first\n\n');
  assert.equal((await opening).status, 200);
  assert.equal(text((await readNetworkStream('sdk-order')).data), 'data: first\n\n');
  cancelNetworkStream('sdk-order');
});

test('a missing final stream URL still fails closed after the deferred SDK header event', async () => {
  const opening = openNetworkStream('sdk-missing', events, {});
  const request = last();
  request.sdkHeaders(200, '');
  assert.equal(request.aborted, false);
  request.finish();
  await assert.rejects(opening, error => code(error) === 'event_redirect');
  assert.equal(request.aborted, true);
});

test('a redirected SDK response is refused before any stream piece is exposed', async () => {
  const opening = openNetworkStream('sdk-moved', events, {});
  const request = last();
  request.sdkHeaders(200, 'http://other.test/v1/events');
  assert.equal(request.aborted, false);
  request.write('data: refused\n\n');
  await assert.rejects(opening, error => code(error) === 'event_redirect');
  assert.equal(request.aborted, true);
  await assert.rejects(readNetworkStream('sdk-moved'), error => code(error) === 'event_local');
});

test('only the two event routes may be opened, with a short identifier and no credentials', async () => {
  // The listener the server retired is refused like any other address.
  for (const url of ['http://192.168.1.20:32500/v1/notifications/events', 'http://192.168.1.20:32500/v1/items', 'ftp://192.168.1.20/v1/events', 'http://u:p@192.168.1.20/v1/events', 'http://192.168.1.20/v1/groups/a b/events', 'http://192.168.1.20/v1/events#x']) {
    await assert.rejects(openNetworkStream('a', url, {}), error => code(error) === 'event_local');
  }
  await assert.rejects(openNetworkStream('', events, {}), error => code(error) === 'event_local');
  await assert.rejects(openNetworkStream('x'.repeat(129), events, {}), error => code(error) === 'event_local');
  assert.equal(openStreamCount(), 0);
});

test('a stream opens with the headers it was given and is read a piece at a time', async () => {
  const opening = openNetworkStream('one', 'http://192.168.1.20:32500/v1/groups/G1/events', {Accept: 'text/event-stream', 'Last-Event-ID': '41', skipped: 5});
  const request = last();
  assert.equal(request.method, 'GET');
  assert.equal(request.withCredentials, false);
  assert.deepEqual(request.headers, {Accept: 'text/event-stream', 'Last-Event-ID': '41', 'Cache-Control': 'no-store'});
  request.begin();
  const opened = await opening;
  assert.equal(opened.status, 200);
  assert.equal(opened.headers['Content-Type'], 'text/event-stream');
  // A read waits for what has not arrived yet; one read at a time.
  const first = readNetworkStream('one');
  await assert.rejects(readNetworkStream('one'), error => code(error) === 'event_local');
  request.write('id: 42\ndata: {"a":1}\n\n');
  const piece = await first;
  assert.equal(piece.done, false);
  assert.equal(text(piece.data), 'id: 42\ndata: {"a":1}\n\n');
  // What arrived while nobody was reading is kept, in order, and never handed on twice.
  request.write('data: é\n\n');
  request.write('data: 3\n\n');
  assert.equal(text((await readNetworkStream('one')).data), 'data: é\n\n');
  assert.equal(text((await readNetworkStream('one')).data), 'data: 3\n\n');
  // The server ends the stream: what is left is read, then it is done and gone.
  request.write('data: last\n\n');
  request.finish();
  assert.equal(text((await readNetworkStream('one')).data), 'data: last\n\n');
  assert.deepEqual(await readNetworkStream('one'), {done: true});
  assert.equal(openStreamCount(), 0);
  await assert.rejects(readNetworkStream('one'), error => code(error) === 'event_local');
});

test('a piece is never more than 16 KiB', async () => {
  const opening = openNetworkStream('big', events, {});
  const request = last();
  request.begin();
  await opening;
  request.write('x'.repeat(40000));
  const sizes: number[] = [];
  for (let i = 0; i < 3; i++) sizes.push(Buffer.from((await readNetworkStream('big')).data ?? '', 'base64').length);
  assert.deepEqual(sizes, [16384, 16384, 7232]);
  cancelNetworkStream('big');
});

test('a stream is ended as done after a megabyte, so its owner opens the next from its last event', async () => {
  const opening = openNetworkStream('long', events, {});
  const request = last();
  request.begin();
  await opening;
  request.write('y'.repeat(1048577));
  assert.deepEqual(await readNetworkStream('long'), {done: true});
  assert.equal(request.aborted, true);
  assert.equal(openStreamCount(), 0);
});

test('a redirect is refused before anything is handed on', async () => {
  const opening = openNetworkStream('moved', events, {});
  last().begin(200, 'http://elsewhere.test/v1/events');
  await assert.rejects(opening, error => code(error) === 'event_redirect');
  assert.equal(last().aborted, true);
  assert.equal(openStreamCount(), 0);
});

test('a connection that cannot be opened, or breaks, says so as the route\'s trouble', async () => {
  const opening = openNetworkStream('down', events, {});
  last().fail();
  await assert.rejects(opening, error => code(error) === 'event_transport');
  assert.equal(openStreamCount(), 0);

  const second = openNetworkStream('breaks', events, {});
  const request = last();
  request.begin();
  await second;
  const reading = readNetworkStream('breaks');
  request.fail();
  await assert.rejects(reading, error => code(error) === 'event_transport');
  assert.equal(openStreamCount(), 0);
});

test('an answer that is not a success is still handed to the owner, who closes the stream', async () => {
  const opening = openNetworkStream('denied', events, {});
  last().begin(401);
  assert.equal((await opening).status, 401);
  cancelNetworkStream('denied');
  assert.equal(openStreamCount(), 0);
});

test('cancelling ends a waiting read, and at most four streams are open at once', async () => {
  const opened: string[] = [];
  for (const id of ['a', 'b', 'c', 'd']) { const opening = openNetworkStream(id, events, {}); last().begin(); await opening; opened.push(id); }
  await assert.rejects(openNetworkStream('e', events, {}), error => code(error) === 'event_local');
  await assert.rejects(openNetworkStream('a', events, {}), error => code(error) === 'event_local');
  const reading = readNetworkStream('a');
  cancelNetworkStream('a');
  await assert.rejects(reading, error => code(error) === 'event_cancelled');
  for (const id of opened) cancelNetworkStream(id);
  cancelNetworkStream('never-opened');
  assert.equal(openStreamCount(), 0);
});


test('an authenticated stream opens on its selected HTTP origin', async () => {
  const opening = openNetworkStream('private', events, {Authorization: 'Bearer event-token'});
  assert.equal(last().headers.Authorization, 'Bearer event-token');
  last().begin();
  assert.equal((await opening).status, 200);
  cancelNetworkStream('private');
});

test('a cross-origin stream response retires the bearer before handing out headers', async () => {
  const opening = openNetworkStream('private-moved', events, {Authorization: 'Bearer moved-event-token'});
  last().begin(200, 'http://elsewhere.test/v1/events');
  await assert.rejects(opening, error => code(error) === 'credential_exposed');
  assert.equal(openStreamCount(), 0);
  const before = FakeRequest.made.length;
  await assert.rejects(openNetworkStream('again', events, {Authorization: 'Bearer moved-event-token'}));
  assert.equal(FakeRequest.made.length, before);
});

test('cancelling before headers settles the opening and ignores the retired request', async () => {
  const opening = openNetworkStream('opening', events, {});
  const old = last();
  const settled = opening.then(() => 'opened', error => code(error));
  cancelNetworkStream('opening');
  const result = await Promise.race([settled, new Promise(resolve => setTimeout(() => resolve('pending'), 30))]);
  assert.equal(result, 'event_cancelled');
  assert.equal(openStreamCount(), 0);
  const replacement = openNetworkStream('opening', events, {});
  old.begin();
  assert.equal(openStreamCount(), 1);
  last().begin();
  await replacement;
  cancelNetworkStream('opening');
});
