import test from 'node:test';
import assert from 'node:assert/strict';
import {createDetailBindings} from '../src/screens/detail/bindings.ts';
import {createWatchBodyBindings} from '../src/screens/detail/watch-body.ts';
import type {ScreenContext} from '../src/screens/detail/context.ts';

function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});
  return {promise, resolve, reject};
}
const media = (id = 'movie', kind = 'movie') => ({id, libraryId: 'library', kind, title: id, durationMs: 1000, progressMs: 0, available: true});
const related = (kind: string) => ({version: 1, libraryId: 'library', libraryName: 'Library', rows: [{
  id: 'genre:local:genre', relation: 'genre', provider: 'local', evidenceId: 'genre', heading: 'Related titles',
  entries: [{id: 'other', libraryId: 'library', kind: kind === 'song' ? 'album' : 'movie', title: 'Other', available: true,
    navigation: {view: kind === 'song' ? 'album' : 'item', entityId: 'other'}}],
}]});
const page = (id = 'movie', kind = 'movie', embedded = false) => ({
  scope: {serverId: 'server', libraryId: 'library', itemId: id, viewerFence: 'fence'}, revision: {catalog: 1, viewer: 1}, item: media(id, kind),
  personal: {watchlisted: false, favorite: false, rating: null, revision: 1, watched: false, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: []},
  actions: [], metadata: {status: 'unavailable', ratings: [], genres: [], credits: []}, ...(embedded ? {related: related(kind)} : {}),
});
function setup(handler: (path: string, signal?: AbortSignal, body?: unknown) => Promise<unknown>) {
  const paths: string[] = []; let current = true;
  const context = {
    api: {
      request: async (path: string, _method?: string, body?: unknown, signal?: AbortSignal) => {paths.push(path);return handler(path, signal, body);},
      requestRaw: async () => {throw new Error('No raw request expected');},
    },
    scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, isCurrent: () => current,
    preferences: {getSnapshot: () => ({}), subscribe: () => () => {}},
    effects: {openEntry: () => {}, openSettings: () => {}, play: async () => {}, reportError: () => {}},
  } as unknown as ScreenContext;
  return {context, paths, retire: () => {current = false;}};
}

test('the bare movie route reuses its item kind and never computes embedded recommendations on success', async () => {
  // A bare /item route learns the title's library and kind from its own detail route, which serves
  // Reading titles too; the timed-media item read does not (E-web-23).
  const owner = setup(async path => path.includes('/recommendations?') ? {itemId: 'movie', rows: [], revision: {catalog: 1, viewer: 1}} : page());
  const binding = createDetailBindings(owner.context);
  try {
    await binding.select({itemId: 'movie'});
    await binding.readRecommendations(binding.service.getSnapshot().data!);
    assert.deepEqual(owner.paths, ['/v1/items/movie/detail?related=none', '/v1/items/movie/detail?related=none', '/v1/items/movie/recommendations?limit=12']);
    assert.equal(binding.recommendations.getSnapshot().phase, 'ready');
    assert.deepEqual(binding.recommendations.getSnapshot().data?.rows, [], 'an empty successful full read is not a failure');
    await binding.service.revalidate();
    assert.equal(owner.paths.at(-1), '/v1/items/movie/detail?related=none', 'freshness reads retain the selected computation mode');
  } finally {binding.dispose();}
});

test('a known movie target uses its kind hint without an extra item lookup, including the retained Watch body', async () => {
  const owner = setup(async () => page());
  const watch = createWatchBodyBindings(owner.context, {libraryId: 'library', itemId: 'movie', kind: 'movie'});
  try {
    await watch.load();
    assert.deepEqual(owner.paths, ['/v1/items/movie/detail?related=none']);
    assert.equal(watch.body.kind, 'title');
    assert.equal(watch.body.binding.service.getSnapshot().phase, 'ready');
  } finally {watch.dispose();}
});

