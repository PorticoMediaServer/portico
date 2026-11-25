import test from 'node:test';
import assert from 'node:assert/strict';
import type {ContentEntry} from '@core/library-content.ts';
import {FreshnessHub} from '@core/freshness-hub.ts';
import {createViewerQueries} from '@core/query/viewer-queries.ts';
import {createLibraryPlaySelection, type ExplicitCatalogueSelectionSource} from '../src/screens/browse/play-selection.ts';

function makeEntries(count: number): ContentEntry[] {
  return Array.from({length: count}, (_, i) => ({
    id: `m-${i}`,
    kind: 'movie',
    title: `Movie ${i}`,
    libraryId: 'lib-1',
    available: true,
  } as ContentEntry));
}

function makeFixture(count: number, opts?: {deferDetail?: boolean}) {
  const entries = makeEntries(count);
  let resident = new Set<ContentEntry>(entries);
  const sourceListeners = new Set<() => void>();
  const collectionListeners = new Set<() => void>();
  const source: ExplicitCatalogueSelectionSource = {
    viewIdentity: () => 'movies|title-asc',
    getSnapshot: () => ({phase: 'ready', revision: 'rev-1', queryRevision: 1}),
    subscribe: listener => { sourceListeners.add(listener); return () => { sourceListeners.delete(listener); }; },
    contains: entry => resident.has(entry),
    collection: {
      subscribe: listener => { collectionListeners.add(listener); return () => { collectionListeners.delete(listener); }; },
      itemAt: index => entries[index] ? {entry: entries[index]} : undefined,
    },
  };

  const detailCalls: string[] = [];
  const pendingResolvers: (() => void)[] = [];
  const api = {
    request: async <T>(path: string): Promise<T> => {
      detailCalls.push(path);
      if (opts?.deferDetail) await new Promise<void>(resolve => pendingResolvers.push(resolve));
      const id = path.split('/').pop()!;
      return {
        id,
        kind: 'movie',
        title: `Title ${id}`,
        libraryId: 'lib-1',
        available: true,
        images: {},
        state: {watched: false, inWatchlist: false, favorite: false},
      } as T;
    },
  };
  const hub = new FreshnessHub({settleMs: 0});
  // gcTimeMs: 0 ensures ViewerQueries does not mask play-selection's own resolvedGrants cache.
  const queries = createViewerQueries({scope: {serverId: 's1', viewerId: 'v1'}, hub, api, gcTimeMs: 0});
  const life = new AbortController();
  const play = createLibraryPlaySelection({
    source,
    queries,
    api,
    scope: {serverId: 's1', viewerId: 'v1'},
    principal: {serverId: 's1', authority: 'local', accountId: 'a1', profileId: 'p1'},
    requestId: async () => 'req-1',
    signal: life.signal,
    isCurrent: () => !life.signal.aborted,
    personal: {watched: true, myList: true, favorite: true},
  });
  return {
    entries,
    play,
    detailCalls,
    releaseDetails: () => { while (pendingResolvers.length) pendingResolvers.shift()!(); },
    setResident: (next: Set<ContentEntry>) => {
      resident = next;
      for (const listener of [...collectionListeners]) listener();
    },
    dispose: () => { life.abort(); play.dispose(); queries.dispose(); },
  };
}

test('scrolling between windows never wipes rows to empty while inactive or while grants resolve', async () => {
  const f = makeFixture(40, {deferDetail: true});
  try {
    const observedRowCounts: number[] = [];
    f.play.subscribe(() => { observedRowCounts.push(f.play.getSnapshot().rows.length); });

    const firstRows = await f.play.registerWindow(0, 9);
    assert.equal(firstRows.length, 10);
    observedRowCounts.length = 0;

    // Inactive selection scroll swaps atomically without wiping rows: [].
    const secondRows = await f.play.registerWindow(5, 14);
    assert.equal(secondRows.length, 10);
    assert.ok(observedRowCounts.every(n => n > 0), `inactive scroll must never publish rows: [], saw ${JSON.stringify(observedRowCounts)}`);

    // Start selection (toggling activates selection) and immediately scroll to an overlapping window.
    observedRowCounts.length = 0;
    f.play.toggle(secondRows[0]!);
    const pendingScroll = f.play.registerWindow(8, 17);
    // While async Detail grants are still in flight, previously committed rows stay populated!
    assert.equal(f.play.getSnapshot().resolving, true);
    assert.equal(f.play.getSnapshot().rows.length, 10, 'committed rows remain visible while next window grants resolve');
    assert.ok(observedRowCounts.every(n => n > 0), `active scroll must never wipe rows to [], saw ${JSON.stringify(observedRowCounts)}`);

    // Pump all queued Detail requests (concurrency 4) until resolved.
    for (let i = 0; i < 8; i++) {
      await new Promise(resolve => setImmediate(resolve));
      f.releaseDetails();
    }
    const grantedRows = await pendingScroll;
    assert.equal(grantedRows.length, 10);
    assert.equal(f.play.getSnapshot().resolving, false);
    assert.equal(f.play.getSnapshot().rows.length, 10);
    assert.equal(grantedRows[0]!.capabilities.watched, true);
  } finally {
    f.dispose();
  }
});

test('resolved Detail grants are cached across page eviction and scroll-back within the same revision', async () => {
  const f = makeFixture(30);
  try {
    const initial = await f.play.registerWindow(0, 4);
    f.play.toggle(initial[0]!);
    // Wait for regrant microtask + Detail fetches for [0..4].
    await new Promise(resolve => setTimeout(resolve, 30));
    const afterFirstWindow = f.detailCalls.length;
    assert.equal(afterFirstWindow, 5, 'each visible item fetched Detail once');

    // Scroll far away and evict [0..4] from resident pages.
    await f.play.registerWindow(15, 19);
    f.setResident(new Set(f.entries.slice(15, 30)));
    assert.equal(f.detailCalls.length, 10);

    // Re-admit [0..4] and scroll back: cached grants must prevent redundant Detail refetches!
    f.setResident(new Set(f.entries));
    const callsBeforeReturn = f.detailCalls.length;
    const returned = await f.play.registerWindow(0, 4);
    assert.equal(returned.length, 5);
    assert.equal(returned[0]!.capabilities.watched, true);
    assert.equal(f.detailCalls.length, callsBeforeReturn, 'scrolling back reuses cached grants with zero refetches');
  } finally {
    f.dispose();
  }
});

test('visible windows larger than 60 items (including >256 chunked runs) register cleanly', async () => {
  const f = makeFixture(320);
  try {
    const rows120 = await f.play.registerWindow(0, 119);
    assert.equal(rows120.length, 120);
    const rows300 = await f.play.registerWindow(0, 299);
    assert.equal(rows300.length, 300);
    assert.equal(f.play.toggle(rows300[280]!).ok, true);
    assert.equal(f.play.selected(rows300[280]!), true);
  } finally {
    f.dispose();
  }
});
