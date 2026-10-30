import test from 'node:test';
import assert from 'node:assert/strict';
import type {SearchGenrePage, SearchGenreRequest} from '../../../packages/client-core/src/search-genres.ts';
import type {LibraryContentApi} from '../../../packages/client-core/src/library-content.ts';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles.ts';
import {screenModule} from './helpers/screen-harness.mjs';

const art = (id: string, slot = 'poster') => ({kind: 'item' as const, id,
  path: `/v1/metadata/item/${id}/art/${slot}?v=${'a'.repeat(64)}&w=${slot === 'poster' ? 400 : 1920}`});
const genre = (value: string, count = 1) => ({value, label: `Metadata ${value}`, count,
  covers: [art(`${value}_a`), art(`${value}_b`)], backdrop: art(`${value}_wide`, 'backdrop')});
type Page = SearchGenrePage & {hasGenres?: boolean; previousCursor?: string; reloadCursor?: string};
type Request = SearchGenreRequest & {preview?: 'covers' | 'backdrop'};
const api: LibraryContentApi = {request: async () => {throw new Error('native adapter must use shared reader');}};
async function adapters(read: (api: LibraryContentApi, request: Request, signal: AbortSignal, options?:{onFallback?:()=>void}) => Promise<Page>, release: (page: Page|undefined)=>void=()=>{}) {
  return await screenModule(new URL('../src-next/screens/search/explore.ts', import.meta.url), {
    react: {default: {}}, '../../../../../packages/client-core/src/search-genres': {readSearchGenres: read,releaseSearchGenres:release,searchGenresReadMetadata:()=>undefined},
  }) as typeof import('../src-next/screens/search/explore.ts');
}
const options = {api, current: () => true, openGenre() {}};

test('phone requests two bounded slot previews and preserves each independent server prefix and exact art', async () => {
  const requests: Request[] = [], opened: unknown[] = [];
  const covers = ['z', 'a', 'c', 'd', 'e', 'f'].map(id => genre(id));
  const backdrops = ['b', 'z', 'q', 'a'].map(id => genre(id));
  const {NativeSearchExplore} = await adapters(async (_api, request) => {
    requests.push(request); return {queryId:'fixture_query',revision: 'opaque', hasGenres: true, genres: request.preview === 'covers' ? covers : backdrops};
  });
  const controller = new NativeSearchExplore({...options, surface: 'phone', openGenre: value => opened.push(value)});
  try {
    assert.equal(requests.length, 0);
    const loading = controller.load(); assert.equal(controller.load(), loading); await loading;
    const state = controller.getSnapshot(); assert.equal(state.phase, 'ready'); assert.equal(state.hasGenres, true);
    assert.deepEqual(requests, [{preview: 'covers', limit: 6}, {preview: 'backdrop', limit: 4}]);
    assert.deepEqual(nativeSearchGenreTiles(state.genres, 'phone').map(value => value.id), covers.map(value => value.value));
    assert.deepEqual(nativeSearchGenreTiles(state.backdropGenres!, 'phone-empty').map(value => value.id), ['b', 'z', 'q', 'a']);
    assert.deepEqual(state.genres[0].coverPaths, covers[0].covers.map(value => value.path));
    assert.equal(state.backdropGenres![0].backdropPath, backdrops[0].backdrop.path);
    controller.openGenre({...state.genres[0]}); assert.deepEqual(opened, []);
    controller.openGenre(state.genres[0]); controller.openGenre(state.backdropGenres![0]);
    assert.deepEqual(opened, [state.genres[0], state.backdropGenres![0]]);
    await controller.load(); assert.equal(requests.length, 2);
    assert.equal(state.initialHeading, 'next.search.browseByGenre'); assert.equal(state.emptyHeading, 'next.search.browseInstead');
  } finally {controller.dispose();}
});

test('TV requests one four-backdrop preview, and zero drawable tiles retains hasGenres without walking the directory', async () => {
  const requests: Request[] = [];
  const {NativeSearchExplore} = await adapters(async (_api, request) => {
    requests.push(request); return {queryId:'fixture_query',revision: 'opaque', genres: [], hasGenres: true};
  });
  for (const surface of ['tv', 'phone'] as const) {
    const controller = new NativeSearchExplore({...options, surface});
    await controller.load(); assert.equal(controller.getSnapshot().phase, 'ready');
    assert.equal(controller.getSnapshot().hasGenres, true); assert.deepEqual(controller.getSnapshot().genres, []); controller.dispose();
  }
  assert.deepEqual(requests, [{preview: 'backdrop', limit: 4}, {preview: 'covers', limit: 6}, {preview: 'backdrop', limit: 4}]);
});

