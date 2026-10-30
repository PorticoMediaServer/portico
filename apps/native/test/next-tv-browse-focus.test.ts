import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {tvEpisodeRegionMap} from '../src-next/screens/watch/tv-title-focus.ts';
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const theme = {isTV: true, textScale: 1, colors: {ink: '#000', text: '#fff', textSecondary: '#ccc', textTertiary: '#999', accent: '#abc', accentStrong: '#def'}, type: {caption: {}}};
const i18n = {defaultI18n: {t: (key: string) => key}};
const native = {Text: 'Text', View: 'View', ScrollView: 'ScrollView'};
const type = {browseType: (_theme: unknown, fontSize: number) => ({fontSize})};

test('Jump mounts separate held-panel and letter regions and restores its actual selected child', async () => {
  const h = hooks(), registry = createFocusRegistry(), focused: string[] = [];
  const {LibraryJumpSheet} = await screenModule(new URL('../src-next/screens/library/LibraryJumpSheet.tsx', import.meta.url), {
    react: h.react, 'react-native': native, '../../../../../packages/i18n/src': i18n,
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => registry},
    '../../kit/overlays': {SidePanel: 'SidePanel'}, '../../kit/browse/presentation': {...type, alpha: () => ''}, '../../kit/theme': {useTheme: () => theme},
  });
  const tree = h.render(() => LibraryJumpSheet({open: true, panelId: 'library.jump', letters: [{key: 'A'}, {key: 'M'}], selected: 'M', onSelect() {}, onClose() {}}));
  const region = nodes(tree).find(node => node.type === 'FocusRegion');
  const releasePanel = registry.mountRegion(tree.props.testID, {}), releaseLetters = registry.mountRegion(region.props.id, {});
  try {
    assert.notEqual(tree.props.testID, region.props.id);
    registry.configure({id: tree.props.testID, hold: true}); registry.configure({...region.props, parent: tree.props.testID});
    for (const child of nodes(region).filter(node => node.type === 'Focusable')) registry.attach({regionId: region.props.id, childId: child.props.focusId}, {requestTVFocus: () => focused.push(child.props.focusId)});
    tree.props.initialFocusRef.current.focus(); assert.deepEqual(focused, ['jump:M']);
  } finally {releaseLetters(); releasePanel();}
});

test('season focus and accessibility activation agree, current seasons avoid reload, and focus prefetches the real tail', async () => {
  const h = hooks(), selected: string[] = []; let tails = 0;
  const {TVEpisodeBrowser} = await screenModule(new URL('../src-next/screens/watch/TVEpisodeBrowser.tsx', import.meta.url), {
    react: h.react, 'react-native': native, '../../../../../packages/i18n/src': i18n,
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({}), episodeLabel: () => undefined},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText: String},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion'}, './tv-title-focus': {tvEpisodeRegionMap},
    '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: any) => entry.id}, '../../kit/cards': {EpisodeTile: 'EpisodeTile'},
    '../../kit/overlays': {Menu: 'Menu'}, '../../kit/browse/presentation': type, '../../kit/theme': {useTheme: () => theme},
  });
  const props: any = {seasons: Array.from({length: 6}, (_, index) => ({id: `s${index + 1}`, title: `Season ${index + 1}`})), selectedSeasonId: 's1', episodes: [],
    focus: {id: 'title.episodes', edges: {up: 'title.actions', left: 'rail'}}, show: {title: 'Show'}, open: {}, text: {}, artwork() {},
    onSeasonFocus() {}, onSelectSeason: (id: string) => selected.push(id), onMoreSeasons: () => {tails++;}};
  const render = () => nodes(h.render(() => TVEpisodeBrowser(props)));
  let tree = render(), seasons = tree.filter(node => node.props.testID?.startsWith('tv-season-'));
  assert.equal(tree.find(node => node.props.id === 'title.episodes.seasons').props.edges.up, 'title.actions');
  seasons[0].props.onFocus(); seasons[0].props.onPress(); assert.deepEqual(selected, []); assert.equal(tails, 0);
  seasons[1].props.onFocus(); seasons[1].props.onPress(); assert.deepEqual(selected, ['s2']);
  seasons[2].props.onPress(); assert.deepEqual(selected, ['s2', 's3'], 'accessibility activation without focus selects the requested season');
  seasons[4].props.onFocus(); assert.equal(tails, 1, 'near-tail focus requests actual more-seasons');
  props.requestedSeasonId = 's5'; render(); props.requestedSeasonId = undefined; render();
  seasons = render().filter(node => node.props.testID?.startsWith('tv-season-'));
  seasons[4].props.onPress(); assert.deepEqual(selected, ['s2', 's3', 's5', 's5'], 'a failed request can be deliberately retried after its route witness retires');
});

