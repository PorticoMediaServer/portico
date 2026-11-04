import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type Viewer} from '../src/index.ts';
import {routePath, type PostReadingSessionsResponse} from '../../contracts/src/index.ts';

const rev = 'a'.repeat(64);
const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const chapter = new TextEncoder().encode('<html xmlns="http://www.w3.org/1999/xhtml"><body>Original text.</body></html>');
const png = Uint8Array.from(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl1sAAAAASUVORK5CYII=', 'base64'));
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json', ETag: '"' + rev + '"'}});
type Call = {url: URL; init: RequestInit; headers: Headers};
function setup(format: 'pdf' | 'epub' | 'cbz' | 'cbr' | 'cb7' = 'pdf') {
  const manifest: PostReadingSessionsResponse = {itemId: 'book', sourceId: 'asset', sessionId: 'session', revision: rev, format, size: 131072, metadata: {title: 'Stored title'}};
  if (format === 'epub') { manifest.resources = [{path: 'EPUB/chapter.xhtml', mime: 'application/xhtml+xml', size: chapter.length}]; manifest.spine = ['EPUB/chapter.xhtml']; }
  if (format === 'cbz' || format === 'cbr' || format === 'cb7') manifest.pageNames = ['page2.png', 'page10.png'];
  const calls: Call[] = [];
  let hook: ((call: Call) => Promise<Response> | Response | undefined) | undefined;
  let current = true;
  const fetcher: typeof fetch = async (input, init = {}) => {
    const call = {url: new URL(String(input)), init, headers: new Headers(init.headers)}; calls.push(call);
    const answer = hook?.(call); if (answer !== undefined) return answer;
    const path = call.url.pathname;
    if (path === routePath('post_reading_sessions')) return json(manifest);
    if (path === routePath('delete_reading_sessions_sessionId', {sessionId: 'session'})) return new Response(null, {status: 204});
    if (path === routePath('get_reading_sessions_sessionId_progress', {sessionId: 'session'})) return json(init.method === 'PUT' ? {progress: null, saved: false} : {progress: null});
    if (path === routePath('get_reading_sessions_sessionId_assets', {sessionId: 'session'})) return new Response(chapter, {headers: {'Content-Type': 'application/xhtml+xml', 'Content-Length': String(chapter.length), ETag: '"' + rev + '"'}});
    if (path === routePath('get_reading_sessions_sessionId_pages_pageIndex', {sessionId: 'session', pageIndex: 1})) return new Response(png, {headers: {'Content-Type': 'image/png', 'Content-Length': String(png.length), ETag: '"' + rev + '"'}});
    if (path === routePath('get_reading_sessions_sessionId_source', {sessionId: 'session'})) {
      if (init.method === 'HEAD') return new Response(null, {headers: {'Content-Type': 'application/pdf', 'Content-Length': String(manifest.size), 'Accept-Ranges': 'bytes', ETag: '"' + rev + '"'}});
      const match = /^bytes=(\d+)-(\d+)$/.exec(call.headers.get('Range') ?? ''); assert.ok(match);
      const start = Number(match[1]), end = Number(match[2]);
      return new Response(Uint8Array.from({length: end - start + 1}, (_, i) => (start + i) % 251), {status: 206, headers: {'Content-Type': 'application/pdf', 'Content-Length': String(end - start + 1), 'Content-Range': `bytes ${start}-${end}/${manifest.size}`, ETag: '"' + rev + '"'}});
    }
    throw new Error('Unexpected generated operation');
  };
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'captured-token', fetcher);
  const client = api.reading({viewer, isCurrent: () => current});
  return {api, client, calls, manifest, hook(value: typeof hook) { hook = value; }, retire() { current = false; }};
}
const coded = (code: string) => (error: unknown) => error instanceof ReadingError && error.code === code;

test('session contents retains only real EPUB spine targets and bounded labels', async () => {
  const f = setup('epub');
  f.manifest.contents = [{title: 'Publisher chapter', section: 'EPUB/chapter.xhtml', fragment: 'opening', depth: 0}];
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  assert.deepEqual(session.manifest.contents?.map(entry => ({...entry})), f.manifest.contents);
  assert.ok(Object.isFrozen(session.manifest.contents));
  for (const change of [
    {section: 'EPUB/missing.xhtml'}, {section: '../private.xhtml'}, {title: '\nBad'}, {fragment: 'bad\n'}, {depth: 17},
  ]) {
    const bad = setup('epub'); bad.manifest.contents = [{title: 'Chapter', section: 'EPUB/chapter.xhtml', depth: 0, ...change}];
    await assert.rejects(bad.client.open({itemId: 'book', sourceId: 'asset'}));
  }
  const pdf = setup('pdf'); pdf.manifest.contents = [{title: 'Invented EPUB target', section: 'EPUB/chapter.xhtml', depth: 0}];
  await assert.rejects(pdf.client.open({itemId: 'book', sourceId: 'asset'}), coded('invalid_manifest'));
});

