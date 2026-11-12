import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import {NativeGenreBrowseDestination} from '../src-next/screens/search/genre-browse.ts';

// A genre's own page against the rebuilt server: its films and shows across every library,
// GET /v1/items?kind=movie,show&tag={genre}, opened at any position (`at`) with the list's count.
const tick = async () => {for (let n = 0; n < 6; n++) await new Promise<void>(resolve => setImmediate(resolve));};
const total = 5_000_000;
function items(path: string) {
  const query = new URL(path, 'https://server.test').searchParams;
  if (query.get('total') === 'true') return {items: [], total};
  const start = Number(query.get('at') ?? (query.get('cursor') ? query.get('cursor')!.slice(1) : 0)), count = Number(query.get('limit') ?? 40);
  return {items: Array.from({length: count}, (_, index) => ({id: `actual-${start + index}`, libraryId: index % 2 ? 'tv-library' : 'movie-library', kind: index % 2 ? 'show' : 'movie',
    title: `Actual ${start + index}`, images: {}, state: {}, addedAt: '2026-11-12T00:00:00Z'})), nextCursor: `c${start + count}`};
}

test('a genre page lists the genre\'s films and shows across every library, in title order, at any position', async () => {
  const paths: string[] = [];
  const api: LibraryContentApi = {request: async <T>(path: string): Promise<T> => {
    paths.push(path);
    if (path.startsWith('/v1/facets')) return {facet: 'letter', values: []} as T;
    return items(path) as T;
  }};
  const source = new NativeGenreBrowseDestination({api, value: 'actual-genre-value', current: () => true});
  try {
    const loading = source.load(); assert.equal(source.load(), loading); await loading; await tick();
    assert.equal(source.getSnapshot().phase, 'ready', String((source.getSnapshot().error as Error | undefined)?.message));
    const first = paths.find(path => path.startsWith('/v1/items?') && !path.includes('total=true'))!;
    const query = new URL(first, 'https://server.test').searchParams;
    assert.deepEqual([query.get('kind'), query.get('tag'), query.get('sort'), query.get('order')], ['movie,show', 'actual-genre-value', 'title', 'asc']);
    assert.ok(!paths.some(path => path.includes('/browse')), 'no old-server browse read is sent');
    assert.ok(source.getSnapshot().revision, 'the reading of the list is named');
    const collection = source.getSnapshot().collection!; collection.ensureRange(100_000, 100_001); await tick();
    assert.ok(paths.some(path => /[?&]at=\d{5,}/.test(path)), paths.slice(-3).join(' | '));
    assert.ok(collection.residentItems().length <= 240); assert.equal(collection.getSnapshot().total, total);
    const entry = collection.itemAt(100_000)!; assert.equal(source.contains(entry), true);
    source.remember(100_000, 18342.25); assert.equal(source.getMemory().offset, 18342.25);
    assert.throws(() => source.setQuery([]), /anchor/);
    const draft = source.createFilters(); draft.clear();
    assert.deepEqual(draft.getSnapshot().predicates, [{variant: 'predicate', field: 'genre', operator: 'equals', value: 'actual-genre-value'}]); draft.dispose();
  } finally {source.dispose();}
});

test('a list the server no longer knows ends the page in an error, and loading again starts it afresh', async () => {
  let refuse = false;
  const api: LibraryContentApi = {request: async <T>(path: string): Promise<T> => {
    if (path.startsWith('/v1/facets')) return {facet: 'letter', values: []} as T;
    if (refuse && !path.includes('total=true')) throw Object.assign(new Error('gone'), {status: 404, code: 'not_found'});
    return items(path) as T;
  }};
  const source = new NativeGenreBrowseDestination({api, value: 'genre', current: () => true});
  await source.load(); await tick(); const before = source.getSnapshot().collection!.itemAt(0)!;
  refuse = true; source.getSnapshot().collection!.ensureRange(100_000, 100_001); await tick();
  assert.equal(source.getSnapshot().phase, 'error');
  refuse = false; await source.load(); await tick();
  assert.equal(source.getSnapshot().phase, 'ready');
  assert.equal(source.contains(before), false, 'a title from the ended reading is not part of the new one');
  source.dispose();
});

test('retiring the principal aborts the page read and refuses its late reply', async () => {
  let current = true, signal: AbortSignal | undefined, finish!: (value: unknown) => void;
  const api: LibraryContentApi = {request: <T>(path: string, _method?: string, _body?: unknown, actualSignal?: AbortSignal): Promise<T> => {
    // The page read, not the count beside it.
    if (!path.includes('total=true') && !path.startsWith('/v1/facets')) signal ??= actualSignal;
    return new Promise(resolve => {finish = value => resolve(value as T);});
  }};
  const source = new NativeGenreBrowseDestination({api, value: 'genre', current: () => current});
  await source.load(); await tick();
  current = false; const retired = source.getSnapshot(); assert.ok(signal?.aborted);
  finish(items('/v1/items?at=0&limit=60')); await tick(); assert.equal(source.getSnapshot(), retired);
});
