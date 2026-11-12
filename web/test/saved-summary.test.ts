import test from 'node:test';
import assert from 'node:assert/strict';
import {createSavedRuntime} from '../src/screens/saved/runtime.ts';

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};
const summary = {serverId: 'server', viewerFence: 'viewer', revision: {catalog: 1, viewer: 2}, myList: {posters: 2000, squares: 300}, favorites: {posters: 40, squares: 4}, playlists: 11, collections: 10, savedViews: 6};

/** The rebuilt server's answers that make up `summary`: the lists, and one counted read per shape of My List and Favorites. */
function answer(path: string, value = summary): unknown {
  const url = new URL(path, 'https://x');
  if (url.pathname === '/v1/lists') return {items: [...Array(value.playlists).fill({kind: 'playlist'}), ...Array(value.collections).fill({kind: 'collection'}), ...Array(value.savedViews).fill({kind: 'saved_view'})]};
  const counts = url.searchParams.has('inList') ? value.myList : value.favorites;
  return {items: [], total: url.searchParams.get('kind')?.startsWith('artist') ? counts.squares : counts.posters};
}
const totals = (value: unknown) => {const {revision: _r, viewerFence: _v, serverId: _s, ...rest} = value as typeof summary; return rest;};

test('directory totals come from the lists and counted reads, independently of resident resource pages', async () => {
  const paths: string[] = [];
  const runtime = createSavedRuntime({scope, isCurrent: () => true, api: {async request<T>(path: string): Promise<T> { paths.push(path); return answer(path) as T; }}});
  try {
    await runtime.summary.refresh();
    assert.deepEqual(paths.map(path => new URL(path, 'https://x').pathname).sort(), ['/v1/items', '/v1/items', '/v1/items', '/v1/items', '/v1/lists']);
    assert.deepEqual(totals(runtime.summary.getSnapshot().value), totals(summary));
    assert.equal(runtime.personal.getSnapshot().resources.length, 0);
    assert.equal(Object.isFrozen(runtime.summary.getSnapshot().value?.revision), true);
  } finally { runtime.dispose(); }
});

test('a replacement read wins and parent retirement aborts and erases summary facts synchronously', async () => {
  const pending: {signal: AbortSignal; path: string; resolve: (value: unknown) => void}[] = [];
  let current = true;
  const runtime = createSavedRuntime({scope, isCurrent: () => current, api: {request: (path, _method, _body, signal) => new Promise(resolve => { assert.ok(signal); pending.push({signal: signal!, path, resolve}); })}});
  const settle = () => new Promise(resolve => setTimeout(resolve, 0));
  /** One refresh's reads: the lists and four counted reads, in the order it sent them. */
  const reads = async (from: number) => {await settle(); return pending.slice(from);};
  const answerAll = (rows: typeof pending, value = summary) => {for (const read of rows) read.resolve(answer(read.path, value));};
  let from = pending.length; const old = runtime.summary.refresh(), oldReads = await reads(from);
  from = pending.length; const fresh = runtime.summary.refresh(), freshReads = await reads(from);
  assert.equal(oldReads.length, 5); assert.ok(oldReads.every(read => read.signal.aborted), 'the older read is abandoned');
  answerAll(freshReads, {...summary, collections: 12}); await fresh;
  answerAll(oldReads); await old;
  assert.equal(runtime.summary.getSnapshot().value?.collections, 12);
  from = pending.length; const closing = runtime.summary.refresh(), closingReads = await reads(from);
  current = false; runtime.dispose();
  assert.ok(closingReads.every(read => read.signal.aborted));
  assert.equal(runtime.summary.getSnapshot().value, undefined);
  answerAll(closingReads); await closing;
  assert.equal(runtime.summary.getSnapshot().value, undefined);
});
