import assert from 'node:assert/strict';
import test from 'node:test';
import {HomeService, type HomePageOutcome} from '../../../packages/client-core/src/home-service';
import type {HomeRow} from '../../../packages/client-core/src/home';
import {NativeHomePaging} from '../src-next/shell/home-paging';
import {reconcileRowSlots} from '../src-next/kit/layout/row-state';

const entry = (id: string) => ({id, libraryId: 'movies', kind: 'movie' as const, title: id, available: true});
const row = (cursor = 'cursor/raw'): HomeRow => ({id: 'recent', title: 'Recent', kind: 'recent', artworkShape: 'poster', entries: [entry('a'), entry('b')], start: 0,
  endpoint: '', privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true,
  critical: false, cursorCapable: true, total: 20, limit: 2, hasMore: true, nextCursor: cursor});
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
