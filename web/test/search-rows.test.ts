import test from 'node:test';
import assert from 'node:assert/strict';
import {createSearchRows} from '../src/screens/search/rows.ts';

const entry = (id: string, libraryId = 'library') => ({id, libraryId, kind: 'movie', title: id});
const group = (id = 'movies', items = [entry('first')], nextCursor = 'next') => ({id, items, nextCursor, hasMore: !!nextCursor, status: 'success', errorCode: '', title: id, entityKind: 'movie', totalCount: 100, totalCountExact: true});
function snapshot(groups = [group()], changes: any = {}) {
  const query = {q: 'harbor', groups: ['movies', 'shows'], sort: 'relevance', direction: 'desc', libraryIds: ['library']};
  const data = {scope: {serverId: 'server', viewerFence: 'viewer'}, revision: {catalog: 1, viewer: 1}, query: {...query, group: ''}, groups};
  return {generation: 1, scope: {serverId: 'server', viewerId: 'viewer'}, query, phase: 'ready', groups, data, pagination: {cursor: null, canPrevious: false}, error: null, ...changes};
}
function fixture(initial = snapshot()) {
  let state = initial, current = true;
  const listeners = new Set<() => void>(), children: any[] = [], selections: any[] = [];
  const source = {getSnapshot: () => state, subscribe: (fn: () => void) => {listeners.add(fn);return () => listeners.delete(fn);},
    retry: async () => {}, select: async (...args: any[]) => {selections.push(args);},
    forkContinuation(id: string) {
      let childState = state;
      const watchers = new Set<() => void>();
      const child = {id, disposed: false, reads: 0, retries: 0,
        getSnapshot: () => childState, subscribe(fn: () => void) {watchers.add(fn);return () => watchers.delete(fn);},
        emit(next: any) {childState = next; for (const fn of [...watchers]) fn();},
        async next(groupId: string) {
          assert.equal(groupId, id);child.reads++;
          const cursor = childState.groups.find((result: any) => result.id === id)?.nextCursor;
          child.emit({...childState, phase: 'loading', data: null, error: null, pagination: {cursor, canPrevious: true}});
        },
        async retry() {child.retries++;child.emit({...childState, phase: 'loading', data: null, error: null});},
        dispose() {child.disposed = true;watchers.clear();},
      };
      children.push(child); return child;
    }};
  const rows = createSearchRows(source as any, () => current);
  return {rows, children, selections, source, retire: () => {current = false;},
    replace(next: any) {state = next;for (const fn of [...listeners]) fn();},
    answer(child: any, items: any[], cursor = '', overrides: any = {}) {
      const result = group(child.id, items, cursor), page = snapshot([result], {pagination: {cursor: 'used-cursor', canPrevious: true}});
      page.data = {...page.data, query: {...page.data.query, group: child.id}, ...overrides};
      child.emit(page);
    }};
}

test('All rows independently append offered pages, preserve objects and source order, deduplicate and stop at EOF', async () => {
  const first = entry('first'), show = entry('show'), f = fixture(snapshot([group('movies', [first]), group('shows', [show])]));
  const original = f.source.getSnapshot(), offeredShow = f.rows.getSnapshot().groups[1];
  await f.rows.next(f.rows.getSnapshot().groups[0] as any);
  assert.equal(f.rows.page('movies').loading, true);
  assert.deepEqual(f.rows.getSnapshot().groups[0].items, [first]);
  assert.equal(f.rows.getSnapshot().groups[1], offeredShow, 'another row keeps its callback identity');
  await f.rows.next(offeredShow as any);
  assert.equal(f.children.length, 2);
  const second = entry('second'), otherLibrary = entry('first', 'other');
  f.answer(f.children[0], [entry('first'), second, otherLibrary], 'last');
  const appended = f.rows.getSnapshot().groups[0];
  assert.deepEqual(appended.items, [first, second, otherLibrary]);
  assert.strictEqual(appended.items[0], first);
  assert.strictEqual(f.source.getSnapshot(), original, 'continuation never narrows the parent All query');
  assert.strictEqual(f.rows.getSnapshot().data?.groups, f.rows.getSnapshot().groups, 'actions and selection read the same admitted page');
  await f.rows.next(appended);
  f.answer(f.children[0], [entry('third')]);
  const eof = f.rows.getSnapshot().groups[0];
  assert.equal(eof.nextCursor, '');
  await f.rows.next(eof);assert.equal(f.children[0].reads, 2);
  f.rows.dispose();assert.ok(f.children.every(child => child.disposed));
});

test('concurrent loads and stale row callbacks cannot claim or repeat an offered cursor', async () => {
  const f = fixture(), offered = f.rows.getSnapshot().groups[0];
  await f.rows.next({...offered} as any);assert.equal(f.children.length, 0);
  await Promise.all([f.rows.next(offered as any), f.rows.next(offered as any)]);
  assert.equal(f.children[0].reads, 1);
  f.answer(f.children[0], [entry('second')], 'third');
  await f.rows.next(offered as any);assert.equal(f.children[0].reads, 1, 'pre-append callback cannot advance the successor');
  f.rows.dispose();
});

