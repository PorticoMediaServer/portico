import assert from 'node:assert/strict';
import test from 'node:test';
import {HomeService, type HomePageOutcome} from '../../../packages/client-core/src/home-service';
import type {HomeRow} from '../../../packages/client-core/src/home';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {reconcileRowSlots} from '../src-next/kit/layout/row-state';

const entry = (id: string) => ({id, libraryId: 'movies', kind: 'movie' as const, title: id, available: true});
const row = (cursor = 'cursor/raw'): HomeRow => ({id: 'recent', title: 'Recent', kind: 'recent', artworkShape: 'poster', entries: [entry('a'), entry('b')], start: 0,
  viewerFence: 'fence', endpoint: '', privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true,
  critical: false, cursorCapable: true, total: 20, limit: 2, hasMore: true, nextCursor: cursor, revision: {catalog: 1, viewer: 1}});
function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(yes => {resolve = yes;}); return {promise, resolve};}
const applied = (firstItemId: string | null, requestedCursor = 'cursor/raw'): HomePageOutcome => ({rowId: 'recent', requestedCursor, status: 'applied', applied: true, firstItemId});
const waitingId = JSON.stringify(['page', 'recent', 'cursor/raw']);

test('pending presentation defers new members but immediately exposes current objects/removals; exact receipt releases waiting alias atomically', async () => {
  const response = deferred<HomePageOutcome>(), errors: unknown[] = [], source = row();
  const pages = new NativeHomePaging({more: () => response.promise}, () => true, error => errors.push(error));
  const request = pages.request(source, waitingId), latestA = {...source.entries[0], title: 'Current capability-bearing object'};
  const incoming = {...source, entries: [latestA, entry('c')], nextCursor: 'next'};
  assert.deepEqual(pages.present(incoming).entries, [latestA]);
  assert.equal(pages.present(incoming).entries[0], latestA);
  const page = {hasMore: true, loading: true, waitingId, waitingItem: source.entries[1], requestNext() {}};
  const before = reconcileRowSlots([], source.entries, item => item.id, true, page);
  const held = reconcileRowSlots(before, pages.present(incoming).entries, item => item.id, true, page);
  assert.equal(held.find(slot => slot.waiting)?.focusId, waitingId);
  response.resolve(applied('c')); await request;
  assert.equal(pages.present(incoming), incoming);
  assert.deepEqual(pages.state('recent')?.fulfilled, {waitingId, firstItemId: 'c'});
  const slots = reconcileRowSlots(held, pages.present(incoming).entries, item => item.id, true, {...page, loading: false, waitingId: 'next-page', fulfilled: pages.state('recent')?.fulfilled});
  assert.equal(slots.find(slot => slot.id === 'c')?.focusId, waitingId);
  assert.deepEqual(errors, []); pages.dispose();
});

test('applied empty continuation keeps the visible waiting identity across the next backend cursor', async () => {
  const pages = new NativeHomePaging({more: async () => applied(null)}, () => true, () => {}), source = row();
  await pages.request(source, waitingId);
  assert.deepEqual(pages.state('recent')?.fulfilled, {waitingId});
  const page = {hasMore: true, loading: false, waitingId, waitingItem: source.entries[1], requestNext() {}};
  const before = reconcileRowSlots([], source.entries, item => item.id, true, page);
  const after = reconcileRowSlots(before, source.entries, item => item.id, true, {...page, waitingId: 'next-page', fulfilled: pages.state('recent')?.fulfilled});
  assert.equal(after.find(slot => slot.waiting)?.focusId, waitingId);
  assert.equal(after.find(slot => slot.waiting)?.requestId, 'next-page'); pages.dispose();
});

test('empty cursor advances keep the same physical host when a later genuine item finally arrives', async () => {
  let calls = 0;
  const pages = new NativeHomePaging({more: async () => ++calls === 1 ? applied(null) : applied('c', 'next')}, () => true, () => {});
  const source = row(), page = {hasMore: true, loading: false, waitingId, waitingItem: source.entries[1], requestNext() {}};
  const initial = reconcileRowSlots([], source.entries, item => item.id, true, page);
  await pages.request(source, waitingId);
  const advanced = reconcileRowSlots(initial, source.entries, item => item.id, true, {...page, waitingId: 'next-semantic', fulfilled: pages.state('recent')?.fulfilled});
  await pages.request({...source, nextCursor: 'next'}, 'next-semantic');
  const filled = reconcileRowSlots(advanced, [...source.entries, entry('c')], item => item.id, true, {...page, waitingId: 'later-semantic', fulfilled: pages.state('recent')?.fulfilled});
  assert.equal(filled.find(slot => slot.id === 'c')?.focusId, waitingId);
  assert.equal(pages.state('recent')?.hostId, waitingId, 'preview reconciliation follows the actual stationary host across empty cursors');
  assert.equal(pages.state('recent')?.fulfilled?.waitingId, 'next-semantic', 'fulfilment still matches the exact current request rather than the old host cursor');
  pages.dispose();
});

