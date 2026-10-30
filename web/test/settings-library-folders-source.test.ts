import test from 'node:test';
import assert from 'node:assert/strict';
import type {LibraryAdminDocument, LibrarySettings} from '@core/administration.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {SettingsLibraryFoldersSource, type SettingsLibraryFolderEdit} from '../src/screens/settings/library-folders-source.ts';
import {SettingsLibrarySource} from '../src/screens/settings/library-source.ts';

const scope = {serverId: 'server', viewerId: 'owner'};
function source(id: string, path: string, resolvedPath = path, libraryId = 'selected') {
  return {id, libraryId, kind: 'local', name: `${id} folder`, path, resolvedPath, classification: 'local', generation: 1, health: 'healthy',
    enabled: true, followSymlinks: false, identityConfirmed: true, intervalMs: 3600000, nextScanAt: '', lastCompleteAt: '', lastProgressAt: ''};
}
function configuration(libraryId = 'selected', revision = 9, sources = [source('one', '/media/one')]) {
  return {scope: {serverId: 'server', viewerFence: 'fence-7'}, libraryId, revision, sources, policy: {tier: 'basic', revision: 4, operations: ['probe']}};
}
function fixture(request: LibraryContentApi['request']) {
  let current = true, notified = 0;
  const errors: unknown[] = [];
  const folders = new SettingsLibraryFoldersSource({request}, scope, () => current, () => {notified++;}, error => {errors.push(error);});
  return {folders, errors, notified: () => notified, retire: () => {current = false;}};
}

