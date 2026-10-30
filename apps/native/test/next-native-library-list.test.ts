import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import type {BrowseCapabilities, BrowseRequestBody} from '../../../packages/client-core/src/browse';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const pivot = {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid', 'list'], browsable: true, sorts: ['title'], filters: []} as const;
const capabilities: BrowseCapabilities = {
  library: {id: 'movies', name: 'Movies', kind: 'movie', defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [], quickFilters: [], sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie']}],
  surfaces: [{id: 'library', endpoint: '/v1/libraries/{id}/browse', paging: 'keyset', sorts: ['title'], filters: []}], queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000},
};
const movie = {id: 'actual-movie', libraryId: 'movies', kind: 'movie', title: 'Stored Movie (Full Edition)', displayTitle: 'Actual display title', year: 2026,
  posterUrl: `/v1/metadata/item/actual-movie/art/poster?v=${'a'.repeat(64)}&w=400`, navigation: {view: 'item', entityId: 'actual-movie'}, playback: {itemId: 'actual-movie'}};

test('phone Library mode switches the same sparse collection and keeps genuine row actions through Title/Back', async () => {
  let browseReads = 0;
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string; method: string; body?: unknown}) => {
    const path = new URL(request.path, 'https://fixture.invalid').pathname;
    if (path === '/v1/libraries') return {status: 200, body: {items: [{id: 'movies', name: 'Movies', kind: 'movie'}]}};
    if (path === '/v1/libraries/movies/browse-capabilities') return {status: 200, body: capabilities};
    if (path === '/v1/libraries/movies/browse' && request.method === 'POST') {
      browseReads++; const body = request.body as BrowseRequestBody;
      const start = body.range?.start ?? 0;
      const entries = Array.from({length: Math.min(40, 81 - start)}, (_, offset) => {const index = start + offset; return {...movie, id: `movie-${index}`,
        displayTitle: `Actual display title ${index}`, navigation: {view: 'item', entityId: `movie-${index}`}, playback: {itemId: `movie-${index}`}};});
      return {status: 200, body: {pivot: 'movies', applied: {query: body.query ?? null, sort: body.sort, seek: null}, entries,
        pageInfo: {start: body.range?.start ?? 0, total: 81, totalIsLowerBound: false, revision: 'opaque-library-page', hasMore: false, nextCursor: ''}, positionIndex: []}};
    }
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime); await app.mount({runtime});
    assert.equal(context.navigation.open({kind: 'shell', destination: {kind: 'library', libraryId: 'movies'}}, context.authority()), true); await app.settle();
    const movies = app.hosts().find((host: any) => host.props.accessibilityRole === 'tab' && host.props.accessibilityLabel === 'Movies');
    assert.ok(movies, JSON.stringify({libraries: context.libraries(), tabs: app.hosts().filter((host: any) => host.props.accessibilityRole === 'tab').map((host: any) => host.props.accessibilityLabel)})); movies.props.onPress(); await app.settle();
    const grid = app.hosts().find((host: any) => host.props.testID === 'library-grid'); assert.ok(grid);
    grid.props.onLayout({nativeEvent: {layout: {width: 390, height: 600}}}); await app.settle();
    grid.props.onScroll({contentOffset: {x: 0, y: 800}}); await app.settle();
    const before = browseReads;
    const list = app.hosts().find((host: any) => host.props.accessibilityRole === 'radio' && host.props.accessibilityLabel === defaultI18n.t('next.browse.native.list')); assert.ok(list);
    list.props.onPress(); await app.settle();
    const listHost = app.hosts().find((host: any) => host.props.testID === 'library-list'); assert.ok(listHost);
    listHost.props.onLayout({nativeEvent: {layout: {width: 390, height: 600}}});
    // The synthetic native boundary must acknowledge the ScrollView's actual initial contentOffset.
    listHost.props.onScroll({contentOffset: listHost.props.contentOffset}); await app.settle();
    assert.equal(browseReads, before, 'presentation does not reselect or refetch the arrived Browse source');
    const selectedList = app.hosts().find((host: any) => host.props.accessibilityRole === 'radio' && host.props.accessibilityLabel === defaultI18n.t('next.browse.native.list'));
    assert.equal(selectedList?.props.accessibilityState.checked, true);
    const row = app.hosts().find((host: any) => host.props.testID?.startsWith('library-list-') && host.props.accessibilityLabel?.startsWith('Actual display title ')); assert.ok(row);
    assert.equal(row.props.style.height, 70, 'the list uses the drawn catalogue row height');
    // The row reads its title and caption; its actions name the title.
    const title = /^Actual display title \d+/.exec(row.props.accessibilityLabel)![0];
    assert.ok(app.hosts().some((host: any) => host.props.accessibilityLabel === `Play ${title}`));
    assert.ok(app.hosts().some((host: any) => host.props.accessibilityLabel === `More for ${title}`));
    const listOffset = listHost.props.contentOffset.y; assert.ok(listOffset > 0, 'switching preserves the actual first-visible item, not a raw grid pixel offset');
    grid.props.onScroll({contentOffset: {x: 0, y: 0}}); await app.settle();
    row.props.onPress({nativeEvent: {}, stopPropagation() {}}); await app.settle();
    assert.equal(context.navigation.getSnapshot().route.kind, 'title');
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    const retained = app.hosts().find((host: any) => host.props.testID === 'library-list'); assert.ok(retained);
    assert.equal(retained.host, listHost.host, 'Title/Back retains the same physical list host');
    assert.equal(browseReads, before);
    const toGrid = app.hosts().find((host: any) => host.props.accessibilityRole === 'radio' && host.props.accessibilityLabel === defaultI18n.t('next.browse.native.grid')); assert.ok(toGrid);
    toGrid.props.onPress(); await app.settle();
    assert.ok(app.hosts().some((host: any) => host.props.testID === 'library-grid'));
    const restoredGrid = app.hosts().find((host: any) => host.props.testID === 'library-grid');
    assert.ok(restoredGrid.props.contentOffset.y > 0, 'an old unmounted grid scroll cannot reset the new presentation anchor');
    assert.equal(browseReads, before);
    assert.deepEqual(app.errors, []);
  } finally {
    try {
      await app.unmount();
    } finally {
      runtime?.dispose();
      app.disposeLoader();
    }
  }
});
