import assert from 'node:assert/strict';
import test from 'node:test';
import {parseSearchGenres, readSearchGenres, searchGenresPath, searchGenreBrowsePivot, searchGenreBrowseRequest,
  parseSearchGenrePreparing, releaseSearchGenres, searchGenresReadMetadata, type SearchGenreApi, type SearchGenreReadOptions} from '../src/search-genres.ts';
import {parseSearchGenreFallback} from '../src/search-genres.ts';
import type {LibraryContentApi} from '../src/library-content.ts';
import {ApiError} from '../src/index.ts';
import type {PreparationResponse, PreparationRuntime} from '../src/read-preparation.ts';
import {getEventListeners} from 'node:events';

const artwork = (id: string, role: 'poster' | 'backdrop', kind = 'item') => ({kind, id, path: `/v1/metadata/${kind}/${id}/art/${role}?v=${'a'.repeat(64)}&w=${role === 'backdrop' ? 1920 : 400}`});
const answer = () => ({variant: 'ready', queryId: 'genre_1', revision: 'opaque-authorized-revision', genres: [
  {value: 'Sci-Fi & Fantasy', label: 'Sci-Fi & Fantasy', count: 17, backdrop: artwork('film-a', 'backdrop'), covers: [artwork('film-b', 'poster'), artwork('show-c', 'poster', 'show')]},
  {value: 'Crime', label: 'Crime', count: 9, covers: []},
], nextCursor: 'opaque_cursor'});

test('preserves exact server order, metadata labels, counts and missing art immutably', () => {
  const raw = answer(), page = parseSearchGenres(raw);
  assert.deepEqual(page.genres.map(genre => [genre.value, genre.label, genre.count]), [['Sci-Fi & Fantasy', 'Sci-Fi & Fantasy', 17], ['Crime', 'Crime', 9]]);
  assert.equal(page.genres[1].backdrop, undefined);
  assert.deepEqual(page.genres[1].covers, []);
  assert.equal(page.nextCursor, 'opaque_cursor');
  assert.ok(Object.isFrozen(page) && Object.isFrozen(page.genres) && Object.isFrozen(page.genres[0]) && Object.isFrozen(page.genres[0].covers) && Object.isFrozen(page.genres[0].covers[0]) && Object.isFrozen(page.genres[0].backdrop));
  raw.genres.reverse();
  raw.genres[1].label = 'changed later';
  assert.equal(page.genres[0].label, 'Sci-Fi & Fantasy');
});

test('refuses unsupported/null art, invalid counts, duplicate genres and unbounded pages', () => {
  for (const mutate of [
    (raw: any) => raw.genres[0].backdrop = null,
    (raw: any) => raw.genres[0].covers = null,
    (raw: any) => raw.genres[0].covers.push(artwork('film-d', 'poster')),
    (raw: any) => raw.genres[0].covers[1] = raw.genres[0].covers[0],
    (raw: any) => raw.genres[0].count = 0,
    (raw: any) => raw.genres[0].count = 1.2,
    (raw: any) => raw.genres[1].value = raw.genres[0].value,
    (raw: any) => raw.nextCursor = null,
    (raw: any) => raw.nextCursor = 'x'.repeat(513),
    (raw: any) => raw.genres = Array(51).fill(raw.genres[0]),
  ]) {const raw = answer(); mutate(raw); assert.throws(() => parseSearchGenres(raw));}
});

test('accepts only the exact selected-art kind, subject, role and bounded relative route', () => {
  for (const path of [
    'https://provider.invalid/art?token=private', '//provider.invalid/art', '/private/file.jpg',
    `https://metadata.invalid${artwork('film-a', 'backdrop').path}`, `//metadata.invalid${artwork('film-a', 'backdrop').path}`,
    artwork('another-title', 'backdrop').path,
    artwork('film-a', 'poster').path,
    artwork('film-a', 'backdrop', 'show').path,
    `${artwork('film-a', 'backdrop').path}&token=private`,
    `${artwork('film-a', 'backdrop').path}#fragment`,
    artwork('film-a', 'backdrop').path.replace('v=', 'subject=someone&v='),
    artwork('film-a', 'backdrop').path.replace('a'.repeat(64), 'A'.repeat(64)),
    artwork('film-a', 'backdrop').path.replace('w=1920', 'w=400'),
    `${artwork('film-a', 'backdrop').path}&v=${'b'.repeat(64)}`,
  ]) {const raw = answer(); raw.genres[0].backdrop!.path = path; assert.throws(() => parseSearchGenres(raw), path);}
  const raw: any = answer();
  raw.token = 'private'; raw.genres[0].providerURL = 'https://private.invalid'; raw.genres[0].backdrop.token = 'secret';
  const page = parseSearchGenres(raw);
  assert.equal(JSON.stringify(page).includes('private'), false);
  assert.equal(JSON.stringify(page).includes('secret'), false);
});