test('wrong row/cursor and every non-applied outcome never fulfil from newly arrived snapshot members', async () => {
  const outcomes: HomePageOutcome[] = [applied('c', 'foreign-cursor'), {...applied('c'), rowId: 'foreign-row'},
    {rowId: 'recent', requestedCursor: 'cursor/raw', status: 'failed', applied: false},
    {rowId: 'recent', requestedCursor: 'cursor/raw', status: 'stale', applied: false},
    {rowId: 'recent', requestedCursor: 'cursor/raw', status: 'revalidated', applied: false},
    {rowId: 'recent', requestedCursor: 'cursor/raw', status: 'ignored', applied: false, reason: 'row_changed'},
    {rowId: 'recent', requestedCursor: 'cursor/raw', status: 'refused', applied: false, reason: 'permission_denied'}];
  for (const outcome of outcomes) {
    const pages = new NativeHomePaging({more: async () => outcome}, () => true, () => {});
    await pages.request(row(), waitingId);
    assert.equal(pages.state('recent')?.fulfilled, undefined);
    assert.equal(pages.state('recent')?.failed, ['failed', 'stale'].includes(outcome.status)); pages.dispose();
  }
});

test('one pending call per row and explicit failed-boundary retry preserve the source cursor association', async () => {
  const first = deferred<HomePageOutcome>(); let calls = 0;
  const pages = new NativeHomePaging({more: () => ++calls === 1 ? first.promise : Promise.resolve(applied('c'))}, () => true, () => {});
  const run = pages.request(row(), waitingId); await pages.request(row(), waitingId); assert.equal(calls, 1);
  first.resolve({rowId: 'recent', requestedCursor: 'cursor/raw', status: 'failed', applied: false}); await run;
  assert.equal(pages.state('recent')?.failed, true);
  await pages.request(row(), waitingId);
  assert.equal(calls, 2); assert.equal(pages.state('recent')?.failed, false);
  assert.deepEqual(pages.state('recent')?.fulfilled, {waitingId, firstItemId: 'c'}); pages.dispose();
});

test('authority retirement, disposal and Strict lifecycle replay cannot publish a former pending receipt', async () => {
  const old = deferred<HomePageOutcome>(), fresh = deferred<HomePageOutcome>(); let current = true, calls = 0, published = 0;
  const pages = new NativeHomePaging({more: () => ++calls === 1 ? old.promise : fresh.promise}, () => current, () => {});
  const deactivate = pages.activate(); pages.subscribe(() => published++);
  const first = pages.request(row(), waitingId); deactivate(); pages.activate();
  const second = pages.request(row(), waitingId);
  const before = published; old.resolve(applied('obsolete')); await first; assert.equal(published, before);
  current = false; fresh.resolve(applied('foreign')); await second; assert.equal(published, before);
  pages.dispose(); assert.equal(pages.state('recent'), undefined);
});

test('real HomeService publishes new source members before its receipt without exposing them prematurely to native Row', async () => {
  const source = row(), response = deferred<unknown>();
  const document = {serverId: 'server', viewerFence: 'fence', rows: [source], spotlight: [], layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T20:00:00Z'};
  const home = new HomeService({request: async path => JSON.parse(JSON.stringify(path.includes('/rows/') ? await response.promise : document))});
  await home.load();
  const pages = new NativeHomePaging(home, () => true, error => {throw error;});
  let observed: readonly string[] | undefined;
  const remove = home.subscribe(() => {const latest = home.getSnapshot().document!.rows[0]; if (latest.entries.some(item => item.id === 'c')) observed = pages.present(latest).entries.map(item => item.id);});
  const request = pages.request(home.getSnapshot().document!.rows[0], waitingId);
  response.resolve({...source, entries: [entry('c')], start: 2, nextCursor: 'next'});
  await request;
  assert.deepEqual(observed, ['a', 'b']);
  assert.deepEqual(pages.present(home.getSnapshot().document!.rows[0]).entries.map(item => item.id), ['a', 'b', 'c']);
  assert.deepEqual(pages.state('recent')?.fulfilled, {waitingId, firstItemId: 'c'});
  remove(); pages.dispose(); home.dispose();
});
