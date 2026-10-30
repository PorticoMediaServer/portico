import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules} from './react-native-stub.mjs';
import {openEventStream} from '../src-next/platform/event-stream.ts';
import {readEventStream} from '../../../packages/client-core/src/sse.ts';

function fixture() {
  let snapshot = {phase: 'ready', origin: 'https://selected.test', revision: 1};
  const listeners = new Set<() => void>();
  let recoveryCount = 0;
  const route = {
    async resolve() { return snapshot.origin; },
    getSnapshot: () => snapshot,
    subscribe(fn: () => void) { listeners.add(fn); return () => listeners.delete(fn); },
    async recover() { recoveryCount++; },
  };
  const api = {baseUrl: 'https://logical.test', getRouteConnection: () => route};
  const opened: any[] = [], cancelled: string[] = [];
  Object.assign(NativeModules.PorticoRuntime, {
    async openNetworkStream(...args: any[]) { opened.push(args); return {status: 200, headers: {'content-type': 'text/event-stream'}}; },
    async readNetworkStream() { return {done: true}; },
    cancelNetworkStream(id: string) { cancelled.push(id); },
  });
  return {api: api as any, opened, cancelled, recoveries: () => recoveryCount, change() { snapshot = {...snapshot, phase: 'probing', revision: 2}; listeners.forEach(fn => fn()); }};
}

test('auxiliary streams send credentials only to the selected verified route', async () => {
  const f = fixture();
  const response = await openEventStream(f.api, '/v1/events', {Authorization: 'Bearer private'}, new AbortController().signal);
  assert.equal(f.opened[0][1], 'https://selected.test/v1/events');
  assert.equal(f.opened[0][2].Authorization, 'Bearer private');
  f.change();
  assert.ok(f.cancelled.includes(f.opened[0][0]));
  await assert.rejects(response.body!.getReader().read());
});

test('a missing verified connection, cross-origin path, or cancellation sends no bearer', async () => {
  const f = fixture();
  await assert.rejects(openEventStream({...f.api, getRouteConnection: () => undefined} as any, '/v1/events', {}, new AbortController().signal));
  await assert.rejects(openEventStream(f.api, 'https://other.test/v1/events', {Authorization: 'secret'}, new AbortController().signal));
  const abort = new AbortController(); abort.abort();
  await assert.rejects(openEventStream(f.api, '/v1/events', {}, abort.signal));
  assert.equal(f.opened.length, 0);
});

test('the native stream reader preserves split UTF-8 and cleans up at EOF', async () => {
  const f = fixture();
  const bytes = Buffer.from('id: 7\ndata: café 😀\n\n');
  const parts = [bytes.subarray(0, 17), bytes.subarray(17, 20), bytes.subarray(20)];
  NativeModules.PorticoRuntime.readNetworkStream = async () => parts.length ? {done: false, data: parts.shift()!.toString('base64')} : {done: true};
  const response = await openEventStream(f.api, '/v1/events', {}, new AbortController().signal);
  const events: any[] = [];
  await readEventStream(response, event => events.push(event));
  assert.equal(events[0].data, 'café 😀');
  assert.equal(events[0].id, '7');
  assert.ok(f.cancelled.length);
});

test('a route change queued after resolution fences dispatch and late open completion', async () => {
  const f = fixture();
  const route = f.api.getRouteConnection();
  route.resolve = async () => { f.change(); return 'https://selected.test'; };
  await assert.rejects(openEventStream(f.api, '/v1/events', {}, new AbortController().signal));
  assert.equal(f.opened.length, 0);

  const g = fixture();
  let complete!: (value: any) => void;
  NativeModules.PorticoRuntime.openNetworkStream = (...args: any[]) => {
    g.opened.push(args);
    return new Promise(resolve => { complete = resolve; });
  };
  const opening = openEventStream(g.api, '/v1/events', {}, new AbortController().signal);
  await new Promise(resolve => setImmediate(resolve));
  g.change();
  complete({status: 200, headers: {}});
  await assert.rejects(opening);
  assert.ok(g.cancelled.includes(g.opened[0][0]));
});

test('native stream contracts refuse redirects and bound connections, chunks and lifetime', async () => {
  const {readFileSync} = await import('node:fs');
  const apple = readFileSync(new URL('../Native/PorticoPlayer.m', import.meta.url), 'utf8');
  const android = readFileSync(new URL('../android/app/src/main/java/tv/getportico/portico/native/PorticoEventStream.kt', import.meta.url), 'utf8');
  for (const method of ['openNetworkStream', 'readNetworkStream', 'cancelNetworkStream']) {
    assert.match(apple, new RegExp('RCT_REMAP_METHOD\\(' + method));
  }
  assert.match(apple.slice(apple.indexOf('@interface PorticoEventStream')), /completionHandler\(nil\)/);
  assert.match(apple, /PorticoEventStreams.count>=4/);
  assert.match(apple, /timeoutIntervalForResource=2100/);
  assert.match(apple, /1048576-self.received/);
  assert.match(android, /followRedirects\(false\).followSslRedirects\(false\)/);
  assert.match(android, /entries.size >= 4/);
  assert.match(android, /callTimeout\(2100,/);
  assert.match(android, /entry.received > 1048576/);
  assert.match(android, /ByteArray\(16384\)/);
  assert.match(android, /readTimeout\(60,/);
  assert.match(apple, /timeoutIntervalForRequest=60/);
});


test('idle timeouts and local stream refusals never invalidate the verified API route', async () => {
  for (const code of ['event_timeout', 'event_local', 'event_redirect', 'event_cancelled']) {
    const f = fixture();
    NativeModules.PorticoRuntime.openNetworkStream = async () => { throw Object.assign(new Error('Stream refused'), {code}); };
    await assert.rejects(openEventStream(f.api, '/v1/events', {}, new AbortController().signal));
    assert.equal(f.recoveries(), 0, code + ' while opening');

    const g = fixture();
    const response = await openEventStream(g.api, '/v1/events', {}, new AbortController().signal);
    NativeModules.PorticoRuntime.readNetworkStream = async () => { throw Object.assign(new Error('Stream interrupted'), {code}); };
    await assert.rejects(response.body!.getReader().read());
    assert.equal(g.recoveries(), 0, code + ' while reading');
  }
});

test('a genuine stream connection failure still requests fenced route recovery', async () => {
  const f = fixture();
  NativeModules.PorticoRuntime.openNetworkStream = async () => { throw Object.assign(new Error('Connection refused'), {code: 'event_transport'}); };
  await assert.rejects(openEventStream(f.api, '/v1/events', {}, new AbortController().signal));
  assert.equal(f.recoveries(), 1);
});

test('a time-out before the stream opens is route evidence on both platforms; an idle open stream is not', async () => {
  const {readFileSync} = await import('node:fs');
  const android = readFileSync(new URL('../android/app/src/main/java/tv/getportico/portico/native/PorticoEventStream.kt', import.meta.url), 'utf8');
  const opening = android.slice(android.indexOf('override fun onFailure'), android.indexOf('override fun onResponse'));
  assert.match(opening, /promise\.reject\("event_transport"/);
  assert.doesNotMatch(opening, /event_timeout/);
  assert.match(android.slice(android.indexOf('override fun onResponse')), /e is InterruptedIOException -> "event_timeout"/);
  const apple = readFileSync(new URL('../Native/PorticoPlayer.m', import.meta.url), 'utf8');
  assert.match(apple, /error\.code==NSURLErrorTimedOut\?\(self\.openingReject\?@"event_transport":@"event_timeout"\)/);
});
