import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeSearchDestination, nativeSearchEntryKey} from '../src-next/screens/search/model.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import type {SearchEntry, SearchGroup} from '../../../packages/client-core/src/search.ts';

const scope = {serverId: 'server-a', viewerId: 'local/account-a/profile-a'};
const history = {serverId: scope.serverId, viewerFence: 'search-history-fence', maxEntries: 8,
  entries: [{q: 'severance', updatedAt: '2026-10-23T00:00:00Z', uses: 1}]};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const deferred = <T>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done;}); return {promise, resolve};};
function result(path: string, ids: string[], nextCursor = '') {
  const query = new URL(path, 'https://test.invalid').searchParams;
  const group = (query.get('group') || 'movies') as SearchGroup;
  return {
    scope: {serverId: scope.serverId, libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'search-fence'},
    revision: {catalog: 1, viewer: 1},
    query: {q: query.get('q'), group: query.get('group') || '', groups: [group], limit: 40,
      sort: 'relevance', direction: 'desc', libraryIds: [], recorded: query.get('record') === '1'},
    capabilities: {groups: [{id: group, title: '', available: true, reason: '', sorts: ['relevance']}],
      sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
    groups: [{id: group, entityKind: 'movie', title: '', status: 'success', errorCode: '', totalCount: 3,
      totalCountExact: true, nextCursor, items: ids.map(id => ({id, kind: 'movie', title: id, libraryId: 'movies', navigation: {view: 'item', entityId: id}}))}],
  };
}
function setup(request: LibraryContentApi['request']) {
  let current = true, remember = true;
  const errors: unknown[] = [];
  const destination = new NativeSearchDestination({api: {request}, scope, current: () => current,
    rememberHistory: () => remember, reportError: error => errors.push(error)});
  return {destination, errors, retire: () => {current = false; destination.dispose();}, historyOff: () => {remember = false;}};
}

test('the actual search service appends cursor pages and keeps real result identities', async () => {
  const calls: string[] = [];
  const {destination} = setup(async <T>(path: string) => {
    calls.push(path);
    if (path.includes('/history')) return history as T;
    return result(path, path.includes('cursor=') ? ['movie-b', 'movie-c'] : ['movie-a', 'movie-b'], path.includes('cursor=') ? '' : 'next-page') as T;
  });
  try {
    destination.setText('matrix'); destination.selectGroup('movies');
    await tick(); await destination.next('movies');
    assert.equal(destination.getSnapshot().search.phase, 'ready');
    assert.deepEqual(destination.getSnapshot().groups[0]?.items.map(entry => entry.id), ['movie-a', 'movie-b', 'movie-c']);
    assert.equal(calls.length, 2);
    assert.equal(new URL(calls[1], 'https://test.invalid').searchParams.get('cursor'), 'next-page');
    const entry = destination.getSnapshot().groups[0].items[0];
    assert.equal(nativeSearchEntryKey('movies', entry), JSON.stringify(['movies', 'movie', 'movie-a']));
  } finally {destination.dispose();}
});

test('a newly typed query cancels and cannot publish an older response', async () => {
  const slow = deferred<unknown>(), signals: AbortSignal[] = [];
  const {destination} = setup(async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
    if (signal) signals.push(signal);
    if (path.includes('old-query')) return await slow.promise as T;
    return path.includes('/history') ? history as T : result(path, ['new-result']) as T;
  });
  try {
    destination.setText('old-query'); const pending = destination.commit();
    destination.setText('new-query'); await destination.commit();
    slow.resolve(result('/v1/search?q=old-query&record=1', ['old-result'])); await pending;
    assert.equal(signals[0].aborted, true);
    assert.equal(destination.getSnapshot().text, 'new-query');
    assert.deepEqual(destination.getSnapshot().groups[0]?.items.map(entry => entry.id), ['new-result']);
  } finally {destination.dispose();}
});

test('history reads cannot restore erased history after Clear', async () => {
  const oldRead = deferred<unknown>(); let read = 0;
  const {destination} = setup(async <T>(_path: string, method?: string) => {
    if (method === 'DELETE') return undefined as T;
    return (++read === 1 ? history : await oldRead.promise) as T;
  });
  try {
    await destination.refreshHistory();
    assert.equal(destination.getSnapshot().recent.length, 1);
    const pending = destination.refreshHistory();
    await destination.clearHistory();
    oldRead.resolve(history); await pending;
    assert.deepEqual(destination.getSnapshot().recent, []);
    assert.equal(destination.getSnapshot().clearing, false);
  } finally {destination.dispose();}
});

test('disabled search history is never requested or recorded', async () => {
  const calls: string[] = [];
  const controls = setup(async <T>(path: string) => {calls.push(path); return result(path, []) as T;});
  try {
    controls.historyOff(); await controls.destination.refreshHistory();
    controls.destination.setText('no-history'); await controls.destination.commit();
    assert.equal(calls.length, 1);
    assert.equal(new URL(calls[0], 'https://test.invalid').searchParams.has('record'), false);
    assert.deepEqual(controls.destination.getSnapshot().recent, []);
  } finally {controls.destination.dispose();}
});

test('retiring a viewer clears retained content, aborts history and stops the debounce', async () => {
  const late = deferred<unknown>(); let signal: AbortSignal | undefined, calls = 0;
  const controls = setup(async <T>(_path: string, _method?: string, _body?: unknown, value?: AbortSignal) => {
    calls++; signal = value; return await late.promise as T;
  });
  const pending = controls.destination.refreshHistory();
  controls.destination.setText('waiting'); controls.retire();
  late.resolve(history); await pending;
  await new Promise(resolve => setTimeout(resolve, 280));
  assert.equal(signal?.aborted, true);
  assert.equal(calls, 1);
  assert.equal(controls.destination.getSnapshot().text, '');
  assert.deepEqual(controls.destination.getSnapshot().recent, []);
  assert.equal(controls.destination.getSnapshot().search.data, null);
});