test('the folder configuration is inert until a library is explicitly selected', async () => {
  let calls = 0;
  const folders = new SettingsLibraryFoldersSource({request: async <T>() => {calls++;throw new Error('unexpected read');}}, scope, () => true,
    () => {throw new Error('unexpected notification');}, () => {throw new Error('unexpected read failure');});
  try {
    assert.equal(calls, 0);
    assert.deepEqual(folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.equal(folders.config(), undefined);assert.equal(folders.config('selected'), undefined);
    await folders.select();await folders.refresh();
    assert.equal(calls, 0);assert.deepEqual(folders.getSnapshot(), {loading: false, mutation: 'idle'});
  } finally {folders.dispose();}
});

test('the selected library keeps the server order, path and resolved path exactly', async () => {
  const calls: {path: string; method?: string}[] = [];
  const f = fixture(async <T>(path: string, method?: string) => {
    calls.push({path, method});
    return configuration('selected', 9, [source('third', '/media/z', '/mnt/z'), source('first', '/media/a'), source('second', '/media/m', '/mnt/m')]) as T;
  });
  try {
    await f.folders.select('selected');
    const value = f.folders.config('selected');
    assert.ok(value);
    assert.deepEqual(calls, [{path: '/v1/admin/libraries/selected/inventory-config', method: 'GET'}]);
    assert.deepEqual(value.sources.map(item => [item.id, item.path, item.resolvedPath]), [['third', '/media/z', '/mnt/z'], ['first', '/media/a', '/media/a'], ['second', '/media/m', '/mnt/m']]);
    assert.equal(value.sources[0].intervalSeconds, 3600);
    assert.deepEqual(value.scope, {serverId: 'server', viewerFence: 'fence-7'});
    assert.equal(value.revision, 9);assert.equal(value.policy.revision, 4);
    assert(Object.isFrozen(value));assert(Object.isFrozen(value.sources));assert(Object.isFrozen(f.folders.getSnapshot()));
    assert.equal(f.folders.getSnapshot().error, undefined);assert.equal(f.folders.getSnapshot().loading, false);
    assert.equal(f.folders.config('other'), undefined);
    await f.folders.select('selected');assert.equal(calls.length, 1);
  } finally {f.folders.dispose();}
});

test('the folder configuration carries the inventory revision, never the Settings document revision', async () => {
  const settings: LibrarySettings = {scanDepth: 'basic', groupCollections: true, localTags: 'prefer', lyricsSource: 'files', titleSource: 'tags', episodeNumbering: 'absolute', findExtras: true, sortIgnoreArticles: true, refreshMode: 'replace_unlocked', acoustidEnabled: false,
    continueWatching: {weeks: 16, maximumItems: 40, includeSeasonPremieres: true, videoPlayedThreshold: 95, videoCompletion: 'earliest'}, allowMediaDeletion: false, monitorChanges: true, providers: [], analysis: ['probe', 'local_metadata', 'subtitle_discovery'],
    navigation: {trickplayIntervalMs: 10000, trickplayTileWidth: 320, trickplayMaxTiles: 1080, chapterThumbnailMode: 'embedded', videoPreviewEnabled: false, videoPreviewMs: 30000}, segmentDetection: 'server'};
  const document = (libraryId: string, revision: number): LibraryAdminDocument => ({libraryId, libraryName: libraryId, libraryKind: 'anime', revision, digest: 'actual-document',
    eligibleControls: ['libraries.cwWeeks', 'libraries.episodeNumbering'], audience: {everyone: false, accounts: 3}, actions: ['scan', 'rename'],
    settings, defaults: {...settings, continueWatching: {...settings.continueWatching, weeks: 8}}, analysisMatrix: {costClasses: [], operations: []}, availableProviders: [], enumerations: {}, sources: []});
  const calls: string[] = [];
  const request = async <T>(path: string) => {
    calls.push(path);
    return (path.endsWith('/inventory-config') ? configuration('selected', 41, [source('one', '/media/one', '/media/one')]) : document('selected', 7)) as T;
  };
  const library = new SettingsLibrarySource({request}, () => true, () => {}, error => {throw error;});
  const f = fixture(request);
  try {
    await library.select('selected');await f.folders.select('selected');
    assert.equal(library.document('selected')?.revision, 7);
    // The existing write contract already states expectedRevision is inventory-config.revision, never settings.revision.
    assert.equal(f.folders.config('selected')?.revision, 41);
    assert.deepEqual(calls, ['/v1/admin/libraries/selected/settings', '/v1/admin/libraries/selected/inventory-config']);
    assert.deepEqual(Object.keys(f.folders.getSnapshot()).sort(), ['config', 'libraryId', 'loading', 'mutation']);
  } finally {f.folders.dispose();library.dispose();}
});

test('a library with no folders is a successful, genuine empty configuration', async () => {
  const f = fixture(async <T>() => configuration('selected', 1, []) as T);
  try {
    await f.folders.select('selected');
    const snapshot = f.folders.getSnapshot();
    assert.equal(snapshot.loading, false);assert.equal(snapshot.error, undefined);
    assert.deepEqual(f.folders.config('selected')?.sources, []);
    assert.deepEqual(f.folders.config('selected')?.policy, {tier: 'basic', revision: 4, operations: ['probe']});
    assert.deepEqual(f.errors, []);
  } finally {f.folders.dispose();}
});

test('replacing the selection aborts the old read and never publishes its late answer', async () => {
  let release!: (value: unknown) => void, firstSignal: AbortSignal | undefined;
  const f = fixture(async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
    if (path.includes('/first/')) {firstSignal = signal;return new Promise(resolve => {release = resolve;}) as Promise<T>;}
    return configuration('second', 5, [source('only', '/media/b', '/media/b', 'second')]) as T;
  });
  try {
    const first = f.folders.select('first');
    await f.folders.select('second');
    assert.equal(firstSignal?.aborted, true);
    release(configuration('first', 1, [source('one', '/media/a', '/media/a', 'first')]));
    await first;
    assert.equal(f.folders.config('first'), undefined);
    assert.equal(f.folders.config('second')?.sources[0].path, '/media/b');
    assert.equal(f.folders.getSnapshot().libraryId, 'second');assert.deepEqual(f.errors, []);
  } finally {f.folders.dispose();}
});

test('clearing or disposing during a read aborts it and suppresses the late configuration', async () => {
  for (const close of ['clear', 'dispose'] as const) {
    let release!: (value: unknown) => void, signal: AbortSignal | undefined;
    const f = fixture(async <T>(_path: string, _method?: string, _body?: unknown, captured?: AbortSignal) => {
      signal = captured;return new Promise(resolve => {release = resolve;}) as Promise<T>;
    });
    try {
      const pending = f.folders.select('selected');
      if (close === 'clear') f.folders.clear();else f.folders.dispose();
      assert.equal(signal?.aborted, true);
      release(configuration());
      await pending;
      assert.deepEqual(f.folders.getSnapshot(), {loading: false, mutation: 'idle'});
      assert.equal(f.folders.config('selected'), undefined);
      assert.deepEqual(f.errors, []);
    } finally {f.folders.dispose();}
  }
});

