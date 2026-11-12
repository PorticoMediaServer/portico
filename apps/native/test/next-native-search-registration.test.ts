import assert from 'node:assert/strict';
import test from 'node:test';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {openNativeSearchGenres} from '../src-next/screens/search/navigation';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import {legacySearchFetch, legacySearchRequest, legacySearchResponse} from '../../../packages/client-core/test/fixtures/legacy-search.ts';

const art = (id: string, slot: 'poster' | 'backdrop' = 'poster') => ({kind: 'item', id,
  path: `/v1/metadata/item/${id}/art/${slot}?v=${'a'.repeat(64)}&${slot === 'poster' ? `sd=533x800.54008.${'d'.repeat(16)}&ld=600x900.65669.${'e'.repeat(16)}` : `sd=800x450.49940.${'b'.repeat(16)}&ld=1920x1080.162149.${'c'.repeat(16)}`}`});
const movie = {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Stored Genre Movie (Full Cut)', displayTitle: 'Genuine display title',
  posterUrl: art('movie').path, navigation: {view: 'item', entityId: 'movie'}, playback: {itemId: 'movie'}};
// The rebuilt server's genres (GET /v1/genres?counts=true): the whole list, each with its count, cover and two posters.
const genres = [{value: 'artless', label: 'Artless metadata', count: 1, posters: []},
  {value: 'actual-value', label: 'Stored Genre', count: 2, cover: {backdrop: art('movie', 'backdrop').path}, posters: [{poster: art('movie').path}, {poster: art('other').path}]},
  {value: 'backdrop-only', label: 'Backdrop prefix first', count: 8, cover: {backdrop: art('other', 'backdrop').path}, posters: []}];
