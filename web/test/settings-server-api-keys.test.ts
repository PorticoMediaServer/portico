import test from 'node:test';
import assert from 'node:assert/strict';
import type {ApiCredential} from '../../packages/client-core/src/api-access.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {SettingsApiKeysSource} from '../src/screens/settings/server/integrations/api-keys-source.ts';
const key: ApiCredential = {id: 'key-1', kind: 'key', name: 'Automation', scopes: ['read'], hint: '1234', createdAt: '2026-10-22T19:00:00Z', state: 'active', member: {accountId: 'owner', username: 'Owner', profileId: 'profile', profile: 'Owner'}};
const page = {items: [key], limit: 100, total: 1};
const options = {scopes: [{id: 'read'}, {id: 'admin'}], maximumKeys: 20, maximumTokens: 20, tokensAllowed: true, requestsPerMinute: 600, queryParameter: 'api_key'};
const read = (path: string) => path === routePath('get_api_access_options') ? options : page;

test('late pages cannot replace the latest cursor read', async () => {
  const releases: ((value: unknown) => void)[] = [];
  const source = new SettingsApiKeysSource({request: async <T>(path: string) => path === routePath('get_api_access_options') ? options as T : new Promise<T>(resolve => releases.push(value => resolve(value as T)))}, () => true, () => {});
  const old = source.load(), latest = source.load('next');releases[1]({...page, items: [{...key, id: 'key-2'}]});await latest;releases[0](page);await old;
  assert.equal(source.getSnapshot().page?.items[0].id, 'key-2');source.dispose();
});

test('uncertain key creation freezes the original intent and never stores a returned secret', async () => {
  const writes: Record<string, unknown>[] = [];let failed = false;
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string, body?: unknown) => {
    if (method === 'POST') {writes.push(body as Record<string, unknown>);if (!failed) {failed = true;throw new Error('Response lost');}return {credential: {...key, name: (body as {name: string}).name, scopes: (body as {scopes: string[]}).scopes, expiresAt: (body as {expiresAt?: string}).expiresAt}} as T;}
    return read(path) as T;
  }}, () => true, () => {});
  await source.load();const scopes: ('read'|'admin')[] = ['read'];
  await source.create({name: 'Original', scopes, expiresAt: '2026-11-21T19:00:00Z'});scopes.push('admin');
  await source.create({name: 'Replacement', scopes: ['admin']});assert.equal(writes.length, 1);
  const issued = await source.retryCreate();assert.equal(issued?.secret, undefined);assert.deepEqual(writes[1], writes[0]);assert.equal(writes[1].name, 'Original');assert.deepEqual(writes[1].scopes, ['read']);assert.equal(source.getSnapshot().pendingCreate, false);source.dispose();
});

test('one-time secret is returned to the current caller without entering observable state', async () => {
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => (method === 'POST' ? {credential: key, secret: 'pk.secret-once'} : read(path)) as T}, () => true, () => {});
  await source.load();const issued = await source.create({name: 'Automation', scopes: ['read']});assert.equal(issued?.secret, 'pk.secret-once');assert.equal(JSON.stringify(source.getSnapshot()).includes('pk.secret-once'), false);source.dispose();
});

test('a retired authority drops a late issued secret and makes no later request', async () => {
  let current = true, release!: (value: unknown) => void, calls = 0;
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => {calls++;return method === 'POST' ? new Promise<T>(resolve => {release = value => resolve(value as T);}) : read(path) as T;}}, () => current, () => {});
  await source.load();const pending = source.create({name: 'Automation', scopes: ['read']});current = false;release({credential: key, secret: 'pk.retired'});assert.equal(await pending, undefined);
  const count = calls;await source.load();await source.retryCreate();assert.equal(calls, count);assert.equal(source.getSnapshot().phase, 'idle');source.dispose();
});

test('a token in the owner key list is refused whole and cannot be revoked here', async () => {
  let deletes = 0;const errors: unknown[] = [];
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => {if (method === 'DELETE') deletes++;return (path === routePath('get_api_access_options') ? options : {...page, items: [{...key, kind: 'token'}]}) as T;}}, () => true, error => errors.push(error));
  await source.load();assert.equal(source.getSnapshot().page, undefined);
  // Invalid credential documents stay refused and local; only terminal authority failures report globally.
  assert.equal(source.getSnapshot().phase, 'error');assert.equal(source.getSnapshot().error, 'read');assert.equal(errors.length, 0);
  await source.revoke(key);assert.equal(deletes, 0);source.dispose();
});


test('synchronous retirement during busy publication cannot dispatch a credential command', async () => {
  let writes = 0;
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => {if (method === 'POST' || method === 'DELETE') writes++;return read(path) as T;}}, () => true, () => {});
  await source.load();source.subscribe(() => {if (source.getSnapshot().busy) source.dispose();});
  await source.create({name: 'Automation', scopes: ['read']});assert.equal(writes, 0);
});


test('a synchronous retry subscriber cannot overlap the claimed creation attempt', async () => {
  let posts = 0, reentered = false;
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string, body?: unknown) => {
    if (method === 'POST') {posts++;return {credential: {...key, name: (body as {name: string}).name}, secret: 'pk.once'} as T;}
    return read(path) as T;
  }}, () => true, () => {});
  await source.load();source.subscribe(() => {if (source.getSnapshot().pendingCreate && !reentered) {reentered = true;void source.retryCreate();}});
  assert.equal((await source.create({name: 'Automation', scopes: ['read']}))?.secret, 'pk.once');assert.equal(posts, 1);source.dispose();
});

test('recoverable create and revoke errors remain local and retain uncertain intent', async () => {
  const reported: unknown[] = [];
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => {
    if (method === 'POST' || method === 'DELETE') throw new Error('Response lost');
    return read(path) as T;
  }}, () => true, error => reported.push(error));
  await source.load();await source.create({name: 'Automation', scopes: ['read']});assert.equal(source.getSnapshot().pendingCreate, true);assert.equal(source.getSnapshot().error, 'create');assert.equal(reported.length, 0);
  await source.load();await source.revoke(source.getSnapshot().page!.items[0]);assert.equal(source.getSnapshot().error, 'revoke');assert.equal(source.getSnapshot().pendingCreate, true);assert.equal(reported.length, 0);source.dispose();
});

test('a mismatched create receipt never returns its secret or consumes the frozen intent', async () => {
  for (const change of [{kind: 'token'}, {name: 'Another'}, {scopes: ['admin']}, {expiresAt: '2026-12-22T19:00:00Z'}]) {
    const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => {
      if (method === 'POST') return {credential: {...key, expiresAt: '2026-11-22T19:00:00Z', ...change}, secret: change.kind === 'token' ? 'pat.foreign' : 'pk.foreign'} as T;
      return read(path) as T;
    }}, () => true, () => {});
    await source.load();assert.equal(await source.create({name: 'Automation', scopes: ['read'], expiresAt: '2026-11-22T19:00:00Z'}), undefined);assert.equal(source.getSnapshot().pendingCreate, true);assert.equal(source.getSnapshot().error, 'create');source.dispose();
  }
});

test('a revoke receipt for another key stays a local failure after readback', async () => {
  const reported: unknown[] = [];
  const source = new SettingsApiKeysSource({request: async <T>(path: string, method?: string) => (method === 'DELETE' ? {...key, id: 'foreign', state: 'revoked'} : read(path)) as T}, () => true, error => reported.push(error));
  await source.load();await source.revoke(source.getSnapshot().page!.items[0]);assert.equal(source.getSnapshot().error, 'revoke');assert.equal(reported.length, 0);source.dispose();
});