test('uses the generated route and preserves the exact opaque continuation', async () => {
  assert.equal(searchGenresPath(), '/v1/search/genres?limit=50');
  assert.equal(searchGenresPath({limit: 8, cursor: 'opaque_-'}), '/v1/search/genres?limit=8&cursor=opaque_-');
  for (const limit of [0, -1, 51, Infinity, 1.2]) assert.throws(() => searchGenresPath({limit}));
  const controller = new AbortController();
  const api: LibraryContentApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    assert.equal(path, '/v1/search/genres?limit=8'); assert.equal(method, 'GET'); assert.equal(body, undefined); assert.ok(signal instanceof AbortSignal);
    return answer() as T;
  }};
  const page = await readSearchGenres(api, {limit: 8}, controller.signal);
  assert.equal(page.genres.length, 2); releaseSearchGenres(page);
});

test('a cancelled caller neither starts another read nor installs a late response', async () => {
  const controller = new AbortController();
  let reads = 0;
  const api: LibraryContentApi = {request: async <T>(): Promise<T> => {reads++; controller.abort(); return answer() as T;}};
  await assert.rejects(readSearchGenres(api, {}, controller.signal), {name: 'AbortError'});
  assert.equal(reads, 1);
  await assert.rejects(readSearchGenres(api, {}, controller.signal), {name: 'AbortError'});
  assert.equal(reads, 1);
});

test('a genre opens the canonical video-only across browse with its exact value', () => {
  const request = searchGenreBrowseRequest('Sci-Fi & Fantasy');
  assert.equal(searchGenreBrowsePivot, 'video-titles');
  assert.deepEqual(request, {pivot: 'video-titles', query: {variant:'predicate',field: 'genre', operator: 'equals', value: 'Sci-Fi & Fantasy'}, sort: [{field: 'title', direction: 'asc'}]});
  assert.ok(Object.isFrozen(request) && Object.isFrozen(request.query) && Object.isFrozen(request.sort));
  assert.throws(() => searchGenreBrowseRequest(''));
  for (const value of ['x'.repeat(10000), 'Crime\u0000metadata']) assert.equal((searchGenreBrowseRequest(value).query as {value: string}).value, value);
});

test('slot previews are independent bounded reads and retain artless-directory presence', async () => {
  assert.equal(searchGenresPath({limit: 6, preview: 'covers'}), '/v1/search/genres?limit=6&preview=covers');
  assert.throws(() => searchGenresPath({preview: 'covers', cursor: 'list-only'}));
  for (const raw of [
    {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: [], hasGenres: true},
    {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: [], hasGenres: false},
  ]) {
    const page = await readSearchGenres({request: async <T>() => raw as T}, {limit: 4, preview: 'backdrop'});
    assert.equal(page.hasGenres, raw.hasGenres);
    assert.deepEqual(page.genres, []);
  }
  for (const raw of [
    {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: []},
    {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: [], hasGenres: true, nextCursor: 'do-not-drain'},
    {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: [answer().genres[1]], hasGenres: true},
    {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: answer().genres, hasGenres: true},
  ]) {
    await assert.rejects(readSearchGenres({request: async <T>() => raw as T}, {limit: 1, preview: 'backdrop'}));
  }
});

const response = (status: number, body?: unknown, headers: Record<string, string> = {}): PreparationResponse => ({status, body, headers});
const pending = (queryId = 'genre_1', retryAfterMs = 1000) => ({variant: 'preparing', kind: 'genre-preparing', state: 'preparing', queryId, retryAfterMs});
const nestedPending = (queryId = 'genre_1') => { const {variant: _variant, ...rest} = pending(queryId); return rest; };
const ready = (queryId = 'genre_1', over: Record<string, unknown> = {}) => ({...answer(), queryId, ...over});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(yes => { resolve = yes; });
  return {promise, resolve};
}
async function flush() { for (let i = 0; i < 48; i++) await Promise.resolve(); }
class Clock implements PreparationRuntime {
  time = Date.parse('Wed, 28 Oct 2026 12:00:00 GMT');
  now = () => this.time;
  random = () => 0;
  next = 0;
  timers = new Map<number, {callback: () => void; ms: number}>();
  delays: number[] = [];
  setTimeout = (callback: () => void, ms: number) => {
    assert.equal(this.timers.size, 0, 'one timer per operation');
    assert.ok(ms > 0 && ms <= 2_147_483_647);
    const id = ++this.next;
    this.timers.set(id, {callback, ms}); this.delays.push(ms); return id;
  };
  clearTimeout = (id: unknown) => { this.timers.delete(id as number); };
  fire() {
    assert.equal(this.timers.size, 1);
    const [id, timer] = this.timers.entries().next().value!;
    this.timers.delete(id); this.time += timer.ms; timer.callback();
  }
}
function rawFixture(replies: (PreparationResponse | Promise<PreparationResponse>)[]) {
  const clock = new Clock(), scope = new AbortController();
  let current = true, ordinaryCalls = 0;
  const calls: {path: string; method: string; signal?: AbortSignal; singleAttempt?: boolean}[] = [];
  const api: SearchGenreApi = {
    request: async () => { ordinaryCalls++; throw new Error('production must use raw transport'); },
    requestRaw(path, method, options = {}) {
      calls.push({path, method, ...options});
      if (method === 'DELETE') return Promise.resolve(response(204));
      assert.ok(replies.length, 'no unplanned/eager genre read');
      return Promise.resolve(replies.shift()!);
    },
  };
  const options: SearchGenreReadOptions = {runtime: clock, isCurrent: () => current};
  return {api, options, clock, scope, calls, supersede() { current = false; }, ordinaryCalls: () => ordinaryCalls,
    gets: () => calls.filter(c => c.method === 'GET'), deletes: () => calls.filter(c => c.method === 'DELETE')};
}