test('group and media kind are part of a mixed selection key', () => {
  const movie = {id: 'shared-id', kind: 'movie'} as SearchEntry;
  const song = {id: 'shared-id', kind: 'song'} as SearchEntry;
  assert.notEqual(nativeSearchEntryKey('movies', movie), nativeSearchEntryKey('songs', song));
});

test('pause retains only arrived query/cursor data and refuses hidden callbacks or late next pages', async () => {
  const slow = deferred<unknown>(), calls: string[] = [], signals: AbortSignal[] = [];
  const {destination} = setup(async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => {
    calls.push(path); if (signal) signals.push(signal);
    if (path.includes('cursor=third')) return await slow.promise as T;
    return result(path, path.includes('cursor=second') ? ['movie-b'] : ['movie-a'], path.includes('cursor=second') ? 'third' : 'second') as T;
  });
  try {
    destination.setText('matrix'); destination.selectGroup('movies'); await tick();
    await destination.next('movies'); const next = destination.next('movies');
    destination.pause(); assert.equal(signals.at(-1)?.aborted, true);
    const paused = destination.getSnapshot();
    assert.equal(paused.search.phase, 'ready'); assert.equal(paused.search.pagination.cursor, 'second');
    assert.equal(paused.search.pagination.canPrevious, true); assert.equal(paused.search.query?.q, 'matrix');
    assert.deepEqual(paused.groups[0].items.map(entry => entry.id), ['movie-a', 'movie-b']);
    destination.setText('hidden callback'); destination.selectGroup('people');
    await destination.commit(); await destination.next('movies'); await destination.retry(); await destination.clearHistory();
    assert.equal(calls.length, 3); assert.equal(destination.getSnapshot().text, 'matrix');
    slow.resolve(result(calls[2], ['late-private-page'])); await next;
    destination.resume(); await tick();
    assert.equal(calls.length, 3, 'Back reuses the actual arrived page rather than replaying a cursor or recording history');
    assert.deepEqual(destination.getSnapshot().groups[0].items.map(entry => entry.id), ['movie-a', 'movie-b']);
  } finally {destination.dispose();}
});

test('pause stops the real debounce and an interrupted draft resumes without recording history', async () => {
  const calls: string[] = [];
  const {destination} = setup(async <T>(path: string) => {calls.push(path); return result(path, ['actual-result']) as T;});
  try {
    destination.setText('draft'); destination.pause();
    await new Promise<void>(resolve => setTimeout(resolve, 280)); assert.equal(calls.length, 0);
    destination.resume(); await tick();
    assert.equal(calls.length, 1); assert.notEqual(new URL(calls[0], 'https://test.invalid').searchParams.get('record'), '1');
    assert.notEqual(destination.getSnapshot().search.query?.record, true);
    assert.equal(destination.getSnapshot().search.data?.query.recorded, false);
    assert.equal(destination.getSnapshot().search.query?.q, 'draft');
    assert.equal(destination.getSnapshot().groups[0].items[0].id, 'actual-result');
  } finally {destination.dispose();}
});

test('privacy-off clears arrived history during an explicit clear, and pause cannot revive its late result', async () => {
  const clear = deferred<unknown>(), calls: string[] = []; let signal: AbortSignal | undefined;
  const f = setup(async <T>(path: string, method?: string, _body?: unknown, abort?: AbortSignal) => {
    calls.push(path);
    if (method === 'DELETE') {signal = abort; return await clear.promise as T;}
    return history as T;
  });
  try {
    await f.destination.refreshHistory(); assert.equal(f.destination.getSnapshot().recent.length, 1);
    const pending = f.destination.clearHistory(); f.historyOff(); await f.destination.refreshHistory();
    assert.deepEqual(f.destination.getSnapshot().recent, []); assert.equal(f.destination.getSnapshot().clearing, true);
    f.destination.pause(); assert.equal(signal?.aborted, true); assert.equal(f.destination.getSnapshot().clearing, false);
    clear.resolve({cleared: true}); await pending;
    f.destination.resume(); await f.destination.refreshHistory();
    assert.deepEqual(f.destination.getSnapshot().recent, []); assert.equal(calls.length, 2); assert.deepEqual(f.errors, []);
  } finally {f.destination.dispose();}
});

test('an arrived read error survives pause/resume until the actual retry gesture', async () => {
  let reads = 0, fail = false;
  const f = setup(async <T>(path: string) => {
    reads++;
    if (fail) throw Object.assign(new Error('actual read failure'), {status: 503, code: 'unavailable'});
    return result(path, ['actual-result'], 'actual-next-cursor') as T;
  });
  try {
    f.destination.setText('matrix'); f.destination.selectGroup('movies'); await tick();
    assert.equal(f.destination.getSnapshot().search.phase, 'ready');
    fail = true; await f.destination.next('movies');
    const error = f.destination.getSnapshot().search.error; assert.equal(f.destination.getSnapshot().search.phase, 'error'); assert.ok(error);
    f.destination.pause(); f.destination.resume(); await tick();
    assert.equal(reads, 2); assert.deepEqual(f.destination.getSnapshot().search.error, error);
    assert.equal(f.destination.getSnapshot().search.phase, 'error');
    fail = false; await f.destination.retry();
    assert.equal(reads, 3); assert.equal(f.destination.getSnapshot().search.phase, 'ready');
  } finally {f.destination.dispose();}
});
