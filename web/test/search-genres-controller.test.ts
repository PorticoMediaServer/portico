import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {SearchGenresController, type SearchGenreSlot} from '../src/screens/search/genres.ts';

const artwork = (id: string, role: 'backdrop' | 'poster') => ({kind: 'item', id,
  path: `${routePath('get_metadata_kind_id_art_role', {kind: 'item', id, role})}?v=${'a'.repeat(64)}&w=${role === 'backdrop' ? 1920 : 400}`});
const genre = (value: string, count: number, backdrop = true, covers = 2) => ({value, label: value, count,
  ...(backdrop ? {backdrop: artwork(value + '-backdrop', 'backdrop')} : {}),
  covers: Array.from({length: covers}, (_, index) => artwork(value + '-cover-' + index, 'poster'))});
const page = (genres: readonly unknown[], nextCursor?: string, revision = 'genre-only-opaque') => ({variant:'ready', queryId:'genre_query', revision, genres, ...(nextCursor ? {nextCursor} : {})});
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
function setup(handler: (path: string, signal?: AbortSignal) => Promise<unknown>, slot: SearchGenreSlot = 'backdrop', limit = 2) {
  let current = true, requests = 0;
  const parent = new AbortController();
  const controller = new SearchGenresController({api: {request: async<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    assert.equal(method, 'GET');assert.equal(body, undefined);requests++;return await handler(path, signal) as T;
  }}, isCurrent: () => current, signal: parent.signal, slot, limit});
  return {controller, parent, requests: () => requests, retire: () => {current = false;}};
}

test('genre factory is inert and requests exactly the authored server slot prefix', async () => {
  const paths: string[] = [];
  const owner = setup(async path => {paths.push(path); return {...page([genre('first', 90), genre('second', 80)]), hasGenres: true};});
  try {
    assert.equal(owner.requests(), 0);
    await owner.controller.load();
    const state = owner.controller.getSnapshot();
    assert.deepEqual(state.genres.map(({value, count}) => [value, count]), [['first', 90], ['second', 80]]);
    assert.equal(new URL(paths[0], 'https://local.invalid').searchParams.get('preview'), 'backdrop');
    assert.equal(new URL(paths[0], 'https://local.invalid').searchParams.get('limit'), '2');
    assert.equal(state.canContinue, false);assert(Object.isFrozen(state.genres));
    await owner.controller.next();assert.equal(owner.requests(), 1);
  } finally {owner.controller.dispose();}
});

test('phone requests the cover prefix without inferring backdrop eligibility', async () => {
  const owner = setup(async path => {
    assert.equal(new URL(path, 'https://local.invalid').searchParams.get('preview'), 'covers');
    return {...page([{...genre('science-fiction', 80, false), label: 'Sci-Fi & Fantasy'}]), hasGenres: true};
  }, 'covers');
  try {
    await owner.controller.load();
    assert.deepEqual(owner.controller.getSnapshot().genres.map(({value, label}) => [value, label]), [['science-fiction', 'Sci-Fi & Fantasy']]);
    assert.equal(owner.controller.getSnapshot().canContinue, false);
  } finally {owner.controller.dispose();}
});

test('a preview cannot silently resume a list cursor or accept a non-drawable row', async () => {
  for (const raw of [{...page([genre('first', 90)], 'unexpected'), hasGenres: true}, {...page([genre('missing', 80, false)]), hasGenres: true}]) {
    const owner = setup(async () => raw);
    try {
      await assert.rejects(owner.controller.load());
      assert.equal(owner.requests(), 1);
      assert.equal(owner.controller.getSnapshot().phase, 'error');
      assert.deepEqual(owner.controller.getSnapshot().genres, []);
    } finally {owner.controller.dispose();}
  }
});