test('a focused empty credits host advances cast to crew, stops on blur/loading, and leaves failures for Retry', async () => {
  const h = hooks(), effects: (() => void)[] = [], react: any = {...h.react}; react.default = react;
  react.useEffect = (effect: () => void, deps: any[]) => {const before = h.react.useRef(undefined); if (!before.current || deps.some((value, index) => value !== before.current[index])) {before.current = deps; effects.push(effect);}};
  const {TitlePeople} = await screenModule(new URL('../src-next/screens/watch/TitlePeople.tsx', import.meta.url), {
    react, 'react-native': native, '../../kit/artwork': {Artwork: 'Artwork', PersonCircle: 'PersonCircle'},
    '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (entry: any) => entry.id}, '../../tv/layout/TVArtFocus': {TVArtFocus: 'TVArtFocus'},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/states': {SkeletonBlock: 'SkeletonBlock'},
    '../../../../../packages/i18n/src': i18n, '../../kit/theme': {useTheme: () => theme, fontFamilies: {}, nativeType: () => ({})},
  });
  let requests = 0, paging: any = {hasMore: true, loading: false, waitingId: 'actual-cast-first', requestNext: () => {requests++;}};
  const render = () => {const tree = h.render(() => TitlePeople({credits: [], artwork() {}, focus: {id: 'credits'}, paging})); effects.splice(0).forEach(effect => effect()); return nodes(tree);};
  let empty = render().find(node => node.props.testID === 'watch-credits-empty-pending'); assert.equal(requests, 0);
  empty.props.onFocus(); assert.equal(requests, 1);
  paging = {...paging, loading: true}; render(); assert.equal(requests, 1);
  paging = {...paging, loading: false, waitingId: 'actual-crew-first'}; empty = render().find(node => node.props.testID === 'watch-credits-empty-pending'); assert.equal(requests, 2);
  paging = {...paging, failed: true}; empty = render().find(node => node.props.testID === 'watch-credits-empty-pending'); assert.equal(requests, 2);
  assert.equal(empty.props.accessibilityLabel, 'action.tryAgain'); empty.props.onPress(); assert.equal(requests, 3);
  empty.props.onBlur(); paging = {...paging, failed: false, waitingId: 'actual-crew-next'}; render(); assert.equal(requests, 3);
});