test('reading capture is lazy, freezes identity and uses generated paths with exact PDF proof', async () => {
  const f = setup(); assert.equal(f.calls.length, 0);
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  assert.equal(session.manifest.metadata.author, undefined);
  assert.ok(Object.isFrozen(session.manifest.metadata)); assert.ok(Object.isFrozen(session));
  await session.headPDF();
  const body = await session.readRange({start: 65536, endExclusive: 131072});
  assert.equal(body.length, 65536); assert.equal(body[0], 65536 % 251);
  for (const call of f.calls) {
    assert.equal(call.url.origin, 'http://127.0.0.1:39117'); assert.equal(call.url.searchParams.has('apiKey'), false);
    assert.equal(call.headers.get('Authorization'), 'Bearer captured-token');
    assert.equal(call.init.redirect, 'error'); assert.equal(call.init.credentials, 'omit');
    if (call.init.method !== 'POST') assert.equal(call.headers.get('If-Match'), '"' + rev + '"');
  }
  await assert.rejects(session.readRange({start: 0, endExclusive: 65537}), coded('invalid_range'));
  await session.close(); assert.equal(session.signal.aborted, true);
  assert.equal(f.calls.at(-1)!.init.method, 'DELETE');
  await assert.rejects(session.readRange({start: 0, endExclusive: 5}), coded('cancelled'));
  f.client.dispose();
});

test('200, changed ETag, wrong total and short ranges are rejected, never whole-file buffered', async t => {
  for (const mismatch of ['200', 'etag', 'total', 'short'] as const) await t.test(mismatch, async () => {
    const f = setup(), session = await f.client.open({itemId: 'book', sourceId: 'asset'});
    let cancelled = 0;
    f.hook(() => new Response(new ReadableStream<Uint8Array>({start(c) { c.enqueue(new Uint8Array(mismatch === 'short' ? 4 : 5)); c.close(); }, cancel() { cancelled++; }}), {status: mismatch === '200' ? 200 : 206, headers: {'Content-Type': 'application/pdf', 'Content-Length': '5', 'Content-Range': `bytes 0-4/${mismatch === 'total' ? 99 : 131072}`, ETag: '"' + (mismatch === 'etag' ? 'b'.repeat(64) : rev) + '"'}}));
    await assert.rejects(session.readRange({start: 0, endExclusive: 5}), error => error instanceof ReadingError);
    assert.equal(session.signal.aborted, true); assert.ok(session.signal.reason instanceof ReadingError); if (mismatch !== 'short') assert.equal(cancelled, 1);
    f.client.dispose();
  });
});

test('only two PDF extents are active; request abort cancels a stalled stream without ending the book', async () => {
  const f = setup(), session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  let cancelled = 0;
  f.hook(() => new Response(new ReadableStream<Uint8Array>({pull() { return new Promise(() => {}); }, cancel() { cancelled++; }}), {status: 206, headers: {'Content-Type': 'application/pdf', 'Content-Length': '5', 'Content-Range': 'bytes 0-4/131072', ETag: '"' + rev + '"'}}));
  const one = new AbortController(), two = new AbortController();
  const a = session.readRange({start: 0, endExclusive: 5}, one.signal), b = session.readRange({start: 0, endExclusive: 5}, two.signal);
  await assert.rejects(session.readRange({start: 0, endExclusive: 5}), coded('range_busy'));
  one.abort(); two.abort(); await Promise.all([assert.rejects(a, coded('cancelled')), assert.rejects(b, coded('cancelled'))]);
  assert.equal(cancelled, 2); assert.equal(session.signal.aborted, false);
  f.hook(undefined); assert.equal((await session.readRange({start: 0, endExclusive: 5})).length, 5);
  f.client.dispose();
});

test('authority retirement aborts a pending response and cancels its late body without new credentials', async () => {
  const f = setup(), session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  let release!: (r: Response) => void, cancelled = 0;
  f.hook(() => new Promise<Response>(resolve => { release = resolve; }));
  const pending = session.readRange({start: 0, endExclusive: 5});
  f.api.setAccessToken('replacement-token');
  await assert.rejects(pending, coded('cancelled')); assert.equal(session.signal.aborted, true);
  release(new Response(new ReadableStream({cancel() { cancelled++; }}))); await Promise.resolve(); await Promise.resolve();
  assert.equal(cancelled, 1); const count = f.calls.length;
  await assert.rejects(session.progress(), coded('cancelled')); assert.equal(f.calls.length, count);
  assert.ok(f.calls.every(call => call.headers.get('Authorization') === 'Bearer captured-token'));
});

