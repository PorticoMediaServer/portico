import test from 'node:test';
import assert from 'node:assert/strict';
import {createSavedRuntime} from '../src/screens/saved/runtime.ts';

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};
const summary = {serverId: 'server', viewerFence: 'viewer', revision: {catalog: 1, viewer: 2}, myList: {posters: 2000, squares: 300}, favorites: {posters: 40, squares: 4}, playlists: 11, collections: 10, savedViews: 6};

test('directory totals come from the MAIN summary producer, independently of resident resource pages', async () => {
  const paths: string[] = [];
  const runtime = createSavedRuntime({scope, isCurrent: () => true, api: {async request<T>(path: string): Promise<T> { paths.push(path); return summary as T; }}});
  try {
    await runtime.summary.refresh();
    assert.deepEqual(paths, ['/v1/me/saved/summary']);
    assert.deepEqual(runtime.summary.getSnapshot().value, summary);
    assert.equal(runtime.personal.getSnapshot().resources.length, 0);
    assert.equal(Object.isFrozen(runtime.summary.getSnapshot().value?.revision), true);
  } finally { runtime.dispose(); }
});

test('a replacement read wins and parent retirement aborts and erases summary facts synchronously', async () => {
  const pending: {signal: AbortSignal; resolve: (value: unknown) => void}[] = [];
  let current = true;
  const runtime = createSavedRuntime({scope, isCurrent: () => current, api: {request: (_path, _method, _body, signal) => new Promise(resolve => { assert.ok(signal); pending.push({signal, resolve}); })}});
  const old = runtime.summary.refresh(), fresh = runtime.summary.refresh();
  assert.equal(pending[0].signal.aborted, true);
  pending[1].resolve({...summary, collections: 12}); await fresh;
  pending[0].resolve(summary); await old;
  assert.equal(runtime.summary.getSnapshot().value?.collections, 12);
  const closing = runtime.summary.refresh(); current = false; runtime.dispose();
  assert.equal(pending[2].signal.aborted, true);
  assert.equal(runtime.summary.getSnapshot().value, undefined);
  pending[2].resolve(summary); await closing;
  assert.equal(runtime.summary.getSnapshot().value, undefined);
});
