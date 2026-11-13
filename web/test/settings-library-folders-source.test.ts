import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import {SettingsLibraryFoldersSource} from '../src/screens/settings/library-folders-source.ts';

const scope = {serverId: 'server', viewerId: 'owner'};
const folder = (id = 'one', path = '/media/one', source?: string) => ({id, path, available: true, ...(source ? {source} : {})});
const library = (id = 'selected', revision = '9', folders = [folder()]) => ({id, revision, name: id, kind: 'movies', folders,
  metadataProvider: 'local', metadataProviders: [{id: 'local', available: true}], options: {depth: 'basic', watch: false}});
function fixture(api: LibraryContentApi) {
  let current = true;
  const errors: unknown[] = [];
  const folders = new SettingsLibraryFoldersSource(api, scope, () => current, () => {}, error => errors.push(error));
  return {folders, errors, retire: () => {current = false;}};
}

test('selection reads only AdminLibrary and retains real member order, paths, IDs and shared revision', async () => {
  const calls: string[] = [];
  const f = fixture({request: async <T>(path: string) => {calls.push(path);return library('selected', '9', [folder('remote', 'Movies', 'nas'), folder('local', '/media/local')]) as T;}});
  try {
    await f.folders.select();await f.folders.refresh();assert.deepEqual(calls, []);
    await f.folders.select('selected');
    assert.deepEqual(calls, ['/v1/admin/libraries/selected']);
    const config = f.folders.config('selected')!;
    assert.equal(config.revision, 9);assert.equal(config.policy.revision, 9);
    assert.deepEqual(config.sources.map(s => [s.id, s.path, s.remoteSource]), [['remote', 'Movies', 'nas'], ['local', '/media/local', undefined]]);
    assert.deepEqual(config.eligibleSourceControls, ['libraries.folderPath']);
    assert.equal(config.sources[0].classification, undefined);assert.equal(config.sources[0].intervalSeconds, undefined);
    assert.deepEqual(config.scope, {serverId: 'server', viewerFence: 'owner'});
    assert.equal(f.folders.config('another'), undefined);await f.folders.select('selected');assert.equal(calls.length, 1);
  } finally {f.folders.dispose();}
});

test('empty folders are a successful configuration; an unreadable read recovers only with a real reread', async () => {
  let broken = true, reads = 0;
  const f = fixture({request: async <T>() => {reads++;if (broken) throw new Error('offline');return library('selected', '9', []) as T;}});
  try {
    await f.folders.select('selected');assert.equal(f.folders.config('selected'), undefined);assert.equal(f.errors.length, 1);
    broken = false;await f.folders.refresh();assert.deepEqual(f.folders.config('selected')?.sources, []);
    assert.equal(f.folders.getSnapshot().error, undefined);assert.equal(reads, 2);
  } finally {f.folders.dispose();}
});

test('replacement, clearing, disposal and caller retirement suppress late reads', async () => {
  for (const close of ['replace', 'clear', 'dispose', 'retire']) {
    let release!: (value: unknown) => void, signal: AbortSignal | undefined;
    const f = fixture({request: async <T>(path: string, _method?: string, _body?: unknown, captured?: AbortSignal) => {
      if (path.endsWith('/first')) {signal = captured;return new Promise(resolve => {release = resolve;}) as Promise<T>;}
      return library('second') as T;
    }});
    try {
      const pending = f.folders.select('first');
      if (close === 'replace') await f.folders.select('second');else if (close === 'clear') f.folders.clear();else if (close === 'dispose') f.folders.dispose();else f.retire();
      assert.equal(signal?.aborted, close !== 'retire');release(library('first'));await pending;
      assert.equal(f.folders.config('first'), undefined);assert.deepEqual(f.errors, []);
      if (close === 'replace') assert.equal(f.folders.config('second')?.libraryId, 'second');
    } finally {f.folders.dispose();}
  }
});

test('add, edit and remove PATCH membership with If-Match, preserve remote members, and confirm by reread', async () => {
  let server = library('selected', '9', [folder(), folder('remote', 'Movies', 'nas')]);
  const writes: unknown[] = [];
  const f = fixture({request: async <T>(path: string) => {assert.equal(path, '/v1/admin/libraries/selected');return server as T;},
    requestRaw: async (path, method, options) => {
      assert.equal(path, '/v1/admin/libraries/selected');assert.equal(method, 'PATCH');
      assert.deepEqual(options?.headers, {'If-Match': '"' + server.revision + '"'});assert.equal(options?.singleAttempt, true);
      const body = options?.body as {folders: string[]; remoteFolders: {source: string; path: string}[]};writes.push(body);
      server = {...server, revision: String(Number(server.revision) + 1), folders: [...body.folders.map(path => folder(path, path)), ...body.remoteFolders.map(remote => folder('remote', remote.path, remote.source))]};
      return {status: 200, headers: {}, body: server};
    }});
  try {
    await f.folders.select('selected');assert.equal(await f.folders.save('selected', undefined, {path: '/media/new'}), true);
    assert.deepEqual(writes[0], {folders: ['/media/one', '/media/new'], remoteFolders: [{source: 'nas', path: 'Movies'}]});
    assert.equal(f.folders.config('selected')?.revision, 10);
    assert.equal(await f.folders.save('selected', '/media/new', {path: '/media/changed'}), true);
    assert.equal(await f.folders.remove('selected', '/media/changed'), true);
    assert.deepEqual(writes[2], {folders: ['/media/one'], remoteFolders: [{source: 'nas', path: 'Movies'}]});
    assert.equal(f.folders.config('selected')?.revision, 12);
    assert.equal(await f.folders.remove('selected', 'unknown'), false);assert.equal(await f.folders.save('foreign', undefined, {path: '/no'}), false);
    assert.equal(writes.length, 3);
  } finally {f.folders.dispose();}
});