test('EPUB reads only original enumerated bytes; CBZ exposes actual page names and bounded image responses', async () => {
  const epub = setup('epub'), book = await epub.client.open({itemId: 'book', sourceId: 'asset'});
  assert.deepEqual(await book.readAsset('EPUB/chapter.xhtml'), chapter);
  assert.equal(epub.calls.at(-1)!.url.searchParams.get('path'), 'EPUB/chapter.xhtml');
  const count = epub.calls.length;
  await assert.rejects(book.readAsset('../secret'), coded('invalid_asset'));
  await assert.rejects(book.readRange({start: 0, endExclusive: 5}), coded('invalid_format')); assert.equal(epub.calls.length, count);
  const comic = setup('cbz'), pages = await comic.client.open({itemId: 'book', sourceId: 'asset'});
  const page = await pages.page(1, {width: 2048});
  assert.equal(page.name, 'page10.png'); assert.equal(page.index, 1); assert.equal(page.mime, 'image/png'); assert.deepEqual(page.bytes, png);
  await assert.rejects(pages.page(0, {width: 31}), coded('invalid_page'));
  await assert.rejects(pages.page(2, {width: 320}), coded('invalid_page'));
  comic.hook(() => new Response(png, {headers: {'Content-Type': 'image/png', 'Content-Length': String((16 << 20) + 1), ETag: '"' + rev + '"'}}));
  await assert.rejects(pages.page(1, {width: 32}), coded('response_size'));
  epub.client.dispose(); comic.client.dispose();
});

test('nullable progress and paused history stay explicit; CAS conflict does not retire a valid source', async () => {
  const f = setup(), session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  const initial = await session.progress(); assert.equal(initial.progress, null); assert.deepEqual(Object.keys(initial), ['progress']);
  const paused = await session.saveProgress({expectedRevision: 0, location: {format: 'pdf', pageIndex: 0, progression: .1}}); assert.equal(paused.progress, null); assert.equal(paused.saved, false);
  f.hook(call => call.init.method === 'PUT' ? json({error: {code: 'revision_mismatch'}}, 412) : undefined);
  await assert.rejects(session.saveProgress({expectedRevision: 0, location: {format: 'pdf', pageIndex: 0, progression: .1}}), coded('revision_mismatch'));
  assert.equal(session.signal.aborted, false);
  assert.equal((await session.progress()).progress, null);
  f.retire(); const count = f.calls.length;
  await assert.rejects(session.progress(), coded('cancelled')); assert.equal(f.calls.length, count);
});

test('transport failures and unknown server errors never retain an origin or response text', async () => {
  const f = setup(), session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  f.hook(() => { throw new Error('https://private-book-origin/?private=value'); });
  await assert.rejects(session.readRange({start: 0, endExclusive: 5}), error => {
    assert.ok(error instanceof ReadingError); assert.equal(error.code, 'network_error'); assert.equal('cause' in error, false); return true;
  });
  f.hook(() => json({error: {code: 'private_unrecognized_value', message: 'https://private-book-origin/'}}, 503));
  await assert.rejects(session.progress(), coded('request_failed')); f.client.dispose();
});


test('continue uses canonical bounded rows and saved locations without preparing sessions', async () => {
  const f = setup();
  const row = {item: {id: 'book', sourceId: 'alternate', libraryId: 'library', title: 'Stored title', format: 'epub', inventoryAvailable: true}, progress: {sourceId: 'alternate', sourceRevision: rev, revision: 3, observedAt: 100, stale: true, location: {format: 'epub', cfi: 'epubcfi(/6/4!/4/2:0)', section: 'EPUB/chapter.xhtml', progression: .4}}};
  f.hook(call => call.url.pathname === routePath('get_reading_continue') ? json({items: [row], nextCursor: 'MTAwOjI6Mw'}) : undefined);
  const out = await f.client.continue({libraryId: 'library', limit: 1});
  assert.equal(out.items.length, 1); assert.equal(out.items[0].item.sourceId, 'alternate'); assert.equal(out.items[0].progress.stale, true); assert.equal(out.items[0].progress.revision, 3);
  assert.equal(Object.isFrozen(out.items[0].progress.location), true); assert.equal(f.calls.length, 1); assert.equal(f.calls[0].headers.get('Authorization'), 'Bearer captured-token');
  assert.equal(f.calls[0].url.searchParams.get('limit'), '1');
  await f.client.continue({cursor: out.nextCursor, limit: 1});
  assert.equal(f.calls[1].url.searchParams.get('cursor'), 'MTAwOjI6Mw');
  const before = f.calls.length;
  await assert.rejects(f.client.continue({limit: 101}), coded('invalid_request'));
  await assert.rejects(f.client.continue({cursor: '../source'}), coded('invalid_request')); assert.equal(f.calls.length, before);
  f.hook(() => json({items: [{...row, progress: {...row.progress, sourceId: 'different'}}]}));
  await assert.rejects(f.client.continue(), coded('invalid_progress'));
  f.hook(() => json({items: [row, row]})); await assert.rejects(f.client.continue(), coded('invalid_progress'));
  f.retire(); const retired = f.calls.length; await assert.rejects(f.client.continue(), coded('cancelled')); assert.equal(f.calls.length, retired);
});

