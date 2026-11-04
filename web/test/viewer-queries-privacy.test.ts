import test from 'node:test';
import assert from 'node:assert/strict';
import type {HttpLocalApi} from '../../packages/client-core/src/index.ts';
import {disposeFreshness, freshnessFor} from '../src/platform/freshness.ts';
import {viewerQueryScope} from '../../packages/client-core/src/query/viewer-queries.ts';

/** The shell's requester: answers are given by hand, and an aborted request rejects. */
function api() {
  const calls: {path: string; signal?: AbortSignal; resolve(value: unknown): void}[] = [];
  const value = {
    getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {},
    request<T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal): Promise<T> {
      return new Promise<T>((resolve, reject) => {
        calls.push({path, signal, resolve: resolve as (v: unknown) => void});
        signal?.addEventListener('abort', () => reject(Object.assign(new Error('aborted'), {name: 'AbortError'})), {once: true});
      });
    },
  } as unknown as HttpLocalApi;
  return {calls, value};
}
const flush = async () => { for (let i = 0; i < 5; i++) await new Promise(resolve => setTimeout(resolve, 1)); };

test('privacy: switching to another profile empties the previous profile\'s cache at once, with its reads aborted', async () => {
  const server = api();
  const alex = freshnessFor(server.value, {serverId: 'server', viewerId: '["local","acct","alex"]'});
  const key = ['item', 'a', 'detail', 'none'];
  const read = alex.queries.client.fetchQuery({queryKey: key, queryFn: ({signal}) => alex.queries.api.request('/v1/items/a/detail?related=none', 'GET', undefined, signal)});
  await flush();
  server.calls[0].resolve({title: 'for Alex'});
  await read;
  const late = alex.queries.client.fetchQuery({queryKey: ['search', 'private'], queryFn: ({signal}) => alex.queries.api.request('/v1/search?q=private', 'GET', undefined, signal)});
  await flush();

  // The shell signs in as Kids on the same server: a new runtime, and Alex's cache is emptied now,
  // not after the screens that showed it have finished unmounting.
  const kids = freshnessFor(server.value, {serverId: 'server', viewerId: '["local","acct","kids"]'});
  assert.notEqual(kids, alex);
  assert.equal(alex.queries.isDisposed(), true);
  assert.equal(server.calls[1].signal?.aborted, true, 'Alex\'s read on its way is aborted');
  await assert.rejects(late);
  assert.equal(alex.queries.client.getQueryData(key), undefined, 'nothing Alex read is kept');
  assert.equal(kids.queries.client.getQueryData(key), undefined, 'Kids start with nothing');
  assert.equal(kids.queries.client.getQueryCache().getAll().length, 0);
  // An answer for Alex arriving now is refused before it can be stored anywhere.
  server.calls[1].resolve({results: ['private']});
  await flush();
  assert.equal(alex.queries.client.getQueryCache().getAll().length, 0);

  // A restart for the same viewer (the shell's privacy fence moved) ends the runtime too: the next
  // runtime for the same viewer is a new one, never the ended cache.
  disposeFreshness(kids);
  const again = freshnessFor(server.value, {serverId: 'server', viewerId: '["local","acct","kids"]'});
  assert.notEqual(again, kids);
  assert.equal(again.queries.isDisposed(), false);
  disposeFreshness(again);
});

test('privacy: a role change empties admin documents, even when nothing else about the viewer changed', async () => {
  const server = api();
  const viewer = {serverId: 'server', authority: 'local', accountId: 'acct', profileId: 'alex'};
  // The shell builds the cache scope from the viewer, role included, as App.tsx and the shell bootstrap both do.
  const owner = freshnessFor(server.value, viewerQueryScope({...viewer, role: 'owner'}));
  assert.equal(freshnessFor(server.value, viewerQueryScope({...viewer, role: 'owner'})), owner, 'the same viewer and role share one cache');
  const activity = ['admin', 'activity'];
  const read = owner.queries.client.fetchQuery({queryKey: activity, queryFn: ({signal}) => owner.queries.api.request('/v1/admin/activity', 'GET', undefined, signal)});
  await flush();
  server.calls[0].resolve({jobs: ['scan of Films']});
  await read;
  const late = owner.queries.client.fetchQuery({queryKey: ['admin', 'storage'], queryFn: ({signal}) => owner.queries.api.request('/v1/admin/storage', 'GET', undefined, signal)});
  await flush();

  // Demoted to member: same server, account and profile, so only the role tells the caches apart.
  const member = freshnessFor(server.value, viewerQueryScope({...viewer, role: 'member'}));
  assert.notEqual(member, owner);
  assert.equal(owner.queries.isDisposed(), true);
  assert.equal(server.calls[1].signal?.aborted, true, 'an owner read on its way is aborted');
  await assert.rejects(late);
  assert.equal(owner.queries.client.getQueryData(activity), undefined, 'nothing read as an owner is kept');
  assert.equal(member.queries.client.getQueryData(activity), undefined, 'the member starts with no admin documents');
  assert.equal(member.queries.client.getQueryCache().getAll().length, 0);
  server.calls[1].resolve({volumes: []});
  await flush();
  assert.equal(owner.queries.client.getQueryCache().getAll().length, 0, 'an answer for the owner arriving now is refused');
  assert.throws(() => viewerQueryScope({...viewer, role: ''}), 'a scope without a role is refused');
  disposeFreshness(member);
});