// A genre's films and shows across every library (GET /v1/items?kind=movie,show&tag=).
const genreItems = [{id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Genuine display title', images: {poster: art('movie').path}, state: {}, addedAt: '2026-11-12T00:00:00Z'},
  {id: 'other', libraryId: 'movies', kind: 'movie', title: 'Other authorized title', available: false, images: {poster: art('other').path}, state: {}, addedAt: '2026-11-12T00:00:00Z'}];

test('real App Search keeps separate eligible previews and an artless All genres directory in retained registration', async () => {
  let genreReads = 0;
  const browse: URLSearchParams[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: legacySearchResponse(async (request: {path: string; method: string; body?: unknown}, fixture: {pin: {serverId: string}}) => {
    const url = new URL(request.path, 'https://fixture.invalid');
    if (url.pathname === '/v1/genres') {genreReads++; return {status: 200, body: {genres}};}
    if (url.pathname === '/v1/facets') return {status: 200, body: {facet: 'letter', values: [{value: 'G', count: 1}, {value: 'O', count: 1}]}};
    if (url.pathname === '/v1/items' && url.searchParams.get('tag')) {
      if (url.searchParams.get('total') !== 'true') browse.push(url.searchParams);
      return {status: 200, body: {items: genreItems, total: 2}};
    }
    if (url.pathname === '/v1/search') return {status: 200, body: {
      scope: {serverId: fixture.pin.serverId, libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'Search-own-fence'}, revision: {catalog: 12, viewer: 7},
      query: {q: url.searchParams.get('q'), group: '', groups: ['movies'], limit: 40, sort: 'relevance', direction: 'desc', libraryIds: [], recorded: false},
      capabilities: {groups: [{id: 'movies', title: '', available: true, reason: '', sorts: ['relevance']}], sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
      groups: [{id: 'movies', entityKind: 'movie', title: '', status: 'success', errorCode: '', totalCount: url.searchParams.get('q') === 'no matches' ? 0 : 1, totalCountExact: true, nextCursor: '', items: url.searchParams.get('q') === 'no matches' ? [] : [movie]}],
    }};
  })});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime); await app.mount({runtime});
    assert.equal(genreReads, 0, 'registering Search never fetches while Home is current');
    await app.tapLabel('Search'); await app.settle();
    const root = context.navigation.getSnapshot().route;
    assert.equal(root.kind, 'shell'); if (root.kind === 'shell') assert.equal(root.destination.kind, 'search');
    assert.ok(app.hosts().some((host: any) => host.props.testID === 'native-search-screen'));
    assert.equal(genreReads, 2);
    const previews = app.requests.filter((request: any) => new URL(request.path, 'https://fixture.invalid').pathname === '/v1/genres').map((request: any) => request.path);
    assert.deepEqual(previews, ['/v1/genres?counts=true', '/v1/genres?counts=true'], 'the phone\'s two tile slots, each from the rebuilt genre read');
    assert.equal(app.requests.filter((request: any) => request.path.startsWith('/v1/search/history')).length, 0, 'confirmed privacy-off means no history read');
    assert.equal(openNativeSearchGenres(context), true); await app.settle();
    const all = context.navigation.getSnapshot().route; assert.equal(all.kind, 'search-genres');
    const allList = app.hosts().find((host: any) => host.name === 'FlatList' && host.props.data?.some((item: any) => item.id === 'artless'));
    assert.ok(allList); assert.equal(allList.props.data.length, 3); assert.equal(genreReads, 3, 'All genres starts its own ordinary read');
    const artlessRow = allList.props.renderItem({item: allList.props.data[0]});
    assert.equal(artlessRow.props.label, 'Artless metadata'); assert.equal(artlessRow.props.description, '1 title');
    const actual = allList.props.data.find((item: any) => item.id === 'actual-value');
    allList.props.renderItem({item: actual}).props.onPress(); await app.settle();
    const genre = context.navigation.getSnapshot().route;
    assert.deepEqual(genre, {kind: 'search-genre', value: 'actual-value', label: 'Stored Genre'});
    assert.ok(browse.length);
    assert.deepEqual([browse[0]!.get('kind'), browse[0]!.get('tag'), browse[0]!.get('sort'), browse[0]!.get('order')], ['movie,show', 'actual-value', 'title', 'asc']);
    const card = app.hosts().find((host: any) => host.name === 'Pressable' && host.props.accessibilityLabel === 'Genuine display title');
    assert.ok(card); card.props.onPress({nativeEvent: {}, stopPropagation() {}}); await app.settle();
    assert.equal(context.navigation.getSnapshot().route.kind, 'title');
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    assert.equal(context.navigation.getSnapshot().route, genre); assert.equal(browse.length, 1, 'Title/Back keeps the same actual sparse Browse window');
    assert.equal(context.navigation.back(context.authority()), true); await app.settle(); assert.equal(context.navigation.getSnapshot().route, all);
    const afterAll = app.hosts().find((host: any) => host.name === 'FlatList' && host.props.data === allList.props.data);
    assert.ok(afterAll); assert.equal(afterAll.host, allList.host);
    assert.equal(context.navigation.back(context.authority()), true); await app.settle(); assert.equal(context.navigation.getSnapshot().route, root);
    await app.fill('Search', 'stored');
    const input = app.hosts().find((host: any) => host.name === 'TextInput' && host.props.accessibilityLabel === 'Search'); assert.ok(input);
    await input.props.onSubmitEditing(); await app.settle();
    assert.ok(app.hosts().some((host: any) => host.name === 'Pressable' && host.props.accessibilityLabel === 'Genuine display title'));
    await app.fill('Search', 'no matches'); await input.props.onSubmitEditing(); await app.settle();
    assert.ok(app.hosts().some((host: any) => host.props.testID === 'search-genre-backdrop-only'), 'phone empty draws the separate backdrop prefix');
    assert.equal(genreReads, 3, 'changing slots does not read the directory or request more previews');
    const queryReads = app.requests.filter((request: any) => new URL(request.path, 'https://fixture.invalid').pathname === '/v1/search').length;
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    // Back from Search's first screen returns Home; the Search tab keeps its own stack (motion 2.11), parked and inert.
    assert.deepEqual(context.navigation.getSnapshot().route, {kind: 'shell', destination: {kind: 'home'}});
    await input.props.onSubmitEditing(); await app.settle();
    assert.equal(app.requests.filter((request: any) => new URL(request.path, 'https://fixture.invalid').pathname === '/v1/search').length, queryReads, 'dropped old input cannot submit');
    assert.deepEqual(app.errors, []);
  } finally {
    try {
      await app.unmount();
    } finally {
      try {
        runtime?.dispose();
      } finally {
        app.disposeLoader();
      }
    }
  }
});
