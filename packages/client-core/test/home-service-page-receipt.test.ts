import test from 'node:test';
import assert from 'node:assert/strict';
import {type HomeApi} from '../src/home.ts';
import {HomeService, type HomePageOutcome} from '../src/home-service.ts';

const entry = (id: string) => ({id, kind: 'movie', title: id, libraryId: 'movies', available: true, navigation: {view: 'item', entityId: id}, playback: {itemId: id}});
const row = (id: string, ids = ['old'], changes: Record<string, unknown> = {}) => ({
  id, viewerFence: 'viewer', title: id, entries: ids.map(entry), nextCursor: 'cursor:' + id,
  revision: {catalog: 1, viewer: 1}, total: 200, start: 0, limit: 2, hasMore: true, ...changes,
});
const home = (rows = [row('recent')], changes: Record<string, unknown> = {}) => ({
  serverId: 'server', viewerFence: 'viewer', rows, spotlight: [],
  layout: {revision: 1, rowOrder: rows.map(value => value.id), hiddenRowIds: []},
  revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T12:00:00.000Z', ...changes,
});
function deferred<T = unknown>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
}
const api = (handler: (path: string, signal?: AbortSignal) => unknown): HomeApi => ({
  request: async <T>(path: string, _method?: string, _body?: unknown, signal?: AbortSignal) => await handler(path, signal) as T,
});
const shown = (service: HomeService, id = 'recent') => service.getSnapshot().document!.rows.find(value => value.id === id)!;
const applied = (rowId = 'recent', firstItemId: string | null = 'new'): HomePageOutcome => ({rowId, requestedCursor: 'cursor:' + rowId, status: 'applied', applied: true, firstItemId});

test('receipt names only its requested row/cursor and first newly appended raw ID', async t => {
  const first = deferred(), second = deferred();
  const paths: string[] = [];
  const service = new HomeService(api(path => {
    paths.push(path);
    return path === '/v1/home' ? home([row('recent'), row('other')]) : path.includes('/recent?') ? first.promise : second.promise;
  }));
  t.after(() => service.dispose());
  await service.load();
  const pending = service.more('recent');
  const unrelated = service.more('other');
  second.resolve(row('other', ['other-new'], {nextCursor: 'other-next'}));
  assert.deepEqual(await unrelated, applied('other', 'other-new'));
  assert.deepEqual([...service.getSnapshot().paging], ['recent']);
  first.resolve(row('recent', ['old', 'raw:new:/id', 'later'], {nextCursor: 'next'}));
  assert.deepEqual(await pending, applied('recent', 'raw:new:/id'));
  assert.deepEqual(shown(service).entries.map(value => value.id), ['old', 'raw:new:/id', 'later']);
  assert.equal(new URL(paths[1], 'http://test.invalid').searchParams.get('cursor'), 'cursor:recent');
  assert.equal(service.getSnapshot().paging.size, 0);
});

for (const [label, page] of [
  ['empty', row('recent', [], {nextCursor: 'advanced'})],
  ['duplicates only', row('recent', ['old', 'old'], {nextCursor: 'advanced'})],
  ['filtered unreadable', row('recent', [], {entries: [{id: 'invalid'}], nextCursor: 'advanced'})],
] as const) {
  test(`${label} advanced page is applied with an explicit no-item receipt`, async t => {
    const service = new HomeService(api(path => path === '/v1/home' ? home() : page));
    t.after(() => service.dispose());
    await service.load();
    assert.deepEqual(await service.more('recent'), applied('recent', null));
    assert.equal(shown(service).nextCursor, 'advanced');
    assert.deepEqual(shown(service).entries.map(value => value.id), ['old']);
    assert.equal(service.getSnapshot().paging.size, 0);
  });
}

test('receipt includes new cards beyond the former retained-row cap', async t => {
  const full = Array.from({length: 100}, (_, index) => 'old-' + index);
  const service = new HomeService(api(path => path === '/v1/home' ? home([row('recent', full)]) : row('recent', ['discarded'], {nextCursor: 'advanced'})));
  t.after(() => service.dispose());
  await service.load();
  assert.deepEqual(await service.more('recent'), applied('recent', 'discarded'));
  assert.equal(shown(service).entries.length, 101);
  assert.equal(shown(service).nextCursor, 'advanced');
});