test('a retired caller lifetime cannot start reads or publish a late answer', async () => {
  let calls = 0, release!: (value: unknown) => void;
  const f = fixture(async <T>() => {calls++;return new Promise(resolve => {release = resolve;}) as Promise<T>;});
  try {
    const pending = f.folders.select('selected');
    f.retire();
    release(configuration());
    await pending;
    assert.equal(calls, 1);
    assert.deepEqual(f.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.equal(f.folders.config('selected'), undefined);
    await f.folders.select('selected');await f.folders.refresh();
    assert.equal(calls, 1);assert.deepEqual(f.errors, []);
  } finally {f.folders.dispose();}
});

test('a failed read exposes its error without a fabricated configuration or another library\'s folders', async () => {
  const failure = new Error('inventory configuration unavailable');
  const f = fixture(async <T>(path: string) => {
    if (!path.includes('/other/')) throw failure;
    return configuration('other', 5, [source('only', '/media/only', '/media/only', 'other')]) as T;
  });
  try {
    await f.folders.select('broken');
    assert.equal(f.folders.getSnapshot().libraryId, 'broken');
    assert.equal(f.folders.getSnapshot().loading, false);
    assert.equal(f.folders.getSnapshot().error, failure);
    assert.equal('config' in f.folders.getSnapshot(), false);
    assert.equal(f.folders.config('broken'), undefined);
    assert.deepEqual(f.errors, [failure]);
    await f.folders.select('other');
    assert.equal(f.folders.config('other')?.revision, 5);
    await f.folders.select('empty-error');
    assert.equal(f.folders.config('other'), undefined);
    assert.equal(f.folders.getSnapshot().error, failure);
    assert.equal('config' in f.folders.getSnapshot(), false);
    assert.deepEqual(f.errors, [failure, failure]);
  } finally {f.folders.dispose();}
});

test('an explicit refresh recovers from an error without duplicating a read already in flight', async () => {
  const failure = new Error('transient inventory failure');
  let attempts = 0, gate: Promise<void> | undefined, open!: () => void, signal: AbortSignal | undefined;
  const f = fixture(async <T>(_path: string, _method?: string, _body?: unknown, captured?: AbortSignal) => {
    attempts++;signal = captured;
    if (attempts === 1) throw failure;
    await gate;
    return configuration('selected', 40 + attempts, [source('recovered', '/media/recovered', '/media/recovered')]) as T;
  });
  try {
    await f.folders.select('selected');
    assert.equal(f.folders.getSnapshot().error, failure);
    assert.equal('config' in f.folders.getSnapshot(), false);
    gate = new Promise(resolve => {open = resolve;});
    const first = f.folders.refresh();
    assert.equal(f.folders.getSnapshot().loading, true);
    const second = f.folders.refresh();
    assert.equal(first, second);assert.equal(attempts, 2);
    assert.equal(f.folders.select('selected'), first);
    open();await first;
    assert.equal(f.folders.getSnapshot().loading, false);
    assert.equal(f.folders.getSnapshot().error, undefined);
    assert.equal(f.folders.config('selected')?.revision, 42);
    assert.equal(signal?.aborted, false);
    gate = new Promise(resolve => {open = resolve;});
    const third = f.folders.refresh();
    assert.equal(f.folders.getSnapshot().loading, true);
    assert.equal(f.folders.config('selected')?.sources[0].id, 'recovered');
    open();await third;
    assert.equal(attempts, 3);
    assert.equal(f.folders.config('selected')?.revision, 43);
    assert.deepEqual(f.errors, [failure]);
  } finally {f.folders.dispose();}
});

test('two controllers over one transport keep independent state, aborts and scope fences', async () => {
  let release!: (value: unknown) => void;
  const held = new Promise(resolve => {release = resolve;});
  const calls: {path: string; signal?: AbortSignal}[] = [];
  const request = async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
    calls.push({path, signal});
    if (path.includes('/held/')) return held as Promise<T>;
    const libraryId = path.includes('/other/') ? 'other' : 'selected';
    return configuration(libraryId, 5, [source('only', `/media/${libraryId}`, `/media/${libraryId}`, libraryId)]) as T;
  };
  const first = fixture(request), second = fixture(request), foreignErrors: unknown[] = [];
  const foreign = new SettingsLibraryFoldersSource({request}, {serverId: 'another-server', viewerId: 'owner'}, () => true, () => {}, error => {foreignErrors.push(error);});
  try {
    const reading = first.folders.select('held');
    await second.folders.select('other');
    assert.equal(first.folders.getSnapshot().loading, true);assert.equal(first.folders.config('held'), undefined);
    assert.equal(second.folders.config('other')?.sources[0].path, '/media/other');
    assert.deepEqual(calls.map(call => call.path), ['/v1/admin/libraries/held/inventory-config', '/v1/admin/libraries/other/inventory-config']);
    const firstNotified = first.notified(), secondNotified = second.notified();
    second.folders.dispose();
    assert.deepEqual(second.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    assert.equal(calls[0].signal?.aborted, false);
    release(configuration('held', 8, [source('only', '/media/held', '/media/held', 'held')]));
    await reading;
    assert.equal(first.folders.config('held')?.revision, 8);
    assert.equal(first.notified() > firstNotified, true);assert.equal(second.notified(), secondNotified);
    await foreign.select('selected');
    assert.equal(foreign.getSnapshot().error instanceof Error, true);
    assert.equal('config' in foreign.getSnapshot(), false);
    assert.equal(foreignErrors.length, 1);
    assert.equal(first.folders.config('held')?.revision, 8);
  } finally {first.folders.dispose();foreign.dispose();}
});

test('the controller keeps the caller scope it was constructed with', async () => {
  const callerScope = {serverId: 'server', viewerId: 'owner'};
  const errors: unknown[] = [];
  const folders = new SettingsLibraryFoldersSource({request: async <T>() => configuration('selected', 2) as T}, callerScope, () => true, () => {}, error => {errors.push(error);});
  try {
    callerScope.serverId = 'another-server';
    await folders.select('selected');
    assert.equal(folders.config('selected')?.revision, 2);
    assert.equal(folders.config('selected')?.scope.serverId, 'server');
    assert.deepEqual(errors, []);
  } finally {folders.dispose();}
});

test('clearing and reselecting the same library ignores a retired refresh success and failure', async () => {
  for (const outcome of ['success', 'failure'] as const) {
    let reads = 0, settle!: (value: unknown) => void, fail!: (error: unknown) => void, retiredSignal: AbortSignal | undefined;
    const f = fixture(async <T>(_path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
      reads++;
      if (reads === 2) {retiredSignal = signal;return new Promise((resolve, reject) => {settle = resolve;fail = reject;}) as Promise<T>;}
      return configuration('selected', reads === 1 ? 50 : 60, [source('only', '/media/current', '/media/current')]) as T;
    });
    try {
      await f.folders.select('selected');
      const retired = f.folders.refresh();
      f.folders.clear();
      await f.folders.select('selected');
      assert.equal(retiredSignal?.aborted, true);
      assert.equal(f.folders.config('selected')?.revision, 60);
      if (outcome === 'success') settle(configuration('selected', 9, [source('stale', '/media/stale')]));
      else fail(new Error('retired refresh failure'));
      await retired;
      assert.equal(f.folders.config('selected')?.revision, 60);
      assert.deepEqual(f.folders.config('selected')?.sources.map(item => item.id), ['only']);
      assert.equal(f.folders.getSnapshot().error, undefined);
      assert.deepEqual(f.errors, []);
    } finally {f.folders.dispose();}
  }
});

test('a superseded selection or a disposed controller never publishes or reports a stale error', async () => {
  for (const close of ['select', 'dispose'] as const) {
    let rejectRetired!: (error: unknown) => void, retiredSignal: AbortSignal | undefined;
    const f = fixture(async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
      if (path.includes('/retired/')) {retiredSignal = signal;return new Promise((_resolve, reject) => {rejectRetired = reject;}) as Promise<T>;}
      return configuration('current', 4, [source('only', '/media/c', '/media/c', 'current')]) as T;
    });
    try {
      const retired = f.folders.select('retired');
      if (close === 'select') await f.folders.select('current');
      else f.folders.dispose();
      assert.equal(retiredSignal?.aborted, true);
      rejectRetired(new Error('retired transport failure'));
      await retired;
      assert.deepEqual(f.errors, []);
      if (close === 'select') {assert.equal(f.folders.config('current')?.revision, 4);assert.equal(f.folders.getSnapshot().error, undefined);}
      else assert.deepEqual(f.folders.getSnapshot(), {loading: false, mutation: 'idle'});
    } finally {f.folders.dispose();}
  }
});