test('factual sequential PDF is one backpressured200 with no range or complete-body buffer', async () => {
  const f = setup(); f.manifest.sequential = true; f.manifest.size = 8;
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  let pulls = 0;
  f.hook(call => {
    assert.equal(call.headers.get('If-Match'), '"' + rev + '"');
    assert.equal(call.headers.has('Range'), false);
    const headers = {'Content-Type': 'application/pdf', 'Content-Length': '8', 'Accept-Ranges': 'none', ETag: '"' + rev + '"'};
    if (call.init.method === 'HEAD') return new Response(null, {headers});
    return new Response(new ReadableStream<Uint8Array>({pull(c) { pulls++; c.enqueue(pulls === 1 ? Uint8Array.of(1, 2, 3) : Uint8Array.of(4, 5, 6, 7, 8)); if (pulls === 2) c.close(); }}, {highWaterMark: 0}), {headers});
  });
  await session.headPDF();
  const count = f.calls.length;
  await assert.rejects(session.readRange({start: 0, endExclusive: 5}), coded('invalid_format'));
  assert.equal(f.calls.length, count);
  const stream = await session.openSequential(); assert.equal(pulls, 0);
  await assert.rejects(session.openSequential(), coded('sequential_busy'));
  const reader = stream.getReader();
  assert.deepEqual((await reader.read()).value, Uint8Array.of(1, 2, 3)); assert.equal(pulls, 1);
  assert.deepEqual((await reader.read()).value, Uint8Array.of(4, 5, 6, 7, 8));
  assert.equal((await reader.read()).done, true); reader.releaseLock();
  assert.equal(session.signal.aborted, false);
  const ranged = setup(), rangedSession = await ranged.client.open({itemId: 'book', sourceId: 'asset'});
  await assert.rejects(rangedSession.openSequential(), coded('invalid_format'));
  const epub = setup('epub'); epub.manifest.sequential = true;
  await assert.rejects(epub.client.open({itemId: 'book', sourceId: 'asset'}), coded('invalid_manifest'));
  f.client.dispose(); ranged.client.dispose(); epub.client.dispose();
});

test('sequential request cancellation survives headers, cancels a stalled body and preserves the reader', async () => {
  const f = setup(); f.manifest.sequential = true; f.manifest.size = 5;
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  let cancelled = 0;
  const headers = {'Content-Type': 'application/pdf', 'Content-Length': '5', 'Accept-Ranges': 'none', ETag: '"' + rev + '"'};
  f.hook(() => new Response(new ReadableStream<Uint8Array>({pull() { return new Promise(() => {}); }, cancel() { cancelled++; }}, {highWaterMark: 0}), {headers}));
  const request = new AbortController(), stream = await session.openSequential(request.signal), reader = stream.getReader();
  const pending = reader.read(); request.abort();
  await assert.rejects(pending, coded('cancelled')); assert.equal(cancelled, 1); assert.equal(session.signal.aborted, false);
  const second = await session.openSequential(); await second.cancel(); assert.equal(cancelled, 2);
  const third = await session.openSequential(), held = third.getReader(), waiting = held.read();
  const before = f.calls.length; f.api.setAccessToken('replacement-token');
  await assert.rejects(waiting, coded('cancelled')); assert.equal(cancelled, 3); assert.equal(session.signal.aborted, true);
  await assert.rejects(session.openSequential(), coded('cancelled')); assert.equal(f.calls.length, before);
  assert.ok(f.calls.every(call => call.headers.get('Authorization') === 'Bearer captured-token'));
});

test('sequential206, changed revision, false length, excess bytes and oversized chunks retire without replay', async t => {
  for (const mismatch of ['206', 'etag', 'length', 'content-range', 'short', 'excess', 'chunk'] as const) await t.test(mismatch, async () => {
    const f = setup(); f.manifest.sequential = true; f.manifest.size = mismatch === 'chunk' ? (1 << 20) + 1 : 5;
    const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
    let cancelled = 0;
    const headers: Record<string, string> = {'Content-Type': 'application/pdf', 'Content-Length': String(mismatch === 'length' ? 6 : f.manifest.size), 'Accept-Ranges': 'none', ETag: '"' + (mismatch === 'etag' ? 'b'.repeat(64) : rev) + '"'};
    if (mismatch === 'content-range') headers['Content-Range'] = 'bytes 0-4/5';
    f.hook(() => new Response(new ReadableStream<Uint8Array>({start(c) { c.enqueue(new Uint8Array(mismatch === 'short' ? 4 : mismatch === 'excess' ? 6 : f.manifest.size)); if (mismatch === 'short') c.close(); }, cancel() { cancelled++; }}), {status: mismatch === '206' ? 206 : 200, headers}));
    const before = f.calls.length;
    if (['short', 'excess', 'chunk'].includes(mismatch)) {
      const reader = (await session.openSequential()).getReader();
      await assert.rejects(async () => { while (!(await reader.read()).done) { /* consume until exact EOF is verified */ } }, coded('response_size'));
    } else await assert.rejects(session.openSequential(), error => error instanceof ReadingError);
    assert.equal(session.signal.aborted, true); assert.equal(f.calls.length, before + 1);
    if (mismatch !== 'short') assert.equal(cancelled, 1);
    f.client.dispose();
  });
});

async function withoutStreamConstructor(work: () => Promise<void>) {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'ReadableStream');
  Object.defineProperty(globalThis, 'ReadableStream', {configurable: true, writable: true, value: undefined});
  try { await work(); assert.equal(typeof globalThis.ReadableStream, 'undefined'); }
  finally { if (descriptor) Object.defineProperty(globalThis, 'ReadableStream', descriptor); else Reflect.deleteProperty(globalThis, 'ReadableStream'); }
}

