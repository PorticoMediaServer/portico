import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {routePath} from '../../packages/contracts/src/index.ts';
import {createMusicMixesController} from '../src/screens/browse/music-mixes.ts';
import {retainMusicMixes} from '../src/screens/browse/music-mixes-owner.ts';
import {bindMusicMixes} from '../src/screens/browse/music-mixes-binding.ts';
import type {ShellAssemblyContext} from '../src/shell/bootstrap';
import type {QueueHeader} from '@core/playback-v1/types.ts';

const principal = {serverId: 'unit-server', authority: 'local' as const, accountId: 'unit-account', profileId: 'unit-profile'};
const operationId = '11111111-1111-4111-8111-111111111111';
const receipt = () => ({serverId: principal.serverId, operationId, playlistId: 'unit-playlist', revision: 1, deleted: false, viewerFence: 'saved-only'});
const queue = {id: 'unit-queue', revision: '1', total: 12, repeat: 'off', segments: []} as QueueHeader;
const denied = (status: number) => Object.assign(new Error('unit rejected'), {status});
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
function mix(id = 'unit-mix', artist = false) {
  return {id, nameSource: artist ? {kind: 'artist', artistId: 'unit-artist'} : {kind: 'style', tag: 'rock', model: 'musicnn-mtt', version: 'unit-model'},
    covers: Array.from({length: 3}, (_, index) => ({album: {id: 'unit-album-' + index, name: 'Stored album ' + index},
      posterUrl: routePath('get_metadata_kind_id_art_role', {kind: 'album', id: 'unit-album-' + index, role: 'poster'})})),
    artists: [{id: 'unit-artist', name: 'Stored artist'}], more: true, songCount: 12, tint: '#28587a'};
}
const page = (id = 'unit-mix') => ({scope: {...principal}, data: {mixes: [mix(id), mix(id + '-artist', true)]}});
function setup(handler?: (path: string, method: string, body: unknown, signal?: AbortSignal) => Promise<unknown>,
  options: {requestId?: () => Promise<string>; t?: typeof defaultI18n.t} = {}) {
  const life = new AbortController(), calls: {path: string; method: string; body: unknown; signal?: AbortSignal}[] = [];
  let active = true;
  const api = {async request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) {
    calls.push({path, method, body, signal});return (handler ? await handler(path, method, body, signal) : method === 'GET' ? page() : receipt()) as T;
  }};
  const source = createMusicMixesController({api, principal, requestId: options.requestId ?? (async () => operationId),
    signal: life.signal, isCurrent: () => active, t: options.t ?? defaultI18n.t});
  return {source, api, calls, life, authority: (value: boolean) => {active = value;}};
}
const caller = () => ({signal: new AbortController().signal, isCurrent: () => true});

test('one generated read preserves server order/art/counts and exposes only canonical localized names', async () => {
  const f = setup();assert.equal(f.calls.length, 0);
  try {
    const a = f.source.load(), b = f.source.load();assert.strictEqual(a, b);await a;
    assert.equal(f.calls.length, 1);assert.equal(f.calls[0].path, routePath('get_music_mixes'));
    const {rows} = f.source.getSnapshot();assert.deepEqual(rows.map(row => row.name), ['Rock mix', 'Stored artist mix']);
    assert.deepEqual(rows.map(row => row.mix.id), ['unit-mix', 'unit-mix-artist']);
    assert.equal(rows[0].mix.covers.length, 3);assert.equal(rows[0].mix.artists.length, 1);assert.equal(rows[0].mix.more, true);
    assert.equal(rows[0].mix.songCount, 12);assert.equal(rows[0].mix.tint, '#28587a');assert.equal(Object.hasOwn(rows[0].mix, 'keys'), false);
    assert.equal(Object.isFrozen(rows[0].mix.covers[0].album), true);
    assert.equal(f.source.contains({...rows[0]}), false);
  } finally {f.source.dispose();}
});

test('all four captured principal fields fence the response without borrowing a viewerFence', async () => {
  for (const field of ['serverId', 'authority', 'accountId', 'profileId'] as const) {
    const f = setup(async () => {const data = page();data.scope = {...data.scope, [field]: 'other'};return data;});
    await assert.rejects(f.source.load());assert.equal(f.source.getSnapshot().phase, 'retired');assert.deepEqual(f.source.getSnapshot().rows, []);
    assert.equal(f.calls.length, 1);f.source.dispose();
  }
});

test('Save uses the displayed canonical name and one captured operation receipt; no duplicate copy is dispatched', async () => {
  const held = deferred<unknown>(), f = setup(async (_path, method) => method === 'GET' ? page() : held.promise);
  try {
    await f.source.load();const row = f.source.getSnapshot().rows[1];
    const save = f.source.save(row, caller());await assert.rejects(f.source.save(row, caller()), {name: 'AbortError'});
    await setImmediate();
    assert.equal(f.calls.length, 2);assert.equal(f.calls[1].path, routePath('post_music_mixes_id_playlist', {id: row.mix.id}));
    assert.deepEqual(f.calls[1].body, {operationId, name: row.name});
    held.resolve(receipt());const result = await save;assert.equal(result?.playlistId, 'unit-playlist');assert.equal(result?.viewerFence, 'saved-only');
  } finally {held.resolve(receipt());f.source.dispose();}
});

