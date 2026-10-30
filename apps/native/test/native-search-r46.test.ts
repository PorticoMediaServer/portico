import test from 'node:test';
import assert from 'node:assert/strict';
import {createI18n, defaultI18n, resolveRegion} from '../../../packages/i18n/src';
import {channelTint} from '../../../packages/client-core/src/presentation/kit-model';
import {SEARCH_CHIPS, orderedSearchGroups, searchGroupLabel, topResultKindLabel, topSearchResult} from '../../../packages/client-core/src/presentation/search-model';
import {browseType} from '../src-next/kit/browse/presentation';
import {createTheme} from '../src-next/kit/theme';
import {homeCardText} from '../src-next/screens/home/model';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles';
import {nativeSearchEntryKey} from '../src-next/screens/search/model';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const flat = (node: any) => Array.isArray(node) ? node : nodes(node);
const texts = (node: any) => flat(node).filter(n => n.type === 'Text');
const label = (node: any) => Array.isArray(node.props.children) ? node.props.children[0] : node.props.children;
const flatten = (style: any): Record<string, unknown> => Array.isArray(style) ? Object.assign({}, ...style.filter(Boolean).map(flatten)) : style ?? {};
const state = (node: any) => ({focused: false, hovered: false, pressed: false, holding: false});
// JSX always hands children through the createElement rest argument, so a lone render prop arrives in an array.
const childFn = (node: any) => Array.isArray(node.props.children) ? node.props.children[0] : node.props.children;

const movie = {id: 'movie-1', kind: 'movie', title: 'Avengers: Endgame', displayTitle: 'Avengers: Endgame', year: 2019, duration: 10860, posterUrl: '/poster/endgame', playback: {itemId: 'movie-1'}, navigation: {view: 'item', entityId: 'movie-1'}};
const show = {id: 'show-1', kind: 'show', title: 'Game of Thrones', year: 2011, seasonCount: 1, backdropUrl: '/backdrop/got', posterUrl: '/poster/got', playback: {itemId: 'show-1'}, navigation: {view: 'item', entityId: 'show-1'}};
const episode = {id: 'episode-1', kind: 'episode', title: "It's Like a Game", show: {id: 'show-2', name: 'Solo Leveling'}, seasonNumber: 1, episodeNumber: 3, stillUrl: '/still/game', navigation: {view: 'item', entityId: 'show-2'}};
const song = {id: 'song-1', kind: 'song', title: 'The Game of Love', artist: {id: 'artist-1', name: 'Daft Punk'}, album: {id: 'album-1', name: 'Random Access Memories'}, posterUrl: '/cover/ram', navigation: {view: 'item', entityId: 'song-1'}};
const book = {id: 'book-1', kind: 'book', title: 'The Hunger Games: Special Edition', author: 'Suzanne Collins', posterUrl: '/cover/hunger', navigation: {view: 'item', entityId: 'book-1'}};

const group = (id: any, items: readonly any[]) => ({id, title: id, entityKind: 'item', status: 'success', errorCode: '', items, totalCount: items.length, totalCountExact: true, hasMore: false, nextCursor: ''});
const capabilities = {groups: SEARCH_CHIPS.map(chip => ({id: chip.id, title: chip.id, entityKind: 'item', available: true, reason: '', sorts: []})), sorts: [], directions: [], maxLimit: 40, groupBudgetMs: 600};

const genres = [
  {id: 'animation', name: 'Animation', count: 12, coverPaths: ['/animation/1', '/animation/2']},
  {id: 'sci-fi', name: 'Sci-Fi & Fantasy', count: 9, coverPaths: ['/scifi/1', '/scifi/2']},
  {id: 'drama', name: 'Drama', count: 8, coverPaths: ['/drama/1', '/drama/2']},
  {id: 'comedy', name: 'Comedy', count: 7, coverPaths: ['/comedy/1', '/comedy/2']},
  {id: 'crime', name: 'Crime', count: 6, coverPaths: ['/crime/1', '/crime/2']},
  {id: 'action', name: 'Action & Adventure', count: 5, coverPaths: ['/action/1', '/action/2']},
  // Ruling 36.4: a genre without the two covers its phone tile needs is skipped.
  {id: 'western', name: 'Western', count: 1, coverPaths: ['/western/1']},
];