test('independent slot previews accept separately disclosed complete bases during content churn', async () => {
  let calls = 0;
  const {NativeSearchExplore} = await adapters(async () => ({queryId:'fixture_query',revision: String(++calls), genres: [genre('a')]}));
  const controller = new NativeSearchExplore({...options, surface: 'phone'});
  await controller.load(); assert.equal(calls, 2); assert.equal(controller.getSnapshot().phase, 'ready');
  assert.equal(controller.getSnapshot().revision,'1');assert.equal(controller.getSnapshot().backdropRevision,'2');
  assert.equal(controller.getSnapshot().genres[0].id,'a');controller.dispose();
});

test('preview retirement, hiding and background cancellation discard late responses and suppress old actions', async () => {
  let current = true, signal: AbortSignal | undefined, finish!: (value: Page) => void, started!: () => void, opens = 0;
  let entered = new Promise<void>(resolve => {started = resolve;});
  const {NativeSearchExplore} = await adapters(async (_api, _request, actualSignal) => {
    signal = actualSignal; started(); return new Promise(resolve => {finish = resolve;});
  });
  const controller = new NativeSearchExplore({...options, current: () => current, surface: 'tv', openGenre: () => {opens++;}});
  const loading = controller.load(); await entered; controller.setActive(false); assert.ok(signal?.aborted);
  finish({queryId:'fixture_query',revision: 'late', genres: [genre('late')]}); await loading;
  assert.equal(controller.getSnapshot().phase, 'idle'); assert.deepEqual(controller.getSnapshot().genres, []);
  entered = new Promise<void>(resolve => {started = resolve;}); controller.setActive(true); await entered;
  const resumed = controller.load(); finish({queryId:'fixture_query',revision: 'live', genres: [genre('live')]}); await resumed;
  const live = controller.getSnapshot().genres[0]; assert.ok(live);
  controller.setActive(false); controller.openGenre(live); assert.equal(opens, 0);
  current = false; const retired = controller.getSnapshot(); controller.openGenre(live);
  assert.deepEqual(retired.genres, []); assert.equal(opens, 0); assert.equal(controller.getSnapshot(), retired);
});

const page = (number: number): Page => ({queryId:`fixture_query_${number}`,revision: 'basis', genres: Array.from({length: 50}, (_, index) => ({value: `${number}:${index}`, label: `Genre ${number}:${index}`, count: 10, covers: []})),
  ...(number < 8 ? {nextCursor: `after:${number}`} : {}), ...(number ? {previousCursor: `before:${number}`} : {}), reloadCursor: `at:${number}`});
const pageNumber = (cursor?: string) => cursor?.startsWith('after:') ? Number(cursor.slice(6)) + 1 : cursor?.startsWith('before:') ? Number(cursor.slice(7)) - 1 : cursor ? Number(cursor.slice(3)) : 0;

test('All genres is an independent ordinary list preserving every artless row/count and bounded reversible windows', async () => {
  const requests: Request[] = [], opened: unknown[] = [];
  const {NativeSearchExplore, NativeSearchGenreList} = await adapters(async (_api, request) => {
    requests.push(request); return request.preview ? {queryId:'fixture_query',revision: 'basis', genres: [], hasGenres: true} : page(pageNumber(request.cursor));
  });
  const preview = new NativeSearchExplore({...options, surface: 'phone'});
  const list = new NativeSearchGenreList({...options, openGenre: value => opened.push(value)});
  try {
    await preview.load(); await list.load(); assert.equal(requests.length, 3);
    assert.deepEqual(requests[2], {limit: 50});
    const first = list.getSnapshot().genres[0]; assert.equal(first.count, 10); assert.deepEqual(first.coverPaths, []);
    list.openGenre({...first}); assert.deepEqual(opened, []); list.openGenre(first); assert.deepEqual(opened, [first]);
    for (let number = 1; number <= 8; number++) {
      await list.next(); const state = list.getSnapshot();
      assert.equal(state.phase, 'ready'); assert.ok(state.genres.length <= 150); assert.equal(state.genres.at(-1)?.id, `${number}:49`);
    }
    assert.equal(list.getSnapshot().nextCursor, undefined);
    for (let number = 5; number >= 0; number--) {
      await list.previous(); const state = list.getSnapshot();
      assert.equal(state.phase, 'ready'); assert.equal(state.genres.length, 150); assert.equal(state.genres[0].id, `${number}:0`);
    }
    assert.equal(list.getSnapshot().previousCursor, undefined);
    assert.ok(requests.slice(2).every(request => !request.preview && request.limit === 50));
    assert.deepEqual(preview.getSnapshot().genres, [], 'ordinary paging never feeds tile state');
  } finally {preview.dispose(); list.dispose();}
});