test('a membership conflict refreshes the library version without replay; unsupported fields never reach PATCH', async () => {
  let server = library(), writes = 0;
  const f = fixture({request: async <T>() => server as T, requestRaw: async () => {writes++;return {status: 412, headers: {}, body: {error: {code: 'changed_elsewhere'}}};}});
  try {
    await f.folders.select('selected');server = library('selected', '10');
    assert.equal(await f.folders.save('selected', undefined, {path: '/new'}), false);assert.equal(writes, 0);
    assert.equal(f.folders.config('selected')?.revision, 10);assert.equal(f.folders.getSnapshot().mutation, 'rejected');
    assert.equal(await f.folders.save('selected', undefined, {path: '/new'}), false);assert.equal(writes, 1);
    assert.equal((f.folders.getSnapshot().error as {code: string}).code, 'changed_elsewhere');
    assert.equal(await f.folders.save('selected', undefined, {path: '/new', name: 'Not honoured'}), false);assert.equal(writes, 1);
    assert.equal(f.folders.getSnapshot().mutation, 'rejected');
  } finally {f.folders.dispose();}
});

test('unknown write outcomes and missing confirmation require reconciliation before another command', async () => {
  for (const confirmed of [false, true]) {
    let reads = 0, writes = 0;
    const f = fixture({request: async <T>() => {if (++reads > 2) throw new Error('read lost');return library() as T;},
      requestRaw: async () => {writes++;if (!confirmed) throw new Error('answer lost');return {status: 200, headers: {}, body: library('selected', '10', [folder(), folder('new', '/new')])};}});
    try {
      await f.folders.select('selected');assert.equal(await f.folders.save('selected', undefined, {path: '/new'}), false);
      assert.equal(f.folders.getSnapshot().mutation, confirmed ? 'reconciliation' : 'ambiguous');
      assert.equal(await f.folders.save('selected', undefined, {path: '/again'}), false);assert.equal(writes, 1);
    } finally {f.folders.dispose();}
  }
});

test('scan uses a folder ID and readability uses existing browsing; foreign membership sends nothing', async () => {
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const f = fixture({request: async <T>(path: string, method?: string, body?: unknown) => {
    calls.push({path, method, body});return (path === '/v1/admin/libraries/selected' ? library() : {}) as T;
  }});
  try {
    await f.folders.select('selected');assert.equal(await f.folders.scan('selected', 'one'), true);assert.equal(await f.folders.check('selected', 'one'), true);
    assert.ok(calls.some(c => c.path === '/v1/admin/libraries/selected/scan' && c.method === 'POST' && JSON.stringify(c.body) === '{"folder":"one"}'));
    assert.ok(calls.some(c => c.path === '/v1/admin/filesystem?path=%2Fmedia%2Fone' && c.method === 'GET'));
    const before = calls.length;assert.equal(await f.folders.scan('selected', 'foreign'), false);assert.equal(await f.folders.check('other', 'one'), false);assert.equal(calls.length, before);
  } finally {f.folders.dispose();}
});

test('a held command is serialized, aborted by selection replacement, and cannot publish after returning', async () => {
  let release!: (value: unknown) => void, signal: AbortSignal | undefined, writes = 0;
  const f = fixture({request: async <T>(path: string) => library(path.split('/').at(-1)) as T,
    requestRaw: async (_path, _method, options) => {writes++;signal = options?.signal;return new Promise(resolve => {release = resolve as typeof release;});}});
  try {
    await f.folders.select('selected');const pending = f.folders.save('selected', undefined, {path: '/new'});
    await new Promise(resolve => setImmediate(resolve));assert.equal(await f.folders.remove('selected', 'one'), false);
    await f.folders.select('other');await f.folders.select('selected');assert.equal(signal?.aborted, true);
    release({status: 200, headers: {}, body: library('selected', '10', [folder(), folder('new', '/new')])});
    assert.equal(await pending, false);assert.equal(writes, 1);assert.equal(f.folders.config('selected')?.revision, 9);
  } finally {f.folders.dispose();}
});
