/**
 * The Vega player's own HLS reader (`hls.ts`), against a stand-in media element and server: a
 * title by its playlist's seconds, a live channel by its timeline, the connection measured as the
 * segments arrive, and each way a stream can stop told apart.
 */
import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {finishResponse, releaseResponse, resetServer, segment, server} from './stubs/server.mjs';
import {mp4Init} from './stubs/mp4.mjs';
import {VideoPlayer, resetWorld, world} from './stubs/w3cmedia.mjs';
import {HlsSource, type SourceError} from '../src/bridge/hls.ts';
import {segmentNumbered} from '../src/bridge/hls-playlist.ts';

const base = 'http://server.test/v1/media/GRANT/';
const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
async function until(what: () => boolean, ms = 4000): Promise<void> {
  const end = Date.now() + ms;
  while (!what()) { if (Date.now() > end) throw new Error('timed out waiting'); await sleep(10); }
}
const spans = (player: any): [number, number][] => player.source.buffers[0].spans;
const near = (actual: number, expected: number, slack = 0.011) => assert.ok(Math.abs(actual - expected) <= slack, `${actual} is not ${expected}`);

let open: HlsSource[] = [];
afterEach(() => { for (const source of open) source.close(); open = []; resetServer(); resetWorld(); });

function start(url: string, originMs?: number, now?: () => number) {
  const player = new VideoPlayer() as any;
  const failures: SourceError[] = [];
  const source = new HlsSource(player, url, error => failures.push(error), originMs, now);
  open.push(source);
  return {player, source, failures};
}

/** A film of `count` six-second segments in fragmented MP4, whose own clock starts a second in. */
function film(count = 40, bytes = 0) {
  let text = '#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:7\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-MAP:URI="init.mp4"\n';
  server.routes.set(base + 'init.mp4', segment({init: true}));
  for (let n = 0; n < count; n++) { text += `#EXTINF:6.000,\ns${n}.m4s\n`; server.routes.set(`${base}s${n}.m4s`, segment({pts: 1 + n * 6, duration: 6, bytes})); }
  server.routes.set(base + 'index.m3u8', text + '#EXT-X-ENDLIST\n');
}

test('a title opens where it is to start and is kept filled a little ahead, no further', async () => {
  film();
  const {player, source, failures} = start(base + 'index.m3u8');
  await source.open(0);
  // The segments' own clock runs a second ahead of the playlist's: positions are the playlist's.
  assert.equal(source.elementTime(0), 1);
  player.currentTime = source.elementTime(0);
  assert.equal(source.position(), 0);
  await until(() => spans(player)[0]?.[1] === 37);
  await sleep(450);
  assert.deepEqual(spans(player), [[1, 37]]);
  assert.equal(server.log.includes(base + 's6.m4s'), false);
  near(source.bufferedSeconds(), 36);
  assert.equal(player.source.duration, 240);
  assert.deepEqual(failures, []);
});

test('a title resumed part way is filled from there, and a seek refills only when it must', async () => {
  film();
  const {player, source} = start(base + 'index.m3u8');
  await source.open(62);
  assert.equal(server.log.includes(base + 's0.m4s'), false);
  await until(() => spans(player)[0]?.[1] === 97);
  assert.deepEqual(spans(player), [[61, 97]]);
  // Inside what is held: buffered segments stay in place and are not fetched again.
  // Moving forward may fill one more segment inside the new 30-second look-ahead.
  const asked = server.log.length;
  assert.equal(source.seek(70), 71);
  await until(() => spans(player)[0]?.[1] === 103);
  assert.deepEqual(server.log.slice(asked), [base + 's16.m4s']);
  assert.deepEqual(spans(player), [[61, 103]]);
  // Outside it: emptied and filled from the segment that holds the place.
  assert.equal(source.seek(200), 201);
  await until(() => spans(player).length === 1 && spans(player)[0][0] === 199 && spans(player)[0][1] === 235);
  player.currentTime = 201;
  near(source.position(), 200);
});

test('the connection is measured from segments big enough to say something, while it is fresh', async () => {
  film(40, 100000);
  // The clock and each answer's completion are the test's: the figure is exact, not the machine's.
  let clock = 1_800_000_000_000;
  server.hold = url => url.endsWith('.m4s');
  const {source} = start(base + 'index.m3u8', undefined, () => clock);
  assert.equal(source.throughputKbps(), undefined);
  const bytes = (number: number) => Buffer.byteLength(JSON.stringify({pts: 1 + number * 6, duration: 6, bytes: 100000, pad: 'x'.repeat(100000)}));
  let downloaded = 0;
  /** Answers the segment's headers, then its last byte thirty counted milliseconds later. */
  const answer = async (number: number) => {
    const url = `${base}s${number}.m4s`;
    await until(() => server.held.some(request => request.url === url));
    releaseResponse(url);
    clock += 30;
    finishResponse(url);
    downloaded += bytes(number);
  };
  const opening = source.open(0);
  await answer(0);
  await opening;
  // Opened where it starts, five more segments fill the thirty seconds ahead.
  for (let number = 1; number <= 5; number++) await answer(number);
  const kbps = source.throughputKbps();
  assert.equal(kbps, Math.round(downloaded * 8 / 180), `measured ${kbps}`);
  // Only what was downloaded in the last fifteen seconds says anything of the connection.
  clock += 15001;
  assert.equal(source.throughputKbps(), undefined);
});

test('small segments say nothing of the connection', async () => {
  film(40, 100);
  const {source} = start(base + 'index.m3u8');
  await source.open(0);
  await until(() => server.log.includes(base + 's5.m4s'));
  assert.equal(source.throughputKbps(), undefined);
});

const origin = Date.parse('2026-10-13T18:00:00Z');
/** A channel of four-second segments. Segment n was on air n × 4 s after the origin. The
 * segments' own clock is `clock(n)`; a new programme may restart it. */
function channel(window: {first: number; count: number}, clock: (n: number) => number = n => 5000 + n * 4, more = '') {
  server.routes.set(base + 'index.m3u8', () => {
    let text = `#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:${window.first}\n${more}`;
    for (let n = window.first; n < window.first + window.count; n++) text += `#EXT-X-PROGRAM-DATE-TIME:${new Date(origin + n * 4000).toISOString()}\n#EXTINF:4.000000,\ns${n}.ts\n`;
    return text;
  });
  for (let n = 0; n < 400; n++) server.routes.set(`${base}s${n}.ts`, () => (n >= window.first && n < window.first + window.count ? segment({pts: clock(n), duration: 4}) : {status: 404}));
}

test('a live channel begins behind the newest segment and says where it is on the channel\'s timeline', async () => {
  const window = {first: 100, count: 12};
  channel(window);
  const {player, source, failures} = start(base + 'index.m3u8', origin);
  await source.open(NaN);
  // Three segments behind the newest (111): segment 109, which follows nothing, so it is at zero.
  assert.equal(source.beginning(), 0);
  near(source.position(), 109 * 4);
  await until(() => spans(player)[0]?.[1] === 12);
  player.advance(5.5);
  near(source.position(), 110 * 4 + 1.5);
  assert.equal(source.throughputKbps(), undefined);
  // The playlist slides: the new segments are fetched once, by number, as they appear.
  window.first = 103;
  await until(() => spans(player)[0]?.[1] === 24, 6000);
  assert.deepEqual(server.log.filter(url => url.endsWith('.ts')).map(url => Number(/s(\d+)\.ts/.exec(url)![1])), [109, 110, 111, 112, 113, 114]);
  player.advance(14);
  near(source.position(), 113 * 4 + 3.5);
  assert.deepEqual(failures, []);
});

test('a live channel is opened at a moment on its timeline when one is asked for', async () => {
  channel({first: 100, count: 12});
  const {source} = start(base + 'index.m3u8', origin);
  await source.open(104 * 4 + 1.25);
  near(source.beginning(), 1.25);
  assert.equal(server.log.filter(url => url.endsWith('.ts'))[0], base + 's104.ts');
});

test('a live channel honours where the server says live viewing begins', async () => {
  channel({first: 100, count: 12}, undefined, '#EXT-X-START:TIME-OFFSET=-30.000000,PRECISE=YES\n');
  const {source} = start(base + 'index.m3u8', origin);
  await source.open(NaN);
  near(source.position(), 104 * 4);
});

