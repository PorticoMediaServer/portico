import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type Viewer} from '../src/index.ts';

// The reader against the rebuilt server: a book route says how to read a book, its bytes come
// from a media address, its place and read mark from the state route, its annotations from theirs.
const fingerprint = '0123456789abcdef';
const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const chapter = new TextEncoder().encode('<html xmlns="http://www.w3.org/1999/xhtml"><body>Original text.</body></html>');
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}});
type Call = {url: URL; init: RequestInit; body?: Record<string, unknown>};

function server(format: 'epub' | 'cbz' | 'pdf' = 'epub', saveSize?: number) {
  const calls: Call[] = [];
  let address = 'b.first', position = '', annotations: Record<string, unknown>[] = [];
  const book = () => ({itemId: 'itm_book', version: 'med_one', versions: [{id: 'med_one', format}], format, fingerprint, size: saveSize ?? (format === 'epub' ? 4096 : 131072),
    mediaUrl: '/v1/media/' + address, saveUrl: '/v1/media/' + address + '/source?save=1',
    ...(format === 'epub' ? {parts: [{path: 'OEBPS/chapter 1.xhtml', type: 'application/xhtml+xml', size: chapter.length}], spine: ['OEBPS/chapter 1.xhtml'],
      contents: [{title: 'Chapter One', part: 'OEBPS/chapter 1.xhtml', depth: 0}]} : {}),
    ...(format === 'cbz' ? {pages: ['p1.png', 'p2.png', 'p3.png', 'p4.png']} : {})});
  const fetcher: typeof fetch = async (input, init = {}) => {
    const url = new URL(String(input)), body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined;
    calls.push({url, init, body});
    const path = decodeURIComponent(url.pathname);
    if (path === '/v1/items/itm_film/book') return json({error: {code: 'not_found', message: 'This is not available.', retry: false}}, 404);
    if (path === '/v1/media/b.first/source' && saveSize) {
      let left = saveSize; const chunk = new Uint8Array(65536);
      return new Response(new ReadableStream({pull(controller) {if (!left) {controller.close();return;} const n = Math.min(left, chunk.length);controller.enqueue(chunk.subarray(0,n));left -= n;}}), {headers: {'Content-Disposition': 'attachment; filename=book.epub'}});
    }
    if (path === '/v1/items/itm_book/book') return json(book());
    if (path === '/v1/items/itm_book') return json({id: 'itm_book', title: 'A Book', state: {position, watched: false, updatedAt: '2026-11-11T00:00:00Z'}});
    if (path === '/v1/items/itm_book/state') { if (typeof body?.position === 'string') position = body.position; return json({position, watched: body?.watched === true}); }
    if (path === '/v1/items/itm_book/annotations' && (init.method ?? 'GET') === 'GET') return json({items: annotations});
    if (path.startsWith('/v1/items/itm_book/annotations/')) return init.method === 'DELETE' ? new Response(null, {status: 204}) : json({id: path.split('/').pop()});
    if (path === '/v1/media/b.first/parts/OEBPS/chapter 1.xhtml') return json({error: {code: 'not_found'}}, 404);
    if (path === '/v1/media/b.second/parts/OEBPS/chapter 1.xhtml') return new Response(chapter, {headers: {'Content-Type': 'application/xhtml+xml', 'Content-Length': String(chapter.length)}});
    throw new Error('unexpected request ' + init.method + ' ' + path);
  };
  const client = new HttpLocalApi('http://127.0.0.1:39117', 'token', fetcher).reading({viewer, isCurrent: () => true});
  return {client, calls, renew() { address = 'b.second'; }, set place(value: string) { position = value; }, get place() { return position; },
    set annotations(value: Record<string, unknown>[]) { annotations = value; }};
}

test('a title that is not a book is not found, and a book lists its versions with the one the server chose', async () => {
  const s = server();
  await assert.rejects(s.client.versions('itm_film'), (error: unknown) => error instanceof ReadingError && error.status === 404);
  assert.deepEqual(await s.client.versions('itm_book'), {chosenSourceId: 'med_one', versions: [{sourceId: 'med_one', format: 'epub', size: 4096, inventoryAvailable: true}]});
});

test('an EPUB opens from the book route, and a part whose address has ended is read under a new one', async () => {
  const s = server();
  const session = await s.client.open({itemId: 'itm_book', sourceId: 'med_one'});
  assert.equal(session.manifest.revision, fingerprint);
  assert.deepEqual(session.manifest.contents?.map(c => ({...c})), [{title: 'Chapter One', section: 'OEBPS/chapter 1.xhtml', depth: 0}]);
  s.renew();
  assert.deepEqual(await session.readAsset('OEBPS/chapter 1.xhtml'), chapter);
  assert.equal(s.calls.filter(call => call.url.pathname === '/v1/items/itm_book/book').length, 2, 'the book is asked for again once');
});