test('an empty page without cursor progress never fulfils a wait', async t => {
  const service = new HomeService(api(path => path === '/v1/home' ? home() : row('recent', [])));
  t.after(() => service.dispose());
  await service.load();
  const before = shown(service);
  assert.deepEqual(await service.more('recent'), {rowId: 'recent', requestedCursor: 'cursor:recent', status: 'ignored', applied: false, reason: 'no_progress'});
  assert.equal(shown(service), before);
  assert.equal(service.getSnapshot().paging.size, 0);
});

for (const refreshFails of [false, true]) {
  test(`stale refusal performs a genuine refresh and returns ${refreshFails ? 'stale' : 'revalidated'}, never applied`, async t => {
    let reads = 0;
    const service = new HomeService(api(path => {
      if (path !== '/v1/home') throw {code: 'stale_continuation', status: 409};
      if (++reads === 1) return home([row('new_on_server')]);
      if (refreshFails) throw {status: 503};
      return home([row('new_on_server', ['refresh-only'], {nextCursor: 'fresh'})]);
    }));
    t.after(() => service.dispose());
    await service.load();
    assert.deepEqual(await service.more('new_on_server'), {rowId: 'new_on_server', requestedCursor: 'cursor:new_on_server', status: refreshFails ? 'stale' : 'revalidated', applied: false});
    assert.equal(reads, 2);
    assert.equal(shown(service, 'new_on_server').nextCursor, refreshFails ? '' : 'fresh');
    assert.equal(service.getSnapshot().paging.size, 0);
  });
}

test('while a library is scanned, stale pages refresh Home at most once per spacing and the row stops asking', async t => {
  let reads = 0, now = 0;
  const service = new HomeService(api(path => {
    if (path !== '/v1/home') throw {code: 'stale_continuation', status: 409};
    reads++;
    return home([row('recent', ['old'], {nextCursor: 'cursor:recent'}), row('other', ['other'], {nextCursor: 'cursor:other'})]);
  }), {now: () => now, staleRefreshEveryMs: 5000});
  t.after(() => service.dispose());
  await service.load();
  assert.equal((await service.more('recent')).status, 'revalidated');
  assert.equal(reads, 2, 'the first stale page brings Home up to date');
  now = 400;
  assert.deepEqual(await service.more('other'), {rowId: 'other', requestedCursor: 'cursor:other', status: 'stale', applied: false});
  assert.equal(reads, 2, 'a second stale page inside the spacing does not reload Home');
  assert.equal(shown(service, 'other').nextCursor, '', 'that row keeps its cards and stops asking until a later read');
  assert.deepEqual(shown(service, 'other').entries.map(value => value.id), ['other']);
  now = 5400;
  assert.equal((await service.more('recent')).status, 'revalidated');
  assert.equal(reads, 3, 'after the spacing a stale page refreshes again');
  assert.equal(shown(service, 'other').nextCursor, 'cursor:other', 'the refresh gave the quiet row its cursor back');
});

test('request failure retains the exact cursor for a later successful retry', async t => {
  let fail = true;
  const service = new HomeService(api(path => {
    if (path === '/v1/home') return home();
    if (fail) throw {status: 503};
    return row('recent', ['new'], {nextCursor: 'advanced'});
  }));
  t.after(() => service.dispose());
  await service.load();
  assert.deepEqual(await service.more('recent'), {rowId: 'recent', requestedCursor: 'cursor:recent', status: 'failed', applied: false});
  assert.equal(shown(service).nextCursor, 'cursor:recent');
  assert.equal(service.getSnapshot().paging.size, 0);
  fail = false;
  assert.deepEqual(await service.more('recent'), applied());
});

test('permission refusal removes the row and never fulfils its page', async t => {
  const service = new HomeService(api(path => { if (path === '/v1/home') return home(); throw {status: 403}; }));
  t.after(() => service.dispose());
  await service.load();
  assert.deepEqual(await service.more('recent'), {rowId: 'recent', requestedCursor: 'cursor:recent', status: 'refused', applied: false, reason: 'permission_denied'});
  assert.equal(shown(service), undefined);
  assert.equal(service.getSnapshot().paging.size, 0);
});

