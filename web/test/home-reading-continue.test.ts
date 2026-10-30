import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import {HttpLocalApi, type PreferenceSnapshot} from '@portico/client-core';
import {routePath} from '../../packages/contracts/src/index.ts';
import {createReadingContinueBindings} from '../src/screens/home/reading-continue.ts';

async function settle(done: () => boolean) {
  for (let index = 0; index < 100 && !done(); index++) await setImmediate();
  assert(done(), 'bounded canonical reading request settled');
}
function fixture(options: {total?: number; libraryId?: string; delayed?: boolean} = {}) {
  const calls: URL[] = [], signals: (AbortSignal | null | undefined)[] = [];
  const opened: unknown[] = [];
  let selected = {sessionFamilyId: 'unit-family', viewer: {serverId: 'unit-server', authority: 'local' as const,
    accountId: 'unit-account', profileId: 'unit-profile', role: 'member' as const}};
  let finish = () => {};
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-test-token', async (input, init) => {
    const url = new URL(String(input));calls.push(url);signals.push(init?.signal);
    assert.equal(url.pathname, routePath('get_reading_continue'));assert.equal(init?.method ?? 'GET', 'GET');
    // Only this synthetic server decodes its cursor; the binding preserves it.
    const start = Number(url.searchParams.get('cursor')?.slice(6) ?? 0), limit = Number(url.searchParams.get('limit'));
    const total = options.total ?? 2;
    const items = Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, offset) => {
      const index = start + offset, sourceId = 'unit-source-' + index;
      return {item: {id: 'unit-item-' + index, sourceId, libraryId: options.libraryId ?? (index % 2 ? 'unit-comics' : 'unit-books'),
        title: 'Stored title ' + index, format: index === 0 ? 'epub' : 'pdf', inventoryAvailable: index !== 1},
      progress: {sourceId, sourceRevision: 'b'.repeat(64), revision: 7 + index, observedAt: 10000 + index, stale: index === 0,
        location: {format: 'pdf', pageIndex: 17 + index, progression: .25}}};
    });
    const response = () => new Response(JSON.stringify({items, ...(start + items.length < total ? {nextCursor: 'after_' + (start + items.length)} : {})}),
      {headers: {'Content-Type': 'application/json'}});
    return options.delayed ? new Promise<Response>(resolve => {finish = () => resolve(response());}) : response();
  });
  const binding = createReadingContinueBindings({context: {api, session: () => selected, isCurrent: () => true,
    facts: {preferences: {} as PreferenceSnapshot}}, libraryId: options.libraryId, open: destination => {opened.push(destination);}});
  return {binding, api, calls, signals, opened, finish: () => finish(),
    switchProfile() {selected = {...selected, viewer: {...selected.viewer, profileId: 'unit-other-profile'}};}};
}

test('Home Continue binds the canonical profile-wide read lazily and preserves actual progress/source facts', async () => {
  const f = fixture();
  try {
    assert.equal(f.calls.length, 0);f.binding.collection.ensureRange(0, 1);
    await settle(() => !!f.binding.collection.itemAt(1));
    assert.equal(f.calls.length, 1);assert.equal(f.calls[0].searchParams.has('libraryId'), false);
    const first = f.binding.collection.itemAt(0)!, second = f.binding.collection.itemAt(1)!;
    assert.equal(first.item.libraryId, 'unit-books');assert.equal(second.item.libraryId, 'unit-comics');
    assert.equal(first.progress.stale, true);assert.equal(first.progress.sourceRevision, 'b'.repeat(64));
    assert.equal(first.progress.revision, 7);assert.equal(first.progress.observedAt, 10000);
    assert.equal(first.progress.location.format, 'pdf');assert.equal(first.progress.location.pageIndex, 17);
    assert.equal(first.progress.location.progression, .25);assert.equal(first.item.format, 'epub');
    assert.equal(Object.isFrozen(first.progress.location), true);
    await f.binding.open(first);
    // Read opens the version the server chooses for this reader (DECISIONS 28 Oct), as the title page does.
    assert.deepEqual(f.opened, [{to: '/read/$itemId', params: {itemId: first.item.id}, search: {}}]);
    await assert.rejects(f.binding.open(second), {name: 'AbortError'});
    await assert.rejects(f.binding.open({...first}), {name: 'AbortError'});
    assert.equal(f.calls.length, 1, 'opening only dispatches the existing route; no per-row Prepare');
  } finally {await f.binding.dispose();}
});

test('library-scoped Continue uses the same real producer and rejects evicted row handles', async () => {
  const f = fixture({total: 480, libraryId: 'unit-books'});
  try {
    f.binding.collection.ensureRange(0, 1);await settle(() => !!f.binding.collection.itemAt(0));
    const first = f.binding.collection.itemAt(0)!;
    assert.equal(f.calls[0].searchParams.get('libraryId'), 'unit-books');
    for (let start = 60; start < 480; start += 60) {
      f.binding.collection.ensureRange(start, start + 1);await settle(() => !!f.binding.collection.itemAt(start));
      assert(f.binding.collection.resident().pages <= 6);
    }
    assert.equal(f.binding.contains(first), false);await assert.rejects(f.binding.open(first), {name: 'AbortError'});
    assert(f.calls.length <= 10);assert.equal(f.binding.collection.getSnapshot().total, 480);
  } finally {await f.binding.dispose();}
});

test('token or profile replacement erases cached Continue rows and never opens under the next authority', async () => {
  for (const change of ['token', 'profile']) {
    const f = fixture();
    try {
      f.binding.collection.ensureRange(0, 1);await settle(() => !!f.binding.collection.itemAt(0));
      const row = f.binding.collection.itemAt(0)!;
      if (change === 'token') f.api.setAccessToken('unit-replacement-token');else f.switchProfile();
      assert.equal(f.binding.isCurrent(), false);assert.equal(f.binding.collection.resident().items, 0);
      await assert.rejects(f.binding.open(row), {name: 'AbortError'});assert.equal(f.opened.length, 0);assert.equal(f.calls.length, 1);
    } finally {await f.binding.dispose();}
  }
});

test('Home retirement aborts its pending canonical read and never admits late rows or cleanup writes', async () => {
  const f = fixture({delayed: true});
  f.binding.collection.ensureRange(0, 1);await settle(() => f.calls.length === 1);
  const disposed = f.binding.dispose();assert.strictEqual(f.binding.dispose(), disposed);await disposed;
  assert.equal(f.signals[0]?.aborted, true);f.finish();
  for (let index = 0; index < 8; index++) await setImmediate();
  assert.equal(f.binding.collection.resident().items, 0);assert.equal(f.binding.isCurrent(), false);assert.equal(f.calls.length, 1);
});