test('All genres protects visible/focused rows, reloads at a resident anchor and keeps exact unchanged row identities', async () => {
  const requests: Request[] = [];
  const {NativeSearchGenreList} = await adapters(async (_api, request) => {requests.push(request); return page(pageNumber(request.cursor));});
  const list = new NativeSearchGenreList(options);
  try {
    await list.load(); await list.next(); await list.next();
    list.setVisible(['0:49']); await list.next(); assert.equal(requests.length, 3, 'visible head cannot be evicted');
    list.setVisible(['2:1']); list.setFocused('0:0', true); await list.next(); assert.equal(requests.length, 3, 'focused head cannot be evicted');
    list.setFocused('2:1', true); list.setFocused('0:0', false); await list.next(); assert.equal(requests.length, 4, 'late blur cannot clear the newer focus');
    assert.equal(list.getSnapshot().genres[0].id, '1:0');
    const stable = list.getSnapshot().genres.find(value => value.id === '2:1');
    await list.reload(); assert.equal(requests.at(-1)?.cursor, 'at:2'); assert.equal(list.getSnapshot().genres.find(value => value.id === '2:1'), stable);
    assert.equal(list.getSnapshot().genres[0].id, '2:0'); assert.equal(list.getSnapshot().previousCursor, 'before:2');
  } finally {list.dispose();}
});

test('All genres refuses mixed revisions, retries the failed edge, and cancels hidden late reads', async () => {
  let changed = true, pending: ((value: Page) => void) | undefined, signal: AbortSignal | undefined;
  const requests: Request[] = [];
  const {NativeSearchGenreList} = await adapters(async (_api, request, actualSignal) => {
    requests.push(request); signal = actualSignal;
    if (request.cursor === 'after:1') return new Promise(resolve => {pending = resolve;});
    return {...page(pageNumber(request.cursor)), ...(request.cursor && changed ? {queryId:'fixture_query',revision: 'changed'} : {})};
  });
  const list = new NativeSearchGenreList(options);
  try {
    await list.load(); const rows = list.getSnapshot().genres;
    await list.next(); assert.equal(list.getSnapshot().phase, 'error'); assert.equal(list.getSnapshot().genres, rows);
    changed = false; await list.load(); assert.equal(requests.at(-1)?.cursor, 'after:0'); assert.equal(list.getSnapshot().phase, 'ready');
    const loading = list.next(); await new Promise(resolve => setImmediate(resolve)); list.setActive(false); assert.ok(signal?.aborted);
    const retained = list.getSnapshot(); pending!({...page(2)}); await loading;
    assert.equal(list.getSnapshot(), retained); assert.equal(retained.genres.length, 100);
  } finally {list.dispose();}
});