test('seeking back on a live channel: at once inside what is held, refilled when it is not', async () => {
  channel({first: 100, count: 12});
  const {player, source} = start(base + 'index.m3u8', origin);
  await source.open(NaN);
  await until(() => spans(player)[0]?.[1] === 12);
  near(await source.seek(110 * 4 + 2), 6);
  // Twenty-odd seconds back: not held, so the buffer is emptied and filled from segment 103.
  const element = await source.seek(103 * 4 + 1);
  player.currentTime = element;
  near(source.position(), 103 * 4 + 1);
  assert.ok(server.log.includes(base + 's103.ts'));
  // A moment that has left the window is its oldest segment; one not broadcast yet is where live viewing begins.
  player.currentTime = await source.seek(10);
  near(source.position(), 100 * 4);
  player.currentTime = await source.seek(999999);
  near(source.position(), 109 * 4);
});

test('a change of programme on a device that keeps the segments\' own clock is carried across', async () => {
  world.sequenceMode = false;
  // The clock restarts at segment 105.
  channel({first: 100, count: 12}, n => (n < 105 ? 5000 + n * 4 : 100 + (n - 105) * 4));
  const {player, source, failures} = start(base + 'index.m3u8', origin);
  await source.open(103 * 4);
  assert.equal(source.beginning(), 5412);
  player.currentTime = source.beginning();
  await until(() => spans(player).length === 2 && spans(player)[0][1] === 128);
  assert.deepEqual(spans(player), [[100, 128], [5412, 5420]]);
  near(source.position(), 103 * 4);
  // Playing to the end of the first programme: playback is taken to where the next one landed.
  player.paused = false;
  player.advance(7.95);
  near(source.position(), 104 * 4 + 3.95);
  await until(() => player.currentTime < 200);
  near(player.currentTime, 100.01);
  near(source.position(), 105 * 4);
  player.advance(6);
  near(source.position(), 106 * 4 + 2);
  assert.deepEqual(failures, []);
});

test('a channel that moved on while viewing was paused goes on from the oldest segment it still has', async () => {
  const window = {first: 100, count: 12};
  channel(window);
  const {player, source, failures} = start(base + 'index.m3u8', origin);
  await source.open(100 * 4);
  // Half a minute is held and nothing more is fetched; meanwhile the channel moves a long way on.
  await until(() => spans(player)[0]?.[1] === 32);
  window.first = 200;
  await sleep(450);
  // Viewing resumes and plays on: segment 108 is gone, so the playlist is read again.
  player.advance(20);
  await until(() => server.log.includes(base + 's200.ts'), 6000);
  await until(() => spans(player)[0]?.[1] >= 36);
  assert.deepEqual(failures, []);
  player.advance(12.5);
  near(source.position(), 200 * 4 + 0.5);
});

test('what has been watched on a channel is given back', async () => {
  channel({first: 100, count: 40});
  const {player, source} = start(base + 'index.m3u8', origin);
  await source.open(100 * 4);
  await until(() => spans(player)[0]?.[1] === 32);
  // 70 s in: more is fetched ahead, and all but the last half minute behind is removed.
  player.advance(70);
  await until(() => spans(player)[0]?.[1] >= 100 && spans(player)[0][0] >= 36, 6000);
  assert.ok(spans(player)[0][0] <= 40);
  near(source.position(), 100 * 4 + 70);
});

test('each way a stream stops is told apart', async () => {
  // The server refuses: its answer is kept.
  film();
  server.routes.set(base + 's1.m4s', {status: 404});
  let run = start(base + 'index.m3u8');
  await run.source.open(0);
  await until(() => run.failures.length === 1);
  assert.deepEqual([run.failures[0].status, run.failures[0].network, run.failures[0].refused], [404, false, '']);
  run.source.close();

  // The server is busy: an answer that waiting may change.
  server.routes.set(base + 's1.m4s', {status: 500});
  run = start(base + 'index.m3u8');
  await run.source.open(0);
  await until(() => run.failures.length === 1);
  assert.deepEqual([run.failures[0].status, run.failures[0].network], [500, true]);
  run.source.close();

  // No answer at all.
  server.offline = true;
  run = start(base + 'index.m3u8');
  await run.source.open(0);
  assert.deepEqual([run.failures[0].status, run.failures[0].network, run.failures[0].refused], [0, true, '']);
  server.offline = false;

  // The media element will not take the stream, or its bytes.
  film();
  world.refuse = /video\/mp4/;
  run = start(base + 'index.m3u8');
  await run.source.open(0);
  assert.deepEqual([run.failures[0].network, run.failures[0].refused], [false, 'unsupported']);
  world.refuse = null;
  world.appendFails = true;
  run = start(base + 'index.m3u8');
  await run.source.open(0);
  assert.deepEqual([run.failures[0].network, run.failures[0].refused], [false, 'decode']);
});

test('a closed stream fetches nothing more and reports nothing', async () => {
  film();
  const {source, failures} = start(base + 'index.m3u8');
  await source.open(0);
  source.close();
  const asked = server.log.length;
  server.offline = true;
  await sleep(500);
  assert.equal(server.log.length, asked);
  assert.deepEqual(failures, []);
});

test('HLS discards a followed cross-origin response before any media is appended', async () => {
  const url = 'http://redirect-hls.test/v1/media/HLSREDIRECT/index.m3u8';
  server.routes.set(url, {status: 200, from: 'http://capture.test/index.m3u8', text: '#EXTM3U\n#EXTINF:6,\nsecret.m4s\n'});
  const {source, player, failures} = start(url);
  await source.open(0);
  assert.ok(failures.length > 0);
  assert.equal(player.source, null);
  assert.deepEqual(server.log, [url]);
});

test('a playlist cannot send its resource grant to another origin', async () => {
  const url = 'http://playlist-hls.test/v1/media/HLSPLAYLIST/index.m3u8';
  server.routes.set(url, '#EXTM3U\n#EXTINF:6,\nhttp://capture.test/v1/media/HLSPLAYLIST/s0.m4s\n#EXT-X-ENDLIST\n');
  const {source, failures} = start(url);
  await source.open(0);
  assert.ok(failures.length > 0);
  assert.deepEqual(server.log, [url]);
});

test('download timeout measures byte inactivity and retires its own request', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const original = globalThis.XMLHttpRequest;
  const requests: ControlledRequest[] = [];
  class ControlledRequest {
    readyState = 0; status = 0; responseURL = ''; responseType = ''; response: ArrayBuffer | null = null;
    responseText = ''; timeout = -1; withCredentials = false;
    onreadystatechange?: () => void; onprogress?: (event: {loaded: number}) => void;
    onload?: () => void; onerror?: () => void; ontimeout?: () => void; onabort?: () => void;
    aborted = false; headers: Record<string, string> = {};
    open(_method: string, url: string) { this.responseURL = url; }
    setRequestHeader() {}
    getResponseHeader(name: string) { return this.headers[name.toLowerCase()] ?? null; }
    send() { requests.push(this); }
    abort() { this.aborted = true; this.onabort?.(); }
    progress(loaded: number) { this.status = 200; this.readyState = 3; this.onprogress?.({loaded}); }
    finish() { this.status = 200; this.readyState = 4; this.response = new ArrayBuffer(3); this.onload?.(); }
  }
  globalThis.XMLHttpRequest = ControlledRequest as unknown as typeof XMLHttpRequest;
  t.after(() => { globalThis.XMLHttpRequest = original; });
  const {source} = start(base + 'index.m3u8');
  const get = (signal?: AbortSignal) => (source as any).get(base + 's0.m4s', 'arraybuffer', signal) as Promise<ArrayBuffer>;
  const long = get();
  const first = requests[0]!;
  assert.equal(first.timeout, 0);
  for (let n = 1; n <= 4; n++) { t.mock.timers.tick(15000); first.progress(n); }
  first.finish();
  assert.equal((await long).byteLength, 3);
  const idle = get();
  const idleResult = assert.rejects(idle, (error: SourceError) => error.network && error.message === 'request timed out');
  const second = requests[1]!;
  second.progress(1);
  t.mock.timers.tick(10000);
  second.progress(1); // repeated notifications without bytes must not extend the deadline
  t.mock.timers.tick(10000);
  await idleResult;
  assert.equal(second.aborted, true);
  const controller = new AbortController();
  const retired = get(controller.signal);
  const retiredResult = assert.rejects(retired, (error: SourceError) => error.message === 'superseded');
  const third = requests[2]!;
  controller.abort();
  await retiredResult;
  const successor = get();
  const fourth = requests[3]!;
  t.mock.timers.tick(19999);
  assert.equal(third.aborted, true);
  assert.equal(fourth.aborted, false);
  fourth.finish();
  await successor;
  t.mock.timers.tick(20000);
  assert.equal(fourth.aborted, false);
  // An arraybuffer stays reserved after load, until its owner releases it.
  const loadMedia = (signal?: AbortSignal) => (source as any).getMedia(base + 's0.m4s', signal) as Promise<{data: ArrayBuffer; release(): void}>;
  const heldPromise = loadMedia();
  await Promise.resolve();
  const heldRequest = requests[4]!;
  heldRequest.finish();
  const held = await heldPromise;
  const waitingController = new AbortController();
  const waiting = loadMedia(waitingController.signal);
  const waitingResult = assert.rejects(waiting, (error: SourceError) => error.message === 'superseded');
  assert.equal(requests.length, 5, 'a second media request waits for its reservation');
  waitingController.abort();
  await waitingResult;
  const replacement = loadMedia();
  assert.equal(requests.length, 5);
  held.release(); held.release();
  await Promise.resolve();
  const replacementRequest = requests[5]!;
  replacementRequest.finish();
  const next = await replacement;
  assert.equal(next.data.byteLength, 3);
  next.release();
  const silent = get();
  const silentResult = assert.rejects(silent, (error: SourceError) => error.network && error.message === 'request timed out');
  const silentRequest = requests[6]!;
  t.mock.timers.tick(20000);
  await silentResult;
  assert.equal(silentRequest.aborted, true);
  const oversizedHeader = get();
  const headerResult = assert.rejects(oversizedHeader, (error: SourceError) => error.refused === 'unsupported');
  const headerRequest = requests[7]!;
  headerRequest.status = 200; headerRequest.readyState = 2;
  headerRequest.headers['content-length'] = String(128 * 1024 * 1024 + 1);
  headerRequest.onreadystatechange?.();
  await headerResult;
  assert.equal(headerRequest.aborted, true);
  for (const length of [undefined, '1']) {
    const oversizedBody = get();
    const bodyResult = assert.rejects(oversizedBody, (error: SourceError) => error.refused === 'unsupported');
    const request: ControlledRequest = requests[requests.length - 1]!;
    if (length) request.headers['content-length'] = length;
    request.progress(128 * 1024 * 1024 + 1);
    await bodyResult;
    assert.equal(request.aborted, true);
  }
  const oversizedText = (source as any).get(base + 'index.m3u8', 'text') as Promise<string>;
  const textResult = assert.rejects(oversizedText, (error: SourceError) => error.message === 'media response too large');
  const textRequest = requests[requests.length - 1]!;
  textRequest.status = 200; textRequest.responseText = 'é'.repeat(524289); textRequest.readyState = 4;
  textRequest.onload?.(); // no Content-Length and no progress events
  await textResult;
  const closing = get();
  const closingResult = assert.rejects(closing, (error: SourceError) => error.message === 'closed');
  const closingRequest = requests[requests.length - 1]!;
  source.close();
  await closingResult;
  assert.equal(closingRequest.aborted, true);
  t.mock.timers.tick(20000);
});