test('stale Save or Play 404 only refreshes this row and never retries a playlist copy', async () => {
  for (const action of ['save', 'play']) {
    let reads = 0;
    const f = setup(async (_path, method) => {if (method === 'GET') return page(++reads === 1 ? 'old' : 'fresh');throw denied(404);});
    try {
      await f.source.load();const row = f.source.getSnapshot().rows[0];
      const result = action === 'save' ? await f.source.save(row, caller())
        : await f.source.play(row, {playSegments: async () => {throw denied(404);}}, caller());
      assert.equal(result, undefined);assert.equal(reads, 2);assert.equal(f.source.getSnapshot().rows[0].mix.id, 'fresh');
      assert.equal(f.source.contains(row), false);assert.equal(f.calls.filter(call => call.method === 'POST').length, action === 'save' ? 1 : 0);
    } finally {f.source.dispose();}
  }
});

test('an unknown copy outcome retains its original operation across refresh and never creates a replacement receipt', async () => {
  let copies = 0, ids = 0;
  const f = setup(async (_path, method) => {
    if (method === 'GET') return page();
    if (++copies === 1) throw new TypeError('unit connection interrupted');
    return receipt();
  }, {requestId: async () => {ids++;return operationId;}});
  try {
    await f.source.load();await assert.rejects(f.source.save(f.source.getSnapshot().rows[0], caller()));
    await f.source.load();await f.source.save(f.source.getSnapshot().rows[0], caller());
    const writes = f.calls.filter(call => call.method === 'POST');assert.equal(ids, 1);assert.equal(writes.length, 2);
    assert.deepEqual(writes[0].body, writes[1].body);
  } finally {f.source.dispose();}
});

test('Play sends only the genuine mix container through the original caller lifetime and same port', async () => {
  const held = deferred<QueueHeader>(), f = setup();let active = true;
  let captured: {signal?: AbortSignal; isCurrent?: () => boolean} | undefined;
  const port = {async playSegments(segments: unknown, options?: {signal?: AbortSignal; isCurrent?: () => boolean}) {
    assert.strictEqual(this, port);assert.deepEqual(segments, [{source: {container: {kind: 'mix', id: 'unit-mix'}}}]);
    captured = options;return held.promise;
  }};
  try {
    await f.source.load();const playing = f.source.play(f.source.getSnapshot().rows[0], port, {signal: caller().signal, isCurrent: () => active});
    assert.equal(captured?.isCurrent?.(), true);active = false;assert.equal(captured?.isCurrent?.(), false);
    held.resolve(queue);await assert.rejects(playing, {name: 'AbortError'});assert.equal(f.calls.length, 1);
  } finally {held.resolve(queue);f.source.dispose();}
});

test('original signal retirement during ID generation refuses before any POST', async () => {
  const id = deferred<string>(), f = setup(undefined, {requestId: () => id.promise}), navigation = new AbortController();
  try {
    await f.source.load();const save = f.source.save(f.source.getSnapshot().rows[0], {signal: navigation.signal, isCurrent: () => true});
    navigation.abort();id.resolve(operationId);await assert.rejects(save, {name: 'AbortError'});assert.equal(f.calls.length, 1);
  } finally {id.resolve(operationId);f.source.dispose();}
});

test('retired authority discards a late copy receipt and cannot revive on Back', async () => {
  const held = deferred<unknown>(), f = setup(async (_path, method) => method === 'GET' ? page() : held.promise);
  try {
    await f.source.load();const row = f.source.getSnapshot().rows[0], save = f.source.save(row, caller());
    await setImmediate();f.authority(false);
    assert.equal(f.source.getSnapshot().phase, 'retired');assert.equal(f.calls[1].signal?.aborted, true);
    f.authority(true);held.resolve(receipt());await assert.rejects(save, {name: 'AbortError'});
    assert.deepEqual(f.source.getSnapshot().rows, []);assert.equal(f.source.contains(row), false);assert.equal(f.calls.length, 2);
  } finally {held.resolve(receipt());f.source.dispose();}
});

test('relabel uses the same formatter and retires old-language handles without another server read', async () => {
  let language = 'one';
  const f = setup(undefined, {t: (key, values) => (key === 'music.mix.name' ? language + ':' : '') + defaultI18n.t(key, values)});
  try {
    await f.source.load();const old = f.source.getSnapshot().rows[0];language = 'two';f.source.relabel();
    const row = f.source.getSnapshot().rows[0];assert.equal(row.name, 'two:Rock mix');assert.equal(f.source.contains(old), false);
    await f.source.save(row, caller());assert.deepEqual(f.calls[1].body, {operationId, name: row.name});assert.equal(f.calls.length, 2);
  } finally {f.source.dispose();}
});