test('TV saved-name form uses the remote keyboard, authored panel size, trim/bounds, and locked Retry values', async () => {
  const h = hooks(), react: any = {...h.react, useId: () => 'form-instance'}; react.default = react;
  const focused: any[] = [], saved: any[] = [];
  const {SavedNameSheet} = await screenModule(new URL('../src-next/screens/saved/SavedForms.tsx', import.meta.url), {
    react, 'react-native': native, '../../../../../packages/i18n/src': i18n,
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({focus: (address: any) => focused.push(address)})},
    '../../kit/browse/presentation': type, '../search/TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'},
    '../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet', Dialog: 'Dialog', SidePanel: 'SidePanel'}, '../../kit/theme': {useTheme: () => theme},
  });
  const props: any = {open: true, title: 'Name', action: 'Save', initialName: '', withSummary: true, withVisibility: true, busy: false, testID: 'create', onClose() {}, onSave: (input: any) => saved.push(input)};
  const render = () => h.render(() => {const branch = SavedNameSheet(props); return branch.type(branch.props);});
  let tree = render(); assert.equal(tree.type, 'SidePanel'); assert.equal(tree.props.width, 680);
  assert.deepEqual(tree.props.style, {paddingTop: 42, paddingLeft: 56, paddingRight: 96, paddingBottom: 56});
  assert.equal(nodes(tree).some(node => node.type === 'TextField'), false);
  let keyboard = nodes(tree).find(node => node.type === 'NativeTVKeyboard'); assert.equal(keyboard.props.width, 500); assert.equal(keyboard.props.columns, 9);
  tree.props.initialFocusRef.current.requestTVFocus(); assert.deepEqual(focused[0], {regionId: 'create.form-instance.keyboard.row.0', childId: 'a'});
  keyboard.props.onChangeText('  Real collection  '); tree = render();
  nodes(tree.props.footer).find(node => node.props.focusId === 'save').props.onPress();
  assert.deepEqual(saved, [{name: 'Real collection', summary: '', visibility: 'private'}]);
  nodes(tree).find(node => node.props.focusId === 'summary').props.onFocus(); tree = render();
  nodes(tree).find(node => node.type === 'NativeTVKeyboard').props.onChangeText('x'.repeat(4010)); tree = render();
  assert.equal(nodes(tree).find(node => node.type === 'NativeTVKeyboard').props.value.length, 4000);
  props.locked = true; tree = render(); keyboard = nodes(tree).find(node => node.type === 'NativeTVKeyboard');
  assert.equal(keyboard.props.activation.visible, false); keyboard.props.onChangeText('wrong'); tree = render();
  assert.equal(nodes(tree).find(node => node.type === 'NativeTVKeyboard').props.value.length, 4000);
  tree.props.initialFocusRef.current.requestTVFocus(); assert.deepEqual(focused.at(-1), {regionId: 'create.form-instance.footer', childId: 'save'});
});