async function mount(snapshot: any, landing: any, text = defaultI18n) {
  const host = hooks();
  (host.react as any).useId = () => 'search-r46';
  const theme = createTheme({width: 390, isTV: false});
  const calls: unknown[] = [];
  const view = await screenModule(new URL('../src-next/screens/search/SearchScreen.tsx', import.meta.url), {
    react: host.react,
    'react-native': {FlatList: 'FlatList', Keyboard: {dismiss: () => {}}, ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47, bottom: 34, left: 0, right: 0})},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/presentation/search-model': {orderedSearchGroups, SEARCH_CHIPS, searchGroupLabel, topSearchResult, topResultKindLabel},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/cards': {GenreTile: 'GenreTile'}, '../../kit/cards/primitives': {CardFrame: 'CardFrame'},
    '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable', TextField: 'TextField'}, '../../kit/selection': {SelectionCircle: 'SelectionCircle'},
    '../../kit/overlays': {Banner: 'Banner'}, '../../kit/states': {GridSkeleton: 'GridSkeleton', StateView: 'StateView'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType}, '../../kit/theme': {useTheme: () => theme},
    '../home/model': {homeCardText}, './model': {NativeSearchDestination: class {}, nativeSearchEntryKey}, './genre-tiles': {nativeSearchGenreTiles},
    './SearchGridCard': {SearchGridCard: 'SearchGridCard'}, './TVSearchScreen': {TVSearchScreen: 'TVSearchScreen'},
    '../../../../../packages/client-core/src/presentation/kit-model': {channelTint},
  });
  const tree = host.render(() => view.SearchScreen({
    destination: {subscribe: () => () => {}, getSnapshot: () => snapshot, refreshHistory: async () => {}, clearHistory: async () => calls.push('clear'), setText: () => {},
      selectGroup: () => {}, commit: async () => {}, useRecent: () => calls.push('recent'), next: async () => {}, retry: async () => {}},
    runtime: {artwork: (path: string) => ({uri: path, cacheKey: path}), text, reportError: () => {}, error: () => ({title: 'error'}),
      registerScrollToTop: () => () => {}},
    explore: {subscribe: () => () => {}, getSnapshot: () => landing, load: async () => {}, openGenre: (genre: any) => calls.push(genre.id)},
    actions: {open: () => {}, play: () => {}, menu: () => {}}, activation: {subscribe: () => () => {}, getSnapshot: () => ({visible: true, intent: 'enter', revision: 1})},
    tvCopy: {preferToTalk: 'next.tv.preferToTalk', voiceHint: 'next.tv.voiceHint'},
  }));
  // The screen composes its sections from local components; render one exactly as the screen created it.
  const paint = (node: any) => nodes(host.render(() => node.type(node.props)));
  const find = (match: (props: any) => boolean) => nodes(tree).find(node => typeof node.type === 'function' && match(node.props));
  return {tree, theme, calls, paint, find};
}

const idle = {generation: 1, scope: {}, query: null, phase: 'idle', data: null, groups: [], pagination: {cursor: null, canPrevious: false}, error: null};