test('a place is saved through the state route with its file, resumes exactly in that file, and in a comic opens near it from another', async () => {
  const s = server();
  const session = await s.client.open({itemId: 'itm_book', sourceId: 'med_one'});
  const location = {format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', progression: 0.4};
  const saved = await session.saveProgress({expectedRevision: 0, location});
  assert.equal(saved.saved, true);
  assert.deepEqual(JSON.parse(s.place), {...location, fingerprint});
  const progress = await session.progress();
  assert.equal(progress.progress?.stale, false);
  assert.equal(progress.progress?.location.cfi, location.cfi);

  const comic = server('cbz');
  comic.place = JSON.stringify({format: 'cbz', pageIndex: 9, progression: 0.5, fingerprint: 'ffffffffffffffff'});
  const opened = await comic.client.open({itemId: 'itm_book', sourceId: 'med_one'});
  const near = await opened.progress();
  assert.equal(near.progress?.stale, true);
  assert.equal(near.progress?.location.pageIndex, 2, 'half way through four pages');
  await opened.saveProgress({expectedRevision: 0, location: {format: 'cbz', pageIndex: 3, progression: 1}});
  assert.equal(JSON.parse(comic.place).atEnd, true, 'the last page marks the book read');
});

test('annotations made in this file are shown, those from an earlier copy stay listed with their provenance, and a highlight is sent whole', async () => {
  const s = server();
  s.annotations = [
    {id: 'h1', kind: 'highlight', color: 'green', location: JSON.stringify({format: 'epub', cfi: 'epubcfi(/6/2!/4/2,/1:0,/1:5)', progression: 0.1}), madeOn: fingerprint,
      progress: 0.1, text: 'It was', createdAt: '2026-11-11T00:00:00Z', updatedAt: '2026-11-11T00:00:00Z'},
    {id: 'h2', kind: 'highlight', color: 'pink', location: JSON.stringify({format: 'epub', cfi: 'epubcfi(/6/4)', progression: 0.2}), madeOn: 'aaaaaaaaaaaaaaaa',
      progress: 0.2, text: 'elsewhere', createdAt: '2026-11-11T00:00:00Z', updatedAt: '2026-11-11T00:00:00Z'},
  ];
  const session = await s.client.open({itemId: 'itm_book', sourceId: 'med_one'});
  const page = await session.annotations();
  assert.deepEqual(page.entries.map(entry => [entry.id, entry.colour, entry.text]), [['h1', 'green', 'It was'], ['h2', 'pink', 'elsewhere']]);
  assert.equal(page.entries[1].earlierCopy, true);
  await assert.rejects(session.mutateAnnotation({...page.entries[1], operation: 'upsert', expectedRevision: page.revision}),
    (error: unknown) => error instanceof ReadingError && error.code === 'invalid_request');
  await session.mutateAnnotation({id: page.entries[1].id, operation: 'delete', expectedRevision: page.revision});
  assert.equal(s.calls.at(-1)?.init.method, 'DELETE');
  const long = 'word '.repeat(1500);
  await session.mutateAnnotation({operation: 'upsert', expectedRevision: page.revision, id: 'h3', kind: 'highlight', colour: 'yellow', text: long, note: '',
    location: {format: 'epub', cfi: 'epubcfi(/6/2!/4/2,/1:0,/1:9)', progression: 0.3}});
  const sent = s.calls.at(-1)!;
  assert.equal(sent.init.method, 'PUT');
  assert.equal(sent.url.pathname, '/v1/items/itm_book/annotations/h3');
  assert.deepEqual({...sent.body, location: undefined}, {kind: 'highlight', color: 'yellow', location: undefined, madeOn: fingerprint, progress: 0.3, text: long, note: ''});
});


test('saving an original above 64 MB streams each chunk to the platform without a file-size cap', async () => {
  const size = 65 * 1024 * 1024, s = server('epub', size);
  const session = await s.client.open({itemId: 'itm_book', sourceId: 'med_one'});
  await assert.rejects(session.saveFile('01234567-89ab-cdef-0123-456789abcdef'),
    (error: unknown) => error instanceof ReadingError && error.code === 'response_size');
  let written = 0;
  await session.saveFileTo('01234567-89ab-cdef-0123-456789abcdef', async chunk => {assert.ok(chunk.length <= 65536); written += chunk.length;});
  assert.equal(written, size);
});
