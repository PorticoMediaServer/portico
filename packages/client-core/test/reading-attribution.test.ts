import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ReadingError, type ReadingAuthor, type ReadingSeries, type Viewer} from '../src/index.ts';
import {routePath, type GetReadingContinueResponse, type GetReadingItemsResponse} from '../../contracts/src/index.ts';

const rev = 'a'.repeat(64);
const viewer: Viewer = {accountId: 'account', profileId: 'profile', serverId: 'server', authority: 'local', role: 'member'};
const json = (value: unknown) => new Response(JSON.stringify(value), {status: 200, headers: {'Content-Type': 'application/json'}});
const row = (over: Partial<GetReadingItemsResponse['items'][number]> = {}) => ({id: 'book', libraryId: 'library', title: 'Stored title', format: 'pdf', sourceId: 'asset', inventoryAvailable: true, ...over});
function setup(reply: (path: string) => unknown) {
  const calls: {url: URL; headers: Headers}[] = [];
  let current = true;
  const fetcher: typeof fetch = async (input, init = {}) => {
    const url = new URL(String(input)); calls.push({url, headers: new Headers(init.headers)}); return json(reply(url.pathname));
  };
  const client = new HttpLocalApi('http://127.0.0.1:39117', 'captured-token', fetcher).reading({viewer, isCurrent: () => current});
  return {client, calls, retire() { current = false; }};
}
const coded = (code: string) => (error: unknown) => error instanceof ReadingError && error.code === code;

test('list preserves closed accepted author/series facts and omits absent ones', async () => {
  const f = setup(() => ({items: [
    row({author: {value: 'Ann Author', source: 'owner'}, series: {id: 'series_1', name: 'Saga', number: 0, source: 'folder'}}),
    row({id: 'plain', sourceId: 'asset_2'}),
  ], nextCursor: 'MTAw'}));
  const out = await f.client.list({limit: 2});
  const attributed = out.items[0];
  const author: ReadingAuthor = attributed.author!;
  const series: ReadingSeries = attributed.series!;
  assert.equal(author.value, 'Ann Author'); assert.equal(author.source, 'owner');
  assert.equal(series.id, 'series_1'); assert.equal(series.name, 'Saga'); assert.equal(series.source, 'folder');
  assert.equal(Object.hasOwn(series, 'number'), true); assert.equal(series.number, 0);
  assert.ok(Object.isFrozen(author)); assert.ok(Object.isFrozen(series));
  const plain = out.items[1];
  assert.equal(Object.hasOwn(plain, 'author'), false); assert.equal(Object.hasOwn(plain, 'series'), false);
  assert.equal(out.nextCursor, 'MTAw');
});

test('list retains decimal series numbers and every accepted source verbatim', async () => {
  const sources = ['owner', 'file-metadata', 'file-name', 'folder'] as const;
  const f = setup(() => ({items: sources.map((source, i) => row({
    id: `book_${i}`, sourceId: `asset_${i}`,
    author: {value: `Author ${i}`, source},
    series: {id: `series_${i}`, name: `Saga ${i}`, number: 4.25 + i, source},
  }))}));
  const out = await f.client.list({limit: 4});
  out.items.forEach((item, i) => {
    assert.equal(item.author?.source, sources[i]); assert.equal(item.author?.value, `Author ${i}`);
    assert.equal(item.series?.source, sources[i]); assert.equal(item.series?.number, 4.25 + i);
  });
});

test('continue preserves the nested item attribution unchanged', async () => {
  const response: GetReadingContinueResponse = {items: [{
    item: row({format: 'epub', author: {value: 'Ann Author', source: 'file-name'}, series: {id: 'series_1', name: 'Saga', number: 2, source: 'owner'}}),
    progress: {sourceId: 'asset', sourceRevision: rev, revision: 3, observedAt: 100, stale: true, location: {format: 'epub', cfi: 'epubcfi(/6/4!/4/2:0)', section: 'EPUB/chapter.xhtml', progression: .4}},
  }], nextCursor: 'MTAwOjI6Mw'};
  const f = setup(path => path === routePath('get_reading_continue') ? response : undefined);
  const out = await f.client.continue({limit: 1});
  assert.equal(out.items[0].item.author?.value, 'Ann Author'); assert.equal(out.items[0].item.author?.source, 'file-name');
  assert.equal(out.items[0].item.series?.number, 2); assert.equal(out.items[0].item.series?.source, 'owner');
  assert.equal(out.nextCursor, 'MTAwOjI6Mw');
});

test('R-09b: an attribution with a member a newer server added is read', async () => {
  const f = setup(() => ({items: [{...row(), author: {value: 'Ann', source: 'owner', extra: 1}}]}));
  const out = await f.client.list({limit: 1});
  assert.equal(out.items[0].author?.value, 'Ann');
});

test('malformed attribution is rejected, never invented or repaired', async () => {
  // O-159 (R-09): a newer server's attribution source is vocabulary, not damage. The author it
  // labels is left out (never guessed) and the book still shows.
  {
    const f = setup(() => ({items: [{...row(), author: {value: 'Ann', source: 'provider'}}]}));
    const out = await f.client.list({limit: 1});
    assert.equal(out.items.length, 1);
    assert.equal(out.items[0].author, undefined, 'unknown source: the author is left out, not invented');
  }
  for (const [name, bad] of [
    ['empty author', {...row(), author: {value: '', source: 'owner'}}],
    ['empty series id', {...row(), series: {id: '', name: 'Saga', source: 'folder'}}],
    ['empty series name', {...row(), series: {id: 'series_1', name: '', source: 'folder'}}],
    ['negative number', {...row(), series: {id: 'series_1', name: 'Saga', number: -1, source: 'folder'}}],
    ['string number', {...row(), series: {id: 'series_1', name: 'Saga', number: '2', source: 'folder'}}],
  ] as const) {
    const f = setup(() => ({items: [bad]}));
    await assert.rejects(f.client.list({limit: 1}), coded('invalid_document'), name);
  }
});

test('null facts stay absent and undeclared item keys are never mirrored', async () => {
  const f = setup(() => ({items: [{...row(), author: null, series: null, attribution: {value: 'Ann', source: 'owner'}}]}));
  const out = await f.client.list({limit: 1});
  assert.equal(Object.hasOwn(out.items[0], 'author'), false);
  assert.equal(Object.hasOwn(out.items[0], 'series'), false);
  assert.equal(Object.hasOwn(out.items[0], 'attribution'), false);
});

test('attribution never bypasses cursor, authority or lifetime guards', async () => {
  const f = setup(() => ({items: [row({author: {value: 'Ann', source: 'owner'}})]}));
  await assert.rejects(f.client.list({limit: 101}), coded('invalid_request'));
  await assert.rejects(f.client.list({cursor: '../source'}), coded('invalid_request'));
  assert.equal(f.calls.length, 0);
  const out = await f.client.list({limit: 1});
  assert.equal(out.items[0].author?.value, 'Ann');
  assert.equal(f.calls[0].headers.get('Authorization'), 'Bearer captured-token');
  f.retire();
  await assert.rejects(f.client.list({limit: 1}), coded('cancelled'));
  assert.equal(f.calls.length, 1);
});