test('unknown segment placement stays charged until whole-track removal', async () => {
  film(1, 128);
  const {source, player} = start(base + 'index.m3u8');
  await source.open(0);
  const internals = source as any;
  const entries = ([...internals.retained] as {bytes: number; start?: number; end?: number; kind: string}[]).filter(entry => entry.kind === 'media');
  assert.equal(entries.length, 1);
  assert.ok(entries[0]!.bytes >= 128);
  await internals.remove(player.source.buffers[0], 1, 4, internals.generation);
  assert.equal([...internals.retained].filter((entry: any) => entry.kind === 'media').length, 1, 'a partial interval cannot refund a whole segment');
  await internals.remove(player.source.buffers[0], 0, Number.MAX_SAFE_INTEGER, internals.generation);
  assert.equal([...internals.retained].filter((entry: any) => entry.kind === 'media').length, 0);
});

test('measured compressed rates reduce the shared ahead target across tracks', () => {
  const {source} = start(base + 'index.m3u8');
  const hls = source as any;
  const video = {}, audio = {};
  hls.tracks = [video, audio];
  assert.equal(hls.aheadTarget(), 30);
  hls.measureSegment(video, 80 * 1024 * 1024, 6);
  near(hls.aheadTarget(), 9.6);
  hls.measureSegment(audio, 36 * 1024 * 1024, 6);
  near(hls.aheadTarget(), 128 * 6 / 116);
  hls.measureSegment(video, 1024, 6);
  near(hls.aheadTarget(), 128 * 6 / 116, 0.011);
});

test('overlap and an earlier live interval never refund unknown segment charges on partial removal', async () => {
  film(1);
  const {source, player} = start(base + 'index.m3u8');
  await source.open(0);
  const hls = source as any, buffer = player.source.buffers[0];
  const append = async (pts: number, duration: number) => {
    const bytes = new TextEncoder().encode(JSON.stringify({pts, duration}));
    hls.charge(buffer, hls.generation, bytes.byteLength, 'media');
    await new Promise<void>(resolve => {
      const done = () => { buffer.removeEventListener('updateend', done); resolve(); };
      buffer.addEventListener('updateend', done); buffer.appendBuffer(bytes.buffer);
    });
  };
  await append(3, 6); // overlaps the original segment
  await append(0, 2); // can occur earlier than the current sequence on a discontinuity
  const before = hls.retainedBytes();
  await hls.remove(buffer, 3, 5, hls.generation);
  assert.equal(hls.retainedBytes(), before);
  await hls.remove(buffer, 0, Number.MAX_SAFE_INTEGER, hls.generation);
  const remaining = hls.retainedBytes();
  assert.ok(remaining < before, 'whole-track removal refunds the original media charges');
  await hls.remove(buffer, 0, Number.MAX_SAFE_INTEGER, hls.generation);
  assert.equal(hls.retainedBytes(), remaining, 'the same charges cannot be refunded twice');
});

test('required non-evictable aggregate over 128 MiB refuses before append', () => {
  const {source} = start(base + 'index.m3u8');
  const hls = source as any, video = {}, audio = {};
  hls.charge(video, hls.generation, 80 * 1024 * 1024, 'media');
  assert.throws(() => hls.charge(audio, hls.generation, 80 * 1024 * 1024, 'media'),
    (error: SourceError) => error.refused === 'unsupported');
  assert.equal(hls.retainedBytes(), 80 * 1024 * 1024);
});

test('36 MB and 80 MB successful responses reach the Vega source buffer', async () => {
  for (const bytes of [36_000_000, 80_000_000]) {
    resetServer(); resetWorld();
    film(1, bytes);
    const {source, player, failures} = start(base + 'index.m3u8');
    await source.open(0);
    assert.equal(failures.length, 0);
    assert.ok(player.source.buffers[0].appended.some((value: {duration?: number}) => value.duration === 6));
    assert.ok((source as any).retainedBytes() >= bytes);
    source.close();
  }
});

test('the retained budget accepts 80 MiB plus 36 MiB across two tracks', () => {
  const {source} = start(base + 'index.m3u8');
  const hls = source as any;
  hls.charge({}, hls.generation, 80 * 1024 * 1024, 'media');
  hls.charge({}, hls.generation, 36 * 1024 * 1024, 'media');
  assert.equal(hls.retainedBytes(), 116 * 1024 * 1024);
});

function audioSwitchRoutes() {
  server.routes.set(base + 'index.m3u8', [
    '#EXTM3U',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="A",DEFAULT=YES,URI="a0.m3u8"',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="B",URI="a1.m3u8"',
    '#EXT-X-STREAM-INF:BANDWIDTH=8000000,CODECS="avc1.64001f,mp4a.40.2",AUDIO="audio"',
    'video.m3u8',
  ].join('\n'));
  const playlist = (init: string, media: string) => `#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MAP:URI="${init}"\n#EXTINF:6,\n${media}\n#EXT-X-ENDLIST\n`;
  server.routes.set(base + 'video.m3u8', playlist('v-init.mp4', 'v.m4s'));
  server.routes.set(base + 'a0.m3u8', playlist('a0-init.mp4', 'a0.m4s'));
  server.routes.set(base + 'a1.m3u8', playlist('a1-init.mp4', 'a1.m4s'));
  for (const name of ['v-init.mp4', 'a0-init.mp4', 'a1-init.mp4']) server.routes.set(base + name, segment({init: true}));
  for (const name of ['v.m4s', 'a0.m4s', 'a1.m4s']) server.routes.set(base + name, segment({pts: 1, duration: 6}));
}

test('missing CODECS is derived from init before creating a buffer, fetching init only once', async () => {
  film(2);
  server.routes.set(base + 'init.mp4', {bytes: mp4Init(['hvc1', 'ec-3'])});
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  assert.equal(player.source.buffers[0].type, 'video/mp4; codecs="hvc1.2.4.L120.B0,ec-3"');
  assert.equal(server.log.filter(url => url === base + 'init.mp4').length, 1);
  assert.deepEqual(failures, []);
});

