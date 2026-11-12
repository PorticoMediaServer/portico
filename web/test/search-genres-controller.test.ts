import test from 'node:test';
import assert from 'node:assert/strict';
import {SearchGenresController, type SearchGenreSlot} from '../src/screens/search/genres.ts';

// The rebuilt server's answer (GET /v1/genres?counts=true): the whole list, by name, each with its count and cover.
const genre = (value: string, count: number, backdrop = true, posters?: number) => ({value, label: value, count,
  ...(backdrop ? {cover: {backdrop: `/v1/images/${value}-backdrop`}} : {}),
  ...(posters === undefined ? {} : {posters: Array.from({length: posters}, (_, index) => ({poster: `/v1/images/${value}-poster-${index}`}))})});
const page = (genres: readonly unknown[]) => ({genres});
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
  const owner = setup(async path => {paths.push(path); return page([genre('first', 90), genre('second', 80), genre('third', 70)]);});
  try {
    assert.equal(owner.requests(), 0);
    await owner.controller.load();
    const state = owner.controller.getSnapshot();
    assert.deepEqual(state.genres.map(({value, count}) => [value, count]), [['first', 90], ['second', 80]]);
    assert.equal(paths[0], '/v1/genres?counts=true');
    assert.equal(state.canContinue, false);assert(Object.isFrozen(state.genres));
    await owner.controller.next();assert.equal(owner.requests(), 1);
  } finally {owner.controller.dispose();}
});

test('the covers slot keeps the genres with two posters, without inferring them from a backdrop', async () => {
  const owner = setup(async () => page([{...genre('science-fiction', 80, false, 2), label: 'Sci-Fi & Fantasy'}, genre('drama', 10, true, 1)]), 'covers');
  try {
    await owner.controller.load();
    assert.deepEqual(owner.controller.getSnapshot().genres.map(({value, label}) => [value, label]), [['science-fiction', 'Sci-Fi & Fantasy']]);
    assert.equal(owner.controller.getSnapshot().canContinue, false);
  } finally {owner.controller.dispose();}
});

test('a preview never carries a list cursor and leaves out the genres without its art; an unreadable answer is an error', async () => {
  const owner = setup(async () => page([genre('missing', 80, false), genre('first', 90), genre('second', 70), genre('third', 60)]));
  try {
    await owner.controller.load();
    assert.deepEqual(owner.controller.getSnapshot().genres.map(genre => genre.value), ['first', 'second']);
    assert.equal(owner.controller.getSnapshot().canContinue, false);
  } finally {owner.controller.dispose();}
  const broken = setup(async () => ({items: []}));
  try {
    await assert.rejects(broken.controller.load());
    assert.equal(broken.controller.getSnapshot().phase, 'error');
    assert.deepEqual(broken.controller.getSnapshot().genres, []);
  } finally {broken.controller.dispose();}
});

test('principal retirement and parent abort refuse late reads and erase private genre state', async () => {
  for (const abort of [false, true]) {
    const response = deferred<unknown>();let signal: AbortSignal | undefined;
    const owner = setup(async (_path, provided) => {signal = provided;return response.promise;});
    const read = owner.controller.load();
    if (abort) {owner.parent.abort();assert.equal(signal?.aborted, true);} else owner.retire();
    response.resolve(page([genre('private', 10)]));await assert.rejects(read, {name: 'AbortError'});
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
    second.resolve(page([genre('current', 10)]));await current;
    first.resolve(page([genre('old', 10)]));await old;
    assert.deepEqual(owner.controller.getSnapshot().genres.map(genre => genre.value), ['current']);
  } finally {owner.controller.dispose();}
});

test('only the current actual facet can capture the canonical video-title browse target', async () => {
  const owner = setup(async () => page([genre('actual-value', 10)]));
  try {
    await owner.controller.load();const actual = owner.controller.getSnapshot().genres[0];
    assert.deepEqual(owner.controller.browseTarget(actual), {libraryId: '*', pivot: 'video-titles', query: {variant:'predicate',field: 'genre', operator: 'equals', value: 'actual-value'}, sort: [{field: 'title', direction: 'asc'}]});
    assert.throws(() => owner.controller.browseTarget({...actual}), {name: 'AbortError'});
    owner.controller.cancel();assert.throws(() => owner.controller.browseTarget(actual), {name: 'AbortError'});
  } finally {owner.controller.dispose();}
});


test('All genres starts an independent ordinary directory and pages only on explicit Next', async () => {
  const many = Array.from({length: 52}, (_, n) => genre(`g${String(n).padStart(2, '0')}`, 52 - n, n === 0));
  const owner = setup(async () => page(many), 'backdrop', 1);
  try {
    await owner.controller.load(); const original = owner.controller.getSnapshot();
    assert.deepEqual(original.genres.map(x => x.value), ['g00']);
    await owner.controller.showAll(original);
    assert.equal(owner.requests(), 2);
    assert.equal(owner.controller.getSnapshot().genres.length, 50, 'every genre, art or none, fifty at a time');
    assert.equal(owner.controller.getSnapshot().canContinue, true);
    assert.throws(() => owner.controller.showAll(original), {name: 'AbortError'});
    await owner.controller.next();
    assert.equal(owner.requests(), 3);
    assert.deepEqual(owner.controller.getSnapshot().genres.map(x => x.value), ['g50', 'g51']);
    assert.equal(owner.controller.getSnapshot().all, true);
  } finally {owner.controller.dispose();}
});

test('an artless preview still exposes All genres and loads its true directory', async () => {
  let reads = 0;
  const owner = setup(async () => {reads++; return page([genre('without-art', 4, false)]);});
  try {
    await owner.controller.load(); const preview = owner.controller.getSnapshot();
    assert.equal(preview.genres.length, 0); assert.equal(preview.hasGenres, true);
    await owner.controller.showAll(preview);
    assert.deepEqual(owner.controller.getSnapshot().genres.map(x => x.value), ['without-art']);
    assert.equal(reads, 2);
  } finally {owner.controller.dispose();}
});
