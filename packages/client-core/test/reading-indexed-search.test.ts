import test from 'node:test';
import assert from 'node:assert/strict';
import {parseReadingIndexedSearchPage, readReadingIndexedSearch, readingIndexedSearchPath, validReadingIndexedSearchQuery, type ReadingIndexedSearchApi} from '../src/reading-indexed-search.ts';

const rev = (digit: string) => digit.repeat(64);
const anchor = {normalization: 'xhtml-body-blockspace-fields-v1', offsetUnit: 'utf16', matchUnit: 'utf16'} as const;
const cover = (id = 'item') => `/v1/items/${id}/art/cover?v=${rev('b')}`;
const hit = (over: Record<string, unknown> = {}) => ({libraryId: 'library', itemId: 'item', sourceId: 'source', sourceRevision: rev('a'),
  sectionPath: 'OEBPS/chapter-1.xhtml', sectionIndex: 0, utf16Offset: 6, excerpt: 'Hello world', matchStart: 6, matchLength: 5, ...over});
const page = (over: Record<string, unknown> = {}) => ({indexedOnly: true, indexRevision: 3, anchor, hits: [hit()], ...over});
const lifetime = () => ({signal: new AbortController().signal, isCurrent: () => true});

test('the global indexed page keeps its exact anchor, hits and optional facts', () => {
  const parsed = parseReadingIndexedSearchPage(page({hits: [hit({title: 'Book', coverUrl: cover()})]}), {libraryId: 'library', limit: 20});
  assert.equal(parsed.indexedOnly, true);
  assert.deepEqual(parsed.anchor, anchor);
  assert.equal(parsed.hits[0]?.title, 'Book');
  assert.equal(parsed.hits[0]?.coverUrl, cover());
  assert.equal(parsed.next, undefined);
});

test('options, anchor, IDs, cover, duplicate positions, archive paths and UTF-16 ranges are exact', () => {
  assert.throws(() => parseReadingIndexedSearchPage(page(), {limit: 0}), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page(), {libraryId: 'other'}), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page(), {extra: true} as never), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({indexRevision: Number.MAX_SAFE_INTEGER + 1})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({anchor: {...anchor, normalization: 'other'}})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit(), hit({matchStart: 0})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({sourceRevision: 'revision'})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({libraryId: 'bad id'})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({coverUrl: '/art'})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({coverUrl: cover('other')})]})), /invalid_reading_indexed_search/);
  for (const sectionPath of ['../secret.xhtml', '/abs.xhtml', 'OEBPS//x.xhtml', 'OEBPS\\x.xhtml', 'OEBPS/../x.xhtml', 'urn:isbn:1', 'OEBPS/x?#'])
    assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({sectionPath})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({excerpt: 'a\uD83D\uDE00', matchStart: 1, matchLength: 1})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({hits: [hit({excerpt: '\uD83D'})]})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({next: 'x'.repeat(513)})), /invalid_reading_indexed_search/);
  assert.throws(() => parseReadingIndexedSearchPage(page({next: ''})), /invalid_reading_indexed_search/);
  // R-09b: responses read open; an added member is ignored.
  assert.doesNotThrow(() => parseReadingIndexedSearchPage(page({extra: true})));
});

test('the shared query validator keeps literal words and refuses non-searchable text', () => {
  for (const query of ['Miles Davis', 'artist: Miles', '"quoted words"', 'café', '日本語', '5', '\u0301'])
    assert.equal(validReadingIndexedSearchQuery(query), true, query);
  for (const query of ['?!.,', '😀', '   ', '', 'a\uD800', 'x'.repeat(1025), '\u0000'])
    assert.equal(validReadingIndexedSearchQuery(query), false, query);
  assert.equal(validReadingIndexedSearchQuery(42), false);
  assert.equal(validReadingIndexedSearchQuery(undefined), false);
  assert.equal(validReadingIndexedSearchQuery({query: 'hello'}), false);
});