test('invalid missing-CODECS initialization refuses the stream before adding a guessed buffer', async () => {
  film(2);
  server.routes.set(base + 'init.mp4', {bytes: Buffer.from('not mp4')});
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  assert.equal(failures.length, 1);
  assert.equal(failures[0].refused, 'unsupported');
  assert.equal(player.source.buffers.length, 0);
  assert.equal(server.log.includes(base + 's0.m4s'), false);
});

test('missing CODECS with alternate audio derives separate video and audio buffers', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'index.m3u8', server.routes.get(base + 'index.m3u8').replace(',CODECS="avc1.64001f,mp4a.40.2"', ''));
  server.routes.set(base + 'v-init.mp4', {bytes: mp4Init(['avc1'])});
  server.routes.set(base + 'a0-init.mp4', {bytes: mp4Init(['mp4a'])});
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  assert.deepEqual(player.source.buffers.map((buffer: any) => buffer.type), ['video/mp4; codecs="avc1.64001F"', 'audio/mp4; codecs="mp4a.40.2"']);
  assert.deepEqual(failures, []);
});

test('missing CODECS refuses muxed primary plus alternate audio before misdeclaring or duplicating audio tracks', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'index.m3u8', server.routes.get(base + 'index.m3u8').replace(',CODECS="avc1.64001f,mp4a.40.2"', ''));
  server.routes.set(base + 'v-init.mp4', {bytes: mp4Init(['avc1', 'mp4a'])});
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  assert.equal(player.source.buffers.length, 0);
  assert.equal(failures.length, 1); assert.equal(failures[0].refused, 'unsupported');
  assert.equal(failures[0].message, 'muxed_primary_with_external_audio');
  assert.equal(server.log.includes(base + 'a0-init.mp4'), false);
});

test('missing CODECS uses an audio-only primary init as proof and does not add a second audio buffer', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'index.m3u8', server.routes.get(base + 'index.m3u8').replace(',CODECS="avc1.64001f,mp4a.40.2"', ''));
  server.routes.set(base + 'v-init.mp4', {bytes: mp4Init(['mp4a'])});
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  assert.deepEqual(player.source.buffers.map((buffer: any) => buffer.type), ['audio/mp4; codecs="mp4a.40.2"']);
  assert.equal(server.log.includes(base + 'a0-init.mp4'), false);
  assert.deepEqual(failures, []);
});

test('mixed audio codecs use the selected init and replace the audio buffer when that codec changes', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'index.m3u8', server.routes.get(base + 'index.m3u8').replace('avc1.64001f,mp4a.40.2', 'avc1.64001f,mp4a.40.2,ec-3'));
  server.routes.set(base + 'a0-init.mp4', {bytes: mp4Init(['mp4a'])});
  server.routes.set(base + 'a1-init.mp4', {bytes: mp4Init(['ec-3'])});
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  player.currentTime = 1;
  await until(() => player.source.buffers[1].spans.length > 0);
  const video = player.source.buffers[0], audio = player.source.buffers[1];
  assert.equal(audio.type, 'audio/mp4; codecs="mp4a.40.2"');
  await source.selectAudio(1, {current: () => true, sourceCurrent: () => true, willMutate() {}}).done;
  assert.equal(player.source.buffers[0], video);
  assert.notEqual(player.source.buffers[1], audio);
  assert.equal(player.source.buffers[1].type, 'audio/mp4; codecs="ec-3"');
  assert.equal(source.audioIndex(), 1);
  assert.ok(!(source as any).retained.size || [...(source as any).retained].every((entry: any) => entry.buffer !== audio));
  assert.deepEqual(failures, []);
});

test('codec-less TS audio switches reuse the buffer while the resolved type stays the same', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'index.m3u8', server.routes.get(base + 'index.m3u8').replace(',CODECS="avc1.64001f,mp4a.40.2"', ''));
  server.routes.set(base + 'v-init.mp4', {bytes: mp4Init(['avc1'])});
  for (const index of [0, 1]) {
    server.routes.set(`${base}a${index}.m3u8`, `#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\na${index}.ts\n#EXT-X-ENDLIST\n`);
    server.routes.set(`${base}a${index}.ts`, segment({pts: 1, duration: 6}));
  }
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  player.currentTime = 1;
  await until(() => player.source.buffers[1]?.spans.length > 0);
  const video = player.source.buffers[0], audio = player.source.buffers[1];
  assert.equal(audio.type, 'video/mp2t');
  let created = 0;
  const add = player.source.addSourceBuffer.bind(player.source);
  player.source.addSourceBuffer = (type: string) => {created++; return add(type);};
  for (const index of [1, 0]) {
    await source.selectAudio(index, {current: () => true, sourceCurrent: () => true, willMutate() {}}).done;
    assert.equal(source.audioIndex(), index);
    assert.equal(player.source.buffers[0], video);
    assert.equal(player.source.buffers[1], audio);
    assert.deepEqual(audio.spans, [[1, 7]]);
  }
  assert.equal(created, 0, 'neither rendition switch creates another SourceBuffer');
  assert.deepEqual(failures, []);
});

/** A channel with a separate audio rendition whose segments are longer than the video's: the
 * audio buffer's own sequence runs well ahead of the element clock, so only the offset the
 * production code sets can put the replacement audio where playback is. */
function liveAudioChannel() {
  const playlist = (first: number, count: number, duration: number, init: string, media: string) => {
    let text = `#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:${duration}\n#EXT-X-MEDIA-SEQUENCE:${first}\n#EXT-X-MAP:URI="${init}"\n`;
    for (let n = first; n < first + count; n++) text += `#EXT-X-PROGRAM-DATE-TIME:${new Date(origin + n * duration * 1000).toISOString()}\n#EXTINF:${duration}.000000,\n${media}${n}.m4s\n`;
    return text;
  };
  server.routes.set(base + 'index.m3u8', [
    '#EXTM3U',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="A",DEFAULT=YES,URI="a0.m3u8"',
    '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="B",URI="a1.m3u8"',
    '#EXT-X-STREAM-INF:BANDWIDTH=8000000,CODECS="avc1.64001f,mp4a.40.2",AUDIO="audio"',
    'video.m3u8',
  ].join('\n'));
  server.routes.set(base + 'video.m3u8', playlist(100, 60, 5, 'v-init.mp4', 'v'));
  server.routes.set(base + 'a0.m3u8', playlist(100, 60, 6, 'a0-init.mp4', 'a0-'));
  server.routes.set(base + 'a1.m3u8', playlist(100, 60, 6, 'a1-init.mp4', 'a1-'));
  for (const name of ['v-init.mp4', 'a0-init.mp4', 'a1-init.mp4']) server.routes.set(base + name, segment({init: true}));
  for (let n = 100; n < 160; n++) {
    server.routes.set(`${base}v${n}.m4s`, segment({pts: 5000 + n * 5, duration: 5}));
    for (const prefix of ['a0-', 'a1-']) server.routes.set(`${base}${prefix}${n}.m4s`, segment({pts: n * 6, duration: 6}));
  }
}

test('a live audio switch places the replacement audio at the element clock, not at its own sequence end', async () => {
  liveAudioChannel();
  const {player, source, failures} = start(base + 'index.m3u8', origin);
  await source.open(500);
  const video = player.source.buffers[0], audio = player.source.buffers[1];
  // Twenty five-second segments in: the video's segment that starts at 100 s of element time is
  // on air at 600 s of the channel's timeline, and the six-second audio segments have run past both.
  for (let step = 0; step < 20; step++) {
    player.advance(5);
    await until(() => video.spans.some(([from, to]: number[]) => from <= player.currentTime && to > player.currentTime));
  }
  assert.equal(player.currentTime, 100);
  assert.ok(audio.spans[audio.spans.length - 1][1] > 100, 'the audio sequence itself runs ahead of the clock');
  await source.selectAudio(1, {current: () => true, sourceCurrent: () => true, willMutate() {}}).done;
  assert.equal(source.audioIndex(), 1);
  assert.equal(player.source.buffers[1], audio, 'the resolved type did not change, so the buffer is reused');
  assert.deepEqual(audio.spans, [[100, 106]]);
  assert.deepEqual(failures, []);
});