test('ordinary list keeps its FlatList mounted for paging errors, uses scroll edges and wires focus/visibility protection', async () => {
  const {hooks} = await import('./helpers/screen-harness.mjs');
  const host = hooks(), events: unknown[] = [];
  const rows = [{id: 'artless', name: 'Exact artless name', count: 17, coverPaths: []}];
  let snapshot: any = {phase: 'ready', genres: rows, nextCursor: 'next', previousCursor: 'previous'};
  const explore = {subscribe: () => () => {}, getSnapshot: () => snapshot,
    setVisible: (ids: string[]) => events.push(['visible', ids]), setFocused: (id: string, focused: boolean) => events.push(['focus', id, focused]),
    next: async () => {events.push('next');}, previous: async () => {events.push('previous');}, load: async () => {events.push('retry');}, openGenre: (value: unknown) => events.push(['open', value])};
  const {AllGenresScreen} = await screenModule(new URL('../src-next/screens/search/AllGenresScreen.tsx', import.meta.url), {'../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, 
    react: host.react, 'react-native': {FlatList: 'FlatList', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/overlays': {MenuRow: 'MenuRow'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states': {GridSkeleton: 'GridSkeleton', StateView: 'StateView'}, '../../kit/browse/presentation': {browseType: () => ({})},
    '../../kit/theme': {useTheme: () => ({colors: {ink: '#000', text: '#fff'}})},
  });
  const elements = (node: any): any[] => Array.isArray(node) ? node.flatMap(elements) : node?.props ? [node, ...elements(node.props.children)] : [];
  let current = true;
  const render = () => host.render(() => AllGenresScreen({explore, runtime: {text: {t: (id: string, params?: any) => params?.count ?? id}, error: (_error: unknown, retry: () => void) => ({onRetry: retry})}, visible: true, current: () => current, onBack() {}}));
  const list = elements(render()).find(node => node.type === 'FlatList'); assert.ok(list);
  assert.deepEqual(list.props.maintainVisibleContentPosition, {minIndexForVisible: 0});
  list.props.onEndReached(); list.props.onStartReached(); assert.deepEqual(events, [], 'layout callbacks alone cannot drain pages');
  list.props.onViewableItemsChanged({viewableItems: [{item: rows[0], isViewable: true}]});
  const row = list.props.renderItem({item: rows[0]}); assert.equal(row.props.label, 'Exact artless name'); assert.equal(row.props.description, 17);
  row.props.onFocus(); row.props.onPress(); row.props.onBlur();
  assert.deepEqual(events, [['visible', ['artless']], ['focus', 'artless', true], ['open', rows[0]], ['focus', 'artless', false]]);
  list.props.onScroll({nativeEvent: {contentOffset: {y: 200}, contentSize: {height: 400}, layoutMeasurement: {height: 200}}});
  list.props.onScroll({nativeEvent: {contentOffset: {y: 0}, contentSize: {height: 400}, layoutMeasurement: {height: 200}}});
  assert.deepEqual(events.slice(-2), ['next', 'previous']);
  snapshot = {...snapshot, phase: 'error', error: new Error('edge failed')};
  const retained = elements(render()).find(node => node.type === 'FlatList'); assert.ok(retained); assert.equal(retained.props.data, rows);
  retained.props.ListFooterComponent.props.onRetry(); assert.equal(events.at(-1), 'retry');
  current = false; row.props.onPress(); retained.props.ListFooterComponent.props.onRetry(); assert.equal(events.at(-1), 'retry');
});

test('All genres detects resident cursor cycles without retaining lifetime cursor or ID history', async () => {
  let reads = 0;
  const {NativeSearchGenreList} = await adapters(async () => ({queryId:'fixture_query',revision: 'basis', genres: [], nextCursor: ++reads === 2 ? 'b' : 'a'}));
  const list = new NativeSearchGenreList(options);
  try {
    await list.load(); await list.next(); await list.next(); await list.next();
    assert.equal(reads, 3); assert.equal(list.getSnapshot().phase, 'error');
  } finally {list.dispose();}
});

test('cold preview fallback stops the spinner without claiming an empty genre catalogue, then installs exact', async()=>{
  let finish!:(page:Page)=>void,entered!:()=>void;const started=new Promise<void>(resolve=>{entered=resolve;});
  const {NativeSearchExplore}=await adapters(async(_api,_request,_signal,options)=>{
    options?.onFallback?.();entered();return new Promise(resolve=>{finish=resolve;});
  });
  const controller=new NativeSearchExplore({...options,surface:'tv'});
  try{const loading=controller.load();await started;assert.equal(controller.getSnapshot().phase,'ready');assert.equal(controller.getSnapshot().hasGenres,undefined);
    finish({queryId:'exact_query',revision:'exact',hasGenres:true,genres:[genre('ready')]});await loading;
    assert.equal(controller.getSnapshot().hasGenres,true);assert.equal(controller.getSnapshot().genres[0].id,'ready');
  }finally{controller.dispose();}
});

test('resident directory leases are released on page eviction, background and disposal',async()=>{
  const released=new Set<string>();
  const {NativeSearchGenreList}=await adapters(async(_api,request)=>page(pageNumber(request.cursor)),value=>{if(value)released.add(value.queryId);});
  const list=new NativeSearchGenreList(options);
  try{await list.load();await list.next();await list.next();assert.equal(released.size,0);
    await list.next();assert.deepEqual([...released],['fixture_query_0']);
    list.setActive(false);assert.deepEqual([...released].sort(),['fixture_query_0','fixture_query_1','fixture_query_2','fixture_query_3']);
    assert.equal(list.getSnapshot().genres.length,150);
  }finally{list.dispose();}
});