test('Home and MusicDiscover retain one lazy profile source and erase it when the last lease closes', async () => {
  const f = setup(), preferences = new Set<() => void>();
  const context = {api: f.api, session: () => ({sessionFamilyId: 'unit-family', viewer: {...principal, role: 'member'}}), isCurrent: () => true,
    preferences: {subscribe(listener: () => void) {preferences.add(listener);return () => {preferences.delete(listener);};}}} as unknown as ShellAssemblyContext;
  const home = retainMusicMixes(context), discover = retainMusicMixes(context);
  try {
    assert.strictEqual(home.source, discover.source);assert.equal(f.calls.length, 0);assert.equal(preferences.size, 1);
    await Promise.all([home.source.load(), discover.source.load()]);assert.equal(f.calls.length, 1);
    home.release();assert.equal(discover.source.getSnapshot().phase, 'ready');home.release();
    discover.release();assert.equal(discover.source.getSnapshot().phase, 'retired');assert.equal(preferences.size, 0);
  } finally {home.release();discover.release();f.source.dispose();}
});

test('generated count/art validation and mismatched receipts remain closed; denied authority erases old rows', async () => {
  for (const invalid of ['count', 'covers', 'receipt', 'denied']) {
    const f = setup(async (_path, method) => {
      if (method !== 'GET') {if (invalid === 'denied') throw denied(403);return {...receipt(), operationId: 'wrong-operation'};}
      const data = page();if (invalid === 'count') data.data.mixes[0].songCount = 201;
      if (invalid === 'covers') data.data.mixes[0].covers.pop();return data;
    });
    try {
      if (invalid === 'count' || invalid === 'covers') {await assert.rejects(f.source.load());assert.equal(f.source.getSnapshot().rows.length, 0);}
      else {await f.source.load();await assert.rejects(f.source.save(f.source.getSnapshot().rows[0], caller()));
        if (invalid === 'denied') assert.equal(f.source.getSnapshot().phase, 'retired');}
    } finally {f.source.dispose();}
  }
});

test('a view binding retires only its own command and subscription while the shared profile source remains usable', async () => {
  const held = deferred<QueueHeader>(), f = setup(), navigation = new AbortController();
  let observed: {signal?: AbortSignal; isCurrent?: () => boolean} | undefined, notices = 0;
  const runtime = {cataloguePlayback: () => ({playSegments: async (_segments: unknown, options?: {signal?: AbortSignal; isCurrent?: () => boolean}) => {observed = options;return held.promise;}})};
  const binding = bindMusicMixes(f.source, {signal: navigation.signal, isCurrent: () => true}, () => runtime);
  try {
    await binding.load();const row = binding.getSnapshot().rows[0];assert.equal(binding.canPlay(row), true);
    const off = binding.subscribe(() => notices++), playing = binding.play(row);assert.equal(observed?.isCurrent?.(), true);
    navigation.abort();assert.equal(observed?.signal?.aborted, true);assert.equal(observed?.isCurrent?.(), false);
    assert.equal(binding.getSnapshot().phase, 'retired');assert.equal(f.source.getSnapshot().phase, 'ready');
    const before = notices;f.source.relabel();await f.source.load();assert.equal(notices, before, 'retired view subscription is detached');
    held.resolve(queue);await assert.rejects(playing, {name: 'AbortError'});off();
  } finally {held.resolve(queue);binding.dispose();f.source.dispose();}
});

test('a late same-runtime replacement invalidates only the original Play; an absent executor is genuinely unavailable', async () => {
  const held = deferred<QueueHeader>(), f = setup(), life = new AbortController();let observed: {isCurrent?: () => boolean} | undefined;
  const first = {cataloguePlayback: () => ({playSegments: async (_segments: unknown, options?: {isCurrent?: () => boolean}) => {observed = options;return held.promise;}})};
  let runtime: typeof first | undefined;
  const binding = bindMusicMixes(f.source, {signal: life.signal, isCurrent: () => true}, () => runtime);
  try {
    await binding.load();const row = binding.getSnapshot().rows[0];assert.equal(binding.canPlay(row), false);
    await assert.rejects(binding.play(row), {code: 'music_mix_playback_unavailable'});assert.equal(f.calls.length, 1);
    runtime = first;const playing = binding.play(row);assert.equal(observed?.isCurrent?.(), true);
    runtime = {cataloguePlayback: first.cataloguePlayback};assert.equal(observed?.isCurrent?.(), false);
    held.resolve(queue);await assert.rejects(playing, {name: 'AbortError'});
  } finally {held.resolve(queue);binding.dispose();f.source.dispose();}
});