test('audio switch holds the feed and stages init and media before replacing the old buffer', async () => {
  audioSwitchRoutes();
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  player.currentTime = 1;
  await until(() => player.source.buffers[1].spans.length > 0);
  const hls = source as any, oldGet = hls.getMedia.bind(hls), handoffs: string[] = [];
  hls.getMedia = async (url: string, signal?: AbortSignal) => {
    const lease = await oldGet(url, signal);
    if (!url.endsWith('a1-init.mp4') && !url.endsWith('a1.m4s')) return lease;
    return {...lease, release: () => {
      assert.ok(hls.audioHold, 'the original operation holds the feed while staging');
      assert.ok([...hls.retained].some((entry: any) => entry.kind === 'staging'));
      handoffs.push(url.split('/').pop()!);
      lease.release();
    }};
  };
  let mutations = 0;
  const selection = source.selectAudio(1, {current: () => true, sourceCurrent: () => true, willMutate: () => { mutations++; }});
  await selection.done;
  assert.deepEqual(handoffs, ['a1-init.mp4', 'a1.m4s']);
  assert.ok(mutations > 0);
  assert.equal(source.audioIndex(), 1);
  assert.deepEqual(failures, []);
});

test('foreign non-retirable media still makes the required aggregate unsupported', async () => {
  film(2);
  const {source, player} = start(base + 'index.m3u8');
  const hls = source as any;
  hls.audioHold = {}; // Keep the real pump from racing this one explicitly driven feed.
  await source.open(0);
  player.currentTime = source.elementTime(0);
  const track = hls.tracks[0], next = track.playlist.segments[1];
  hls.charge({}, hls.generation, 128 * 1024 * 1024 - hls.retainedBytes() - 1, 'media');
  let releases = 0;
  const bytes = new TextEncoder().encode(JSON.stringify({pts: 7, duration: 6}));
  hls.getMedia = async () => ({data: bytes.buffer, release: () => { releases++; }});
  assert.equal(await hls.feed(track, next, hls.generation), 'deferred');
  assert.equal(track.next, next.number, 'the deferred segment did not advance the cursor');
  assert.equal(track.deferred.number, next.number);
  assert.equal(releases, 1, 'the unused download reservation is refunded');
  hls.target = undefined;
  player.currentTime = 7.1;
  await assert.rejects(hls.feed(track, next, hls.generation), (error: SourceError) => error.refused === 'unsupported');
  assert.equal(releases, 2);
});

test('audio candidate over the retained budget refuses before old buffer mutation', async () => {
  audioSwitchRoutes();
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  player.currentTime = 1;
  await until(() => player.source.buffers[1].spans.length > 0);
  const hls = source as any, before = player.source.buffers[1].spans.map((span: number[]) => [...span]);
  hls.charge({}, hls.generation, 128 * 1024 * 1024 - hls.retainedBytes() - 1, 'media');
  let mutations = 0;
  const selection = source.selectAudio(1, {current: () => true, sourceCurrent: () => true, willMutate: () => { mutations++; }});
  await assert.rejects(selection.done, (error: SourceError) => error.refused === 'unsupported');
  assert.equal(mutations, 0);
  assert.deepEqual(player.source.buffers[1].spans, before);
  assert.equal(failures[0]?.refused, 'unsupported');
});

test('retiring a hold while its old feed is aborting resumes one fresh pump', async () => {
  film(3);
  const {source} = start(base + 'index.m3u8');
  const hls = source as any;
  hls.audioHold = {};
  await source.open(0);
  hls.audioHold = undefined;
  const originalGet = hls.getMedia.bind(hls);
  let started = false, calls = 0;
  hls.getMedia = (url: string, signal: AbortSignal) => {
    calls++;
    if (calls !== 1) return originalGet(url, signal);
    started = true;
    return new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new Error('retired request')), {once: true});
    });
  };
  const generation = hls.generation;
  hls.startPump(generation);
  await until(() => started);
  assert.equal(started, true);
  hls.audioHold = {};
  hls.pumpControllers.get(generation).abort();
  hls.audioHold = undefined; // retirement may happen before the rejected read resumes
  await until(() => server.log.includes(base + 's1.m4s'));
  assert.ok(calls >= 2, 'the successor pump fetched after the old run settled');
  assert.equal(hls.generation, generation);
});

test('cancelling either candidate handoff discards that operation without changing the old buffer', async () => {
  for (const stopAt of ['a1-init.mp4', 'a1.m4s']) {
    resetServer(); resetWorld(); audioSwitchRoutes();
    const {source, player} = start(base + 'index.m3u8');
    await source.open(0); player.currentTime = 1;
    await until(() => player.source.buffers[1].spans.length > 0);
    const hls = source as any, originalGet = hls.getMedia.bind(hls);
    const old = player.source.buffers[1].spans.map((span: number[]) => [...span]);
    let selection: ReturnType<HlsSource['selectAudio']>, mutations = 0;
    hls.getMedia = async (url: string, signal?: AbortSignal) => {
      const lease = await originalGet(url, signal);
      if (!url.endsWith(stopAt)) return lease;
      return {...lease, release: () => { selection.cancel(); lease.release(); }};
    };
    selection = source.selectAudio(1, {current: () => true, sourceCurrent: () => true, willMutate: () => { mutations++; }});
    await assert.rejects(selection.done);
    assert.equal(mutations, 0);
    assert.deepEqual(player.source.buffers[1].spans, old);
    assert.equal([...hls.retained].filter((entry: any) => entry.kind === 'staging').length, 0);
    source.close();
  }
});

test('unknown segment charges are bounded by their original buffer and generation', () => {
  const {source} = start(base + 'index.m3u8');
  const hls = source as any, buffer = {};
  for (let n = 0; n < 5000; n++) hls.charge(buffer, hls.generation, 1, 'media');
  assert.equal(hls.retainedBytes(), 5000);
  assert.equal(hls.retained.size, 1);
  hls.charge(buffer, hls.generation + 1, 1, 'media');
  assert.equal(hls.retained.size, 2, 'a successor generation owns a distinct charge');
});

test('whole-buffer refunds require a current successful updateend and a readable empty buffer', async () => {
  const {source} = start(base + 'index.m3u8'), hls = source as any;
  class BufferControl {
    updating = false; residual = 1; unreadable = false;
    listeners = new Map<string, Set<() => void>>();
    get buffered() { if (this.unreadable) throw new Error('unreadable'); return {length: this.residual}; }
    addEventListener(type: string, fn: () => void) { if (!this.listeners.has(type)) this.listeners.set(type, new Set()); this.listeners.get(type)!.add(fn); }
    removeEventListener(type: string, fn: () => void) { this.listeners.get(type)?.delete(fn); }
    remove() { this.updating = true; }
    emit(type: string) { for (const fn of [...(this.listeners.get(type) ?? [])]) fn(); }
  }
  for (const outcome of ['held', 'residual', 'unreadable', 'error', 'abort', 'stale', 'empty'] as const) {
    const buffer = new BufferControl();
    const generation = hls.generation;
    const charge = hls.charge(buffer, generation, 10, 'media');
    const removal = hls.remove(buffer, 0, Number.MAX_SAFE_INTEGER, generation) as Promise<void>;
    const settled = outcome === 'empty' ? removal : assert.rejects(removal);
    await Promise.resolve();
    assert.equal(hls.retained.has(charge), true, 'charge stays owned before updateend');
    if (outcome !== 'held') buffer.updating = false;
    if (outcome === 'unreadable') buffer.unreadable = true;
    if (outcome === 'empty' || outcome === 'stale') buffer.residual = 0;
    if (outcome === 'stale') hls.nextGeneration();
    buffer.emit(outcome === 'error' || outcome === 'abort' ? outcome : 'updateend');
    await settled;
    assert.equal(hls.retained.has(charge), outcome !== 'empty', outcome);
    if (outcome === 'empty') {
      const again = hls.remove(buffer, 0, Number.MAX_SAFE_INTEGER, hls.generation) as Promise<void>;
      await Promise.resolve(); buffer.updating = false; buffer.emit('updateend'); await again;
      assert.equal(hls.retained.has(charge), false, 'the captured charge was refunded once');
    }
  }
});

test('two current 80 MB tracks refuse without appending the second or waiting forever', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'v.m4s', segment({pts: 1, duration: 6, bytes: 80_000_000}));
  server.routes.set(base + 'a0.m4s', segment({pts: 1, duration: 6, bytes: 80_000_000}));
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0);
  player.currentTime = 1;
  await until(() => failures.length > 0, 15000);
  assert.equal(failures[0]!.refused, 'unsupported');
  assert.equal(player.source.buffers[0].appended.filter((value: {duration?: number}) => value.duration === 6).length, 1);
  assert.equal(player.source.buffers[1].appended.filter((value: {duration?: number}) => value.duration === 6).length, 0);
  assert.equal(server.log.filter(url => url === base + 'a0.m4s').length, 1);
  assert.ok((source as any).retainedBytes() <= 128 * 1024 * 1024);
});