test('constructor-absent sequential scope pulls owned bytes with one reader and no global patch', async () => {
  const f = setup(); f.manifest.sequential = true; f.manifest.size = 8;
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  let pulls = 0;
  const original = Uint8Array.of(1, 2, 3);
  const response = new Response(new ReadableStream<Uint8Array>({pull(c) { pulls++; c.enqueue(pulls === 1 ? original : Uint8Array.of(4, 5, 6, 7, 8)); if (pulls === 2) c.close(); }}, {highWaterMark: 0}), {headers: {'Content-Type': 'application/pdf', 'Content-Length': '8', 'Accept-Ranges': 'none', ETag: '"' + rev + '"'}});
  f.hook(() => response);
  try { await withoutStreamConstructor(async () => {
    const stream = await session.openSequential(); assert.equal(pulls, 0); assert.equal('tee' in stream, false);
    const reader = stream.getReader();
    assert.throws(() => stream.getReader(), TypeError);
    await assert.rejects(stream.cancel(), TypeError);
    const first = reader.read();
    const duplicate = assert.rejects(reader.read(), coded('stream_busy'));
    assert.throws(() => reader.releaseLock(), TypeError);
    await duplicate;
    const value = (await first).value!; original[0] = 99;
    assert.deepEqual(value, Uint8Array.of(1, 2, 3)); assert.equal(pulls, 1);
    assert.deepEqual((await reader.read()).value, Uint8Array.of(4, 5, 6, 7, 8));
    assert.equal((await reader.read()).done, true); reader.releaseLock();
    await assert.rejects(reader.read(), TypeError);
    const ended = stream.getReader(); assert.equal((await ended.read()).done, true); ended.releaseLock();
    assert.equal(session.signal.aborted, false); assert.equal(f.calls.length, 2);
  }); } finally { f.client.dispose(); }
});

test('constructor-absent stream handles stalled child cancel, reader cancel and captured authority retirement', async () => {
  const f = setup(); f.manifest.sequential = true; f.manifest.size = 5;
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  let cancelled = 0;
  const started: Promise<void>[] = [];
  const replies = Array.from({length: 3}, () => {
    let entered!: () => void;
    started.push(new Promise<void>(resolve => { entered = resolve; }));
    return new Response(new ReadableStream<Uint8Array>({pull() { entered(); return new Promise(() => {}); }, cancel() { cancelled++; }}, {highWaterMark: 0}), {headers: {'Content-Type': 'application/pdf', 'Content-Length': '5', 'Accept-Ranges': 'none', ETag: '"' + rev + '"'}});
  });
  f.hook(() => replies.shift());
  try { await withoutStreamConstructor(async () => {
    const request = new AbortController(), stream = await session.openSequential(request.signal), reader = stream.getReader();
    const pending = reader.read(); await started[0]; request.abort();
    await assert.rejects(pending, coded('cancelled')); reader.releaseLock();
    assert.equal(cancelled, 1); assert.equal(session.signal.aborted, false);
    const second = (await session.openSequential()).getReader(), waiting = second.read();
    await started[1];
    await second.cancel(); assert.equal((await waiting).done, true); second.releaseLock();
    assert.equal(cancelled, 2); assert.equal(session.signal.aborted, false);
    const third = (await session.openSequential()).getReader(), retired = third.read();
    await started[2]; const before = f.calls.length; f.api.setAccessToken('replacement-token');
    await assert.rejects(retired, coded('cancelled')); third.releaseLock();
    assert.equal(cancelled, 3); assert.equal(session.signal.aborted, true);
    await assert.rejects(session.openSequential(), coded('cancelled')); assert.equal(f.calls.length, before);
    assert.ok(f.calls.every(call => call.headers.get('Authorization') === 'Bearer captured-token'));
  }); } finally { f.client.dispose(); }
});

test('constructor-absent stream rejects false EOF and excess bytes without automatic replay', async t => {
  for (const mismatch of ['short', 'excess', 'network'] as const) await t.test(mismatch, async () => {
    const f = setup(); f.manifest.sequential = true; f.manifest.size = 5;
    const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
    const response = new Response(new ReadableStream<Uint8Array>({pull(c) { if (mismatch === 'network') c.error(new Error('https://private-origin.invalid/secret')); else { c.enqueue(new Uint8Array(mismatch === 'short' ? 4 : 6)); c.close(); } }}, {highWaterMark: 0}), {headers: {'Content-Type': 'application/pdf', 'Content-Length': '5', 'Accept-Ranges': 'none', ETag: '"' + rev + '"'}});
    f.hook(() => response);
    try { await withoutStreamConstructor(async () => {
      const reader = (await session.openSequential()).getReader();
      await assert.rejects(async () => { while (!(await reader.read()).done) { /* exact EOF is part of the port */ } }, coded(mismatch === 'network' ? 'network_error' : 'response_size'));
      reader.releaseLock(); assert.equal(session.signal.aborted, mismatch !== 'network'); assert.equal(f.calls.length, 2);
    }); } finally { f.client.dispose(); }
  });
});

