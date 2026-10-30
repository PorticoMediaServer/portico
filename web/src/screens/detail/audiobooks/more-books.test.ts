import test from 'node:test';
import assert from 'node:assert/strict';
import {createAudiobookBindings} from './bindings.ts';
import type {ScreenContext} from '../context.ts';

const scope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
const book = (id: string, changes: Record<string, unknown> = {}) => ({id, libraryId: 'audio', kind: 'book', title: id, available: true, navigation: {view: 'book', entityId: id}, ...changes});
const response = (entries: unknown[], nextCursor?: string, changes: Record<string, unknown> = {}) => ({scope, data: {
  itemId: 'seed', libraryId: 'audio', bySound: false, revision: {catalog: 1, viewer: 1}, entries,
  ...(nextCursor ? {hasMore: true, nextCursor} : {hasMore: false}), ...changes,
}});
function harness(read: (path: string, signal?: AbortSignal) => unknown) {
  let active = true;
  const paths: string[] = [];
  const projection = {scope: {serverId: 'server', libraryId: 'audio', libraryKind: 'audiobook', view: 'book', entityId: 'seed', viewerFence: 'fence'},
    revision: {catalog: 1, viewer: 1}, query: {sort: '', direction: '', category: '', q: '', limit: 40},
    entity: book('seed'), heading: {key: 'entity.title', fallback: 'Seed'}, sections: []};
  const context = {api: {request: async (path: string, _method: unknown, _body: unknown, signal?: AbortSignal) => {
    if (!path.includes('/similar')) return projection;
    paths.push(path);return await read(path, signal);
  }}, identity: {viewer: scope}, scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => active,
  effects: {openEntry: () => {}, reportError: () => {}}} as unknown as ScreenContext;
  const binding = createAudiobookBindings(context, {libraryId: 'audio', view: 'book', entityId: 'seed'});
  return {binding, paths, retire: () => {active = false;}};
}

test('More audiobooks continues past twelve, filters every source page, and deduplicates in server order', async () => {
  const h = harness(path => path.includes('cursor=tail')
    ? response([book('b11'), book('b12'), book('other', {libraryId: 'other'}), book('gone', {missing: true}), book('unavailable', {available: false}), book('album', {kind: 'album', navigation: {view: 'album', entityId: 'album'}}), book('b13')])
    : response(Array.from({length: 12}, (_, i) => book(`b${i}`)), 'tail'));
  await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  const first = await h.binding.moreBooks(page), pager = h.binding.moreBooksPager(page, first);
  assert.equal(h.paths.length, 1, 'constructing the pager does not read ahead');
  assert.equal(pager.getSnapshot().entries.length, 12);
  await pager.more();
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), Array.from({length: 14}, (_, i) => `b${i}`));
  assert.equal(h.paths[0], '/v1/items/seed/similar?limit=12');
  assert.equal(h.paths[1], '/v1/items/seed/similar?limit=24&cursor=tail');
  await pager.more();assert.equal(h.paths.length, 2, 'no request after exhaustion');
  pager.dispose();h.binding.dispose();
});

test('empty filtered first and intermediate pages preserve demand without eagerly draining the source', async () => {
  const h = harness(path => path.includes('cursor=second') ? response([book('beyond')])
    : path.includes('cursor=first') ? response([book('gone', {missing: true})], 'second')
    : response([book('other', {libraryId: 'other'})], 'first'));
  await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  const first = await h.binding.moreBooks(page), pager = h.binding.moreBooksPager(page, first);
  assert.deepEqual(first.entries, []);assert.equal(first.nextCursor, 'first');assert.equal(h.paths.length, 1);
  await pager.more();assert.deepEqual(pager.getSnapshot().entries, []);assert.equal(pager.getSnapshot().cursor, 'second');
  assert.equal(h.paths.length, 2, 'one source page per demand even when empty');
  await pager.more();assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['beyond']);
  assert.equal(pager.getSnapshot().cursor, undefined);pager.dispose();h.binding.dispose();
});

for (const [label, change] of Object.entries({catalog: {revision: {catalog: 2, viewer: 1}}, viewer: {revision: {catalog: 1, viewer: 2}}, library: {libraryId: 'other'}, basis: {bySound: true}})) {
  test(`More audiobooks rejects a moved ${label} fence on its first page and continuation`, async () => {
    const h = harness(path => response(path.includes('cursor=') ? [] : [book('b1')], 'tail', path.includes('cursor=') ? change : {}));
    await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
    const first = await h.binding.moreBooks(page), pager = h.binding.moreBooksPager(page, first);
    await pager.more();assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['b1']);
    assert.equal(pager.getSnapshot().error?.code, 'stale_continuation');assert.equal(pager.getSnapshot().cursor, undefined);
    pager.dispose();h.binding.dispose();
    const initial = harness(() => response([], undefined, change));await initial.binding.load();
    await assert.rejects(initial.binding.moreBooks(initial.binding.service.getSnapshot().projection!), {code: 'stale_continuation'});
    initial.binding.dispose();
  });
}

test('continuations reject another viewer envelope and retain only previously admitted books', async () => {
  const h = harness(path => path.includes('cursor=') ? {...response([book('wrong')]), scope: {...scope, profileId: 'other'}} : response([book('b1')], 'tail'));
  await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  const pager = h.binding.moreBooksPager(page, await h.binding.moreBooks(page));
  await pager.more();assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['b1']);assert.ok(pager.getSnapshot().error);
  pager.dispose();h.binding.dispose();
});

test('one demand at a time and disposal cancels a pending continuation', async () => {
  let release!: (value: unknown) => void, signal: AbortSignal | undefined;
  const h = harness((path, input) => path.includes('cursor=') ? (signal = input, new Promise(resolve => {release = resolve;})) : response([book('b1')], 'tail'));
  await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  const pager = h.binding.moreBooksPager(page, await h.binding.moreBooks(page));
  const pending = pager.more();await pager.more();assert.equal(h.paths.length, 2);
  pager.dispose();assert.equal(signal?.aborted, true);release(response([book('late')]));await pending;
  assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['b1']);h.binding.dispose();
});

for (const replacement of ['page', 'authority'] as const) {
  test(`a replaced ${replacement} cannot publish an in-flight source page`, async () => {
    let release!: (value: unknown) => void;
    const h = harness(path => path.includes('cursor=') ? new Promise(resolve => {release = resolve;}) : response([book('b1')], 'tail'));
    await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
    const pager = h.binding.moreBooksPager(page, await h.binding.moreBooks(page));
    const pending = pager.more();
    if (replacement === 'page') await h.binding.load(); else h.retire();
    release(response([book('late')]));await pending;
    assert.deepEqual(pager.getSnapshot().entries.map(e => e.id), ['b1']);
    await assert.rejects(h.binding.moreBooks(page), {name: 'AbortError'});
    pager.dispose();h.binding.dispose();
  });
}