test('generated ready union requires queryId and preserves previous/reload anchors and arbitrary legal metadata', () => {
  const scalar = '  藝術😀\n'.repeat(100_000);
  const raw = ready('genre_1', {previousCursor: 'previous_1', reloadCursor: 'reload_1',
    genres: [{value: scalar, label: '', count: Number.MAX_SAFE_INTEGER, covers: []}]});
  const page = parseSearchGenres(raw);
  assert.equal(page.queryId, 'genre_1'); assert.equal(page.previousCursor, 'previous_1'); assert.equal(page.reloadCursor, 'reload_1');
  assert.equal(page.genres[0].value, scalar); assert.equal(page.genres[0].label, '');
  assert.equal(page.genres[0].count, Number.MAX_SAFE_INTEGER);
  assert.equal((searchGenreBrowseRequest(scalar).query as {value: string}).value, scalar);
  for (const bad of [
    {...raw, queryId: undefined}, {...raw, queryId: null}, {...raw, queryId: 'x'.repeat(513)}, {...raw, queryId: 'q.bad'},
    {...raw, previousCursor: null}, {...raw, reloadCursor: 'not/a/token'},
    {...raw, hasGenres: null}, {...raw, hasGenres: false}, {...raw, genres: [{...raw.genres[0], count: Number.MAX_SAFE_INTEGER + 1}]},
    pending(), {...raw, kind: 'genre-preparing', state: 'preparing', retryAfterMs: 1000},
  ]) assert.throws(() => parseSearchGenres(bad));
  const max = 'x'.repeat(512);
  assert.equal(parseSearchGenres(ready(max, {nextCursor: max, previousCursor: max, reloadCursor: max})).queryId, max);
});

test('genre preparing is a strict generated envelope, never ready/empty or another domain', () => {
  assert.deepEqual(parseSearchGenrePreparing(pending()), pending());
  for (const bad of [ready(), {state: 'preparing', queryId: 'g', retryAfterMs: 1},
    {...pending(), kind: 'facet-preparing'}, {...pending(), state: 'ready'}, {...pending(), genres: []},
    {...pending(), queryId: ''}, {...pending(), queryId: 'x'.repeat(513)}, {...pending(), queryId: '//foreign'},
    ...[0, -1, 1.5, Infinity, Number.MAX_SAFE_INTEGER + 1, '1000'].map(retryAfterMs => ({...pending(), retryAfterMs})),
  ]) assert.throws(() => parseSearchGenrePreparing(bad));
});

test('request accepts empty/list preview mode and inherited continuation limit, rejects offsets and malformed inputs', () => {
  assert.equal(searchGenresPath({preview: ''}), '/v1/search/genres?limit=50');
  assert.equal(searchGenresPath({cursor: 'next_1'}), '/v1/search/genres?cursor=next_1');
  assert.equal(searchGenresPath({cursor: 'next_1', preview: ''}), '/v1/search/genres?cursor=next_1');
  for (const request of [{limit: null}, {preview: null}, {preview: 'other'}, {cursor: null},
    {cursor: 'x'.repeat(513)}, {cursor: '../foreign'}, {offset: 0}, {preview: 'backdrop', cursor: 'next_1'},
  ]) assert.throws(() => searchGenresPath(request as never));
});

test('raw initial 200 retains a directory lease until explicit release, with no enumerable ownership state', async () => {
  const f = rawFixture([response(200, ready())]);
  const page = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  assert.equal(f.ordinaryCalls(), 0); assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 0);
  assert.equal(Object.keys(page).includes('release'), false); assert.equal(Object.keys(page).includes('reanchored'), false);
  assert.deepEqual(searchGenresReadMetadata(page), {reanchored: false});
  assert.equal(getEventListeners(f.scope.signal, 'abort').length, 1);
  releaseSearchGenres(page); releaseSearchGenres(page); f.scope.abort();
  assert.equal(f.deletes().length, 1); assert.equal(f.deletes()[0].path, '/v1/genre-queries/genre_1');
  assert.equal(f.deletes()[0].singleAttempt, true); assert.equal(f.deletes()[0].signal, undefined);
  assert.equal(getEventListeners(f.scope.signal, 'abort').length, 0);
  assert.equal(searchGenresReadMetadata(parseSearchGenres(ready())), undefined);
  releaseSearchGenres(undefined); releaseSearchGenres(parseSearchGenres(ready()));
});

