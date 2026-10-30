import test from 'node:test';
import * as savedCore from '../../../packages/client-core/src/saved.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {savedNearEnd} from '../src-next/screens/saved/paging.ts';

const i18n = {t: (key: string, values?: any) => ['web.group.ownOrder', 'next.saved.reordering', 'next.collection.automaticDescription'].includes(key) ? defaultI18n.t(key as never, values) : values?.count === undefined ? key : `${key}:${values.count}`, has: () => true,
  number: String, bytes: String, list: (items: string[]) => items.join(', '), date: String};
const palette = {ink: '#000000', text: '#ffffff', textSecondary: '#cccccc', textTertiary: '#999999', raised: '#111111', raisedBright: '#222222', line: '#333333', lineStrong: '#444444', accent: '#0088cc', accentStrong: '#0099dd'};
const theme = (phone = true) => ({isTV: false, platform: phone ? 'phone' : 'tablet', gutter: 20, colors: palette, type: {}, reduceMotion: true,
  spec: {shelf: {phonePaddingTop: 6, phonePaddingBottom: 8, gap: {phone: 12, tablet: 12}, marginTop: {phone: 28, tablet: 28}}}});
function fixture(phone = true) {
  const host = hooks(), react: any = {...host.react, useLayoutEffect() {}, useId: () => 'saved-resource'}; react.default = react;
  const modules: Record<string, any> = {'../share/share-actions': {shareLinkActions: () => []}, react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', VirtualizedList: 'VirtualizedList',
    BackHandler: {addEventListener: () => ({remove() {}})}, useWindowDimensions: () => ({width: phone ? 390 : 1000, height: 800})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 24, bottom: 12})},
    '../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'error'})},
    '../../kit/theme': {useTheme: () => theme(phone)}, '../../kit/browse/presentation': {browseType: (_: any, fontSize: number) => ({fontSize}), alpha: (color: string) => color},
    '../../kit/browse': {SortMenu: 'SortMenu'}, '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame', Parts: 'Parts'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork', AmbientArt: 'AmbientArt', MediaArt: 'MediaArt', PersonCircle: 'PersonCircle'},
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Switch: 'Switch', Tabs: 'Tabs'},
    '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {StateView: 'StateView', LoadingBoundary: 'LoadingBoundary', SkeletonBlock: 'SkeletonBlock'},
    '../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet', Menu: 'Menu', ToastHost: 'ToastHost'},
    '../../shell/model': {sameAuthority: (a: any, b: any) => a === b}, '../destinations/focus': {nativeDestinationFocusIds: () => ({rail: 'rail'})},
    '../home/model': {homeCardText: () => ({caption: []})}, '../read/platform': {isReadingLibraryKind: () => false},
    './SavedForms': {SavedConfirmSheet: 'SavedConfirmSheet', SavedNameSheet: 'SavedNameSheet'}, './SavedSortMenu': {SavedSortMenu: 'SavedSortMenu'},
    '../../../../../packages/client-core/src/saved': savedCore, 
    './SavedLoading': {SavedLoading: 'SavedLoading', SavedTail: 'SavedTail'}, './SavedReorderList': {SavedReorderList: 'SavedReorderList'}, './paging': {savedNearEnd},
    './presentation': {exactDurationSeconds: () => undefined, personalMosaic: () => [], playlistEntryMosaic: () => [], savedDuration: String,
      savedItemCount: String, savedLibraryName: () => undefined, savedPlaylistEyebrow: 'playlist', savedResourceEyebrow: () => 'collection',
      savedTitleCount: String, savedViewCount: String, savedViewConditionCount: () => 1, savedVisibility: () => undefined, savedVisibilityHelp: () => ''},
    '../saved/presentation': {savedDuration: String}, '../../tv/layout/TVShelf': {TVShelf: 'TVShelf'}, '../../kit/layout': {WindowedGrid: 'WindowedGrid', WindowedRow: 'WindowedRow'},
    '../../platform/offline-downloads': {groupOfflineEntries: () => []}, './model': {}, './SavedDownloadCard': {SavedDownloadCard: 'SavedDownloadCard'},
    '../downloads/playback': {openNativeSavedDownload() {}},
  };
  return {host, modules, load: (name: string) => screenModule(new URL(`../src-next/screens/${name}.tsx`, import.meta.url), modules)};
}
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.ListHeaderComponent)] : [];
const find = (tree: any, id: string) => nodes(tree).find(node => node.props.testID === id);
const text = (tree: any): string => typeof tree === 'string' || typeof tree === 'number' ? String(tree) : Array.isArray(tree) ? tree.map(text).join('') : tree?.props ? text(tree.props.children) : '';
const entry = {id: 'movie-a', kind: 'movie', libraryId: 'movies', title: 'A', posterUrl: '/a', duration: 1000, progressSeconds: 250, year: 2020};
const projection = {scope: {viewerFence: 'saved-fence'}, revision: {catalog: 1, viewer: 1}, query: {sort: 'title', direction: 'asc', category: 'all'},
  sorts: [{id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc']}, {id: 'added', labelKey: 'sort.added', directions: ['desc']}], filters: [],
  sections: [{id: 'items', entries: [entry], nextCursor: 'next', totalCount: 90}]};
const activation = {subscribe: () => () => {}, getSnapshot: () => ({visible: true, revision: 1, intent: 'enter'})};
const authority = {}, runtime = {reportError: (error: unknown) => {throw error;}, getSnapshot: () => ({authority}), artwork: (path: string) => path,
  ambient: (path: string) => path, error: () => ({title: 'error'}), text: {}, registerScrollToTop() {}};

// These tests invoke rendered controls and inspect their actual callback arguments,
// rather than searching implementation text for the expected strings.
test('Saved SortMenu offers all fields and only dispatches their explicit offered directions', async () => {
  const f = fixture(), {SavedSortMenu} = await f.load('saved/SavedSortMenu'), changes: unknown[] = [];
  const menu = f.host.render(() => SavedSortMenu({sorts: projection.sorts, sort: 'title', direction: 'desc', scope: 'saved', onChange: (...args: unknown[]) => changes.push(args)}));
  assert.equal(menu.type, 'SortMenu'); assert.deepEqual(menu.props.sorts.map((value: any) => value.id), ['title', 'added']);
  assert.deepEqual(menu.props.value, {field: 'title', direction: 'desc', flippable: true, reshufflable: false});
  menu.props.onValueChange({field: 'added', direction: 'desc'}); menu.props.onValueChange({field: 'added', direction: 'asc'});
  menu.props.onValueChange({field: 'unknown', direction: 'asc'}); assert.deepEqual(changes, [['added', 'desc']]);
});

test('Tablet Saved changes all seven tabs locally and retains its own chrome', async () => {
  const f = fixture(false), {SavedScreen} = await f.load('saved/SavedScreen'), pushed: unknown[] = [], bodies: string[] = [];
  const state = {phase: 'ready', myList: {projection}, myListSquares: {}, favorites: {}, pins: [], summary: undefined};
  const props = {destination: {subscribe: () => () => {}, getSnapshot: () => state}, runtime, device: {subscribe: () => () => {}, getSnapshot: () => ({entries: []})},
    activation, actions: {openView: (value: unknown) => pushed.push(value)}, downloadsCopy: {}, onThisPhone: 'phone',
    tabBody: (tab: string) => {bodies.push(tab); return `body:${tab}`;}, downloads: () => 'body:downloads'};
  let tree = f.host.render(() => SavedScreen(props));
  for (const tab of ['favorites', 'playlists', 'collections', 'views', 'history', 'downloads', 'watchlist']) {
    find(tree, 'saved-tablet-tabs').props.onValueChange(tab); tree = f.host.render(() => SavedScreen(props));
    assert.equal(find(tree, 'saved-tablet-tabs').props.value, tab);
    assert.ok(text(tree).includes('saved.title'));
  }
  assert.deepEqual(bodies, ['favorites', 'playlists', 'collections', 'views', 'history']); assert.deepEqual(pushed, []);
});

test('Saved list carries selection to cards and prefetches before its loaded end', async () => {
  const f = fixture(false), {SavedListScreen} = await f.load('saved/SavedListScreen'), calls: unknown[] = [], picked = {active: false, selected: false, onEnter() {}, onToggle() {}};
  const snapshot = {phase: 'ready', saved: {projection}}, destination = {subscribe: () => () => {}, getSnapshot: () => snapshot,
    select: async (route: unknown) => {calls.push(route);}, next: async (section: string) => {calls.push(section);}};
  const props = {route: {view: 'favorites', shape: 'squares'}, destination, activation, runtime, actions: {}, back() {}, inline: true,
    selection: {item: () => picked, start() {}, render: () => 'selection'}};
  const tree = f.host.render(() => SavedListScreen(props)), card = nodes(tree).find(node => node.type?.name === 'SavedListCard');
  assert.equal(card.props.selection, picked); assert.equal(find(tree, 'saved-list-back'), undefined);
  nodes(tree).find(node => node.type === 'SavedSortMenu').props.onChange('added', 'desc');
  assert.deepEqual(calls[0], {view: 'favorites', shape: 'squares', sort: 'added', direction: 'desc', filter: 'all'});
  const scroll = nodes(tree).find(node => node.type === 'ScrollView' && !node.props.horizontal);
  scroll.props.onLayout({nativeEvent: {layout: {height: 600}}}); scroll.props.onContentSizeChange(900, 2600);
  assert.equal(calls.length, 1); scroll.props.onScroll({nativeEvent: {contentOffset: {y: 1600}}}); assert.equal(calls[1], 'items');
});

test('Saved resource menus keep membership commands beside canonical selection, and manual reorder commits guarded occurrence IDs', async () => {
  const f = fixture(), {SavedResourceScreen} = await f.load('saved/SavedResourceScreen'), calls: any[] = [], selected = {active: false, selected: false, onEnter() {}, onToggle() {}};
  const other = {...entry, id: 'movie-b'}, occurrence = {id: 'occ-a', kind: 'playlist_entry', media: entry}, second = {id: 'occ-b', kind: 'playlist_entry', media: other};
  const sorts = [{id: 'position', labelKey: 'sort.position', directions: ['asc']}, {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc']}];
  let snapshot: any = {phase: 'ready', saved: {playlist: {name: 'Playlist', entryCount: 2, revision: 3, actions: ['reorder', 'remove']},
    projection: {...projection, playlistRevision: 3, sorts, sections: [{id: 'items', entries: [occurrence, second], start: 0, order: {last: 'occ-b'}}]}, pagination: {next: []}}};
  let visible = true;
  const active = {subscribe: () => () => {}, getSnapshot: () => ({visible, revision: 1, intent: 'enter'})};
  const destination = {subscribe: () => () => {}, getSnapshot: () => snapshot, mutateSaved: async (command: unknown) => {calls.push(['command', command]);}};
  const props = {route: {view: 'playlist', playlistId: 'p'}, destination, activation: active, runtime, actions: {open() {}, play() {}, menu: (...args: unknown[]) => calls.push(['menu', ...args])},
    libraries: [], back() {}, selection: {item: () => selected, render: () => null}};
  let tree = f.host.render(() => SavedResourceScreen(props));
  const row = nodes(tree).find(node => node.type?.name === 'SavedResourceRow'), anchor = {current: {native: true}};
  assert.equal(row.props.selection, selected); row.props.onMore(anchor);
  assert.equal(calls[0][1], entry); assert.equal(calls[0][2], anchor); assert.equal(calls[0][3], selected.onEnter);
  assert.ok(calls[0][4].some((action: any) => action.id === 'remove-entry'));
  // collections.html 18: the sort chip names the playlist's own order and offers the server's sorts.
  const orderChip = nodes(tree).find(node => node.type === 'SavedSortMenu');
  assert.equal(orderChip.props.sort, 'position'); assert.deepEqual(orderChip.props.sorts, sorts);
  assert.ok(orderChip.props.label(sorts[0]).includes('Playlist order'));
  find(tree, 'saved-reorder').props.onPress(); tree = f.host.render(() => SavedResourceScreen(props));
  assert.ok(text(tree).includes('Reordering')); assert.ok(find(tree, 'saved-reorder-done')); assert.equal(find(tree, 'saved-play'), undefined);
  const list = nodes(tree).find(node => node.type === 'SavedReorderList');
  list.props.onPlace(list.props.rows[0], 'occ-b', ['occ-a', 'occ-b']); await Promise.resolve(); await Promise.resolve();
  assert.deepEqual(calls[1], ['command', {action: 'reorder', entryIds: ['occ-a'], afterEntryId: 'occ-b'}]);
  visible = false; list.props.onMove(list.props.rows[0], 'down'); assert.equal(calls.length, 2, 'retained hidden controls cannot write');
  visible = true; snapshot = {...snapshot, saved: {...snapshot.saved, playlist: {...snapshot.saved.playlist, actions: []}}};
  list.props.onPlace(list.props.rows[0], 'occ-b', ['occ-a', 'occ-b']); assert.equal(calls.length, 2, 'a revoked capability cannot write');
});

test('Phone collection uses the centred 300-point hero, full-width play and actual metadata rows', async () => {
  const f = fixture(), {NativeCollectionScreen} = await f.load('collections/CollectionScreen');
  const collection = {itemAt: () => entry};
  const state = {phase: 'ready', title: 'Collection', totalCount: 1, collection, first: entry, complete: [entry], sorts: [], projection: {entity: {origin: 'automatic'}}};
  const page = f.host.render(() => NativeCollectionScreen({state, runtime, focusScope: 'collection', onBack() {}, onOpen() {}, onPlay() {}, onShuffle() {}, onSort() {}, onRetry() {}}));
  // The members are one windowed list; the hero, actions and note scroll as its header.
  const list = nodes(page).find(node => node.type === 'WindowedGrid');
  assert.equal(list.props.testID, 'collection-list'); assert.equal(list.props.collection, collection); assert.equal(list.props.itemHeight, 98);
  const tree = [page, list.props.header];
  assert.ok(nodes(tree).some(node => node.type === 'View' && node.props.style?.height === 300));
  assert.equal(find(tree, 'collection-back').props.style.top, 30);
  assert.deepEqual(find(tree, 'collection-play-all').props.style, {flex: 1, height: 48});
  assert.equal(find(tree, 'collection-shuffle').props.iconOnly, true); assert.ok(find(tree, 'collection-menu'));
  assert.ok(text(tree).includes('Portico put this together from the films’ details'));
  const row = list.props.renderItem(entry, {index: 0, id: 'movie:e', onFocus() {}, onBlur() {}}); assert.equal(row.type.name, 'CollectionRow'); assert.equal(row.props.entry, entry);
});

test('Person adds the known-for ambient wash without advertising unsupported owner or sort actions', async () => {
  const f = fixture(), {PersonScreen} = await f.load('people/PersonScreen');
  const page = {person: {id: 'person', name: 'Person Name', roles: [], knownFor: [entry]}, pageInfo: {total: 1}};
  const collection = {pageOf: () => 0, subscribe: () => () => {}, getSnapshot: () => ({generation: 1, total: 1, status: 'ready'}), getPageVersion: () => 0};
  const tree = f.host.render(() => PersonScreen({focusScope: 'person', page, role: 'all', collection, runtime,
    actions: {menuState: {subscribe: () => () => {}, getSnapshot: () => 0}}, onRole() {}, onBack() {}}));
  const ambient = nodes(tree).find(node => node.type === 'AmbientArt'); assert.equal(ambient.props.height, 560); assert.equal(ambient.props.source, '/a');
  assert.equal(nodes(tree).some(node => node.type === 'Menu'), false);
  const list = nodes(tree).find(node => node.type === 'VirtualizedList');
  const creditCollection = {...collection, itemAt: () => ({media: entry, role: 'Actor'})};
  const next = f.host.render(() => PersonScreen({focusScope: 'person', page, role: 'all', collection: creditCollection, runtime,
    actions: {menuState: {subscribe: () => () => {}, getSnapshot: () => 0}}, selection: {item: () => ({}), render: () => null}, onRole() {}, onBack() {}}));
  const credit = nodes(nodes(next).find(node => node.type === 'VirtualizedList').props.renderItem({item: 0})).find(node => node.type?.name === 'PersonEntryCard');
  assert.equal(credit.props.entry, entry); assert.ok(credit.props.selection); assert.ok(list);
});

test('Person tablet draws Select before the role chips and enters selection without a pick; phone does not', async () => {
  const page = {person: {id: 'person', name: 'Person Name', roles: [], knownFor: [entry]}, pageInfo: {total: 1}};
  const collection = {pageOf: () => 0, subscribe: () => () => {}, getSnapshot: () => ({generation: 1, total: 1, status: 'ready'}), getPageVersion: () => 0};
  let started = 0;
  const selection = {start: () => {started++;}, subscribe: () => () => {}, getSnapshot: () => 0, item: () => undefined, render: () => null};
  const draw = async (phone: boolean) => {
    const f = fixture(phone), {PersonScreen} = await f.load('people/PersonScreen');
    return f.host.render(() => PersonScreen({focusScope: 'person', page, role: 'all', collection, runtime,
      actions: {menuState: {subscribe: () => () => {}, getSnapshot: () => 0}}, selection, onRole() {}, onBack() {}}));
  };
  const tablet = await draw(false), chips = nodes(tablet).filter(node => node.type === 'Chip');
  const select = find(tablet, 'person-select');
  assert.equal(select.props.icon, 'i-check'); assert.equal(text(select), 'next.selection.select');
  assert.equal(chips.indexOf(select), chips.findIndex(chip => chip.props.testID === 'person-role-all') - 1, 'Select leads the role chips');
  select.props.onPress(); assert.equal(started, 1);
  assert.equal(nodes(await draw(true)).some(node => node.props?.testID === 'person-select'), false);
});

test('Saved delayed initial skeleton retains its shape and adds the quiet line after eight seconds', async context => {
  const f = fixture(), cleanups: Array<() => void> = []; let effect = false;
  f.modules.react.useEffect = (run: () => () => void) => {if (!effect) {effect = true; cleanups.push(run());}};
  context.mock.timers.enable({apis: ['setTimeout']});
  const {SavedLoading} = await f.load('saved/SavedLoading');
  let tree = f.host.render(() => SavedLoading({kind: 'resource'}));
  const boundary = nodes(tree).find(node => node.type === 'LoadingBoundary'); assert.equal(boundary.props.ready, false);
  assert.ok(nodes(boundary.props.skeleton).some(node => node.type === 'SkeletonBlock' && node.props.width === 152 && node.props.height === 152));
  context.mock.timers.tick(7999); tree = f.host.render(() => SavedLoading({kind: 'resource'})); assert.equal(text(tree).includes('next.states.stillLoading'), false);
  context.mock.timers.tick(1); tree = f.host.render(() => SavedLoading({kind: 'resource'})); assert.equal(text(tree).includes('next.states.stillLoading'), true);
  cleanups.forEach(cleanup => cleanup());
});

test('Phone playlist directory exposes the producer pin and the shared field/direction picker', async () => {
  const f = fixture(), {SavedDirectoryScreen} = await f.load('saved/SavedDirectoryScreen'), calls: unknown[] = [];
  const playlist = {kind: 'playlist', id: 'p', title: 'Pinned playlist', count: 2, pinned: true, artworkPaths: []};
  const snapshot = {phase: 'ready', saved: {projection: {...projection, sections: [{id: 'playlists', entries: [playlist], nextCursor: '', totalCount: 1}]}, pagination: {next: []}}};
  const tree = f.host.render(() => SavedDirectoryScreen({route: {view: 'playlists'}, activation, runtime, libraries: [], actions: {}, back() {}, open() {},
    destination: {subscribe: () => () => {}, getSnapshot: () => snapshot, select: async (route: unknown) => {calls.push(route);}}}));
  const row = nodes(tree).find(node => node.type?.name === 'SavedDirectoryRow'); assert.equal(row.props.pinned, true);
  nodes(tree).find(node => node.type === 'SavedSortMenu').props.onChange('added', 'desc');
  assert.deepEqual(calls, [{view: 'playlists', sort: 'added', direction: 'desc', filter: 'all'}]);
});
