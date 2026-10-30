import test from 'node:test';
import assert from 'node:assert/strict';
import {DrawnSubtitleRetry, revalidateTextSubtitles, subtitleFetch, transientSubtitleFailure} from '../src/platform/playback/subtitle-retry.ts';

let networkFailure: unknown;
await assert.rejects(subtitleFetch(async () => {throw new TypeError('network');})('https://server.example/subtitle'), error => {networkFailure = error;return true;});

const settle = async () => {for (let index = 0; index < 8; index++) await Promise.resolve();};
test('only fetch and response-body failures receive the transport marker', async () => {
  const signal = new AbortController().signal, options = {signal, credentials: 'omit', cache: 'no-store', redirect: 'error'} as const;
  const bytes = new Uint8Array([0xc3, 0x28]); // Invalid UTF-8 must fail closed after a successful transfer.
  const response = await subtitleFetch(async (input, init) => {
    assert.equal(input, 'https://server.example/subtitle');assert.equal(init, options);
    return new Response(bytes, {headers: {'Content-Type': 'application/json', 'Content-Length': '2', ETag: '"one"'}});
  })('https://server.example/subtitle', options);
  assert.equal(response.status, 200);assert.equal(response.headers.get('ETag'), '"one"');assert.equal(response.headers.get('Content-Length'), '2');
  const received = new Uint8Array(await response.arrayBuffer());assert.deepEqual(received, bytes);
  assert.throws(() => new TextDecoder('utf-8', {fatal: true}).decode(received), error => error instanceof TypeError && !transientSubtitleFailure(error));
  const broken = await subtitleFetch(async () => new Response(new ReadableStream({start(controller) {controller.error(new TypeError('connection interrupted'));}})))('https://server.example/subtitle');
  await assert.rejects(broken.arrayBuffer(), error => transientSubtitleFailure(error));
  const aborted = new DOMException('cancelled', 'AbortError');
  await assert.rejects(subtitleFetch(async () => {throw aborted;})('https://server.example/subtitle'), error => error === aborted && !transientSubtitleFailure(error));
  const refusal = Object.assign(new Error('refused'), {status: 403});
  await assert.rejects(subtitleFetch(async () => {throw refusal;})('https://server.example/subtitle'), error => error === refusal && !transientSubtitleFailure(error));
  const refused = new Response(null, {status: 403});
  assert.equal(await subtitleFetch(async () => refused)('https://server.example/subtitle'), refused);
  const unchanged = new Response(null, {status: 304});
  assert.equal(await subtitleFetch(async () => unchanged)('https://server.example/subtitle'), unchanged);
});
test('subtitle response cancellation reaches the original body and releases its reader', async () => {
  let cancelled: unknown, reads = 0;
  const original = new Response(new ReadableStream<Uint8Array>({
    pull(controller) {reads++;controller.enqueue(new Uint8Array([1]));}, cancel(reason) {cancelled = reason;},
  }, {highWaterMark: 0}));
  const response = await subtitleFetch(async () => original)('https://server.example/subtitle');
  assert.equal(reads, 0, 'header validation must precede demand for subtitle bytes');
  await response.body!.cancel('selection retired');
  assert.equal(cancelled, 'selection retired');assert.equal(original.body!.locked, false);
  const pendingBody = new ReadableStream<Uint8Array>({cancel(reason) {cancelled = reason;}}, {highWaterMark: 0});
  const pending = await subtitleFetch(async () => new Response(pendingBody))('https://server.example/subtitle');
  const reader = pending.body!.getReader(), reading = reader.read();
  await reader.cancel('pending selection retired');
  assert.equal((await reading).done, true);assert.equal(cancelled, 'pending selection retired');assert.equal(pendingBody.locked, false);
  reader.releaseLock();
});
function timers() {const pending = new Map<number, {fn(): void; ms: number}>();let serial = 0;return {
  pending, setTimer: (fn: () => void, ms: number) => {const id = ++serial;pending.set(id, {fn, ms});return id;}, clearTimer: (id: unknown) => {pending.delete(id as number);},
  fire: () => {const next = pending.entries().next().value;assert.ok(next, 'a scheduled retry is required');pending.delete(next[0]);next[1].fn();},
};}
test('drawn subtitles retry transport failures with a bounded budget and reject explicit refusal', () => {
  const clock = timers();let retries = 0;
  const retry = new DrawnSubtitleRetry({...clock, current: () => true, retry: () => {retries++;}});
  for (const delay of [1000, 2000, 5000]) {
    assert.equal(retry.schedule(networkFailure), true);assert.equal(retry.schedule(networkFailure), false);
    assert.equal(retry.pending, true, 'a second failure while one waits is not a reason to give up');
    assert.equal([...clock.pending.values()][0]!.ms, delay);clock.fire();assert.equal(retry.pending, false);
  }
  assert.equal(retries, 3);assert.equal(retry.schedule({status: 503}), false);assert.equal(clock.pending.size, 0);
  const refused = new DrawnSubtitleRetry({...clock, current: () => true, retry: () => {assert.fail('refusals must not retry');}});
  for (const status of [401, 403, 404, 410]) assert.equal(refused.schedule({status}), false);
});
test('retiring the selected drawn key cancels its timer and a stale epoch cannot retry', () => {
  const clock = timers();let current = true, retries = 0;
  const retry = new DrawnSubtitleRetry({...clock, current: () => current, retry: () => {retries++;}});
  retry.schedule({status: 503});current = false;clock.fire();assert.equal(retries, 0);
  current = true;retry.schedule(networkFailure);retry.dispose();assert.equal(clock.pending.size, 0);
  assert.equal(retry.schedule(networkFailure), false);
});
test('only network, timeout, rate-limit and server failures retain a subtitle document', () => {
  for (const error of [networkFailure, {status: 500}, {status: 599}, {status: 408}, {status: 429}]) assert.equal(transientSubtitleFailure(error), true);
  for (const error of [new TypeError('unreadable UTF-8'), new Error('invalid document'), new DOMException('cancelled', 'AbortError'), {status: 401}, {status: 403}, {status: 404}, {status: 410}]) assert.equal(transientSubtitleFailure(error), false);
});
test('loaded text captions survive a failed revalidation and recover after five seconds with the same validator', async () => {
  for (const failure of [networkFailure, {status: 503}]) {
    const clock = timers(), abort = new AbortController(), document = {cues: ['caption']}, validators: string[] = [];
    let reads = 0, shown: typeof document | undefined, clears = 0;
    await revalidateTextSubtitles({signal: abort.signal, current: () => true, read: async etag => {
      validators.push(etag);reads++;if (reads === 1) return {unchanged: false, document, etag: '"one"'};if (reads === 2) throw failure;return {unchanged: true};
    }, show: value => {shown = value;}, clear: () => {clears++;shown = undefined;}, failed: () => {assert.fail('admitted captions should remain during a transport outage');}, recovered: () => {}, schedule: clock.setTimer});
    assert.equal(shown, document);assert.equal([...clock.pending.values()][0]!.ms, 25000);
    clock.fire();await settle();assert.equal(shown, document);assert.equal(clears, 0);assert.equal([...clock.pending.values()][0]!.ms, 5000);
    clock.fire();await settle();assert.equal(shown, document);assert.deepEqual(validators, ['', '"one"', '"one"']);assert.equal([...clock.pending.values()][0]!.ms, 25000);abort.abort();
  }
});
test('explicit text subtitle refusals and malformed documents clear the document and validator before the next read', async () => {
  for (const failure of [{status: 401}, {status: 403}, {status: 404}, {status: 410}, new TypeError('unreadable UTF-8')]) {
    const clock = timers(), abort = new AbortController(), validators: string[] = [];let reads = 0, shown: string | undefined, failed = 0;
    await revalidateTextSubtitles({signal: abort.signal, current: () => true, read: async etag => {
      validators.push(etag);reads++;if (reads === 1) return {unchanged: false, document: 'caption', etag: '"one"'};throw failure;
    }, show: value => {shown = value;}, clear: () => {shown = undefined;}, failed: () => {failed++;}, recovered: () => {}, schedule: clock.setTimer});
    clock.fire();await settle();assert.equal(shown, undefined);assert.equal(failed, 1);
    clock.fire();await settle();assert.deepEqual(validators, ['', '"one"', '']);abort.abort();
  }
});
test('late text reads and scheduled retries cannot cross a playback or cancellation boundary', async () => {
  const clock = timers(), abort = new AbortController();let resolve!: (value: {unchanged: false; document: string; etag: string}) => void, current = true, shown = 0;
  const pending = revalidateTextSubtitles({signal: abort.signal, current: () => current, read: () => new Promise(done => {resolve = done;}),
    show: () => {shown++;}, clear: () => {assert.fail('retired reads must not clear a successor');}, failed: () => {assert.fail('retired reads must not report failure');}, recovered: () => {}, schedule: clock.setTimer});
  current = false;abort.abort();resolve({unchanged: false, document: 'old', etag: '"old"'});await pending;assert.equal(shown, 0);assert.equal(clock.pending.size, 0);
});