test('later scope abort releases a ready directory lease; un-signalled caller can explicitly dispose', async () => {
  const f = rawFixture([response(200, ready())]);
  const page = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  f.scope.abort(); releaseSearchGenres(page); assert.equal(f.deletes().length, 1);
  const g = rawFixture([response(200, ready())]);
  const unowned = await readSearchGenres(g.api, {}, undefined, g.options);
  assert.equal(g.deletes().length, 0); releaseSearchGenres(unowned); assert.equal(g.deletes().length, 1);
});

test('previews and anchorless empty directory pages release immediately, including empty/artless hasGenres', async t => {
  for (const hasGenres of [true, false]) await t.test(String(hasGenres), async () => {
    const f = rawFixture([response(200, {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: [], hasGenres})]);
    const page = await readSearchGenres(f.api, {preview: 'backdrop', limit: 4}, f.scope.signal, f.options);
    assert.equal(page.queryId, 'preview_1'); assert.equal(page.hasGenres, hasGenres); assert.deepEqual(page.genres, []);
    assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 1);
    assert.equal(getEventListeners(f.scope.signal, 'abort').length, 0);
    releaseSearchGenres(page); f.scope.abort(); assert.equal(f.deletes().length, 1);
  });
  const f = rawFixture([response(200, {variant: 'ready', queryId: 'empty_1', revision: 'basis', genres: []})]);
  const page = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  assert.equal(f.deletes().length, 1); releaseSearchGenres(page); assert.equal(f.deletes().length, 1); f.scope.abort();
});

test('preview requires exact slot art/nonnullable hasGenres and forbids every continuation anchor', async t => {
  for (const key of ['nextCursor', 'previousCursor', 'reloadCursor']) await t.test(key, async () => {
    const f = rawFixture([response(200, {variant: 'ready', queryId: 'preview_1', revision: 'basis', genres: [], hasGenres: true, [key]: 'opaque'})]);
    await assert.rejects(readSearchGenres(f.api, {preview: 'covers'}, f.scope.signal, f.options));
    assert.equal(f.deletes().length, 1); assert.equal(f.gets().length, 1); f.scope.abort();
  });
  for (const body of [{variant: 'ready', queryId: 'p', revision: 'basis', genres: []},
    {variant: 'ready', queryId: 'p', revision: 'basis', genres: [], hasGenres: null},
    {variant: 'ready', queryId: 'p', revision: 'basis', genres: answer().genres, hasGenres: true},
  ]) {
    const f = rawFixture([response(200, body)]);
    await assert.rejects(readSearchGenres(f.api, {preview: 'covers'}, f.scope.signal, f.options));
    assert.equal(f.deletes().length, 1); f.scope.abort();
  }
});

test('202 raw polls honor full numeric/date/body floors and chunk long waits, with one timer and no eager drain', async t => {
  for (const example of [
    {body: 1, headers: {'Retry-After': '120'}, expected: [120_001]},
    {body: 1, headers: {'rEtRy-AfTeR': 'Wed, 28 Oct 2026 12:02:00 GMT'}, expected: [120_001]},
    {body: 180_000, headers: {'Retry-After': '1'}, expected: [180_001]},
    {body: 2_147_483_647 + 10, headers: {}, expected: [2_147_483_647, 11]},
  ]) await t.test(JSON.stringify(example), async () => {
    const raw = response(202, pending('genre_1', example.body), {...example.headers, Location: '/v1/genre-queries/genre_1'});
    const f = rawFixture([raw, response(200, ready())]); let events = 0;
    const done = readSearchGenres(f.api, {limit: 8}, f.scope.signal, {...f.options, onPreparing(p, meta) {
      assert.deepEqual(p, pending('genre_1', example.body)); assert.equal(meta, raw); events++;
    }});
    await flush(); assert.equal(events, 1); assert.equal(f.gets().length, 1); assert.equal(f.clock.delays[0], example.expected[0]);
    f.clock.fire(); await flush();
    if (example.expected.length > 1) { assert.equal(f.gets().length, 1); assert.equal(f.clock.delays[1], example.expected[1]); f.clock.fire(); }
    const page = await done;
    assert.equal(f.gets()[1].path, '/v1/genre-queries/genre_1'); assert.equal(f.gets().length, 2);
    assert.equal(f.clock.timers.size, 0); assert.equal(f.ordinaryCalls(), 0); releaseSearchGenres(page); f.scope.abort();
  });
});

