import test from 'node:test';
import assert from 'node:assert/strict';
import type {SavedApi} from '../../../packages/client-core/src/saved.ts';
import {NativeSavedViewDestination} from '../src-next/screens/saved/view-model.ts';

const scope = {serverId: 'server-a', viewerId: 'actual-viewer'};
function summary() {return {serverId: scope.serverId, viewerFence: 'summary-fence', revision: {catalog: 4, viewer: 7},
  myList: {posters: 13, squares: 2}, favorites: {posters: 8, squares: 5}, playlists: 3, collections: 27, savedViews: 12};}
const directory = (nextCursor = '') => ({serverId: scope.serverId, viewerFence: 'resource-fence', revision: 9, resources: [], nextCursor});

test('Saved directory reads its actual bounded page and canonical total, and deduplicates a pending continuation', async () => {
  const calls: string[] = []; let finish!: (value: unknown) => void;
  const api: SavedApi = {request: async <T>(path: string) => {
    calls.push(path);
    if (path.endsWith('/saved/summary')) return summary() as T;
    if (path.includes('cursor=')) return await new Promise<unknown>(resolve => {finish = resolve;}) as T;
    return directory('actual-next-cursor') as T;
  }};
  const view = new NativeSavedViewDestination({api, scope, requestId: async () => 'request-id', current: () => true});
  try {
    const loading = view.select({view: 'collections'}); assert.equal(view.select({view: 'collections'}), loading); await loading;
    assert.equal(view.getSnapshot().phase, 'ready'); assert.equal(calls.length, 2);
    assert.equal(view.getSnapshot().summary?.collections, 27, 'zero loaded resources do not become a fake directory total');
    const first = new URL(calls.find(path => path.includes('saved-resources'))!, 'https://test.invalid');
    assert.equal(first.searchParams.get('kind'), 'collection'); assert.equal(first.searchParams.get('limit'), '40');
    const next = view.next(); assert.equal(view.next(), next); finish(directory()); await next;
    assert.equal(calls.length, 3); assert.equal(new URL(calls[2], 'https://test.invalid').searchParams.get('cursor'), 'actual-next-cursor');
    await view.next(); assert.equal(calls.length, 3, 'the real empty continuation stops further reads');
    assert.equal(view.getSnapshot().summary?.collections, 27);
  } finally {view.dispose();}
});

test('switching the real Saved route aborts old reads and cannot restore old directory state', async () => {
  const pending: {path: string; signal?: AbortSignal; resolve: (value: unknown) => void}[] = [];
  const api: SavedApi = {request: <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => new Promise<T>(resolve => {pending.push({path, signal, resolve: value => resolve(value as T)});})};
  const view = new NativeSavedViewDestination({api, scope, requestId: async () => 'request-id', current: () => true});
  try {
    const old = view.select({view: 'collections'}), oldReads = [...pending];
    const current = view.select({view: 'views'});
    assert.ok(oldReads.every(read => read.signal?.aborted));
    for (const read of pending.slice(oldReads.length)) read.resolve(read.path.endsWith('/saved/summary') ? summary() : directory());
    await current;
    const ready = view.getSnapshot(); assert.equal(ready.route?.view, 'views'); assert.equal(ready.phase, 'ready');
    for (const read of oldReads) read.resolve(read.path.endsWith('/saved/summary') ? summary() : directory('old-continuation'));
    await old; assert.equal(view.getSnapshot(), ready);
  } finally {view.dispose();}
});

test('retiring a viewer clears private Saved page/totals and stops late replies', async () => {
  let active = true;
  const pending: {signal?: AbortSignal; resolve: (value: unknown) => void; path: string}[] = [];
  const api: SavedApi = {request: <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => new Promise<T>(resolve => {pending.push({path, signal, resolve: value => resolve(value as T)});})};
  const view = new NativeSavedViewDestination({api, scope, requestId: async () => 'request-id', current: () => active});
  const loading = view.select({view: 'collections'}); active = false;
  const closed = view.getSnapshot(); assert.deepEqual(closed, {phase: 'idle'});
  assert.ok(pending.every(read => read.signal?.aborted));
  for (const read of pending) read.resolve(read.path.endsWith('/saved/summary') ? summary() : directory());
  await loading; assert.equal(view.getSnapshot(), closed);
});
