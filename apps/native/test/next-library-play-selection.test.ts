import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {HttpLocalApi} from '../../../packages/client-core/src';
import * as selection from '../../../packages/client-core/src/selection-model';
import * as catalogueQuerySelection from '../../../packages/client-core/src/catalogue-query-selection';
import * as browseWholeSelection from '../../../packages/client-core/src/browse-whole-selection';
import type {BrowseCapabilities, BrowseRequestBody} from '../../../packages/client-core/src/browse';
import type {QueueHeader} from '../../../packages/client-core/src/playback-v1/types';
import {NativeShellNavigation, nativeDestinationActivation} from '../src-next/shell/routes';
import {nativeDestinationLifetime} from '../src-next/screens/destinations/lifetime';
import {NativeLibraryDestination} from '../src-next/screens/library/model';
import {screenModule} from './helpers/screen-harness.mjs';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
const library = {id: 'actual-movies', name: 'Movies', kind: 'movie'};
const pivot = {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], browsable: true, sorts: ['title'], filters: ['favorite']} as const;
const capabilities: BrowseCapabilities = {
  library: {...library, defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [{id: 'favorite', labelKey: 'filter.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie']}],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}],
  quickFilters: [], surfaces: [{id: 'library', endpoint: '/v1/libraries/{id}/browse', paging: 'keyset', sorts: ['title'], filters: ['favorite']}],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000},
};
const queue: QueueHeader = {id: 'actual-queue-receipt', revision: '1', total: 1, repeat: 'off', segments: []};

async function fixture() {
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'Home-only'};
  const viewer = {...authority, authority: 'local' as const};
  const navigation = new NativeShellNavigation(authority, () => authority), authorityListeners = new Set<() => void>();
  let active = true, hold = false, permitted = true;
  let queued: ((value: QueueHeader) => void) | undefined;
  const detailReads: {signal?: AbortSignal; finish: () => void}[] = [], calls: string[] = [], starts: any[] = [];
  const api = new HttpLocalApi('https://fixture.invalid', '', async (input, init) => {
    const url = new URL(String(input)); calls.push(url.pathname + url.search);
    let data: unknown;
    if (url.pathname.includes('browse-capabilities')) data = capabilities;
    else if (url.pathname.endsWith('/detail')) {
      const id = url.pathname.split('/').at(-2)!;
      const detail = {scope: {serverId: 'server', libraryId: library.id, itemId: id, viewerFence: 'Detail-own-fence'}, revision: {catalog: 3, viewer: 7},
        item: {id, libraryId: library.id, kind: 'movie', title: id, available: true, durationMs: 10000, progressMs: 0},
        actions: [{id: 'play', labelKey: 'detail.play', enabled: permitted, playback: {itemId: id, startMs: 0}},
          {id: 'favorite', labelKey: 'detail.favorite', enabled: true}, {id: 'queue', labelKey: 'detail.queue', enabled: true}], personal: {watchlisted: false, favorite: false, rating: null, revision: 1},
        metadata: {status: 'unavailable', genres: [], ratings: [], credits: []}};
      data = hold ? await new Promise(resolve => {detailReads.push({signal: init?.signal ?? undefined, finish: () => resolve(detail)});}) : detail;
    } else {
      const body = JSON.parse(String(init?.body)) as BrowseRequestBody, start = body.range?.start ?? 0, count = body.limit ?? 60;
      data = {pivot: body.pivot, applied: {query: body.query ?? null, sort: body.sort, seek: null},
        entries: Array.from({length: count}, (_, offset) => ({id: `movie-${start + offset}`, libraryId: library.id, kind: 'movie', title: `Title ${start + offset}`})),
        pageInfo: {start, total: 50000, revision: 'opaque-Browse-token', totalIsLowerBound: false, hasMore: true, nextCursor: ''}, positionIndex: []};
    }
    return new Response(JSON.stringify(data), {status: 200, headers: {'Content-Type': 'application/json'}});
  });
  const context = {api, navigation, current: () => active, authority: () => authority, session: () => ({viewer}),
    subscribeAuthority: (listener: () => void) => {authorityListeners.add(listener); return () => {authorityListeners.delete(listener);};},
    playback: {runtime: {cataloguePlayback: () => ({playSegments: async (segments: unknown, lifetime: unknown) => {
      starts.push({segments, lifetime}); return queued ? await new Promise<QueueHeader>(resolve => {queued = resolve;}) : queue;
    }})}},
  };
  const source = new NativeLibraryDestination({api, scope: {serverId: 'server', viewerId: JSON.stringify(['local', 'account', 'profile'])}, library, current: () => active});
  const {createNativeLibraryPlaySelection} = await screenModule(new URL('../src-next/screens/library/play-selection.ts', import.meta.url), {
    react: {default: React}, '../../../../../packages/client-core/src/selection-model': selection,
    '../../../../../packages/client-core/src/catalogue-query-selection': catalogueQuerySelection,
    '../../../../../packages/client-core/src/browse-whole-selection': browseWholeSelection,
    '../../shell/routes': {nativeDestinationActivation}, '../destinations/lifetime': {nativeDestinationLifetime},
    '../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'request-id'})},
  });
  navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: library.id}}, authority);
  return {context, source, authority, navigation, calls, starts, detailReads, create: () => createNativeLibraryPlaySelection(context, source),
    load: async () => {await source.load(); await tick(); await tick();},
    permitted: (value: boolean) => {permitted = value;}, hold: () => {hold = true;},
    holdQueue: () => {queued = () => {};}, finishQueue: () => {queued!(queue);},
    retire: () => {active = false; for (const listener of [...authorityListeners]) listener();},
  };
}