// Source vectors for the next ordinary CBR-containing generated cohort; UNRUN.
for (const format of ['cbr', 'cb7'] as const) test(`${format.toUpperCase()} keeps its real format and uses only the original session raster endpoint (RD-10: CB7 as CBR)`, async () => {
  const f = setup(format), session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  try {
    const page = await session.page(1, {width: 320});
    assert.equal(session.manifest.format, format);
    assert.equal(page.name, 'page10.png');
    assert.deepEqual(page.bytes, png);
    const count = f.calls.length;
    await assert.rejects(session.page(2, {width: 320}), coded('invalid_page'));
    await assert.rejects(session.readRange({start: 0, endExclusive: 10}), coded('invalid_format'));
    await assert.rejects(session.readAsset('page10.png'), coded('invalid_format'));
    assert.equal(f.calls.length, count, 'no archive, range or EPUB asset fallback');
    await session.saveProgress({expectedRevision: 0, location: {format, pageIndex: 1, progression: .5}});
    const sent = JSON.parse(String(f.calls.at(-1)!.init.body));
    assert.equal(sent.location.format, format);
    assert.equal(sent.location.pageIndex, 1);
    const after = f.calls.length;
    await assert.rejects(session.saveProgress({expectedRevision: 0, location: {format, pageIndex: 2, progression: 1}}), coded('invalid_location'));
    assert.equal(f.calls.length, after);
    f.retire();
    await assert.rejects(session.page(0, {width: 320}), coded('cancelled'));
    assert.equal(f.calls.length, after);
  } finally {f.client.dispose();}
});

test('SRV-A2: a Continue card names a page book\'s page and never an EPUB\'s package path', async () => {
  const {readingResumePage} = await import('../src/reading.ts');
  assert.equal(readingResumePage({location: {format: 'cbz', pageIndex: 11, progression: .5}}), 12);
  assert.equal(readingResumePage({location: {format: 'pdf', pageIndex: 0, progression: 0}}), 1);
  assert.equal(readingResumePage({location: {format: 'epub', progression: .4, section: 'OEBPS/chapter3.xhtml'} as never}), undefined);
  assert.equal(readingResumePage({stale: true, location: {format: 'cbz', pageIndex: 3, progression: .1}}), undefined, 'a stale place is not shown as current');
  assert.equal(readingResumePage({location: {format: 'cbz', pageIndex: -1, progression: 0}}), undefined);
});


// RD-8 / DECISIONS W-19: a profile's own sessions never refuse an open (the server closes its least
// recently used one); only the server-wide cap answers 503 `reading_sessions_busy` with Retry-After.
const busy = (retryAfter?: string) => new Response(JSON.stringify({error: {code: 'reading_sessions_busy', message: 'Too many reading sessions are open. Try again in a moment.', retry: 'same_request'}}),
  {status: 503, headers: {'Content-Type': 'application/json', ...(retryAfter === undefined ? {} : {'Retry-After': retryAfter})}});
const opens = (f: ReturnType<typeof setup>) => f.calls.filter(call => call.url.pathname === routePath('post_reading_sessions')).length;

/** Mock timers, plus the delays the code under test asks for, so a test ticks only once the
 * Retry-After wait is really armed (under load the open's await chain can be slower). */
function busyTimers(t: import('node:test').TestContext) {
  t.mock.timers.enable({apis: ['setTimeout']});
  const mocked = globalThis.setTimeout, delays: number[] = [];
  globalThis.setTimeout = ((fn: () => void, ms?: number, ...rest: unknown[]) => {delays.push(ms ?? 0); return (mocked as (...a: unknown[]) => unknown)(fn, ms, ...rest);}) as typeof setTimeout;
  return {
    async armed(ms: number) {for (let i = 0; i < 20000 && !delays.includes(ms); i++) await new Promise(setImmediate); assert.ok(delays.includes(ms), `a ${ms} ms wait was armed`);},
    restore() {globalThis.setTimeout = mocked;},
  };
}

test('RD-8: a busy server is waited out as Retry-After asks, then the open is tried once more and succeeds', async t => {
  const clock = busyTimers(t);
  try {
    const f = setup(); let refusals = 1;
    f.hook(call => call.url.pathname === routePath('post_reading_sessions') && refusals-- > 0 ? busy('2') : undefined);
    const opening = f.client.open({itemId: 'book', sourceId: 'asset'});
    await clock.armed(2000);
    assert.equal(opens(f), 1);
    t.mock.timers.tick(1900); for (let i = 0; i < 50; i++) await new Promise(setImmediate);
    assert.equal(opens(f), 1, 'not before Retry-After');
    t.mock.timers.tick(100);
    const session = await opening;
    assert.equal(opens(f), 2); assert.equal(session.manifest.sessionId, 'session');
  } finally {clock.restore();}
});