test('the read port copies and validates before awaiting; lifetime/error identity survives', async () => {
  const path = readingIndexedSearchPath();
  assert.equal(typeof path, 'string');
  const seen: string[] = [], held = {query: 'hello', libraryId: 'library', cursor: 'abc_def-123', limit: 50, extra: 'ignored'};
  const api = {request: async (requestPath: string) => {seen.push(requestPath); held.query = 'changed'; held.libraryId = 'other'; held.cursor = 'changed'; held.limit = 20; return page();}} as unknown as ReadingIndexedSearchApi;
  const parsed = await readReadingIndexedSearch(api, held, lifetime());
  assert.equal(parsed.hits.length, 1);
  assert.match(seen[0]!, /^\/v1\/reading\/search\?q=hello&limit=50&libraryId=library&cursor=abc_def-123$/);
  for (const request of [{query: '\uD800'}, {query: '   '}, {query: 'x'.repeat(1025)}, {query: '?!.,'}, {query: 'hello', libraryId: 'bad id'}, {query: 'hello', cursor: 'bad cursor'}, {query: 'hello', limit: 51}])
    await assert.rejects(() => readReadingIndexedSearch(api, request as never, lifetime()), /invalid_reading_indexed_search/);
  let calls = 0;
  const counting = {request: async () => {calls++; return page();}} as unknown as ReadingIndexedSearchApi;
  const aborted = new AbortController(); aborted.abort();
  await assert.rejects(() => readReadingIndexedSearch(counting, {query: 'hello'}, {signal: aborted.signal, isCurrent: () => true}), {name: 'AbortError'});
  assert.equal(calls, 0);
  const throwing = {signal: new AbortController().signal, isCurrent: () => {throw new Error('guard');}};
  await assert.rejects(() => readReadingIndexedSearch(counting, {query: 'hello'}, throwing), {name: 'AbortError'});
  assert.equal(calls, 0);
  const literal: string[] = [];
  const literalApi = {request: async (requestPath: string) => {literal.push(requestPath); return page();}} as unknown as ReadingIndexedSearchApi;
  await readReadingIndexedSearch(literalApi, {query: 'artist: Miles'}, lifetime());
  assert.match(literal[0]!, /^\/v1\/reading\/search\?q=artist%3A\+Miles&limit=20$/);
  const transport = Object.assign(new Error('network'), {code: 'transport'});
  const failing = {request: async () => {throw transport;}} as unknown as ReadingIndexedSearchApi;
  await assert.rejects(() => readReadingIndexedSearch(failing, {query: 'hello'}, lifetime()), error => error === transport);
  let current = true;
  const retiring = {request: async () => {current = false; throw transport;}} as unknown as ReadingIndexedSearchApi;
  await assert.rejects(() => readReadingIndexedSearch(retiring, {query: 'hello'}, {signal: new AbortController().signal, isCurrent: () => current}), {name: 'AbortError'});
  const after = new AbortController();
  const late = {request: async () => {after.abort(); return page();}} as unknown as ReadingIndexedSearchApi;
  await assert.rejects(() => readReadingIndexedSearch(late, {query: 'hello'}, {signal: after.signal, isCurrent: () => true}), {name: 'AbortError'});
  let guardState: 'ok' | 'throw' = 'ok';
  const throwsLater = {request: async () => {guardState = 'throw'; return page();}} as unknown as ReadingIndexedSearchApi;
  await assert.rejects(() => readReadingIndexedSearch(throwsLater, {query: 'hello'}, {signal: new AbortController().signal,
    isCurrent: () => {if (guardState === 'throw') throw new Error('guard'); return true;}}), {name: 'AbortError'});
  let invalidCurrent = true;
  const retiringInvalid = {request: async () => {invalidCurrent = false; return {indexedOnly: true};}} as unknown as ReadingIndexedSearchApi;
  await assert.rejects(() => readReadingIndexedSearch(retiringInvalid, {query: 'hello'}, {signal: new AbortController().signal, isCurrent: () => invalidCurrent}), {name: 'AbortError'});
});