test('continuation keeps predecessor alive until server consumes its token, then releases it once', async () => {
  const ack = deferred<PreparationResponse>(), f = rawFixture([response(200, ready()), ack.promise, response(200, ready('genre_2'))]);
  const first = await readSearchGenres(f.api, {limit: 8}, f.scope.signal, f.options);
  const next = readSearchGenres(f.api, {cursor: first.nextCursor}, f.scope.signal, {...f.options, previousPage: first});
  await flush(); assert.equal(f.deletes().length, 0); assert.equal(f.gets().length, 2);
  const query = new URL(f.gets()[1].path, 'https://local.invalid').searchParams;
  assert.equal(query.get('cursor'), first.nextCursor); assert.equal(query.get('limit'), '8');
  ack.resolve(response(202, pending('genre_2'))); await flush();
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/genre-queries/genre_1']);
  f.clock.fire(); const second = await next;
  assert.equal(second.queryId, 'genre_2'); assert.deepEqual(searchGenresReadMetadata(second), {reanchored: false, previousRevision: first.revision});
  releaseSearchGenres(first); assert.equal(f.deletes().length, 1);
  releaseSearchGenres(second); f.scope.abort(); assert.equal(f.deletes().length, 2);
});

test('previous/reload reuse only existing cursor query; no history, rank, offsets or implicit extra reads', async () => {
  const f = rawFixture([response(200, ready('genre_1', {previousCursor: 'previous_1', reloadCursor: 'reload_1'})),
    response(200, ready('genre_2', {reloadCursor: 'reload_2'})), response(200, ready('genre_3'))]);
  const first = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  const previous = await readSearchGenres(f.api, {cursor: first.previousCursor}, f.scope.signal, {...f.options, previousPage: first});
  const reloaded = await readSearchGenres(f.api, {cursor: previous.reloadCursor}, f.scope.signal, {...f.options, previousPage: previous});
  assert.equal(new URL(f.gets()[1].path, 'https://local.invalid').searchParams.get('cursor'), 'previous_1');
  assert.equal(new URL(f.gets()[2].path, 'https://local.invalid').searchParams.get('cursor'), 'reload_2');
  assert.equal(f.gets().length, 3); assert.equal(f.deletes().length, 2);
  releaseSearchGenres(reloaded); f.scope.abort(); assert.equal(f.deletes().length, 3);
});

test('manual lease owners can release prior page after continuation completion without premature cancellation', async () => {
  const f = rawFixture([response(200, ready()), response(200, ready('genre_2'))]);
  const first = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  const second = await readSearchGenres(f.api, {cursor: first.nextCursor}, f.scope.signal, f.options);
  assert.equal(f.deletes().length, 0);
  releaseSearchGenres(first); assert.equal(f.deletes().length, 1);
  releaseSearchGenres(second); f.scope.abort(); assert.equal(f.deletes().length, 2);
});

test('foreign/released predecessor cursor is rejected before GET and cannot release unrelated pages', async () => {
  const f = rawFixture([response(200, ready())]);
  const first = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  await assert.rejects(readSearchGenres(f.api, {cursor: 'foreign'}, f.scope.signal, {...f.options, previousPage: first}));
  assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 0);
  releaseSearchGenres(first);
  await assert.rejects(readSearchGenres(f.api, {cursor: first.nextCursor}, f.scope.signal, {...f.options, previousPage: first}));
  assert.equal(f.gets().length, 1); f.scope.abort();
});

test('409 reanchors once without cursor, exposes raw refresh and accepted new-root metadata', async () => {
  const stale = response(409, {error: {code: 'stale_continuation'}});
  const f = rawFixture([response(200, ready()), stale, response(200, ready('genre_2', {revision: 'new_basis'}))]);
  const first = await readSearchGenres(f.api, {limit: 8}, f.scope.signal, f.options); let refreshes = 0;
  const page = await readSearchGenres(f.api, {cursor: first.nextCursor}, f.scope.signal, {...f.options, previousPage: first, onReanchor(raw) { assert.equal(raw, stale); refreshes++; }});
  assert.equal(refreshes, 1); assert.equal(page.revision, 'new_basis');
  assert.deepEqual(searchGenresReadMetadata(page), {reanchored: true, previousRevision: first.revision});
  assert.equal(new URL(f.gets()[2].path, 'https://local.invalid').searchParams.has('cursor'), false);
  assert.equal(new URL(f.gets()[2].path, 'https://local.invalid').searchParams.get('limit'), '8');
  assert.equal(f.deletes().length, 1); releaseSearchGenres(page); f.scope.abort();
});

test('continuation revision change without stale refresh fails and releases old/new leases', async () => {
  const f = rawFixture([response(200, ready()), response(200, ready('genre_2', {revision: 'changed_basis'}))]);
  const first = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  await assert.rejects(readSearchGenres(f.api, {cursor: first.nextCursor}, f.scope.signal, {...f.options, previousPage: first}), (error: {code?: string}) => error.code === 'stale_continuation');
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/genre-queries/genre_1', '/v1/genre-queries/genre_2']); f.scope.abort();
});