for (const bare of [true, false]) {
  test(`a ${bare ? 'bare' : 'known-library'} nonmovie target keeps embedded rows without another detail read`, async () => {
    const owner = setup(async () => page('song', 'song', true));
    const binding = createDetailBindings(owner.context);
    try {
      await binding.select(bare ? {itemId: 'song'} : {itemId: 'song', libraryId: 'library', kind: 'song'});
      const data = binding.service.getSnapshot().data!;
      await binding.readRecommendations(data);
      assert.equal(data.related?.rows[0]?.entries[0]?.kind, 'album');
      assert.deepEqual(owner.paths, [...(bare ? ['/v1/items/song/detail?related=none'] : []), '/v1/items/song/detail?related=all']);
    } finally {binding.dispose();}
  });
}

test('an unhinted known-library target keeps the embedded fallback and does not look up its kind', async () => {
  const owner = setup(async path => {if (path.includes('/recommendations?')) throw new TypeError('offline');return page('movie', 'movie', true);});
  const binding = createDetailBindings(owner.context);
  try {
    await binding.select({libraryId: 'library', itemId: 'movie'});
    const data = binding.service.getSnapshot().data!;
    await binding.readRecommendations(data);
    assert.strictEqual(binding.service.getSnapshot().data, data);
    assert.equal(data.related?.rows[0]?.entries[0]?.id, 'other');
    assert.deepEqual(owner.paths, ['/v1/items/movie/detail?related=all', '/v1/items/movie/recommendations?limit=12']);
  } finally {binding.dispose();}
});

test('a stale movie hint cannot remove the actual nonmovie embedded fallback', async () => {
  const owner = setup(async path => page('song', 'song', path.endsWith('related=all')));
  const binding = createDetailBindings(owner.context);
  try {
    await binding.select({libraryId: 'library', itemId: 'song', kind: 'movie'});
    const data = binding.service.getSnapshot().data!;
    await binding.readRecommendations(data);
    assert.equal(data.related?.rows[0]?.entries[0]?.kind, 'album');
    assert.deepEqual(owner.paths, ['/v1/items/song/detail?related=none', '/v1/items/song/detail?related=all']);
  } finally {binding.dispose();}
});

for (const invalidRevision of [false, true]) {
  test(`a ${invalidRevision ? 'revision-mismatched' : 'failed'} full recommendation read falls back to related=all once`, async () => {
    const owner = setup(async path => {
      if (path.includes('/recommendations?')) {
        if (!invalidRevision) throw new TypeError('offline');
        return {itemId: 'movie', rows: [], revision: {catalog: 2, viewer: 1}};
      }
      return page('movie', 'movie', path.endsWith('related=all'));
    });
    const binding = createDetailBindings(owner.context);
    try {
      await binding.select({libraryId: 'library', itemId: 'movie', kind: 'movie'});
      await binding.readRecommendations(binding.service.getSnapshot().data!);
      const fallback = binding.service.getSnapshot().data!;
      assert.equal(binding.recommendations.getSnapshot().phase, 'error');
      assert.equal(fallback.related?.rows[0]?.entries[0]?.id, 'other');
      await binding.readRecommendations(fallback);
      assert.deepEqual(owner.paths, ['/v1/items/movie/detail?related=none', '/v1/items/movie/recommendations?limit=12', '/v1/items/movie/detail?related=all']);
      await binding.service.revalidate();
      assert.equal(owner.paths.at(-1), '/v1/items/movie/detail?related=all');
    } finally {binding.dispose();}
  });
}

for (const retirement of ['selection', 'cancel', 'authority', 'dispose'] as const) {
  test(`a late failed recommendation read after ${retirement} cannot start a fallback or replace the title`, async () => {
    const pending = deferred<unknown>();
    const owner = setup(async path => path.includes('/recommendations?') ? pending.promise : page(path.includes('/second/') ? 'second' : 'movie'));
    const binding = createDetailBindings(owner.context);
    try {
      await binding.select({libraryId: 'library', itemId: 'movie', kind: 'movie'});
      const reading = binding.readRecommendations(binding.service.getSnapshot().data!);
      if (retirement === 'selection') await binding.select({libraryId: 'library', itemId: 'second', kind: 'movie'});
      else if (retirement === 'cancel') binding.cancel();
      else if (retirement === 'authority') owner.retire();
      else binding.dispose();
      const count = owner.paths.length;
      pending.reject(new TypeError('late failure'));await reading;
      assert.equal(owner.paths.length, count);
      assert.equal(owner.paths.some(path => path.endsWith('related=all')), false);
      if (retirement === 'selection') assert.equal(binding.service.getSnapshot().data?.item.id, 'second');
    } finally {binding.dispose();}
  });
}