test('RD-8: still busy after the one wait, the open fails as reading_sessions_busy (503) carrying Retry-After, never looping', async t => {
  const clock = busyTimers(t);
  try {
    const f = setup();
    f.hook(call => call.url.pathname === routePath('post_reading_sessions') ? busy('3') : undefined);
    const settled = f.client.open({itemId: 'book', sourceId: 'asset'}).then(() => assert.fail('opened'), (error: unknown) => error);
    await clock.armed(3000);
    t.mock.timers.tick(3000);
    const error = await settled as ReadingError & {retryAfterSeconds?: number};
    assert.ok(coded('reading_sessions_busy')(error)); assert.equal(error.status, 503);
    assert.equal(error.retryAfterSeconds, 3, 'the reader can say when to try again');
    assert.equal(opens(f), 2, 'one wait, one more try');
  } finally {clock.restore();}
});

test('RD-8: the wait is capped and abortable, and a busy answer without Retry-After waits the short default', async t => {
  const clock = busyTimers(t);
  try {
    const capped = setup(); let once = 1;
    capped.hook(call => call.url.pathname === routePath('post_reading_sessions') && once-- > 0 ? busy('3600') : undefined);
    const long = capped.client.open({itemId: 'book', sourceId: 'asset'});
    await clock.armed(10_000); t.mock.timers.tick(10_000); await long; assert.equal(opens(capped), 2, 'an hour asked, ten seconds waited at most');
    const aborted = setup(), controller = new AbortController();
    aborted.hook(call => call.url.pathname === routePath('post_reading_sessions') ? busy('2') : undefined);
    const cancelled = aborted.client.open({itemId: 'book', sourceId: 'asset'}, controller.signal).then(() => 'opened', (error: unknown) => error instanceof ReadingError ? error.code : String(error));
    await clock.armed(2000);
    controller.abort(); assert.equal(await cancelled, 'cancelled'); assert.equal(opens(aborted), 1, 'leaving the reader stops the wait');
    const plain = setup(); let first = 1;
    plain.hook(call => call.url.pathname === routePath('post_reading_sessions') && first-- > 0 ? busy() : undefined);
    const quick = plain.client.open({itemId: 'book', sourceId: 'asset'});
    for (let i = 0; i < 20000 && opens(plain) < 1; i++) await new Promise(setImmediate);
    for (let i = 0; i < 20000 && opens(plain) < 2; i++) {t.mock.timers.tick(100); await new Promise(setImmediate);}
    await quick; assert.equal(opens(plain), 2);
  } finally {clock.restore();}
});

test('RD-8: a busy reading open presents in the catalogue\'s retry-later words, with its Retry-After', async () => {
  const {presentError} = await import('../src/presentation/errors.ts');
  const {defaultI18n} = await import('../../i18n/src/index.ts');
  const shown = presentError(new ReadingError('reading_sessions_busy', 503, 3));
  assert.equal(shown.category, 'busy'); assert.equal(shown.body, defaultI18n.t('error.busy'));
  assert.equal(shown.retryAfterSeconds, 3);
});

test('RD-5: an EPUB too large to read is refused at open as its own code (422, never waited out or retried)', async () => {
  const f = setup('epub');
  f.hook(call => call.url.pathname === routePath('post_reading_sessions') ? new Response(JSON.stringify({error: {code: 'reading_publication_too_large', message: 'This book is too large to open.', retry: 'never'}}), {status: 422, headers: {'Content-Type': 'application/json'}}) : undefined);
  await assert.rejects(f.client.open({itemId: 'book', sourceId: 'asset'}), (error: unknown) => coded('reading_publication_too_large')(error) && (error as ReadingError).status === 422);
  assert.equal(f.calls.filter(call => call.url.pathname === routePath('post_reading_sessions')).length, 1, 'one attempt only');
});
test('RD-5: an accepted EPUB session keeps the server\'s limits for the reader', async () => {
  const f = setup('epub');
  f.hook(call => call.url.pathname === routePath('post_reading_sessions') ? json({...f.manifest, limits: {memberBytes: 16777216, totalBytes: 268435456}}) : undefined);
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  assert.equal(session.manifest.limits?.memberBytes, 16777216); assert.equal(session.manifest.limits?.totalBytes, 268435456);
});

// RD-7 (#695): the server answers 410 reading_session_expired on every session route once it has
// ended a session (idle, evicted, or not this caller's). The client reopens the book once,
// transparently, and carries on; it never loops.
const expired = () => new Response(JSON.stringify({error: {code: 'reading_session_expired', message: 'This reading session has ended. Open the book again to continue.', retry: 'never'}}), {status: 410, headers: {'Content-Type': 'application/json'}});
const renewedPage = (f: ReturnType<typeof setup>, sessionId: string) => (call: Call) => {
  if (call.url.pathname === routePath('get_reading_sessions_sessionId_pages_pageIndex', {sessionId, pageIndex: 1}))
    return new Response(png, {headers: {'Content-Type': 'image/png', 'Content-Length': String(png.length), ETag: '"' + rev + '"'}});
  if (call.url.pathname === routePath('post_reading_sessions') && f.calls.filter(c => c.url.pathname === routePath('post_reading_sessions')).length > 1) return json({...f.manifest, sessionId});
  return undefined;
};
const posts = (f: ReturnType<typeof setup>) => f.calls.filter(call => call.url.pathname === routePath('post_reading_sessions')).length;

