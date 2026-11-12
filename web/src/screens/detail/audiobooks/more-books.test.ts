import test from 'node:test';
import assert from 'node:assert/strict';
import {createAudiobookBindings} from './bindings.ts';
import type {ScreenContext} from '../context.ts';
import {withQueries} from '../../../../../packages/client-core/test/fixtures/viewer-queries.ts';

// More audiobooks reads the rebuilt server's related titles: the `more_audiobooks` group of
// GET /v1/items/{id}/related (the library's other audiobooks, newest first, at most twenty, never
// the book's own series). One read, with no continuation.
const scope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'};
const book = (id: string, changes: Record<string, unknown> = {}) => ({id, kind: 'audiobook', title: id, libraryId: 'audio', images: {}, state: {}, addedAt: '2026-11-12T00:00:00Z', ...changes});
const related = (items: unknown[], extra: unknown[] = []) => ({groups: [...extra, {relation: 'more_audiobooks', items}]});
const seed = {id: 'seed', kind: 'audiobook', title: 'Seed', libraryId: 'audio', images: {}, state: {}, addedAt: '2026-11-12T00:00:00Z',
  versions: [{id: 'v1', parts: [{size: 1, streams: []}]}]};

function harness(read: (path: string, signal?: AbortSignal) => unknown) {
  let active = true;
  const paths: string[] = [];
  const context = withQueries(({api: {request: async (path: string, _method: unknown, _body: unknown, signal?: AbortSignal) => {
    const url = new URL(path, 'https://server.test');
    if (url.pathname === '/v1/items/seed') return seed;
    if (url.pathname.startsWith('/v1/libraries/')) return {id: 'audio', name: 'Audio', kind: 'audiobooks'};
    paths.push(path);return await read(path, signal);
  }}, identity: {viewer: scope}, scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: () => active,
  effects: {openEntry: () => {}, reportError: () => {}}}) as never) as unknown as ScreenContext;
  const binding = createAudiobookBindings(context, {libraryId: 'audio', view: 'book', entityId: 'seed'});
  return {binding, paths, retire: () => {active = false;}};
}

test('More audiobooks is the server\'s list in its own order, keeping only this library\'s other playable books', async () => {
  const h = harness(() => related([book('b2'), book('b1'), book('seed'), book('other', {libraryId: 'other'}), book('gone', {available: false}), {id: 'album', kind: 'album', title: 'Album', libraryId: 'audio', images: {}, state: {}, addedAt: ''}],
    [{relation: 'series_member', items: [book('in-series')]}]));
  await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  const first = await h.binding.moreBooks(page), pager = h.binding.moreBooksPager(page, first);
  assert.deepEqual(pager.getSnapshot().entries.map(entry => [entry.id, entry.kind]), [['b2', 'book'], ['b1', 'book']]);
  assert.deepEqual(h.paths, ['/v1/items/seed/related']);
  assert.equal(first.nextCursor, undefined, 'one read, no continuation');
  await pager.more();assert.equal(h.paths.length, 1, 'nothing more is read');
  pager.dispose();h.binding.dispose();
});

test('an audiobook with no others has no More audiobooks row', async () => {
  for (const answer of [{groups: []}, related([])]) {
    const h = harness(() => answer);
    await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
    assert.deepEqual((await h.binding.moreBooks(page)).entries, []);
    h.binding.dispose();
  }
});

test('a replaced authority cannot publish the row it was reading', async () => {
  let release!: (value: unknown) => void;
  const h = harness(() => new Promise(resolve => {release = resolve;}));
  await h.binding.load();const page = h.binding.service.getSnapshot().projection!;
  const pending = h.binding.moreBooks(page);
  await new Promise(resolve => setImmediate(resolve));
  h.retire();release(related([book('late')]));
  await assert.rejects(pending, {name: 'AbortError'});
  await assert.rejects(h.binding.moreBooks(page), {name: 'AbortError'});
  h.binding.dispose();
});