test('TV Search song results use full-width track rows and channels use factual wide cards', async () => {
  const h = hooks(), calls: any[] = [], key = (group: string, entry: any) => `${group}:${entry.id}`;
  const {SearchGridCard} = await screenModule(new URL('../src-next/screens/search/SearchGridCard.tsx', import.meta.url), {
    react: h.react, 'react-native': native, '../../kit/focus': {useFocusWaiting: () => false},
    '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard', CircleCard: 'CircleCard', EpisodeTile: 'EpisodeTile'},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/cards/primitives': {ArtFrame: 'ArtFrame', CardFrame: 'CardFrame'},
    '../../kit/browse': {SongRow: 'SongRow'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': type, '../../kit/theme': {useTheme: () => theme},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: (entry: any) => ({title: entry.title, caption: [], detail: []})},
    '../home/model': {homeCardText: () => ({caption: []})}, './model': {nativeSearchEntryKey: key},
  });
  const song = {id: 'track', kind: 'song', title: 'Real track', libraryId: 'music'}, runtime = {artwork: () => undefined, text: {}};
  const actions = {open() {}, play: (entry: any, group: string) => calls.push(['play', entry, group]), menu: (entry: any, group: string) => calls.push(['more', entry, group])};
  const tree = h.render(() => SearchGridCard({entry: song, group: 'songs', runtime, actions, width: 1044, index: 1, previousFocused: true, focusId: 'actual-track',
    selection: {active: true, selected: true, onEnter() {}, onToggle: () => calls.push(['toggle'])}}));
  const row = nodes(tree).find(node => node.type === 'SongRow'); assert.ok(row); assert.equal(tree.props.style.width, 1044);
  assert.equal(row.props.focusId, 'actual-track'); assert.equal(row.props.previousFocused, true); assert.equal(row.props.row.artwork.props.width, 56);
  row.props.onPlay(); row.props.onMore(); row.props.onSelectionChange(); assert.deepEqual(calls, [['play', song, 'songs'], ['more', song, 'songs'], ['toggle']]);
  const channel = h.render(() => SearchGridCard({entry: {id: 'channel', kind: 'channel', title: 'Real station'}, group: 'live-tv', runtime, actions, width: 400}));
  const frame = channel.type(channel.props), art = frame.props.children[0]({focused: false});
  assert.equal(art.type, 'ArtFrame'); assert.equal(art.props.width, 400); assert.equal(art.props.height, 225);
  assert.equal(nodes(art).find(node => node.type === 'Icon').props.name, 'i-tv');
  assert.deepEqual(nodes(art).filter(node => node.type === 'Text').flatMap(node => node.props.children), ['Real station']);
  const poster = h.render(() => SearchGridCard({entry: {id: 'movie', kind: 'movie', title: 'Real title'}, group: 'movies', runtime, actions, width: 196}));
  assert.equal(poster.props.titleLines, 2); assert.equal(poster.props.titleTextStyle.fontSize, 20); assert.equal(poster.props.captionTextStyle.fontSize, 18);
  const gridHost = hooks();
  const {TVSearchSection} = await screenModule(new URL('../src-next/screens/search/TVSearchSection.tsx', import.meta.url), {
    react: gridHost.react, 'react-native': native, '../../../../../packages/i18n/src': i18n,
    '../../../../../packages/client-core/src/presentation/search-model': {searchGroupLabel: (group: string) => group},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => ({})},
    '../../kit/focus/page-boundary': {useRowPageBoundary() {}}, '../../kit/states': {}, '../../kit/browse/presentation': type, '../../kit/theme': {useTheme: () => theme},
    '../../kit/layout/Row': {Row: 'Row'}, '../../tv/layout/TVLoadedGrid': {TVLoadedGrid: 'TVLoadedGrid'},
    './model': {nativeSearchEntryKey: key, nativeSearchTailKey: () => ''}, './SearchGridCard': {SearchGridCard: 'SearchGridCard'},
  });
  const section = gridHost.render(() => TVSearchSection({group: {id: 'songs', status: 'success', items: [song]}, enabled: true, singleRow: true, width: 1044, regionId: 'songs',
    runtime, actions, destination: {}, contentRoot: {current: null}}));
  const grid = nodes(section).find(node => node.type === 'TVLoadedGrid'); assert.ok(grid, 'All-mode songs use vertical rows too');
  assert.deepEqual(section.props.grid, {columns: 1, count: 1}); assert.equal(grid.props.itemWidth, 1044); assert.equal(grid.props.itemHeight, 84); assert.equal(grid.props.rowGap, 0);
});