test('second stale and typed refusal remain terminal ApiErrors with status/code/retry/full header metadata', async t => {
  const f = rawFixture([response(409), response(409, {error: {code: 'stale_continuation', retry: 'after_refresh'}})]);
  let refreshes = 0;
  await assert.rejects(readSearchGenres(f.api, {cursor: 'old'}, f.scope.signal, {...f.options, onReanchor: () => { refreshes++; }}), error => error instanceof ApiError && error.status === 409 && error.code === 'stale_continuation');
  assert.equal(refreshes, 1); assert.equal(f.gets().length, 2); f.scope.abort();
  for (const status of [429, 503, 401, 403, 400]) await t.test(String(status), async () => {
    const raw = response(status, {error: {code: 'original_code', retry: 'same_request', message: 'original message', path: 'field'}}, {'Retry-After': 'Wed, 28 Oct 2026 12:02:00 GMT'});
    const g = rawFixture([response(202, pending()), raw]);
    const done = readSearchGenres(g.api, {}, g.scope.signal, g.options);
    const rejected = assert.rejects(done, error => error instanceof ApiError && error.status === status && error.code === 'original_code' && error.retryable === true
      // Receipt follows the first 1001ms poll wait, so the absolute date's remaining floor
      // is 118.999 seconds, rounded up by the existing ApiError header parser.
      && error.retryAfterSeconds === 119 && error.path === 'field' && error.message === 'original message');
    await flush(); g.clock.fire(); await rejected;
    assert.equal(g.gets().length, 2); assert.equal(g.deletes().length, 1); assert.equal(g.clock.timers.size, 0); g.scope.abort();
  });
});

test('custom typed errors preserve identity and receive raw metadata', async () => {
  const raw = response(503), f = rawFixture([raw]), error = new Error('existing typed error');
  await assert.rejects(readSearchGenres(f.api, {}, f.scope.signal, {...f.options, errorFromResponse: response => { assert.equal(response, raw); return error; }}), actual => actual === error);
  assert.equal(f.gets().length, 1); f.scope.abort();
});

test('abort during pending timer clears it and cancels once; late initial 200 cleans direct-ready handle', async () => {
  const f = rawFixture([response(202, pending())]);
  const done = readSearchGenres(f.api, {}, f.scope.signal, f.options), rejected = assert.rejects(done, {name: 'AbortError'});
  await flush(); f.scope.abort(); await rejected;
  assert.equal(f.clock.timers.size, 0); assert.equal(f.gets().length, 1); assert.equal(f.deletes().length, 1);
  const late = deferred<PreparationResponse>(), g = rawFixture([late.promise]);
  const other = readSearchGenres(g.api, {}, g.scope.signal, g.options), otherRejected = assert.rejects(other, {name: 'AbortError'});
  g.scope.abort(); await otherRejected;
  late.resolve(response(200, ready())); await flush();
  assert.equal(g.deletes().length, 1); assert.equal(g.deletes()[0].path, '/v1/genre-queries/genre_1'); assert.equal(g.gets().length, 1);
});

test('supersession discards late 202 and cannot publish stale page, even if raw transport ignores abort', async () => {
  const late = deferred<PreparationResponse>(), f = rawFixture([late.promise]); let pendingEvents = 0;
  const done = readSearchGenres(f.api, {}, f.scope.signal, {...f.options, onPreparing: () => { pendingEvents++; }});
  const rejected = assert.rejects(done, {name: 'AbortError'});
  f.supersede(); late.resolve(response(202, pending())); await rejected; await flush();
  assert.equal(pendingEvents, 0); assert.equal(f.clock.timers.size, 0); assert.equal(f.deletes().length, 1); f.scope.abort();
});

test('foreign Location, malformed pending and a changed poll handle never reach another URL or empty success', async t => {
  for (const mode of ['location', 'malformed', 'changed-handle']) await t.test(mode, async () => {
    const replies = mode === 'location' ? [response(202, pending(), {Location: 'https://foreign.example/g'})]
      : mode === 'malformed' ? [response(202, {...pending(), genres: []})]
      : [response(202, pending()), response(200, ready('genre_2'))];
    const f = rawFixture(replies); let events = 0;
    const done = readSearchGenres(f.api, {}, f.scope.signal, {...f.options, onPreparing: () => { events++; }}), rejected = assert.rejects(done);
    await flush(); if (mode === 'changed-handle') f.clock.fire(); await rejected;
    assert.equal(events, mode === 'changed-handle' ? 1 : 0); assert.equal(f.clock.timers.size, 0);
    assert.ok(f.calls.every(c => !c.path.includes('foreign.example'))); assert.equal(f.deletes().length, mode === 'malformed' ? 0 : 1); f.scope.abort();
  });
});

test('read-only legacy test ports perform one GET, never hide preparing JSON or send a mutation', async () => {
  let calls = 0;
  const api: LibraryContentApi = {request: async <T>(_path: string, method?: string): Promise<T> => { calls++; assert.equal(method, 'GET'); return pending() as T; }};
  await assert.rejects(readSearchGenres(api)); assert.equal(calls, 1);
});

test('pre-abort prevents both transports and onPreparing retirement cannot leave a timer or poll', async () => {
  const f = rawFixture([]); f.scope.abort();
  await assert.rejects(readSearchGenres(f.api, {}, f.scope.signal, f.options), {name: 'AbortError'}); assert.equal(f.calls.length, 0);
  const g = rawFixture([response(202, pending())]);
  await assert.rejects(readSearchGenres(g.api, {}, g.scope.signal, {...g.options, onPreparing: () => g.scope.abort()}), {name: 'AbortError'});
  assert.equal(g.gets().length, 1); assert.equal(g.deletes().length, 1); assert.equal(g.clock.timers.size, 0);
});