test('Library selection waits for actual Browse continuity and intersects genuine Detail grants with the installed executor', async () => {
  const f = await fixture();
  assert.throws(f.create, {code: 'unavailable'}); assert.equal(f.calls.length, 0);
  await f.load(); const binding = f.create(), collection = f.source.getSnapshot().collection!;
  try {
    const snapshot = binding.model.getSnapshot();
    assert.deepEqual(snapshot.context.revision, {pageRevision: 'opaque-Browse-token'});
    assert.equal('privacyFence' in snapshot.context.scope, false); assert.equal('catalogRevision' in snapshot.context.revision, false);
    assert.deepEqual(JSON.parse(snapshot.context.scope.viewId).slice(1), [library.id, 'movies', null, [{field: 'title', direction: 'asc'}]]);
    const entries = [collection.itemAt(0)!, collection.itemAt(1)!];
    const window = await binding.prepareWindow('actual-visible-window', 0, entries);
    window.toggle(entries[1]); window.toggle(entries[0]);
    assert.equal(binding.model.getSnapshot().summary.every.play, true);
    assert.equal(binding.model.getSnapshot().summary.every.favorite, true, 'the Library has an installed personal Favorites executor');
    assert.equal(binding.model.getSnapshot().summary.every.queue, false, 'a server offer alone does not install a missing queue executor');
    const receipt = await binding.play(); assert.equal(receipt, queue);
    assert.deepEqual(f.starts[0].segments, [{source: {items: {ids: ['movie-0', 'movie-1']}}}], 'Play uses canonical display order');
    assert.equal(f.starts[0].lifetime.isCurrent(), true);
    assert.throws(() => window.toggle({...entries[0]}), {name: 'AbortError'});
    f.permitted(false); const denied = await binding.prepareWindow('actual-visible-window', 0, entries);
    binding.model.clear(); denied.toggle(entries[0]);
    await assert.rejects(binding.play(), {code: 'catalogue-selection-unavailable'}); assert.equal(f.starts.length, 1);
    window.dispose(); assert.equal(binding.model.getSnapshot().registeredWindowCount, 1, 'an old registration cannot remove its replacement');
    denied.dispose();
  } finally {binding.dispose(); f.source.dispose(); f.navigation.dispose();}
});

test('explicit picks survive native page eviction, while old row handles refuse entry actions', async () => {
  const f = await fixture(); await f.load(); const binding = f.create(), collection = f.source.getSnapshot().collection!;
  try {
    const entry = collection.itemAt(0)!, window = await binding.prepareWindow('actual-first-page', 0, [entry]);
    window.toggle(entry); collection.ensureRange(48000, 48005); await tick(); await tick(); await tick();
    assert.equal(collection.itemAt(0), undefined); assert.equal(f.source.contains(entry), false);
    assert.equal(binding.model.getSnapshot().summary.count, 1); assert.equal(binding.model.getSnapshot().registeredWindowCount, 0);
    assert.throws(() => window.toggle(entry), {name: 'AbortError'});
    assert.equal(await binding.play(), queue); assert.deepEqual(f.starts[0].segments, [{source: {items: {ids: ['movie-0']}}}]);
  } finally {binding.dispose(); f.source.dispose(); f.navigation.dispose();}
});

test('applied query retirement aborts genuine pending grant reads and never registers late results', async () => {
  const f = await fixture(); await f.load(); const binding = f.create(), collection = f.source.getSnapshot().collection!;
  f.hold(); const pending = binding.prepareWindow('actual-first-page', 0, [collection.itemAt(0)!]);
  const rejected = assert.rejects(pending, {name: 'AbortError'}); await tick();
  f.source.setQuery([{field: 'favorite', operator: 'equals', value: true}], {field: 'title', direction: 'asc'});
  assert.ok(f.detailReads.every(read => read.signal?.aborted));
  for (const read of f.detailReads) read.finish();
  await rejected; assert.equal(binding.model.getSnapshot().registeredWindowCount, 0); assert.equal(binding.model.getSnapshot().summary.count, 0);
  binding.dispose(); f.source.dispose(); f.navigation.dispose();
});

test('Title/Back cannot revive a pending Play gesture, and authority retirement clears private picks', async () => {
  const f = await fixture(); await f.load(); const binding = f.create(), entry = f.source.getSnapshot().collection!.itemAt(0)!;
  const window = await binding.prepareWindow('actual-first-page', 0, [entry]); window.toggle(entry);
  f.holdQueue(); const pending = binding.play(), rejected = assert.rejects(pending, {name: 'AbortError'}); await tick();
  f.navigation.open({kind: 'title', entry}, f.authority);
  assert.equal(f.starts[0].lifetime.signal.aborted, true); f.navigation.back(f.authority);
  assert.equal(f.starts[0].lifetime.isCurrent(), false); assert.equal(binding.model.getSnapshot().summary.count, 1);
  f.finishQueue(); await rejected;
  f.retire(); assert.equal(binding.model.getSnapshot().summary.count, 0); assert.throws(() => window.toggle(entry), {name: 'AbortError'});
  binding.dispose(); f.source.dispose(); f.navigation.dispose();
});