test('a retryable first continuation failure retains all admitted rows and retries only its child', async () => {
  const f = fixture(), offered = f.rows.getSnapshot().groups[0];
  await f.rows.next(offered as any);
  const child = f.children[0];
  child.emit({...child.getSnapshot(), phase: 'error', data: null, error: {code: 'request_failed', message: '', retryable: true}});
  assert.equal(f.rows.getSnapshot().phase, 'ready');
  assert.strictEqual(f.rows.getSnapshot().groups[0], offered);
  assert.equal(f.rows.page('movies').error?.code, 'request_failed');
  await f.rows.next(offered as any);assert.equal(child.reads, 1);
  await f.rows.retry(offered as any);assert.equal(child.retries, 1);
  f.answer(child, [entry('second')]);
  assert.equal(f.rows.getSnapshot().groups[0].items.length, 2);
  assert.equal(f.rows.page('movies').error, null);
  f.rows.dispose();
});

test('query replacement, same-object pause/replay and disposal retire children and old callbacks', async () => {
  const f = fixture(), initial = f.source.getSnapshot(), offered = f.rows.getSnapshot().groups[0];
  await f.rows.next(offered as any);const child = f.children[0];
  f.replace({...initial, generation: 2});
  assert.equal(child.disposed, true);
  await f.rows.next(offered as any);assert.equal(f.children.length, 1);
  const replacement = f.rows.getSnapshot().groups[0];
  await f.rows.next(replacement);assert.equal(f.children.length, 2);
  f.answer(child, [entry('late')]);assert.equal(f.rows.getSnapshot().groups[0].items.length, 1);
  f.rows.dispose();assert.equal(f.children[1].disposed, true);
  assert.equal(f.rows.getSnapshot().data, null);assert.deepEqual(f.rows.getSnapshot().groups, []);
  await f.rows.next(replacement);assert.equal(f.children.length, 2);
});

test('refused-cursor recovery withdraws All immediately while the replacement loads and stays withdrawn after a transient failure', async () => {
  const f = fixture(), original = f.source.getSnapshot(), offered = f.rows.getSnapshot().groups[0];
  await f.rows.next(offered as any);
  const child = f.children[0], loading = child.getSnapshot();
  assert.equal(loading.pagination.cursor, offered.nextCursor);
  assert.equal(f.rows.getSnapshot().phase, 'ready', 'ordinary offered-cursor loading retains admitted rows');
  // This is core fetch()'s invalidateRead() publish, immediately before its first-page load.
  child.emit({...loading, data: null, groups: []});
  assert.equal(f.rows.getSnapshot().phase, 'ready');
  child.emit({...loading, data: null, groups: [], pagination: {cursor: null, canPrevious: false}});
  const withdrawn = f.rows.getSnapshot();
  assert.equal(withdrawn.phase, 'refresh-required');
  assert.equal(withdrawn.data, null);assert.deepEqual(withdrawn.groups, []);
  assert.equal(child.disposed, true, 'the fallback cannot keep an independent stale lifetime alive');
  // Even a late fallback failure/completion cannot make the old All action source ready.
  child.emit({...child.getSnapshot(), phase: 'error', error: {code: 'timeout', message: '', retryable: true}});
  assert.strictEqual(f.rows.getSnapshot(), withdrawn);
  await f.rows.next(offered as any);assert.equal(f.children.length, 1);
  await f.rows.retry();
  assert.deepEqual(f.selections, [[original.query.q, {...original.query, record: false}]]);
  f.rows.dispose();
});

test('changed continuation identity or revision withdraws all actions and requires a fresh unrecorded All read', async () => {
  for (const change of ['viewer', 'catalog', 'query', 'sort', 'libraries', 'first-page']) {
    const f = fixture(), original = f.source.getSnapshot();
    await f.rows.next(f.rows.getSnapshot().groups[0] as any);
    const child = f.children[0], data = snapshot().data;
    const overrides = change === 'viewer' ? {scope: {...data.scope, viewerFence: 'new'}} : change === 'catalog' ? {revision: {...data.revision, catalog: 2}}
      : change === 'query' ? {query: {...data.query, group: 'movies', q: 'another'}} : change === 'sort' ? {query: {...data.query, group: 'movies', sort: 'title'}}
      : change === 'libraries' ? {query: {...data.query, group: 'movies', libraryIds: ['other']}} : {};
    if (change === 'first-page') {
      const answer = snapshot([group('movies', [entry('new')])]);answer.data.query.group = 'movies';child.emit(answer);
    } else f.answer(child, [entry('new')], '', overrides);
    assert.equal(f.rows.getSnapshot().phase, 'refresh-required', change);
    assert.equal(f.rows.getSnapshot().data, null, change);
    assert.deepEqual(f.rows.getSnapshot().groups, [], change);
    assert.equal(child.disposed, true, change);
    await f.rows.retry();
    assert.deepEqual(f.selections, [[original.query.q, {...original.query, record: false}]], change);
    f.rows.dispose();
  }
});

test('expired viewer and unoffered rows cannot start, retry or expose a continuation', async () => {
  const f = fixture(snapshot([group('movies', [entry('first')], '')]));
  await f.rows.next(f.rows.getSnapshot().groups[0] as any);assert.equal(f.children.length, 0);
  f.retire();await f.rows.retry();assert.deepEqual(f.selections, []);
  f.rows.dispose();
  const denied = fixture();await denied.rows.next(denied.rows.getSnapshot().groups[0] as any);
  const child = denied.children[0];
  child.emit({...child.getSnapshot(), phase: 'error', error: {code: 'forbidden', message: '', retryable: false}});
  assert.equal(denied.rows.getSnapshot().phase, 'error');assert.equal(denied.rows.getSnapshot().data, null);
  denied.rows.dispose();
});