test('the canonical inventory projection passes through without reordering, merging or re-versioning', async () => {
  let serverRevision = 6;
  const f = fixture(async <T>() => {
    if (serverRevision === 6) return {
      scope: {serverId: 'server', viewerFence: 'fence-7'}, libraryId: 'selected', revision: 6,
      sources: [
        source('third', '/media/z', '/mnt/z'),
        {...source('third', '/media/duplicate', '/media/duplicate'), name: 'duplicate'},
        {id: 'unreadable', libraryId: 'selected', kind: 'local', name: 'broken'},
        source('first', '/media/a'),
      ],
      policy: {tier: 'basic', revision: 4, operations: ['probe']},
    } as T;
    return {scope: {serverId: 'server', viewerFence: 'fence-7'}, libraryId: 'selected', revision: 3, sources: [source('first', '/media/a')], policy: {tier: 'basic', revision: 4, operations: ['probe']}} as T;
  });
  try {
    await f.folders.select('selected');
    assert.equal(f.folders.config('selected')?.revision, 6);
    assert.deepEqual(f.folders.config('selected')?.sources.map(item => [item.id, item.path, item.resolvedPath]), [['third', '/media/z', '/mnt/z'], ['first', '/media/a', '/media/a']]);
    serverRevision = 3;
    await f.folders.refresh();
    assert.equal(f.folders.config('selected')?.revision, 3);
    assert.deepEqual(f.folders.config('selected')?.sources.map(item => item.id), ['first']);
    assert.deepEqual(f.errors, []);
  } finally {f.folders.dispose();}
});