test('phone Search draws the frame 7 empty state: 32pt title, bare 44px recent rows, six two-cover genre tiles', async () => {
  const snapshot = {text: '', group: 'all', search: idle, groups: [], clearing: false,
    recent: [{q: 'slow horses', updatedAt: '2026-10-24T09:00:00Z', uses: 2}, {q: 'tolkien', updatedAt: '2026-10-24T08:00:00Z', uses: 1}, {q: 'fleetwood mac', updatedAt: '2026-10-24T07:00:00Z', uses: 1}]};
  const {tree, paint, find} = await mount(snapshot, {phase: 'ready', genres, initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'});

  // .lt: 32/800 with the drawn 1.15 line box and 12pt gap under it.
  const heading = texts(tree).find(node => label(node) === defaultI18n.t('web.shell.search'))!;
  const headingStyle = flatten(heading.props.style);
  assert.equal(heading.props.accessibilityRole, 'header');
  assert.equal(headingStyle.fontSize, 32);
  assert.equal(headingStyle.lineHeight, 36.8);
  assert.equal(typeof headingStyle.letterSpacing, 'number');
  assert.equal(Number(Number(headingStyle.letterSpacing).toFixed(2)), -1.12);
  assert.equal(headingStyle.marginBottom, 12);
  assert.equal(headingStyle.marginHorizontal, 20);

  // .shead: 19/700 "Recent searches" beside Clear, on the drawn 24/20/6 box.
  const recent = paint(find(props => props.title === defaultI18n.t('web.search.recent'))!);
  const recentTitle = texts(recent).find(node => label(node) === defaultI18n.t('web.search.recent'))!;
  assert.equal(flatten(recentTitle.props.style).fontSize, 19);
  const recentBox = flatten(nodes(recent).find(node => node.type === 'View').props.style);
  assert.deepEqual([recentBox.marginTop, recentBox.marginBottom, recentBox.marginHorizontal], [24, 6, 20]);
  assert.ok(nodes(recent).some(node => node.props.accessibilityLabel === defaultI18n.t('web.search.clearRecent')));

  // .rs: bare 44px rows on ink, clock first and the query filling the row. No raised card, no invented control.
  const rows = nodes(tree).filter(node => typeof node.props.testID === 'string' && node.props.testID.startsWith('search-recent-'));
  assert.deepEqual(rows.map(row => row.props.accessibilityLabel), ['slow horses', 'tolkien', 'fleetwood mac']);
  for (const row of rows) {
    const style = flatten(row.props.style);
    assert.equal(style.height, 44);
    assert.equal(style.paddingHorizontal, 20);
    assert.equal(style.borderRadius, undefined);
    assert.equal(style.backgroundColor, undefined);
    assert.deepEqual(nodes(row).filter(n => n.type === 'Icon').map(n => n.props.name), ['i-clock']);
  }

  // .gtiles/.gt: the six drawn tiles, each two covers at 174 x 84 under ruling 36's facet tint.
  assert.ok(find(props => props.title === defaultI18n.t('next.search.browseByGenre')), 'the untyped empty state heads "Browse by genre"');
  assert.equal(find(props => props.title === defaultI18n.t('next.search.browseInstead')), undefined);
  for (const genre of genres.filter(item => item.coverPaths.length === 2)) {
    const card = nodes(paint(find(props => props.genre === genre)!)).find(node => node.type === 'GenreTile')!;
    assert.equal(card.props.title, genre.name);
    assert.equal(card.props.width, 174);
    assert.equal(card.props.variant, 'video');
    assert.deepEqual(card.props.covers.map((source: any) => source.uri), genre.coverPaths);
    assert.equal(card.props.caption, undefined, 'no count line is drawn on the phone tile');
    assert.deepEqual(card.props.palette, {first: channelTint(genre.id), second: channelTint(genre.id)});
  }
  assert.ok(!nodes(tree).some(node => node.props.genre?.id === 'western'), 'a genre without its two covers is skipped');
  assert.ok(!nodes(tree).some(node => node.type === 'Chip'), 'chips stay hidden before a query');
});

test('typed no matches keeps state 17a four backdrop tiles, filtering missing art before its prefix', async () => {
  const groups = [group('movies', []), group('shows', [])];
  const snapshot = {text: 'casablanca', group: 'all', search: {...idle, phase: 'ready', groups,
    data: {groups, capabilities}, query: {q: 'casablanca'}}, groups, recent: [], clearing: false};
  const missing = {id: 'covers-only', name: 'Covers only', count: 99, coverPaths: ['/covers/a', '/covers/b']};
  const backdrops = ['crime', 'drama', 'comedy', 'animation', 'next'].map((id, index) => ({
    id, name: id, count: 11 + index, coverPaths: [], backdropPath: `/backdrops/${id}`,
  }));
  const {tree, paint, find, calls} = await mount(snapshot, {phase: 'ready', genres: [missing], backdropGenres: [missing, ...backdrops],
    initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'});
  assert.ok(nodes(tree).some(node => node.props.testID === 'search-no-matches'));
  assert.ok(find(props => props.title === defaultI18n.t('next.search.browseInstead')));
  const tiles = nodes(tree).filter(node => typeof node.type === 'function' && node.props.genre);
  assert.deepEqual(tiles.map(node => node.props.genre.id), backdrops.slice(0, 4).map(genre => genre.id));
  for (const [index, tile] of tiles.entries()) {
    const rendered = paint(tile);
    assert.equal(rendered.some(node => node.type === 'GenreTile'), false, '17a does not substitute the initial two-cover tile');
    const art = rendered.find(node => node.type === 'Artwork')!;
    assert.equal(art.props.source.uri, backdrops[index]!.backdropPath);
    assert.equal(art.props.height, 84);
    assert.ok(texts(rendered).some(node => label(node) === defaultI18n.t('next.selection.titles', {count: backdrops[index]!.count})));
    rendered.find(node => node.props.testID === `search-genre-${backdrops[index]!.id}`)!.props.onPress();
  }
  assert.deepEqual(calls, backdrops.slice(0, 4).map(genre => genre.id), 'each tile keeps its actual producer entry action');
});

test('phone Search draws the frame 8 results: counted type chips, the 178px top result and per-type row shapes', async () => {
  // The answer reports every requested group, including the ones that matched nothing.
  const groups = [group('movies', [movie]), group('shows', [show]), group('episodes', [episode]), group('songs', [song]), group('books', [book]),
    group('people', []), group('artists', []), group('albums', []), group('reading', []), group('live-tv', [])];
  const snapshot = {text: 'game', group: 'all', search: {generation: 2, scope: {}, query: {q: 'game'}, phase: 'ready', groups,
    data: {scope: {}, revision: {catalog: 1, viewer: 1}, heading: {key: '', fallback: ''}, query: {q: 'game', sort: 'relevance', direction: 'desc', group: '', groups: [], libraryIds: [], limit: 12, searchMode: 'normalized_prefix', recorded: false},
      groups, capabilities, empty: undefined}, pagination: {cursor: null, canPrevious: false}, error: null},
    groups, recent: [], clearing: false};
  const {tree, theme, paint, find} = await mount(snapshot, {phase: 'ready', genres: [], initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'});

  // .hs/.chip: only the groups that answered with results carry a chip, each with its exact count.
  const chips = nodes(tree).filter(node => node.type === 'Chip');
  assert.deepEqual(chips.map(chip => label(chip)), ['All', 'Movies', 'Shows', 'Episodes', 'Songs', 'Audiobooks']);
  assert.deepEqual(chips.map(chip => chip.props.count), [5, 1, 1, 1, 1, 1]);
  assert.deepEqual(chips.map(chip => chip.props.selected), [true, false, false, false, false, false]);
  assert.ok(chips.every(chip => chip.props.retry === false));

  // .topr: 14/16 margins, 178 tall, radius 14, ink-black base, one exact-match title.
  const banner = paint(find(props => props.entry === show && props.width !== undefined)!);
  const top = nodes(banner).find(node => node.props.testID === 'search-top-result')!;
  const topStyle = flatten(top.props.pressableStyle);
  assert.deepEqual([topStyle.height, topStyle.borderRadius, topStyle.backgroundColor], [178, 14, theme.colors.dim]);
  assert.deepEqual([flatten(top.props.style).marginTop, flatten(top.props.style).marginHorizontal], [14, 16]);
  const inner = nodes(childFn(top)(state(top)));
  assert.equal(inner.find(node => node.type === 'Artwork')!.props.source.uri, '/backdrop/got');
  assert.ok(texts(inner).some(node => label(node) === defaultI18n.t('search.topResult')));
  assert.ok(texts(inner).some(node => label(node) === 'Game of Thrones'));
  assert.ok(texts(inner).some(node => label(node) === defaultI18n.t(topResultKindLabel('shows')!)));
  const play = inner.find(node => node.props.testID === 'search-top-play')!;
  const playStyle = flatten(play.props.style);
  assert.deepEqual([playStyle.width, playStyle.height, playStyle.borderRadius, playStyle.backgroundColor], [46, 46, 23, theme.colors.text]);

  // .rr: one 46x69 poster, one 100x56 wide still, square art for songs and audiobooks, canonical captions.
  const shapes: Record<string, [any, number, number]> = {movies: [movie, 46, 69], episodes: [episode, 100, 56], songs: [song, 56, 56], books: [book, 56, 56]};
  const env = {t: defaultI18n.t};
  for (const [id, [entry, width, height]] of Object.entries(shapes)) {
    const row = paint(find(props => props.entry === entry && props.group === id)!);
    const frame = nodes(row).find(node => node.props.testID === `search-result-${nativeSearchEntryKey(id as any, entry)}`)!;
    assert.ok(frame, `${id} row is drawn`);
    const body = nodes(childFn(frame)(state(frame)));
    const artStyle = flatten(body.find(node => node.type === 'View' && flatten(node.props.style).overflow === 'hidden')!.props.style);
    assert.deepEqual([artStyle.width, artStyle.height, artStyle.borderRadius], [width, height, 6], `${id} keeps its drawn art slot`);
    const captions = texts(body).filter(node => flatten(node.props.style).fontSize === 12.5);
    assert.deepEqual(captions.map(node => label(node)), homeCardText(entry, false, env).caption.map(part => part.text));
  }
  // The Shows row is omitted: its only result already leads the page as the Top result.
  assert.equal(find(props => props.entry === show && props.width === undefined), undefined);
  for (const id of ['movies', 'episodes', 'songs', 'books'])
    assert.ok(texts(tree).some(node => label(node) === defaultI18n.t(searchGroupLabel(id as any))), `${id} keeps its drawn section heading`);
});


test('Search no-match genre counts use the actual runtime regional formatter', async () => {
  const groups = [group('movies', [])];
  const snapshot = {text: 'unmatched', group: 'all', search: {...idle, phase: 'ready', groups,
    data: {groups, capabilities}, query: {q: 'unmatched'}}, groups, recent: [], clearing: false};
  const text = createI18n(resolveRegion({locale: 'en-US', formats: 'de-DE'}, {locales: ['en-US']}));
  const genre = {id: 'actual-metadata-value', name: 'Stored metadata label', count: 1234, coverPaths: [], backdropPath: '/actual/backdrop'};
  const {tree, paint} = await mount(snapshot, {phase: 'ready', genres: [], backdropGenres: [genre], initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'}, text);
  const tile = nodes(tree).find(node => typeof node.type === 'function' && node.props.genre === genre);
  assert.ok(tile);
  const count = text.t('next.selection.titles', {count: genre.count});
  assert.notEqual(count, defaultI18n.t('next.selection.titles', {count: genre.count}));
  assert.ok(texts(paint(tile)).some(node => label(node) === count));
});