test('many explicit continuations keep exactly one live lease/scope listener and no background read loop', async () => {
  const f = rawFixture(Array.from({length: 40}, (_, i) => response(200, ready(`genre_${i}`))));
  let page = await readSearchGenres(f.api, {}, f.scope.signal, f.options);
  for (let i = 1; i < 40; i++) {
    page = await readSearchGenres(f.api, {cursor: page.nextCursor}, f.scope.signal, {...f.options, previousPage: page});
    assert.equal(page.queryId, `genre_${i}`);
    assert.equal(f.gets().length, i + 1); assert.equal(f.deletes().length, i);
    assert.equal(getEventListeners(f.scope.signal, 'abort').length, 1);
    assert.equal(f.clock.timers.size, 0);
  }
  releaseSearchGenres(page); f.scope.abort();
  assert.equal(f.deletes().length, 40); assert.equal(getEventListeners(f.scope.signal, 'abort').length, 0);
});

test('retirement during anchorless ready cleanup cannot publish a late empty success', async () => {
  const f = rawFixture([response(200, {variant: 'ready', queryId: 'empty_1', revision: 'basis', genres: []})]);
  const send = f.api.requestRaw!.bind(f.api);
  f.api.requestRaw = (path, method, options) => {
    const reply = send(path, method, options);
    if (method === 'DELETE') f.scope.abort();
    return reply;
  };
  await assert.rejects(readSearchGenres(f.api, {}, f.scope.signal, f.options), {name: 'AbortError'});
  assert.equal(f.deletes().length, 1); assert.equal(f.gets().length, 1);
  assert.equal(getEventListeners(f.scope.signal, 'abort').length, 0);
});

const fallback = (queryId = 'genre_1', over: Record<string, unknown> = {}) => ({variant: 'fallback', kind: 'genre-fallback', queryId,
  revision: 'preparing', genres: [], hasGenresKnown: false, totalIsLowerBound: true, preparation: nestedPending(queryId), ...over});

test('cold fallback explicitly means unknown and cannot parse as an exact ready page', () => {
  const raw = fallback(), value = parseSearchGenreFallback(raw);
  assert.deepEqual(value, raw); assert.ok(Object.isFrozen(value) && Object.isFrozen(value.genres) && Object.isFrozen(value.preparation));
  assert.equal('hasGenres' in value, false); assert.equal('nextCursor' in value, false);
  assert.throws(() => parseSearchGenres(value)); assert.throws(() => parseSearchGenreFallback(ready()));
  assert.equal(searchGenresReadMetadata(value), undefined); releaseSearchGenres(value);
});

test('fallback strictly requires lower-bound/unknown flags, stable ID, empty rows and no anchors', async t => {
  for (const [name, raw] of [
    ['known presence', fallback('genre_1', {hasGenresKnown: true})],
    ['absent known flag', fallback('genre_1', {hasGenresKnown: undefined})],
    ['false presence', fallback('genre_1', {hasGenres: false})], ['null presence', fallback('genre_1', {hasGenres: null})],
    ['exact total', fallback('genre_1', {totalIsLowerBound: false})],
    ['missing total flag', fallback('genre_1', {totalIsLowerBound: undefined})],
    ['wrong revision', fallback('genre_1', {revision: 'pretend-ready-basis'})],
    ['invented rows', fallback('genre_1', {genres: answer().genres})],
    ['missing preparing', fallback('genre_1', {preparation: undefined})],
    ['different preparing ID', fallback('genre_1', {preparation: pending('genre_2')})],
    ['wrong domain', fallback('genre_1', {preparation: {...pending(), kind: 'facet-preparing'}})],
    ...['nextCursor', 'previousCursor', 'reloadCursor'].map(key => [key, fallback('genre_1', {[key]: 'opaque'})]),
  ] as [string, unknown][]) await t.test(name, () => { assert.throws(() => parseSearchGenreFallback(raw)); });
});

test('cold fallback publishes transient unknown panel and continues to exact preview with HasGenres', async () => {
  const interim = response(200, fallback(), {'Retry-After': '120', Location: '/v1/genre-queries/genre_1'});
  const exact = {variant: 'ready', queryId: 'genre_1', revision: 'accepted_basis', genres: [], hasGenres: false};
  const f = rawFixture([response(202, pending()), interim, response(200, exact)]);
  let callbacks = 0, finished = false;
  const done = readSearchGenres(f.api, {preview: 'backdrop', limit: 4}, f.scope.signal, {...f.options, onFallback(panel, raw) {
    callbacks++; assert.equal(raw, interim); assert.equal(panel.hasGenresKnown, false); assert.equal(panel.totalIsLowerBound, true);
    assert.equal('hasGenres' in panel, false); assert.deepEqual(panel.genres, []); assert.equal(searchGenresReadMetadata(panel), undefined);
  }});
  void done.then(() => { finished = true; });
  await flush(); f.clock.fire(); await flush();
  assert.equal(callbacks, 1); assert.equal(finished, false); assert.equal(f.deletes().length, 0);
  assert.deepEqual(f.clock.delays, [1001, 120_001]);
  f.clock.fire(); const page = await done;
  assert.equal(page.revision, 'accepted_basis'); assert.equal(page.hasGenres, false);
  assert.equal(f.deletes().length, 1); assert.equal(f.gets().length, 3); f.scope.abort();
});

