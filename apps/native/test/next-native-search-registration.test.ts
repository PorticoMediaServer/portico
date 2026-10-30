import assert from 'node:assert/strict';
import test from 'node:test';
import type {BrowseCapabilities, BrowseRequestBody} from '../../../packages/client-core/src/browse';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {openNativeSearchGenres} from '../src-next/screens/search/navigation';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const pivot = {id: 'video-titles', labelKey: 'browse.pivot.titles', entityKinds: ['movie', 'show'], defaultSort: [{field: 'title', direction: 'asc'}],
  supportedViews: ['grid'], browsable: true, sorts: ['title'], filters: ['genre', 'favorite']} as const;
// This is the canonical server capability DTO, not a fabricated frontend Library controller.
const capabilities: BrowseCapabilities = {
  library: {id: '*', name: 'Authorized across scope', kind: 'mixed', defaultView: 'browse', pinned: false}, pivots: [pivot], resolvedPivot: pivot,
  fields: [{id: 'genre', labelKey: 'filter.field.genre', type: 'identity-set', operators: ['equals', 'contains-any'], controlHint: 'facet-multi-select', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'show'], facetSource: {field: 'genre', endpoint: '/v1/browse/facets'}},
    {id: 'favorite', labelKey: 'filter.favorite', type: 'boolean', operators: ['equals'], controlHint: 'toggle', complexity: 'quick', cost: 'indexed', applicableKinds: ['movie', 'show']}],
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc'], defaultDirection: 'asc', expensive: false, applicableKinds: ['movie', 'show']}], quickFilters: [],
  surfaces: [{id: 'genre-titles', endpoint: '/v1/browse', paging: 'keyset', sorts: ['title'], filters: ['genre', 'favorite']}],
  queryLimits: {maximumDepth: 6, maximumClauses: 50, maximumBytes: 8192, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 600000},
};
const art = (id: string, slot: 'poster' | 'backdrop' = 'poster') => ({kind: 'item', id,
  path: `/v1/metadata/item/${id}/art/${slot}?v=${'a'.repeat(64)}&w=${slot === 'poster' ? 400 : 1920}`});
const movie = {id: 'movie', libraryId: 'movies', kind: 'movie', title: 'Stored Genre Movie (Full Cut)', displayTitle: 'Genuine display title',
  posterUrl: art('movie').path, navigation: {view: 'item', entityId: 'movie'}, playback: {itemId: 'movie'}};
const otherMovie = {id: 'other', libraryId: 'movies', kind: 'movie', title: 'Other authorized title', available: false, navigation: {view: 'item', entityId: 'other'}, posterUrl: art('other').path};
const genres = [{value: 'artless', label: 'Artless metadata', count: 1, covers: []},
  {value: 'actual-value', label: 'Stored Genre', count: 2, covers: [art('movie'), art('other')], backdrop: art('movie', 'backdrop')}];