const edit: SettingsLibraryFolderEdit = {name: 'New folder', path: '/media/new', classification: 'local', followSymlinks: false, intervalSeconds: 900, acceptReplacement: false};

test('adding a folder captures the inventory revision, serializes commands and publishes only the confirmed reread', async () => {
  let reads = 0, release!: () => void;
  const gate = {hold: undefined as Promise<void> | undefined};
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const f = fixture(async <T>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (method === 'POST') {await gate.hold;return source('added', '/media/new', '/media/new') as T;}
    reads++;
    return configuration('selected', reads === 1 ? 7 : 8, reads === 1 ? [source('existing', '/media/a')] : [source('existing', '/media/a'), source('added', '/media/new')]) as T;
  });
  try {
    await f.folders.select('selected');
    assert.equal(f.folders.getSnapshot().config?.revision, 7);
    gate.hold = new Promise(resolve => {release = resolve;});
    const saving = f.folders.save('selected', undefined, edit);
    assert.equal(f.folders.getSnapshot().mutation, 'pending');
    assert.deepEqual(f.folders.getSnapshot().config?.sources.map(item => item.id), ['existing']);
    assert.equal(await f.folders.save('selected', undefined, {...edit, name: 'Doubled'}), false);
    assert.equal(calls.filter(call => call.method === 'POST').length, 1);
    release();assert.equal(await saving, true);
    assert.deepEqual(calls.map(call => [call.path, call.method]), [
      ['/v1/admin/libraries/selected/inventory-config', 'GET'],
      ['/v1/admin/libraries/selected/sources', 'POST'],
      ['/v1/admin/libraries/selected/inventory-config', 'GET'],
    ]);
    assert.deepEqual(calls[1].body, {name: 'New folder', path: '/media/new', classification: 'local', followSymlinks: false, acceptReplacement: false, expectedRevision: 7, intervalMs: 900000});
    assert.equal(f.folders.getSnapshot().mutation, 'idle');
    assert.equal(f.folders.getSnapshot().error, undefined);
    assert.equal(f.folders.config('selected')?.revision, 8);
    assert.deepEqual(f.folders.config('selected')?.sources.map(item => item.id), ['existing', 'added']);
  } finally {f.folders.dispose();}
});