test('RD-7: mid-read, an ended session is reopened once, transparently, and the page that hit it is read under the new session', async () => {
  const f = setup('cbz');
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  const renewed = renewedPage(f, 'session-2');
  f.hook(call => call.url.pathname === routePath('get_reading_sessions_sessionId_pages_pageIndex', {sessionId: 'session', pageIndex: 1}) ? expired() : renewed(call));
  const page = await session.page(1, {width: 320});
  assert.equal(page.index, 1); assert.deepEqual(page.bytes, png);
  assert.equal(posts(f), 2, 'one reopen'); assert.equal(f.calls.at(-2)!.url.pathname, routePath('post_reading_sessions'));
  assert.deepEqual(JSON.parse(String(f.calls.at(-2)!.init.body)), {itemId: 'book', sourceId: 'asset'}, 'the same book and version');
  assert.equal(session.sessionId, 'session-2'); assert.equal(session.manifest.sessionId, 'session', 'the reader keeps its book; nothing remounts');
  assert.equal(session.signal.aborted, false);
  await session.page(1, {width: 320}); assert.equal(posts(f), 2, 'later reads use the new session without reopening');
});

test('RD-7: a second 410 straight after the reopen ends the session as reading_session_expired, never looping', async () => {
  const f = setup('cbz');
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  f.hook(call => call.url.pathname.includes('/pages/') ? expired() : call.url.pathname === routePath('post_reading_sessions') ? json({...f.manifest, sessionId: 'session-2'}) : undefined);
  await assert.rejects(session.page(1, {width: 320}), (error: unknown) => coded('reading_session_expired')(error) && (error as ReadingError).status === 410);
  assert.equal(posts(f), 2, 'exactly one reopen');
  assert.equal(session.signal.aborted, true); assert.ok(coded('reading_session_expired')(session.signal.reason), 'the reader learns why');
  await assert.rejects(session.page(1, {width: 320})); assert.equal(posts(f), 2, 'an ended session does not reopen again');
});

test('RD-7: a reopen that fails, or finds another revision, ends the session as expired; a sign-in failure stays itself', async () => {
  for (const [label, answer, code] of [
    ['busy', () => new Response(JSON.stringify({error: {code: 'reading_sessions_busy', message: 'Busy.', retry: 'same_request'}}), {status: 503, headers: {'Content-Type': 'application/json'}}), 'reading_session_expired'],
    ['another revision', (f: ReturnType<typeof setup>) => json({...f.manifest, sessionId: 'session-2', revision: 'b'.repeat(64)}), 'reading_session_expired'],
    ['signed out', () => new Response(JSON.stringify({error: {code: 'unauthorized', message: 'Sign in.', retry: 'never'}}), {status: 401, headers: {'Content-Type': 'application/json'}}), 'unauthorized'],
  ] as const) {
    const f = setup('cbz');
    const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
    f.hook(call => call.url.pathname.includes('/pages/') ? expired() : call.url.pathname === routePath('post_reading_sessions') ? answer(f) : call.url.pathname === routePath('delete_reading_sessions_sessionId', {sessionId: 'session-2'}) ? new Response(null, {status: 204}) : undefined);
    const error = await session.page(1, {width: 320}).then(() => assert.fail(label), (value: unknown) => value);
    assert.equal((error as ReadingError).code, code, label); assert.equal(posts(f), 2, label);
    assert.equal(session.signal.aborted, true, label);
  }
});

test('RD-7: requests that meet the ended session together share one reopen, and progress saves carry on under it', async () => {
  const f = setup('epub');
  const session = await f.client.open({itemId: 'book', sourceId: 'asset'});
  f.hook(call => {
    const path = call.url.pathname;
    if (path === routePath('get_reading_sessions_sessionId_progress', {sessionId: 'session'})) return expired();
    if (path === routePath('get_reading_sessions_sessionId_assets', {sessionId: 'session'})) return expired();
    if (path === routePath('post_reading_sessions')) return json({...f.manifest, sessionId: 'session-2'});
    if (path === routePath('get_reading_sessions_sessionId_progress', {sessionId: 'session-2'})) return json(call.init.method === 'PUT' ? {progress: null, saved: false} : {progress: null});
    if (path === routePath('get_reading_sessions_sessionId_assets', {sessionId: 'session-2'})) return new Response(chapter, {headers: {'Content-Type': 'application/xhtml+xml', 'Content-Length': String(chapter.length), ETag: '"' + rev + '"'}});
    return undefined;
  });
  const [progress, asset] = await Promise.all([session.progress(), session.readAsset('EPUB/chapter.xhtml')]);
  assert.equal(progress.progress, null); assert.equal(asset.length, chapter.length);
  assert.equal(posts(f), 2, 'one shared reopen'); assert.equal(session.sessionId, 'session-2');
  f.hook(call => call.url.pathname === routePath('delete_reading_sessions_sessionId', {sessionId: 'session-2'}) ? new Response(null, {status: 204}) : undefined);
  await session.close(); assert.equal(f.calls.at(-1)!.url.pathname, routePath('delete_reading_sessions_sessionId', {sessionId: 'session-2'}), 'close ends the session the server knows now');
});