test('candidate staging counts both real old tracks before changing their audio buffer', async () => {
  audioSwitchRoutes();
  server.routes.set(base + 'v.m4s', segment({pts: 1, duration: 6, bytes: 80_000_000}));
  server.routes.set(base + 'a0.m4s', segment({pts: 1, duration: 6, bytes: 36_000_000}));
  server.routes.set(base + 'a1.m4s', segment({pts: 1, duration: 6, bytes: 20_000_000}));
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0); player.currentTime = 1;
  await until(() => player.source.buffers[1].spans.length > 0, 15000);
  await (source as any).sealCohorts((source as any).generation);
  const before = player.source.buffers[1].spans.map((span: number[]) => [...span]);
  let mutations = 0;
  const operation = source.selectAudio(1, {current: () => true, sourceCurrent: () => true, willMutate: () => { mutations++; }});
  await assert.rejects(operation.done, (error: SourceError) => error.refused === 'unsupported');
  assert.equal(mutations, 0);
  assert.deepEqual(player.source.buffers[1].spans, before);
  assert.equal(failures[0]?.refused, 'unsupported');
  assert.ok((source as any).retainedBytes() <= 128 * 1024 * 1024);
});

/** Release only the stand-in's consumed padding; actual XHR/append byte lengths remain real. */
function discardConsumedPadding(buffer: any): void {
  const clean = () => { for (const value of buffer.appended) delete value.pad; };
  clean(); buffer.addEventListener('updateend', clean);
}

function observeRetainedCohortRemovals(hls: any, player: any, buffer: any) {
  const removed = new Set<unknown>();
  let removals = 0;
  const nativeRemove = buffer.remove.bind(buffer);
  buffer.remove = (from: number, to: number) => {
    if (to === Number.MAX_SAFE_INTEGER) {
      assert.equal(from, 0);
      assert.equal(hls.target, undefined); assert.equal(hls.arrival, undefined);
      assert.ok(buffer.spans.every(([, end]: number[]) => end <= player.currentTime), 'no current or arrival interval is evicted');
      const cohort = hls.cohorts.get(buffer);
      assert.ok(cohort, 'pressure retirement owns one sealed whole-buffer cohort');
      for (const entry of cohort.entries) {
        assert.ok(hls.retained.has(entry), 'charge is still held before native success');
        assert.equal(removed.has(entry), false, 'each original charge retires once');
        removed.add(entry);
      }
      removals++;
    }
    nativeRemove(from, to);
  };
  return () => removals;
}

test('the actual pump continues beyond 128 MiB through repeated 36 MB and 80 MB cohorts', async () => {
  for (const bytes of [36_000_000, 80_000_000]) {
    resetServer(); resetWorld(); film(9, bytes);
    const {source, player, failures} = start(base + 'index.m3u8');
    const hls = source as any;
    let peak = 0;
    const charge = hls.charge.bind(hls);
    hls.charge = (...args: any[]) => { const entry = charge(...args); peak = Math.max(peak, hls.retainedBytes()); assert.ok(peak <= 128 * 1024 * 1024); return entry; };
    await source.open(0);
    player.currentTime = source.elementTime(0); source.settled();
    const buffer = player.source.buffers[0];
    discardConsumedPadding(buffer);
    const removals = observeRetainedCohortRemovals(hls, player, buffer);
    for (let n = 0; n < 9; n++) {
      await until(() => buffer.appended.filter((value: any) => !value.init).length > n || failures.length > 0, 15000);
      assert.deepEqual(failures, []);
      const media = buffer.appended.filter((value: any) => !value.init);
      assert.equal(media[n].pts, 1 + n * 6, 'every supported source segment appends once and in order');
      player.advance(1 + (n + 1) * 6 - player.currentTime);
      assert.ok(hls.retainedBytes() <= 128 * 1024 * 1024);
    }
    assert.ok(bytes * 9 > 128 * 1024 * 1024);
    assert.ok(removals() >= 2, 'played cohorts are reclaimed before further admission');
    assert.ok([...hls.retained].some((entry: any) => entry.kind === 'init'), 'media retirement preserves initialization dependency charges');
    assert.deepEqual(failures, []);
    source.close(); assert.equal(hls.retainedBytes(), 0); assert.equal(hls.mediaBusy, false);
  }
});

function repeatedTwoTrackRoutes(count: number, audioDuration = 6) {
  audioSwitchRoutes();
  const playlist = (prefix: string, duration: number) => {
    let text = `#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MAP:URI="${prefix}-init.mp4"\n`;
    for (let n = 0; n < count * 6 / duration; n++) {
      text += `#EXTINF:${duration},\n${prefix}-${n}.m4s\n`;
      server.routes.set(base + `${prefix}-${n}.m4s`, segment({pts: 1 + n * duration, duration, bytes: prefix === 'v' ? 80_000_000 : 36_000_000}));
    }
    return text + '#EXT-X-ENDLIST\n';
  };
  server.routes.set(base + 'video.m3u8', playlist('v', 6));
  server.routes.set(base + 'a0.m3u8', playlist('a0', audioDuration));
}

test('two-track cohorts retire independently at their actual native frontiers', async () => {
  for (const duration of [6, 3]) {
    resetServer(); resetWorld(); repeatedTwoTrackRoutes(4, duration);
    const {source, player, failures} = start(base + 'index.m3u8');
    await source.open(0); player.currentTime = source.elementTime(0); source.settled();
    const hls = source as any, buffers = player.source.buffers;
    for (const buffer of buffers) discardConsumedPadding(buffer);
    const removed = buffers.map((buffer: any) => observeRetainedCohortRemovals(hls, player, buffer));
    for (let frame = 0; frame < 24; frame += duration) {
      await until(() => buffers.every((buffer: any) => buffer.spans.some(([start, end]: number[]) => start <= player.currentTime && end > player.currentTime)) || failures.length > 0, 15000);
      assert.deepEqual(failures, []);
      player.advance(duration);
      assert.ok(hls.retainedBytes() <= 128 * 1024 * 1024, 'the aggregate includes both real tracks');
    }
    assert.ok(removed.every((count: () => number) => count() >= 2));
    assert.deepEqual(failures, []); source.close();
  }
});

async function stopTestPump(hls: any): Promise<void> {
  hls.audioHold = {};
  await Promise.allSettled([...hls.pumpRuns.values()]);
  hls.audioHold = undefined;
}

test('a sealed unknown/overlapping successor addition is never adopted for cohort retirement', async () => {
  film(1);
  const {source, player} = start(base + 'index.m3u8');
  await source.open(0); await stopTestPump(source as any); source.settled();
  const hls = source as any, buffer = player.source.buffers[0];
  await hls.sealCohorts(hls.generation);
  const original = hls.cohorts.get(buffer); assert.ok(original);
  const data = new TextEncoder().encode(JSON.stringify({pts: 0, duration: 3}));
  hls.charge(buffer, hls.generation, data.byteLength, 'media');
  await hls.append(buffer, data.buffer, hls.generation);
  const before = hls.retainedBytes(); let removed = 0;
  const remove = buffer.remove.bind(buffer); buffer.remove = (...args: any[]) => { removed++; remove(...args); };
  player.currentTime = 100;
  await hls.retirePlayedCohorts(hls.generation);
  assert.equal(removed, 0); assert.equal(hls.retainedBytes(), before);
  assert.equal(hls.cohorts.get(buffer), original, 'the original sealed set does not adopt the late addition');
});

test('cohort retirement errors, unreadable/residual results and stale updateend refund zero', async () => {
  for (const outcome of ['error', 'abort', 'residual', 'unreadable', 'stale'] as const) {
    resetServer(); resetWorld(); film(1);
    const {source, player} = start(base + 'index.m3u8');
    await source.open(0); await stopTestPump(source as any); source.settled();
    const hls = source as any, buffer = player.source.buffers[0];
    await hls.sealCohorts(hls.generation); player.currentTime = 7;
    const before = hls.retainedBytes();
    buffer.remove = () => { buffer.updating = true; };
    const removal = hls.retirePlayedCohorts(hls.generation) as Promise<void>;
    const rejected = assert.rejects(removal);
    await Promise.resolve(); await Promise.resolve();
    assert.equal(buffer.updating, true);
    if (outcome === 'stale') { hls.nextGeneration(); hls.charge(buffer, hls.generation, 123, 'media'); }
    buffer.updating = false;
    if (outcome === 'unreadable') Object.defineProperty(buffer, 'buffered', {get() {throw new Error('unreadable');}});
    else if (outcome !== 'residual') buffer.spans = [];
    buffer.emit(outcome === 'error' || outcome === 'abort' ? outcome : 'updateend');
    await rejected;
    assert.equal(hls.retainedBytes(), before + (outcome === 'stale' ? 123 : 0));
    source.close();
  }
});