test('cancelling an in-flight embedded fallback aborts it and ignores its late result', async () => {
  const pending = deferred<unknown>();let signal: AbortSignal | undefined;
  const owner = setup(async (path, active) => {
    if (path.includes('/recommendations?')) throw new TypeError('offline');
    if (path.endsWith('related=all')) {signal = active;return pending.promise;}
    return page();
  });
  const binding = createDetailBindings(owner.context);
  try {
    await binding.select({libraryId: 'library', itemId: 'movie', kind: 'movie'});
    const reading = binding.readRecommendations(binding.service.getSnapshot().data!);
    for (let i = 0; i < 8 && !signal; i++) await Promise.resolve();
    assert.equal(signal?.aborted, false);
    binding.cancel();assert.equal(signal?.aborted, true);
    pending.resolve(page('movie', 'movie', true));await reading;
    assert.equal(binding.service.getSnapshot().phase, 'idle');
    assert.equal(binding.service.getSnapshot().data, null);
  } finally {binding.dispose();}
});

test('a failed recommendation read cannot cancel an outstanding personal-state write to start its fallback', async () => {
  const recommendation = deferred<unknown>(), receipt = deferred<unknown>();
  let write: Readonly<{signal?: AbortSignal; operationId: string}> | undefined;
  const owner = setup(async (path, signal, body) => {
    if (path.includes('/recommendations?')) return recommendation.promise;
    if (path.endsWith('/personal-state')) {write = {signal, operationId: (body as {operationId: string}).operationId};return receipt.promise;}
    return {...page(), actions: [{id: 'watchlist', labelKey: 'title.watchlist', enabled: true}]};
  });
  const binding = createDetailBindings(owner.context);
  try {
    await binding.select({libraryId: 'library', itemId: 'movie', kind: 'movie'});
    const reading = binding.readRecommendations(binding.service.getSnapshot().data!);
    const mutation = binding.service.mutate({action: 'watchlist', value: true});
    for (let i = 0; i < 8 && !write; i++) await Promise.resolve();
    assert.ok(write);
    recommendation.reject(new TypeError('offline'));await Promise.resolve();await Promise.resolve();
    assert.equal(write.signal?.aborted, false);
    assert.equal(binding.service.getSnapshot().pending.length, 1);
    assert.equal(owner.paths.some(path => path.endsWith('related=all')), false);
    receipt.resolve({operationId: write.operationId, serverId: 'server', libraryId: 'library', itemId: 'movie', viewerFence: 'fence',
      current: {...page().personal, revision: 2, watchlisted: true}});
    await mutation;await reading;
    assert.equal(binding.service.getSnapshot().data?.personal.watchlisted, true);
  } finally {binding.dispose();}
});

test('explicitly dismissing a failed personal command releases the waiting embedded fallback', async () => {
  const recommendation = deferred<unknown>(), write = deferred<unknown>();
  const owner = setup(async path => {
    if (path.includes('/recommendations?')) return recommendation.promise;
    if (path.endsWith('/personal-state')) return write.promise;
    return {...page('movie', 'movie', path.endsWith('related=all')), actions: [{id: 'watchlist', labelKey: 'title.watchlist', enabled: true}]};
  });
  const binding = createDetailBindings(owner.context);
  try {
    await binding.select({libraryId: 'library', itemId: 'movie', kind: 'movie'});
    const reading = binding.readRecommendations(binding.service.getSnapshot().data!);
    const mutation = binding.service.mutate({action: 'watchlist', value: true});
    for (let i = 0; i < 8 && !owner.paths.includes('/v1/items/movie/personal-state'); i++) await Promise.resolve();
    recommendation.reject(new TypeError('offline'));write.reject(new TypeError('write reply lost'));
    await mutation;
    assert.equal(binding.service.getSnapshot().pending[0]?.phase, 'retry-required');
    assert.equal(owner.paths.some(path => path.endsWith('related=all')), false);
    binding.service.dismissMutation();await reading;
    assert.equal(binding.service.getSnapshot().data?.related?.rows[0]?.entries[0]?.id, 'other');
    assert.equal(owner.paths.filter(path => path.endsWith('related=all')).length, 1);
  } finally {binding.dispose();}
});
