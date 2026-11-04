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

test('step 7: a refused-cursor recovery keeps the row while its first page is read again, then replaces the row with it', async () => {
  const f = fixture(), offered = f.rows.getSnapshot().groups[0];
  await f.rows.next(offered as any);
  const child = f.children[0], loading = child.getSnapshot();
  assert.equal(loading.pagination.cursor, offered.nextCursor);
  // This is core fetch()'s invalidateRead() publish, then its group-only first-page load (the viewer's access was decided again).
  child.emit({...loading, data: null, groups: []});
  child.emit({...loading, data: null, groups: [], pagination: {cursor: null, canPrevious: false}});
  const waiting = f.rows.getSnapshot();
  assert.equal(waiting.phase, 'ready', 'the All answer stays'); assert.equal(waiting.groups[0].items[0].id, 'first', 'the row shows what it showed');
  assert.equal(f.rows.page('movies' as any).loading, true); assert.equal(child.disposed, false);
  // The row's first page answers under the moved fence and a moved revision: it is current, and replaces the row.
  const reread = snapshot([group('movies', [entry('moved-1'), entry('moved-2')], 'moved-next')]);
  reread.data = {...reread.data, scope: {...reread.data.scope, viewerFence: 'moved'}, revision: {catalog: 2, viewer: 3}, query: {...reread.data.query, group: 'movies'}};
  child.emit(reread);
  const replaced = f.rows.getSnapshot();
  assert.equal(replaced.phase, 'ready'); assert.deepEqual(replaced.groups[0].items.map((value: any) => value.id), ['moved-1', 'moved-2']);
  assert.equal(replaced.groups[0].nextCursor, 'moved-next'); assert.equal(f.rows.page('movies' as any).loading, false);
  // Paging goes on from the replacement; its continuation is compared with the replacement's revision.
  await f.rows.next(replaced.groups[0] as any); assert.equal(child.reads, 2);
  f.answer(child, [entry('moved-3')], '', {revision: {catalog: 2, viewer: 3}, scope: {serverId: 'server', viewerFence: 'moved'}});
  assert.deepEqual(f.rows.getSnapshot().groups[0].items.map((value: any) => value.id), ['moved-1', 'moved-2', 'moved-3']);
  f.rows.dispose();
});

test('step 7: a transient failure of the row\'s re-read stays on the row and is retried there', async () => {
  const f = fixture(), offered = f.rows.getSnapshot().groups[0];
  await f.rows.next(offered as any);
  const child = f.children[0], loading = child.getSnapshot();
  child.emit({...loading, data: null, groups: [], pagination: {cursor: null, canPrevious: false}});
  child.emit({...child.getSnapshot(), phase: 'error', error: {code: 'timeout', message: '', retryable: true}});
  assert.equal(f.rows.getSnapshot().phase, 'ready'); assert.equal(f.rows.page('movies' as any).error?.code, 'timeout');
  await f.rows.retry(f.rows.getSnapshot().groups[0] as any); assert.equal(child.retries, 1);
  f.rows.dispose();
});

test('step 7: a row whose paging ends (a moved revision, or a refusal straight after its re-read) withdraws All and asks for a fresh read', async () => {
  for (const code of ['stale_continuation', 'invalid_cursor']) {
    const f = fixture(), original = f.source.getSnapshot();
    await f.rows.next(f.rows.getSnapshot().groups[0] as any);
    const child = f.children[0];
    // Core's fork ends the row with refresh-required (no re-read) in both cases.
    child.emit({...child.getSnapshot(), phase: 'refresh-required', data: null, groups: [], error: {code, message: '', retryable: false}});
    assert.equal(f.rows.getSnapshot().phase, 'refresh-required', code); assert.equal(f.rows.getSnapshot().error?.code, code);
    assert.equal(child.disposed, true, code);
    await f.rows.retry();
    assert.deepEqual(f.selections, [[original.query.q, {...original.query, record: false}]], code);
    f.rows.dispose();
  }
});

test('step 7: a continuation answered under a moved viewer fence is the newer answer and is appended', async () => {
  const f = fixture();
  await f.rows.next(f.rows.getSnapshot().groups[0] as any);
  const child = f.children[0], data = snapshot().data;
  f.answer(child, [entry('second')], '', {scope: {...data.scope, viewerFence: 'new'}});
  assert.equal(f.rows.getSnapshot().phase, 'ready'); assert.deepEqual(f.rows.getSnapshot().groups[0].items.map((value: any) => value.id), ['first', 'second']);
  f.rows.dispose();
});

test('changed continuation identity or revision withdraws all actions and requires a fresh unrecorded All read', async () => {
  // Step 7: a moved viewer fence on a continuation is the newer answer, and a first page is the row's re-read (above).
  for (const change of ['catalog', 'viewer-revision', 'query', 'sort', 'libraries', 'first-page-query']) {
    const f = fixture(), original = f.source.getSnapshot();
    await f.rows.next(f.rows.getSnapshot().groups[0] as any);
    const child = f.children[0], data = snapshot().data;
    const overrides = change === 'viewer-revision' ? {revision: {...data.revision, viewer: 2}} : change === 'catalog' ? {revision: {...data.revision, catalog: 2}}
      : change === 'query' ? {query: {...data.query, group: 'movies', q: 'another'}} : change === 'sort' ? {query: {...data.query, group: 'movies', sort: 'title'}}
      : change === 'libraries' ? {query: {...data.query, group: 'movies', libraryIds: ['other']}} : {};
    if (change === 'first-page-query') {
      // A first page (the row's re-read) for another query is still not this row's.
      const answer = snapshot([group('movies', [entry('new')])]);answer.data.query = {...answer.data.query, group: 'movies', q: 'another'};child.emit(answer);
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