test('direct/repeated fallback keeps one handle and no ready lease until exact directory page arrives', async () => {
  const f = rawFixture([response(200, fallback()), response(200, fallback()), response(200, ready())]); let callbacks = 0;
  const done = readSearchGenres(f.api, {}, f.scope.signal, {...f.options, onFallback: panel => { callbacks++; assert.equal(searchGenresReadMetadata(panel), undefined); }});
  await flush(); assert.equal(callbacks, 1); assert.equal(f.deletes().length, 0);
  f.clock.fire(); await flush(); assert.equal(callbacks, 2); assert.equal(f.gets().length, 2);
  f.clock.fire(); const page = await done;
  assert.equal(page.queryId, 'genre_1'); assert.equal(f.deletes().length, 0);
  releaseSearchGenres(page); f.scope.abort(); assert.equal(f.deletes().length, 1);
});

test('invalid fallback/foreign Location/changed pending ID cannot publish the unknown panel', async t => {
  for (const mode of ['invalid-flags', 'foreign-location', 'changed-id']) await t.test(mode, async () => {
    const body = mode === 'invalid-flags' ? fallback('genre_1', {totalIsLowerBound: false}) : fallback(mode === 'changed-id' ? 'genre_2' : 'genre_1');
    const f = rawFixture([response(202, pending()), response(200, body, mode === 'foreign-location' ? {Location: 'https://foreign.example/g'} : {})]); let callbacks = 0;
    const done = readSearchGenres(f.api, {}, f.scope.signal, {...f.options, onFallback: () => { callbacks++; }}), rejected = assert.rejects(done);
    await flush(); f.clock.fire(); await rejected;
    assert.equal(callbacks, 0); assert.equal(f.deletes().length, 1); assert.equal(f.clock.timers.size, 0); f.scope.abort();
  });
});

test('fallback callback retirement/failure cancels once with no poll or cached ready page', async t => {
  for (const mode of ['abort', 'throw']) await t.test(mode, async () => {
    const f = rawFixture([response(200, fallback())]), sentinel = new Error('fallback callback failed');
    await assert.rejects(readSearchGenres(f.api, {}, f.scope.signal, {...f.options, onFallback: () => {
      if (mode === 'throw') throw sentinel; f.scope.abort();
    }}), error => mode === 'throw' ? error === sentinel : (error as Error).name === 'AbortError');
    assert.equal(f.deletes().length, 1); assert.equal(f.gets().length, 1); assert.equal(f.clock.timers.size, 0); f.scope.abort();
  });
});

test('late malformed fallback after abort uses only queryId for safe cleanup and never publishes', async () => {
  const late = deferred<PreparationResponse>(), f = rawFixture([late.promise]); let callbacks = 0;
  const done = readSearchGenres(f.api, {}, f.scope.signal, {...f.options, onFallback: () => { callbacks++; }}), rejected = assert.rejects(done, {name: 'AbortError'});
  f.scope.abort(); await rejected;
  late.resolve(response(200, fallback('genre_1', {genres: 'malformed'}))); await flush();
  assert.equal(callbacks, 0); assert.equal(f.deletes().length, 1); assert.equal(f.gets().length, 1); assert.equal(f.clock.timers.size, 0);
});

test('fallback following continuation consumes predecessor and can still reanchor once to a new exact root', async () => {
  const f = rawFixture([response(200, ready()), response(200, fallback('genre_2')), response(409), response(200, ready('genre_3', {revision: 'new_basis'}))]);
  const first = await readSearchGenres(f.api, {}, f.scope.signal, f.options); let callbacks = 0;
  const done = readSearchGenres(f.api, {cursor: first.nextCursor}, f.scope.signal, {...f.options, previousPage: first, onFallback: () => { callbacks++; }});
  await flush(); assert.equal(callbacks, 1); assert.deepEqual(f.deletes().map(c => c.path), ['/v1/genre-queries/genre_1']);
  f.clock.fire(); const exact = await done;
  assert.equal(exact.queryId, 'genre_3'); assert.equal(exact.revision, 'new_basis'); assert.equal(searchGenresReadMetadata(exact)?.reanchored, true);
  assert.equal(new URL(f.gets().at(-1)!.path, 'https://local.invalid').searchParams.has('cursor'), false);
  assert.deepEqual(f.deletes().map(c => c.path), ['/v1/genre-queries/genre_1', '/v1/genre-queries/genre_2']);
  releaseSearchGenres(exact); f.scope.abort(); assert.equal(f.deletes().length, 3);
});