test('seek during cohort clear keeps the old updateend from owning the arrival generation', async () => {
  film(1);
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0); await stopTestPump(source as any); source.settled();
  const hls = source as any, buffer = player.source.buffers[0], oldGeneration = hls.generation;
  await hls.sealCohorts(oldGeneration); player.currentTime = 7;
  const originalEntries = [...hls.retained].filter((entry: any) => entry.kind === 'media');
  const nativeRemove = buffer.remove.bind(buffer);
  buffer.remove = () => { buffer.updating = true; };
  const retiring = hls.retirePlayedCohorts(oldGeneration) as Promise<void>;
  const rejected = assert.rejects(retiring);
  await Promise.resolve(); await Promise.resolve();
  assert.equal(buffer.updating, true);
  player.currentTime = source.seek(0);
  assert.ok(hls.generation > oldGeneration, 'even an in-buffer seek owns a new operation when a cohort clear has started');
  assert.ok(originalEntries.every(entry => hls.retained.has(entry)), 'old charges are sealed until a current verified removal');
  buffer.remove = nativeRemove; buffer.updating = false; buffer.spans = []; buffer.emit('updateend');
  await rejected;
  await until(() => server.log.filter(url => url === base + 's0.m4s').length >= 2 && buffer.spans.some(([from, to]: number[]) => from <= 1 && to > 1));
  const media = [...hls.retained].filter((entry: any) => entry.kind === 'media');
  assert.ok(media.length > 0 && media.every((entry: any) => entry.generation === hls.generation), 'arrival media keeps its own charge');
  assert.deepEqual(failures, []);
});

test('other-buffer retirement cannot unfreeze a video cohort during changing-size admission', async () => {
  repeatedTwoTrackRoutes(8, 3);
  for (let n = 1; n < 8; n++) server.routes.set(base + `v-${n}.m4s`, segment({pts: 1 + n * 6, duration: 6, bytes: 36_000_000}));
  const {source, player, failures} = start(base + 'index.m3u8');
  await source.open(0); player.currentTime = source.elementTime(0);
  const hls = source as any, video = player.source.buffers[0], audio = player.source.buffers[1];
  // Left alone, the pump takes audio's second segment first (it enters the window first), and
  // that overflow seals both buffers before video's download can start. Hold the pump and run
  // the real admissions in the order this race needs; the pump resumes for playback below.
  await stopTestPump(hls); source.settled();
  const [videoTrack, audioTrack] = hls.tracks;
  if (!audio.spans.length) await hls.feed(audioTrack, segmentNumbered(audioTrack.playlist, audioTrack.next), hls.generation);
  for (const buffer of [video, audio]) discardConsumedPadding(buffer);
  const removals = [video, audio].map(buffer => observeRetainedCohortRemovals(hls, player, buffer));
  const get = hls.getMedia.bind(hls);
  let release!: () => void, downloaded = false;
  const held = new Promise<void>(resolve => { release = resolve; });
  let first = true;
  hls.getMedia = async (url: string, signal?: AbortSignal) => {
    const lease = await get(url, signal);
    if (first && url === base + 'v-1.m4s') { first = false; downloaded = true; await held; }
    return lease;
  };
  assert.ok(audio.spans.length > 0, 'audio\'s first segment is in');
  assert.equal(hls.cohorts.size, 0, 'nothing is sealed yet');
  player.advance(1); // video36 is now inside measured ahead, audio36 is still current.
  const admission = hls.feed(videoTrack, segmentNumbered(videoTrack.playlist, videoTrack.next), hls.generation);
  await until(() => downloaded, 15000);
  player.advance(2); // audio reaches4 while video80 remains current until7.
  release();
  assert.equal(await admission, 'deferred', 'freeing audio\'s bytes does not unseal video');
  hls.startPump(hls.generation);
  await until(() => hls.cohorts.has(video) && audio.spans.some(([from, to]: number[]) => from <= 4 && to > 4) || failures.length > 0, 15000);
  assert.deepEqual(failures, []);
  assert.equal(video.appended.filter((value: any) => !value.init).length, 1, 'the downloaded candidate is discarded while its own buffer remains sealed');
  assert.equal(hls.cohorts.get(video).entries.filter((entry: any) => entry.kind === 'media').length, 1);
  for (let step = 0; step < 14; step++) {
    player.advance(3);
    await until(() => [video, audio].every(buffer => buffer.spans.some(([from, to]: number[]) => from <= player.currentTime && to > player.currentTime)) || failures.length > 0, 15000);
    assert.deepEqual(failures, []);
    assert.ok(hls.retainedBytes() <= 128 * 1024 * 1024);
  }
  assert.ok(removals.every(count => count() >= 2));
  assert.ok(video.appended.filter((value: any) => !value.init).length >= 4);
  assert.ok(video.appended.reduce((bytes: number, value: any) => bytes + (value.bytes ?? 0), 0) > 128 * 1024 * 1024);
});

async function cohortGetterFixture() {
  film(1);
  const run = start(base + 'index.m3u8');
  await run.source.open(0); await stopTestPump(run.source as any); run.source.settled();
  run.player.currentTime = 7;
  return {...run, hls: run.source as any, buffer: run.player.source.buffers[0]};
}

test('native range getters cannot install a stale cohort after seek, close or replacement', async () => {
  for (const action of ['seek', 'close', 'replacement'] as const) {
    resetServer(); resetWorld();
    const {source, player, hls, buffer} = await cohortGetterFixture();
    const ranges = buffer.buffered;
    let armed = true, successor: unknown;
    Object.defineProperty(buffer, 'buffered', {configurable: true, get() {
      if (armed) {
        armed = false;
        if (action === 'seek') { player.currentTime = source.seek(0); source.settled(); }
        if (action === 'close') source.close();
        if (action === 'replacement') { hls.nextGeneration(); successor = hls.charge(buffer, hls.generation, 123, 'media'); }
      }
      return ranges;
    }});
    await hls.sealCohorts(hls.generation).catch(() => {});
    assert.equal(hls.cohorts.has(buffer), false, action);
    if (successor) assert.ok(hls.retained.has(successor));
    source.close();
  }
});

test('dispatch reproves original ownership after every native frontier getter', async () => {
  for (const port of ['currentTime', 'buffered', 'start', 'end', 'updating'] as const) {
    for (const action of ['seek', 'close', 'replacement', 'same-buffer-track'] as const) {
      resetServer(); resetWorld();
      const {source, player, hls, buffer} = await cohortGetterFixture();
      await hls.sealCohorts(hls.generation);
      const ranges = buffer.buffered, clock = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(player), 'currentTime')!;
      let armed = true, removes = 0, refunds = 0, successor: unknown;
      const removeCharge = hls.retained.delete.bind(hls.retained);
      hls.retained.delete = (entry: unknown) => { refunds++; return removeCharge(entry); };
      const retire = () => {
        if (!armed) return;
        armed = false;
        if (action === 'seek') { player.currentTime = source.seek(0); source.settled(); }
        if (action === 'close') source.close();
        if (action === 'replacement') { hls.nextGeneration(); successor = hls.charge(buffer, hls.generation, 123, 'media'); }
        if (action === 'same-buffer-track') hls.tracks = hls.tracks.map((track: any) => ({...track}));
      };
      buffer.remove = () => { removes++; };
      if (port === 'currentTime') Object.defineProperty(player, 'currentTime', {configurable: true, get() {retire(); return 7;}, set(value) {clock.set!.call(player, value);}});
      else if (port === 'updating') Object.defineProperty(buffer, 'updating', {configurable: true, get() {retire(); return false;}});
      else Object.defineProperty(buffer, 'buffered', {configurable: true, get() {
        if (port === 'buffered') retire();
        return {length: ranges.length, start(index: number) {if (port === 'start') retire(); return ranges.start(index);}, end(index: number) {if (port === 'end') retire(); return ranges.end(index);}};
      }});
      await hls.retirePlayedCohorts(hls.generation).catch(() => {});
      assert.equal(removes, 0, `${port}/${action}`); assert.equal(refunds, 0, `${port}/${action}`);
      if (successor) assert.ok(hls.retained.has(successor));
      source.close();
    }
  }
});