test('artist Top songs has real vertical exits and Play entry; album geometry and complete Similar arrays remain reachable', async () => {
  const react: any = {createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, children}}),
    useSyncExternalStore: (_subscribe: any, get: any) => get(), useMemo: (read: any) => read(), useState: () => [undefined, () => {}], useRef: () => ({current: null})}; react.default = react;
  const {TVAlbumScreen, TVArtistScreen} = await screenModule(new URL('../src-next/screens/destinations/music/TVMusicScreens.tsx', import.meta.url), {
    '../../../kit/states': {SkeletonBlock: () => null},
    react, 'react-native': native, '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, number: String}},
    '../../../../../../packages/client-core/src/presentation/kit-model': {cardText: (entry: any) => ({title: entry.title, caption: [], detail: []})},
    '../../../../../../packages/client-core/src/presentation/library-tabs': {artistReleaseGroups: (albums: any[]) => [{id: 'albums', label: 'Albums', entries: albums}]},
    '../../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork', ArtistCircle: 'ArtistCircle'}, '../../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable'},
    '../../../kit/cards': {SquareCard: 'SquareCard'}, '../../../kit/browse': {TrackRow: 'TrackRow'}, '../../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../../kit/browse/presentation': type, '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/theme': {useTheme: () => theme},
    '../../../tv/layout/TVShelf': {TVShelf: 'TVShelf'}, '../../../tv/layout/TVArtFocus': {TVArtFocus: 'TVArtFocus'},
    './MusicErrorView': {MusicErrorView: 'MusicErrorView'}, './MusicSectionControls': {MusicSectionControls: 'MusicSectionControls', musicNearEnd: () => false}, './MusicFormatBadges': {MusicFormatBadges: 'MusicFormatBadges'},
    './MusicWindow': {MusicScrollView: 'MusicScrollView', MusicWindow: 'MusicWindow'},
  });
  const song = {id: 'track', kind: 'song', title: 'Track'}, album = {id: 'release', title: 'Release'}, entry = {id: 'entity', kind: 'artist', title: 'Artist'};
  let state: any = {songs: [song], albums: [album], similar: Array.from({length: 12}, (_, index) => ({id: `similar-${index}`, title: 'Similar', kind: 'artist'})),
    allSongs: {songs: [], phase: 'idle'}, songPage: {offset: 0}, releases: {}};
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, similarPage: () => undefined}, runtime = {ambient: () => ({kind: 'palette'}), artwork: () => ({kind: 'image'}), text: {}}, actions = {isMenuOpen: () => false};
  let tree = TVArtistScreen({controller, entry, runtime, actions, openEntry() {}}), all = nodes(tree);
  const top = all.find(node => node.props.id === 'music-artist-entity.songs');
  assert.deepEqual(top.props.edges, {up: 'music-play-entity.actions', down: 'music-artist-entity.rest', right: 'music-artist-entity.albums'});
  const actionNode = all.find(node => node.type?.name === 'ActionBar'), action = actionNode.type(actionNode.props);
  assert.equal(action.type, 'FocusRegion'); assert.equal(action.props.edges.down, top.props.id);
  const play = nodes(action).find(node => node.props.testID === 'music-play-entity'); assert.equal(play.props.hasTVPreferredFocus, true);
  assert.equal(all.find(node => node.type === 'TVShelf').props.items.length, 12);
  state = {...state, albums: []}; tree = TVArtistScreen({controller, entry, runtime, actions, openEntry() {}});
  assert.equal(nodes(tree).find(node => node.props.id === 'music-artist-entity.songs').props.edges.right, undefined);
  state = {...state, similar: state.similar.map((value: any) => ({...value, kind: 'album'}))};
  tree = TVAlbumScreen({controller, entry: {...entry, kind: 'album', artist: {id: 'artist', name: 'Artist'}}, runtime, actions, openEntry() {}}); all = nodes(tree);
  assert.equal(all.find(node => node.props.style?.left === 742).props.style.width, 1082);
  assert.equal(all.find(node => node.props.style?.left === 200 && node.props.style?.width === 480).props.style.boxShadow[0].blurRadius, 120);
  assert.equal(all.find(node => node.type === 'ArtistCircle').props.size, 44);
  assert.equal(all.find(node => node.type === 'TVShelf').props.items.length, 12);
  const albumActions = all.find(node => node.type?.name === 'ActionBar');
  assert.equal(albumActions.type(albumActions.props).type, 'View', 'artist focus graph must not introduce an edgeless album action boundary');
  // LR03/LR04 TV: the album's songs mount only the rows in view; Top songs (a declared focus order) stays whole.
  const albumSongs = all.find(node => node.type?.name === 'TrackList');
  assert.equal(albumSongs.props.windowed, true);
  const albumWindow = nodes(albumSongs.type(albumSongs.props)).find(node => node.type === 'MusicWindow');
  assert.equal(albumWindow.props.itemHeight, 84); assert.equal(albumWindow.props.items.length, 1);
  state = {...state, albums: [album]};
  const artistSongs = nodes(TVArtistScreen({controller, entry, runtime, actions, openEntry() {}})).find(node => node.type?.name === 'TrackList');
  assert.ok(!artistSongs.props.windowed);
  assert.equal(nodes(artistSongs.type(artistSongs.props)).some(node => node.type === 'MusicWindow'), false);
  const backdrop = all.find(node => node.type?.name === 'Backdrop');
  assert.equal(nodes(backdrop.type(backdrop.props)).find(node => node.type === 'AmbientArt').props.treatment, 'music');
});