for (const change of ['cursor', 'same cursor membership', 'removed', 'viewer', 'server']) {
  test(`a quiet refresh with ${change} opposes a pending page`, async t => {
    const response = deferred();
    let fresh = home();
    const service = new HomeService(api(path => path === '/v1/home' ? fresh : response.promise));
    t.after(() => service.dispose());
    await service.load();
    const pending = service.more('recent');
    fresh = change === 'removed' ? home([]) : home([row('recent', ['refresh-only'], {nextCursor: change === 'cursor' ? 'fresh' : 'cursor:recent'})], change === 'viewer' ? {viewerFence: 'other', rows: [row('recent', ['refresh-only'], {viewerFence: 'other'})]} : change === 'server' ? {serverId: 'other'} : {});
    await service.revalidate();
    const refreshed = service.getSnapshot().document;
    response.resolve(row('recent', ['opposed-page'], {nextCursor: 'opposed-next'}));
    const outcome = await pending;
    assert.equal(outcome.status, 'ignored');
    assert.equal(outcome.applied, false);
    assert.equal(outcome.requestedCursor, 'cursor:recent');
    assert.equal(service.getSnapshot().document, refreshed);
    assert.equal(service.getSnapshot().paging.size, 0);
  });
}

test('a refresh that only changes another row does not steal the requested page receipt', async t => {
  const response = deferred();
  let fresh = home([row('recent'), row('other')]);
  const service = new HomeService(api(path => path === '/v1/home' ? fresh : response.promise));
  t.after(() => service.dispose());
  await service.load();
  const pending = service.more('recent');
  fresh = home([row('recent'), row('other', ['other-refresh'])]);
  await service.revalidate();
  response.resolve(row('recent', ['new'], {nextCursor: 'advanced'}));
  assert.deepEqual(await pending, applied());
});

for (const error of [{status: 403}, {code: 'stale_continuation', status: 409}]) {
  test(`late ${error.status} cannot remove or revalidate a replacement row`, async t => {
    const response = deferred();
    let reads = 0;
    const service = new HomeService(api(path => path === '/v1/home' ? (++reads, home([row('recent', reads === 1 ? ['old'] : ['fresh'])])) : response.promise));
    t.after(() => service.dispose());
    await service.load();
    const pending = service.more('recent');
    await service.revalidate();
    response.reject(error);
    assert.equal((await pending).status, 'ignored');
    assert.equal(reads, 2);
    assert.deepEqual(shown(service).entries.map(value => value.id), ['fresh']);
  });
}

test('load generation aborts an uncooperative page without clearing a newer page owner', async t => {
  const old = deferred(), newer = deferred();
  const signals: AbortSignal[] = [];
  const service = new HomeService(api((path, signal) => {
    if (path === '/v1/home') return home();
    signals.push(signal!);
    return signals.length === 1 ? old.promise : newer.promise;
  }));
  t.after(() => service.dispose());
  await service.load();
  const pending = service.more('recent');
  await service.load();
  const next = service.more('recent');
  assert.equal((await pending).status, 'ignored');
  assert.equal(signals[0].aborted, true);
  assert.deepEqual([...service.getSnapshot().paging], ['recent']);
  old.resolve(row('recent', ['opposed'], {nextCursor: 'wrong'}));
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual([...service.getSnapshot().paging], ['recent']);
  newer.resolve(row('recent', ['new'], {nextCursor: 'advanced'}));
  assert.deepEqual(await next, applied());
  assert.equal(service.getSnapshot().paging.size, 0);
});

test('dispose settles a pending uncooperative page and never publishes its answer', async () => {
  const response = deferred();
  let signal!: AbortSignal;
  const service = new HomeService(api((path, readSignal) => { signal = readSignal!; return path === '/v1/home' ? home() : response.promise; }));
  await service.load();
  const pending = service.more('recent');
  service.dispose();
  assert.equal(signal.aborted, true);
  const before = service.getSnapshot();
  assert.deepEqual(await pending, {rowId: 'recent', requestedCursor: 'cursor:recent', status: 'ignored', applied: false, reason: 'disposed'});
  response.resolve(row('recent', ['late'], {nextCursor: 'wrong'}));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(service.getSnapshot(), before);
});

test('a load started synchronously by a commit listener prevents an obsolete receipt', async t => {
  const service = new HomeService(api(path => path === '/v1/home' ? home() : row('recent', ['new'], {nextCursor: 'advanced'})));
  t.after(() => service.dispose());
  await service.load();
  let reload: Promise<void> | undefined;
  const unsubscribe = service.subscribe(() => {
    if (shown(service)?.nextCursor === 'advanced') { unsubscribe(); reload = service.load({clear: true}); }
  });
  assert.equal((await service.more('recent')).status, 'ignored');
  await reload;
  assert.deepEqual(shown(service).entries.map(value => value.id), ['old']);
  assert.equal(service.getSnapshot().paging.size, 0);
});

