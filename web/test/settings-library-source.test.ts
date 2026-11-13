import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryContentApi} from '@core/library-content.ts';
import {SettingsLibrarySource} from '../src/screens/settings/library-source.ts';

const library = (id = 'selected', revision = '7') => ({id, revision, name: 'Art Films', kind: 'movies', language: 'en', region: 'CA',
  metadataProvider: 'local', metadataProviders: [{id: 'local', available: true}], folders: [],
  options: {depth: 'basic', watch: true, allowDelete: false, groupSeries: true, trash: 'keep', rescan: 3600}});
function fixture(api: LibraryContentApi) {
  let current = true;
  const errors: unknown[] = [];
  const source = new SettingsLibrarySource(api, () => current, () => {}, error => errors.push(error));
  return {source, errors, retire: () => {current = false;}};
}

test('the real AdminLibrary reader offers only honoured fields, without invented defaults', async () => {
  const calls: string[] = [];
  const f = fixture({request: async <T>(path: string) => {calls.push(path);return library() as T;}});
  try {
    assert.equal(f.source.document(), undefined);assert.deepEqual(calls, []);
    await f.source.select('selected');
    assert.deepEqual(calls, ['/v1/admin/libraries/selected']);
    assert.equal(f.source.read('selected', 'libraries.name')?.current, 'Art Films');
    assert.equal(f.source.read('selected', 'libraries.scanDepth')?.current, 'basic');
    assert.equal(f.source.read('selected', 'libraries.allowDelete')?.current, false);
    assert.equal(f.source.read('selected', 'libraries.region')?.current, 'CA');
    assert.equal(f.source.read('selected', 'libraries.scanDepth')?.default, undefined);
    for (const id of ['libraries.cwWeeks', 'libraries.analysisSteps', 'libraries.scrub', 'libraries.strm', 'libraries.sonicAnalysis']) assert.equal(f.source.read('selected', id), undefined, id);
    assert.equal(f.source.read('another', 'libraries.scanDepth'), undefined);
    await f.source.select('selected');assert.equal(calls.length, 1);
    await f.source.select();assert.equal(f.source.document('selected'), undefined);
  } finally {f.source.dispose();}
});

test('a General save sends the captured If-Match once and preserves unrelated library options', async () => {
  let server = library(), release!: () => void;
  const hold = new Promise<void>(resolve => {release = resolve;});
  const writes: unknown[] = [];
  const f = fixture({request: async <T>() => server as T, requestRaw: async (path, method, options) => {
    assert.equal(path, '/v1/admin/libraries/selected');assert.equal(method, 'PATCH');
    assert.deepEqual(options?.headers, {'If-Match': '"7"'});assert.equal(options?.singleAttempt, true);assert.ok(options?.signal);
    writes.push(options?.body);await hold;
    server = {...server, revision: '8', options: {...server.options, allowDelete: true}};
    return {status: 200, headers: {}, body: server};
  }});
  try {
    await f.source.select('selected');
    const saving = f.source.change('selected', 'libraries.allowDelete', true);
    assert.equal(f.source.getSnapshot().saving, true);
    assert.equal(await f.source.change('selected', 'libraries.watch', false), false);
    assert.deepEqual(writes, [{options: {...server.options, allowDelete: true}}]);
    release();assert.equal(await saving, true);
    assert.equal(f.source.document('selected')?.revision, 8);
    assert.equal(f.source.read('selected', 'libraries.allowDelete')?.current, true);
    assert.equal(await f.source.reset('selected', 'libraries.allowDelete'), false, 'no server default was supplied');
    await assert.rejects(f.source.change('selected', 'libraries.cwWeeks', 20));assert.equal(writes.length, 1);
  } finally {release();f.source.dispose();}
});

test('a changed-elsewhere refusal refreshes the version and keeps the refusal without replaying the save', async () => {
  const conflict = {code: 'changed_elsewhere', message: 'This library changed elsewhere.'};
  let reads = 0, writes = 0;
  const f = fixture({request: async <T>() => library('selected', ++reads === 1 ? '7' : '8') as T,
    requestRaw: async () => {writes++;return {status: 412, headers: {}, body: {error: conflict}};}});
  try {
    await f.source.select('selected');
    await assert.rejects(f.source.change('selected', 'libraries.watch', false), {code: 'changed_elsewhere', status: 412});
    assert.equal(reads, 2);assert.equal(writes, 1);assert.equal(f.source.document('selected')?.revision, 8);
    assert.equal((f.source.getSnapshot().error as {code: string}).code, 'changed_elsewhere');
  } finally {f.source.dispose();}
});

test('a foreign answer is unreadable and explicit refresh can recover a failed first read', async () => {
  let wrong = true;
  const f = fixture({request: async <T>() => library(wrong ? 'other' : 'selected') as T});
  try {
    await f.source.select('selected');assert.equal(f.source.document('selected'), undefined);assert.equal(f.errors.length, 1);
    wrong = false;await f.source.select('selected');assert.equal(f.source.document('selected')?.libraryId, 'selected');
  } finally {f.source.dispose();}
});

test('selection replacement and disposal abort reads and suppress their late answers', async () => {
  for (const close of ['replace', 'dispose', 'retire']) {
    let release!: (value: unknown) => void, signal: AbortSignal | undefined;
    const f = fixture({request: async <T>(path: string, _method?: string, _body?: unknown, captured?: AbortSignal) => {
      if (path.endsWith('/first')) {signal = captured;return new Promise(resolve => {release = resolve;}) as Promise<T>;}
      return library('second') as T;
    }});
    try {
      const pending = f.source.select('first');
      if (close === 'replace') await f.source.select('second');else if (close === 'dispose') f.source.dispose();else f.retire();
      assert.equal(signal?.aborted, close !== 'retire');release(library('first'));await pending;
      assert.equal(f.source.document('first'), undefined);assert.equal(f.errors.length, 0);
    } finally {f.source.dispose();}
  }
});

test('a retired save cannot publish a new version or start a second write', async () => {
  let release!: (value: unknown) => void, signal: AbortSignal | undefined, writes = 0;
  const f = fixture({request: async <T>() => library() as T, requestRaw: async (_path, _method, options) => {
    writes++;signal = options?.signal;return new Promise(resolve => {release = resolve as typeof release;});
  }});
  const pending = (async () => {await f.source.select('selected');return f.source.change('selected', 'libraries.watch', false);})();
  await new Promise(resolve => setImmediate(resolve));f.source.dispose();
  assert.equal(signal?.aborted, true);release({status: 200, headers: {}, body: library('selected', '8')});await pending;
  assert.equal(f.source.document('selected'), undefined);assert.equal(await f.source.change('selected', 'libraries.watch', true), false);assert.equal(writes, 1);
});

test('a quiet read cannot overwrite a General save that completes while its answer is held', async () => {
  let reads = 0, release!: (value: unknown) => void;
  const f = fixture({request: async <T>() => ++reads === 2 ? new Promise(resolve => {release = resolve;}) as Promise<T> : library() as T,
    requestRaw: async () => ({status: 200, headers: {}, body: {...library('selected', '8'), options: {...library().options, watch: false}}})});
  try {
    await f.source.select('selected');const quiet = f.source.refreshSettings();
    assert.equal(await f.source.change('selected', 'libraries.watch', false), true);release(library());
    assert.deepEqual(await quiet, {kind: 'deferred'});assert.equal(f.source.document('selected')?.revision, 8);
    assert.equal(f.source.read('selected', 'libraries.watch')?.current, false);
  } finally {f.source.dispose();}
});