test('principal retirement and parent abort refuse late reads and erase private genre state', async () => {
  for (const abort of [false, true]) {
    const response = deferred<unknown>();let signal: AbortSignal | undefined;
    const owner = setup(async (_path, provided) => {signal = provided;return response.promise;});
    const read = owner.controller.load();
    if (abort) {owner.parent.abort();assert.equal(signal?.aborted, true);} else owner.retire();
    response.resolve({...page([genre('private', 10)]), hasGenres: true});await assert.rejects(read, {name: 'AbortError'});
    assert.deepEqual(owner.controller.getSnapshot().genres, []);
    await assert.rejects(owner.controller.next(), {name: 'AbortError'});assert.equal(owner.requests(), 1);
    owner.controller.dispose();
  }
});

test('a cancelled older read cannot erase a newer genre response', async () => {
  const first = deferred<unknown>(), second = deferred<unknown>();let reads = 0;
  const owner = setup(async () => ++reads === 1 ? first.promise : second.promise);
  try {
    const old = assert.rejects(owner.controller.load(), {name: 'AbortError'}), current = owner.controller.load();
    second.resolve({...page([genre('current', 10)]), hasGenres: true});await current;
    first.resolve({...page([genre('old', 10)]), hasGenres: true});await old;
    assert.deepEqual(owner.controller.getSnapshot().genres.map(genre => genre.value), ['current']);
  } finally {owner.controller.dispose();}
});

test('only the current actual facet can capture the canonical video-title browse target', async () => {
  const owner = setup(async () => ({...page([genre('actual-value', 10)]), hasGenres: true}));
  try {
    await owner.controller.load();const actual = owner.controller.getSnapshot().genres[0];
    assert.deepEqual(owner.controller.browseTarget(actual), {libraryId: '*', pivot: 'video-titles', query: {variant:'predicate',field: 'genre', operator: 'equals', value: 'actual-value'}, sort: [{field: 'title', direction: 'asc'}]});
    assert.throws(() => owner.controller.browseTarget({...actual}), {name: 'AbortError'});
    owner.controller.cancel();assert.throws(() => owner.controller.browseTarget(actual), {name: 'AbortError'});
  } finally {owner.controller.dispose();}
});


test('All genres starts an independent ordinary directory and pages only on explicit Next', async () => {
  const paths: string[] = [];
  const pages = [{...page([genre('first', 10)]), hasGenres: true}, page([genre('first', 10), genre('without-art', 4, false)], 'next'), page([genre('later', 2, false)], 'more')];
  const owner = setup(async path => {paths.push(path);return pages.shift();}, 'backdrop', 1);
  try {
    await owner.controller.load(); const original = owner.controller.getSnapshot();
    await owner.controller.showAll(original);
    assert.equal(owner.requests(), 2);
    assert.equal(new URL(paths[1], 'https://local.invalid').searchParams.has('preview'), false);
    assert.deepEqual(owner.controller.getSnapshot().genres.map(x => x.value), ['first', 'without-art']);
    assert.equal(owner.controller.getSnapshot().canContinue, true);
    assert.throws(() => owner.controller.showAll(original), {name: 'AbortError'});
    await owner.controller.next();
    assert.equal(owner.requests(), 3);
    assert.deepEqual(owner.controller.getSnapshot().genres.map(x => x.value), ['later']);
    assert.equal(owner.controller.getSnapshot().all, true);
  } finally {owner.controller.dispose();}
});

test('an artless preview still exposes All genres and loads its true directory', async () => {
  let reads = 0;
  const owner = setup(async () => ++reads === 1 ? {...page([]), hasGenres: true} : page([genre('without-art', 4, false)]));
  try {
    await owner.controller.load(); const preview = owner.controller.getSnapshot();
    assert.equal(preview.genres.length, 0); assert.equal(preview.hasGenres, true);
    await owner.controller.showAll(preview);
    assert.deepEqual(owner.controller.getSnapshot().genres.map(x => x.value), ['without-art']);
    assert.equal(reads, 2);
  } finally {owner.controller.dispose();}
});