test('a load started by paging cleanup also prevents an obsolete receipt', async t => {
  const service = new HomeService(api(path => path === '/v1/home' ? home() : row('recent', ['new'], {nextCursor: 'advanced'})));
  t.after(() => service.dispose());
  await service.load();
  let reload: Promise<void> | undefined;
  const unsubscribe = service.subscribe(() => {
    if (shown(service)?.nextCursor === 'advanced' && !service.getSnapshot().paging.size) { unsubscribe(); reload = service.load({clear: true}); }
  });
  assert.equal((await service.more('recent')).status, 'ignored');
  await reload;
  assert.equal(service.getSnapshot().paging.size, 0);
});

for (const stop of ['load', 'dispose']) {
  test(`${stop} during stale recovery cannot turn the refresh into a page receipt`, async t => {
    const refresh = deferred();
    let reads = 0;
    const service = new HomeService(api(path => {
      if (path !== '/v1/home') throw {code: 'stale_continuation', status: 409};
      return ++reads === 2 ? refresh.promise : home();
    }));
    t.after(() => service.dispose());
    await service.load();
    const pending = service.more('recent');
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(reads, 2);
    if (stop === 'load') await service.load(); else service.dispose();
    refresh.resolve(home([row('recent', ['recovery-only'])]));
    assert.equal((await pending).status, 'ignored');
    assert.deepEqual(shown(service).entries.map(value => value.id), ['old']);
  });
}

test('Live page revision, observation and group guards remain enforced', async t => {
  const source = {id: 'source', name: 'Source', generation: 'g', publishedAt: '2026-10-21T00:00:00Z', availableStart: '2026-10-21T00:00:00Z', availableEnd: '2026-10-23T00:00:00Z', provenance: 'live-source', refreshState: 'ready'};
  const channel = (id: string) => ({source, channel: {id, sourceId: source.id, provenance: 'live-source', name: id, number: '1', group: '', generation: 'g', favorite: false, hidden: false, preferenceRevision: 1, tuneAvailable: true, recordAvailable: false, mark: {text: id, seed: source.id + ':' + id}, programmes: []}});
  const live = (id: string, changes: Record<string, unknown> = {}) => ({revision: 'live', observedAt: '2026-10-22T00:30:00Z', expiresAt: '2026-10-22T00:31:00Z', sort: 'favorites', group: '', groups: [], channels: [channel(id)], ...changes});
  for (const change of [{}, {revision: 'opposed'}, {observedAt: '2026-10-22T00:30:01Z'}, {group: 'opposed'}]) {
    const service = new HomeService(api(path => path === '/v1/home' ? home([row('on_now', [], {live: live('old')})]) : row('on_now', [], {live: live('new', change), nextCursor: 'advanced'})));
    t.after(() => service.dispose());
    await service.load();
    const outcome = await service.more('on_now');
    const valid = !Object.keys(change).length;
    assert.equal(outcome.applied, valid);
    assert.deepEqual(shown(service, 'on_now').live!.channels.map(value => value.channel.id), valid ? ['old', 'new'] : ['old']);
    if (outcome.applied) assert.equal(outcome.firstItemId, null, 'Live does not manufacture a regular-card entry ID');
    assert.equal(service.getSnapshot().paging.size, 0);
  }
});

test('ignored and refused calls are typed non-receipts and issue no extra request', async t => {
  let requests = 0;
  const response = deferred();
  const service = new HomeService(api(path => { requests++; return path === '/v1/home' ? home([row('recent'), row('complete', [], {nextCursor: ''})]) : response.promise; }));
  t.after(() => service.dispose());
  assert.equal((await service.more('recent')).status, 'refused');
  await service.load();
  assert.equal((await service.more('absent')).status, 'refused');
  assert.equal((await service.more('complete')).status, 'refused');
  const pending = service.more('recent');
  assert.deepEqual(await service.more('recent'), {rowId: 'recent', requestedCursor: 'cursor:recent', status: 'refused', applied: false, reason: 'already_paging'});
  assert.equal(requests, 2);
  response.resolve(row('recent', ['new'], {nextCursor: 'advanced'}));
  assert.deepEqual(await pending, applied());
});

test('wrong viewer or wrong row responses cannot produce a receipt', async t => {
  for (const wrong of [row('recent', ['private'], {viewerFence: 'other'}), row('other', ['wrong'])]) {
    const service = new HomeService(api(path => path === '/v1/home' ? home() : wrong));
    t.after(() => service.dispose());
    await service.load();
    assert.equal((await service.more('recent')).applied, false);
    assert.deepEqual(shown(service).entries.map(value => value.id), ['old']);
    assert.equal(service.getSnapshot().paging.size, 0);
  }
});