test('real App Search keeps separate eligible previews and an artless All genres directory in retained registration', async () => {
  let genreReads = 0;
  const browse: BrowseRequestBody[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string; method: string; body?: unknown}, fixture: {pin: {serverId: string}}) => {
    const url = new URL(request.path, 'https://fixture.invalid');
    if (url.pathname === '/v1/search/genres') {
      genreReads++;
      const preview = url.searchParams.get('preview');
      const rows = preview === 'covers' ? genres.slice(1) : preview === 'backdrop' ? [{value: 'backdrop-only', label: 'Backdrop prefix first', count: 8, covers: [], backdrop: art('other', 'backdrop')}, genres[1]] : genres;
      return {status: 200, body: {variant: 'ready', queryId:'genre_query',revision: 'actual-genre-read', hasGenres: true, genres: rows}};
    }
    if (url.pathname === '/v1/browse-capabilities') {assert.equal(url.searchParams.get('pivot'), 'video-titles'); return {status: 200, body: capabilities};}
    if (url.pathname === '/v1/browse' && request.method === 'POST') {
      const body = request.body as BrowseRequestBody; browse.push(body);
      return {status: 200, body: {pivot: 'video-titles', applied: {query: body.query, sort: body.sort, seek: null}, entries: [movie, otherMovie],
        pageInfo: {start: body.range?.start ?? 0, total: 2, totalIsLowerBound: false, revision: 'opaque-server-position', hasMore: false, nextCursor: ''}, positionIndex: []}};
    }
    if (url.pathname === '/v1/browse/counts') return {status: 200, body: {counts: [{id: 'draft', total: 2, totalIsLowerBound: false, revision: 'opaque-server-count'}]}};
    if (url.pathname === '/v1/search') return {status: 200, body: {
      scope: {serverId: fixture.pin.serverId, libraryId: '', libraryKind: 'mixed', view: 'search', entityId: '', viewerFence: 'Search-own-fence'}, revision: {catalog: 12, viewer: 7},
      query: {q: url.searchParams.get('q'), group: '', groups: ['movies'], limit: 40, sort: 'relevance', direction: 'desc', libraryIds: [], recorded: false},
      capabilities: {groups: [{id: 'movies', title: '', available: true, reason: '', sorts: ['relevance']}], sorts: ['relevance'], directions: ['desc'], maxLimit: 40, groupBudgetMs: 1000},
      groups: [{id: 'movies', entityKind: 'movie', title: '', status: 'success', errorCode: '', totalCount: url.searchParams.get('q') === 'no matches' ? 0 : 1, totalCountExact: true, nextCursor: '', items: url.searchParams.get('q') === 'no matches' ? [] : [movie]}],
    }};
  }});
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
    const previews = app.requests.filter((request: any) => new URL(request.path, 'https://fixture.invalid').pathname === '/v1/search/genres').map((request: any) => new URL(request.path, 'https://fixture.invalid'));
    assert.deepEqual(previews.map((url: URL) => [url.searchParams.get('preview'), url.searchParams.get('limit')]), [['covers', '6'], ['backdrop', '4']]);
    assert.equal(app.requests.filter((request: any) => request.path.startsWith('/v1/search/history')).length, 0, 'confirmed privacy-off means no history read');
    assert.equal(openNativeSearchGenres(context), true); await app.settle();
    const all = context.navigation.getSnapshot().route; assert.equal(all.kind, 'search-genres');
    const allList = app.hosts().find((host: any) => host.name === 'FlatList' && host.props.data?.some((item: any) => item.id === 'artless'));
    assert.ok(allList); assert.equal(allList.props.data.length, 2); assert.equal(genreReads, 3, 'All genres starts its own ordinary read');
    const ordinary = new URL(app.requests.filter((request: any) => new URL(request.path, 'https://fixture.invalid').pathname === '/v1/search/genres').at(-1).path, 'https://fixture.invalid');
    assert.equal(ordinary.searchParams.has('preview'), false); assert.equal(ordinary.searchParams.get('limit'), '50');
    const artlessRow = allList.props.renderItem({item: allList.props.data[0]});
    assert.equal(artlessRow.props.label, 'Artless metadata'); assert.equal(artlessRow.props.description, '1 title');
    const actual = allList.props.data.find((item: any) => item.id === 'actual-value');
    allList.props.renderItem({item: actual}).props.onPress(); await app.settle();
    const genre = context.navigation.getSnapshot().route;
    assert.deepEqual(genre, {kind: 'search-genre', value: 'actual-value', label: 'Stored Genre'});
    assert.ok(browse.length); assert.equal(browse[0]!.pivot, 'video-titles');
    assert.deepEqual(browse[0]!.query, {variant: 'predicate', field: 'genre', operator: 'equals', value: 'actual-value'});
    assert.deepEqual(browse[0]!.sort, [{field: 'title', direction: 'asc'}]);
    assert.equal(browse[0]!.range?.start, 0);
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