test('updating a folder uses the captured membership and exact CAS; a missing folder or foreign library never dispatches', async () => {
  let reads = 0;
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const f = fixture(async <T>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (method === 'PATCH') return source('existing', '/media/renamed', '/media/renamed') as T;
    reads++;
    return configuration('selected', 7 + reads, reads === 1 ? [source('existing', '/media/a')] : [source('existing', '/media/renamed', '/media/renamed')]) as T;
  });
  try {
    await f.folders.select('selected');
    assert.equal(await f.folders.save('selected', 'missing', edit), false);
    assert.equal(await f.folders.save('other', undefined, edit), false);
    assert.equal(calls.length, 1);
    assert.equal(await f.folders.save('selected', 'existing', {...edit, name: 'Renamed', path: '/media/renamed'}), true);
    assert.deepEqual(calls.map(call => [call.path, call.method]), [
      ['/v1/admin/libraries/selected/inventory-config', 'GET'],
      ['/v1/admin/libraries/selected/sources/existing', 'PATCH'],
      ['/v1/admin/libraries/selected/inventory-config', 'GET'],
    ]);
    assert.deepEqual(calls[1].body, {name: 'Renamed', path: '/media/renamed', classification: 'local', followSymlinks: false, acceptReplacement: false, expectedRevision: 8, intervalMs: 900000});
    assert.deepEqual(f.folders.config('selected')?.sources.map(item => [item.id, item.path]), [['existing', '/media/renamed']]);
  } finally {f.folders.dispose();}
});

test('removing a captured folder sends the canonical CAS and publishes only the confirmed reread', async () => {
  let reads = 0;
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const f = fixture(async <T>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (method === 'DELETE') return undefined as T;
    reads++;
    return configuration('selected', 4 + reads, reads === 1 ? [source('keep', '/media/keep'), source('gone', '/media/gone')] : [source('keep', '/media/keep')]) as T;
  });
  try {
    await f.folders.select('selected');
    assert.equal(await f.folders.remove('selected', 'missing'), false);
    assert.equal(calls.length, 1);
    assert.equal(await f.folders.remove('selected', 'gone'), true);
    assert.deepEqual(calls.map(call => [call.path, call.method]), [
      ['/v1/admin/libraries/selected/inventory-config', 'GET'],
      ['/v1/admin/libraries/selected/sources/gone', 'DELETE'],
      ['/v1/admin/libraries/selected/inventory-config', 'GET'],
    ]);
    assert.deepEqual(calls[1].body, {expectedRevision: 5});
    assert.deepEqual(f.folders.config('selected')?.sources.map(item => item.id), ['keep']);
    assert.equal(f.folders.getSnapshot().mutation, 'idle');
  } finally {f.folders.dispose();}
});

test('folder commands distinguish a refusal, an unknown outcome and a confirmed write awaiting reconciliation', async () => {
  for (const outcome of ['rejected', 'ambiguous', 'reconciliation'] as const) {
    let writes = 0, rereadFailures = 0;
    const f = fixture(async <T>(_path: string, method = 'GET') => {
      if (method === 'POST') {
        writes++;
        if (outcome === 'rejected') throw Object.assign(new Error('refused'), {code: 'invalid_request'});
        if (outcome === 'ambiguous') throw new TypeError('lost response');
        return source('added', '/media/new', '/media/new') as T;
      }
      if (rereadFailures > 0) {rereadFailures--;throw new Error('confirming reread failed');}
      return configuration('selected', 7, [source('existing', '/media/a')]) as T;
    });
    try {
      await f.folders.select('selected');
      if (outcome === 'reconciliation') rereadFailures = 1;
      assert.equal(await f.folders.save('selected', undefined, edit), false);
      const snapshot = f.folders.getSnapshot();
      assert.equal(snapshot.mutation, outcome);
      assert.equal(snapshot.error instanceof Error, true);
      if (outcome === 'rejected') {
        assert.equal(snapshot.config?.revision, 7);
        assert.equal(await f.folders.save('selected', undefined, edit), false);
        assert.equal(writes, 2);assert.equal(f.folders.getSnapshot().mutation, 'rejected');
      } else {
        assert.equal('config' in snapshot, false);
        assert.equal(await f.folders.save('selected', undefined, edit), false);
        assert.equal(writes, 1);
        await f.folders.refresh();
        assert.equal(f.folders.getSnapshot().mutation, 'idle');
        assert.equal(f.folders.getSnapshot().error, undefined);
        assert.equal(f.folders.config('selected')?.revision, 7);
      }
    } finally {f.folders.dispose();}
  }
});