test('completion getters and nonfinite clocks cannot refund stale cohort tokens', async () => {
  for (const outcome of ['clock-replacement', 'empty-getter-replacement', 'empty-getter-close', 'nan', 'infinity', 'negative-infinity'] as const) {
    resetServer(); resetWorld();
    const {source, player, hls, buffer} = await cohortGetterFixture();
    await hls.sealCohorts(hls.generation);
    const original = [...hls.retained], before = hls.retainedBytes();
    buffer.remove = () => { buffer.updating = true; };
    const removal = hls.retirePlayedCohorts(hls.generation) as Promise<void>;
    const rejected = assert.rejects(removal);
    await Promise.resolve(); await Promise.resolve();
    let successor: unknown, refunds = 0;
    const removeCharge = hls.retained.delete.bind(hls.retained);
    hls.retained.delete = (entry: unknown) => { refunds++; return removeCharge(entry); };
    const replace = () => { hls.nextGeneration(); successor = hls.charge(buffer, hls.generation, 123, 'media'); };
    if (outcome === 'clock-replacement') Object.defineProperty(player, 'currentTime', {configurable: true, get() {replace(); return 7;}});
    if (outcome === 'nan' || outcome === 'infinity' || outcome === 'negative-infinity') Object.defineProperty(player, 'currentTime', {configurable: true, get() {return outcome === 'nan' ? NaN : outcome === 'infinity' ? Infinity : -Infinity;}});
    buffer.updating = false; buffer.spans = [];
    let armed = true;
    Object.defineProperty(buffer, 'buffered', {configurable: true, get() {
      if (armed) {
        armed = false;
        if (outcome === 'empty-getter-replacement') replace();
        if (outcome === 'empty-getter-close') source.close();
      }
      return {length: 0};
    }});
    buffer.emit('updateend'); await rejected;
    assert.equal(refunds, 0, outcome);
    if (successor) assert.ok(hls.retained.has(successor), outcome);
    if (outcome !== 'empty-getter-close') { assert.ok(original.every(entry => hls.retained.has(entry))); assert.equal(hls.retainedBytes(), before + (successor ? 123 : 0)); }
    source.close();
  }
});

test('sealing refuses charge coalescing and dependency changes during native reads', async () => {
  for (const action of ['coalesced-media', 'init', 'staging', 'same-buffer-track'] as const) {
    resetServer(); resetWorld();
    const {source, hls, buffer} = await cohortGetterFixture();
    const ranges = buffer.buffered;
    let armed = true, changed: any;
    Object.defineProperty(buffer, 'buffered', {configurable: true, get() {
      if (armed) {
        armed = false;
        if (action === 'same-buffer-track') hls.tracks = hls.tracks.map((track: any) => ({...track}));
        else changed = hls.charge(buffer, hls.generation, 123, action === 'coalesced-media' ? 'media' : action);
      }
      return ranges;
    }});
    await hls.sealCohorts(hls.generation);
    assert.equal(hls.cohorts.has(buffer), false, action);
    if (changed) assert.ok(hls.retained.has(changed));
    source.close();
  }
});

test('the last empty-result getter cannot lend completion a successor or changed dependency set', async () => {
  for (const port of ['currentTime', 'buffered', 'length', 'updating'] as const) {
    for (const action of ['seek', 'close', 'replacement', 'same-buffer-track', 'init', 'staging'] as const) {
      resetServer(); resetWorld();
      const {source, player, hls, buffer} = await cohortGetterFixture();
      await hls.sealCohorts(hls.generation);
      const original = [...hls.retained];
      buffer.remove = () => { buffer.updating = true; };
      const removal = hls.retirePlayedCohorts(hls.generation) as Promise<void>;
      const rejected = assert.rejects(removal);
      await Promise.resolve(); await Promise.resolve();
      assert.equal(buffer.updating, true);
      buffer.updating = false; buffer.spans = [];
      let armed = true, refunds = 0, successor: unknown;
      const removeCharge = hls.retained.delete.bind(hls.retained);
      hls.retained.delete = (entry: unknown) => { refunds++; return removeCharge(entry); };
      const retire = () => {
        if (!armed) return;
        armed = false;
        if (action === 'seek') { player.currentTime = source.seek(0); source.settled(); }
        if (action === 'close') source.close();
        if (action === 'replacement') { hls.nextGeneration(); successor = hls.charge(buffer, hls.generation, 123, 'media'); }
        if (action === 'same-buffer-track') hls.tracks = hls.tracks.map((track: any) => ({...track}));
        if (action === 'init' || action === 'staging') successor = hls.charge(buffer, hls.generation, 123, action);
      };
      const clock = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(player), 'currentTime')!;
      if (port === 'currentTime') Object.defineProperty(player, 'currentTime', {configurable: true, get() {retire(); return 7;}, set(value) {clock.set!.call(player, value);}});
      if (port === 'updating') Object.defineProperty(buffer, 'updating', {configurable: true, get() {retire(); return false;}});
      Object.defineProperty(buffer, 'buffered', {configurable: true, get() {
        if (port === 'buffered') retire();
        return {get length() {if (port === 'length') retire(); return 0;}};
      }});
      buffer.emit('updateend'); await rejected;
      assert.equal(refunds, 0, `${port}/${action}`);
      if (successor) assert.ok(hls.retained.has(successor));
      if (action !== 'close') assert.ok(original.every(entry => hls.retained.has(entry)));
      source.close();
    }
  }
});


test('the actual pump refuses unprovable sealed media when required coverage drains', async () => {
  for (const drift of ['range', 'range-gap', 'unreadable', 'media', 'init', 'staging'] as const) {
    resetServer(); resetWorld(); film(3);
    const {source, player, failures} = start(base + 'index.m3u8');
    await source.open(0); await stopTestPump(source as any); source.settled();
    const hls = source as any, buffer = player.source.buffers[0];
    player.currentTime = 1;
    await hls.sealCohorts(hls.generation);
    const cohort = hls.cohorts.get(buffer), original = [...hls.retained];
    assert.ok(cohort);
    let refunds = 0, removes = 0;
    const removeCharge = hls.retained.delete.bind(hls.retained);
    hls.retained.delete = (entry: unknown) => { refunds++; return removeCharge(entry); };
    buffer.remove = () => { removes++; };
    if (drift === 'range') buffer.spans = [[cohort.ranges[0][0], cohort.frontier - 0.025]];
    else if (drift === 'range-gap') buffer.spans = [[player.currentTime + 0.025, cohort.frontier]];
    else if (drift === 'unreadable') Object.defineProperty(buffer, 'buffered', {get() {throw new Error('native ranges unavailable');}});
    else hls.charge(buffer, hls.generation, 123, drift);
    const appended = buffer.appended.length;
    if (drift !== 'range-gap') player.advance(cohort.frontier - player.currentTime);
    assert.equal(player.paused, true);
    hls.startPump(hls.generation);
    await until(() => failures.length > 0);
    assert.equal(failures.length, 1, drift);
    assert.equal(failures[0].refused, drift === 'unreadable' ? '' : 'unsupported', drift);
    assert.equal(buffer.appended.length, appended, drift); assert.equal(removes, 0, drift); assert.equal(refunds, 0, drift);
    assert.ok(original.every(entry => hls.retained.has(entry)), drift);
    assert.equal(hls.cohorts.get(buffer), cohort, 'invalid proof is never unsealed or adopted');
    await Promise.all([...hls.pumpRuns.values()]);
    assert.equal(hls.pumpRuns.size, 0, 'terminal original demand leaves no idle pump');
    assert.equal(failures.length, 1);
    source.close();
  }
});


test('the actual pump waits through a valid cohort final milliseconds before retiring at its exact frontier', async () => {
  for (const paused of [true, false]) {
    for (const remaining of [0.01, 0.025, 0.049]) {
      resetServer(); resetWorld(); film(3, 80_000_000);
      const {source, player, failures} = start(base + 'index.m3u8');
      await source.open(0); await stopTestPump(source as any); source.settled();
      const hls = source as any, buffer = player.source.buffers[0];
      discardConsumedPadding(buffer);
      await hls.sealCohorts(hls.generation);
      const cohort = hls.cohorts.get(buffer), original = [...cohort.entries], appended = buffer.appended.length;
      let waited = 0;
      const waiting = hls.waitForCohort.bind(hls);
      hls.waitForCohort = (...args: any[]) => {const result = waiting(...args); waited++; return result;};
      const removed = observeRetainedCohortRemovals(hls, player, buffer);
      player.paused = paused; player.currentTime = cohort.frontier - remaining;
      hls.startPump(hls.generation);
      await until(() => waited >= 2 || failures.length > 0);
      assert.deepEqual(failures, []);
      assert.equal(removed(), 0); assert.equal(buffer.appended.length, appended);
      assert.ok(original.every((entry: any) => hls.retained.has(entry)));
      assert.equal(hls.cohorts.get(buffer), cohort);
      player.advance(remaining);
      await until(() => buffer.appended.length > appended || failures.length > 0, 15000);
      assert.deepEqual(failures, []);
      assert.equal(removed(), 1); assert.ok(original.every((entry: any) => !hls.retained.has(entry)));
      assert.ok(buffer.spans.some(([from, to]: number[]) => from <= cohort.frontier && to > cohort.frontier));
      source.close();
    }
  }
});
