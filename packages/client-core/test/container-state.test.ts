import test from 'node:test';
import assert from 'node:assert/strict';
import {containerCountLabel, getContainerState, setContainerState, CONTAINER_REVISION_MISMATCH} from '../src/container-state.ts';

function fakeApi(routes: Record<string, (body?: unknown) => unknown>): {request<T>(path: string, method?: string, body?: unknown): Promise<T>; calls: {path: string; method?: string; body?: unknown}[]} {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  return {
    calls,
    async request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
      calls.push({path, method, body});
      const route = routes[method + ' ' + path];
      if (!route) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
      return route(body) as T;
    },
  };
}

test('container state reads the revision and the visible-member counts', async () => {
  const api = fakeApi({'GET /v1/containers/show/show-1/personal-state': () => ({revision: 4, watched: true, watermark: '2026-09-20T00:00:00Z', watchedCount: 8, unwatchedCount: 2})});
  const state = await getContainerState(api, 'show', 'show-1');
  assert.equal(state.revision, 4);
  assert.equal(state.watched, true);
  assert.deepEqual(containerCountLabel(state.watchedCount!, state.unwatchedCount!), {watched: 8, unwatched: 2});
  assert.equal(api.calls[0].path, '/v1/containers/show/show-1/personal-state');
});

test('container watched writes the fenced intent and nothing else', async () => {
  const api = fakeApi({'PUT /v1/containers/season/s-1/personal-state': () => ({revision: 5, watched: false, watermark: '2026-09-21T00:00:00Z'})});
  const next = await setContainerState(api, 'season', 's-1', {expectedRevision: 4, watched: false});
  assert.equal(next.revision, 5);
  assert.deepEqual(api.calls[0].body, {expectedRevision: 4, watched: false});
});

test('a stale revision fails as revision_mismatch so the caller rereads', async () => {
  const api = fakeApi({'PUT /v1/containers/album/a-1/personal-state': () => { throw Object.assign(new Error('stale'), {status: 409, code: 'revision_mismatch'}); }});
  await assert.rejects(setContainerState(api, 'album', 'a-1', {expectedRevision: 2, watched: true}), (e: any) => e.code === 'revision_mismatch' && e.code === CONTAINER_REVISION_MISMATCH);
});

test('container state rejects unknown kinds; a read without counts shows none', async () => {
  const api = fakeApi({});
  await assert.rejects(getContainerState(api, 'episode' as any, 'e-1'));
  const missing = fakeApi({'GET /v1/containers/book/b-1/personal-state': () => ({revision: 1, watched: false, watermark: ''})});
  assert.deepEqual(await getContainerState(missing, 'book', 'b-1'), {revision: 1, watched: false, watermark: '', watchlisted: false, favorite: false, watchedCount: 0, unwatchedCount: 0});
});

test('a field this app does not know, or a flag or count it cannot read, never loses the state', async () => {
  const api = fakeApi({'GET /v1/containers/show/s-1/personal-state': () => ({revision: 4, watched: true, watermark: 'w', watchlisted: 'yes', favorite: true, watchedCount: 'many', unwatchedCount: 3, libraryId: 7, rating: 5})});
  assert.deepEqual(await getContainerState(api, 'show', 's-1'), {revision: 4, watched: true, watermark: 'w', watchlisted: false, favorite: true, watchedCount: 0, unwatchedCount: 3});
});

test('what the next change is sent against stays exact', async () => {
  for (const bad of [{watched: true, watermark: ''}, {revision: -1, watched: true, watermark: ''}, {revision: 1, watched: 'true', watermark: ''}, {revision: 1, watched: true}, {revision: 1, watched: true, watermark: 'x'.repeat(200)}, 'nope']) {
    await assert.rejects(getContainerState(fakeApi({'GET /v1/containers/show/s-1/personal-state': () => bad}), 'show', 's-1'));
  }
});