test('clearing or replacing the selection suppresses a late folder command publication', async () => {
  for (const close of ['select', 'clear', 'dispose'] as const) {
    let release!: () => void, writes = 0, confirming = 0;
    const gate = {hold: undefined as Promise<void> | undefined};
    const f = fixture(async <T>(path: string, method = 'GET') => {
      if (method === 'POST') {writes++;await gate.hold;return source('added', '/media/new', '/media/new') as T;}
      const libraryId = path.includes('/other/') ? 'other' : 'selected';
      if (libraryId === 'selected') confirming++;
      return configuration(libraryId, 7, [source('existing', `/media/${libraryId}`, `/media/${libraryId}`, libraryId)]) as T;
    });
    try {
      await f.folders.select('selected');
      gate.hold = new Promise(resolve => {release = resolve;});
      const saving = f.folders.save('selected', undefined, edit);
      assert.equal(f.folders.getSnapshot().mutation, 'pending');
      if (close === 'select') await f.folders.select('other');
      else if (close === 'clear') f.folders.clear();
      else f.folders.dispose();
      const confirmed = confirming;
      release();assert.equal(await saving, false);
      assert.equal(writes, 1);assert.equal(confirming, confirmed);
      if (close === 'select') {assert.equal(f.folders.config('selected'), undefined);assert.equal(f.folders.config('other')?.sources[0].id, 'existing');}
      else assert.deepEqual(f.folders.getSnapshot(), {loading: false, mutation: 'idle'});
      assert.deepEqual(f.errors, []);
    } finally {f.folders.dispose();}
  }
});

test('replacing the selection and returning retires a held folder command without late publication', async () => {
  for (const outcome of ['success', 'failure'] as const) {
    let release!: () => void, signal: AbortSignal | undefined, reads = 0;
    const gate = {hold: undefined as Promise<void> | undefined};
    const f = fixture(async <T>(path: string, method = 'GET', _body?: unknown, captured?: AbortSignal) => {
      if (method === 'POST') {
        signal = captured;await gate.hold;
        if (outcome === 'failure') throw Object.assign(new Error('refused'), {code: 'invalid_request'});
        return source('added', '/media/new', '/media/new') as T;
      }
      const libraryId = path.includes('/other/') ? 'other' : 'selected';
      if (libraryId === 'selected') reads++;
      return configuration(libraryId, 7, [source('existing', `/media/${libraryId}`, `/media/${libraryId}`, libraryId)]) as T;
    });
    try {
      await f.folders.select('selected');
      gate.hold = new Promise(resolve => {release = resolve;});
      const saving = f.folders.save('selected', undefined, edit);
      await f.folders.select('other');
      assert.equal(signal?.aborted, true);
      await f.folders.select('selected');
      const confirmed = reads;
      release();assert.equal(await saving, false);
      assert.equal(reads, confirmed);
      assert.equal(f.folders.getSnapshot().libraryId, 'selected');
      assert.equal(f.folders.getSnapshot().mutation, 'idle');
      assert.equal(f.folders.getSnapshot().error, undefined);
      assert.equal(f.folders.config('selected')?.revision, 7);
      assert.deepEqual(f.folders.config('selected')?.sources.map(item => item.id), ['existing']);
      assert.deepEqual(f.errors, []);
    } finally {f.folders.dispose();}
  }
});
