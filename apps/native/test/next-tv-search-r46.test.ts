import {retainRecognizerReturn} from '../src-next/kit/focus/recognizer-return.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {alpha, browseType} from '../src-next/kit/browse/presentation.ts';
import {createTheme} from '../src-next/kit/theme.ts';
import {nativeDestinationFocusIds} from '../src-next/screens/destinations/focus.ts';
import {applyNativeTVKeyboardKey, nativeTVKeyboardRows} from '../src-next/screens/search/keyboard.ts';
import {nativeSearchEntryKey} from '../src-next/screens/search/model.ts';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

/** TV Search r46: the drawn frames are tv.html#f5 (.sfield/.kb/.voice/.sres/.top-r/.meta/.trk)
 * and states.html#f18 (.sres h2/.lead/h3/.genres/.gtl). These pin the declared paint and
 * geometry the native stylesheet hands to the renderer, and the tv-focus.html rules
 * F-SEARCH-1/3/4/27/28/31/42 this screen owns. */
type Element = {type: any; props: Record<string, any>};
const elements = (node: any): Element[] => Array.isArray(node) ? node.flatMap(elements)
  : node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
const style = (value: any): Record<string, any> => Array.isArray(value) ? Object.assign({}, ...value.map(style)) : value ?? {};
const flatText = (value: any): string => Array.isArray(value) ? value.map(flatText).join('')
  : typeof value === 'string' || typeof value === 'number' ? String(value) : '';
const textOf = (node: any): string[] => elements(node).filter(child => child.type === 'Text').map(child => flatText(child.props.children));
const textNode = (tree: unknown, text: string) => elements(tree).find(node => node.type === 'Text' && flatText(node.props.children) === text);

const theme = createTheme({isTV: true, width: 1920}), colors = theme.colors;
const authority = {serverId: 'server-id', accountId: 'account-id', profileId: 'profile-id', epoch: 'epoch', viewerFence: 'home-fence'};
const ids = nativeDestinationFocusIds(authority);

const show = {id: 'show-1', kind: 'show' as const, libraryId: 'tv', title: 'The Last of Us', displayTitle: 'The Last of Us', year: 2023, backdropUrl: '/backdrop', posterUrl: '/poster'};
const movie = {id: 'movie-1', kind: 'movie' as const, libraryId: 'movies', title: 'The Lord of the Rings: The Fellowship of the Ring', displayTitle: 'The Lord of the Rings: The Fellowship of the Ring', year: 2001, posterUrl: '/lord'};
const movie2 = {id: 'movie-2', kind: 'movie' as const, libraryId: 'movies', title: 'Raiders of the Lost Ark', displayTitle: 'Raiders of the Lost Ark', year: 1981, posterUrl: '/raiders'};
const groups = [{id: 'movies' as const, status: 'success' as const, items: [movie, movie2], totalCount: 4, totalCountExact: true}];
const genre = (id: string, count: number) => ({id, name: id, count, backdropPath: `/genre/${id}`, coverPaths: [] as string[]});
const genres = [genre('Crime', 9), genre('Drama', 31), genre('Adventure', 18), genre('Animation', 14)];

let state: any, landing: any, top: any, modelProgress: number | undefined;
const entryRequests: any[] = [];

const reactNative = {ScrollView: 'ScrollView', View: 'View', Text: 'Text', useWindowDimensions: () => ({width: 1920, height: 1080})};
const presentation = {alpha, browseType};
const landingOf = (value: any) => ({subscribe() {}, getSnapshot: () => value});
const runtime = {getSnapshot: () => ({authority}), artwork: () => undefined, reportError: () => undefined};
const screenProps = () => ({destination: {subscribe() {}, getSnapshot: () => state, setText: () => {}},
  runtime, activation: {subscribe() {}, getSnapshot: () => ({visible: true, intent: 'enter' as const, revision: 1})},
  explore: landingOf(landing), actions: {}, copy: {preferToTalk: 'voice-title', voiceHint: 'voice-body'}});

async function screen(react: unknown) {
  return screenModule(new URL('../src-next/screens/search/TVSearchScreen.tsx', import.meta.url), {
    react, 'react-native': reactNative, 'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/client-core/src/library-content': {}, '../../../../../packages/client-core/src/search': {}, '../../shell/runtime': {}, './types': {},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText: () => ({id: 'text', title: 'text',
      caption: [{id: 'season-count', text: '1 season'}, {id: 'year', text: '2023'}], detail: [], progress: modelProgress})},
    '../../../../../packages/client-core/src/presentation/search-model': {orderedSearchGroups: (value: unknown) => value,
      SEARCH_CHIPS: [{id: 'all', label: 'search.chip.all'}, {id: 'movies', label: 'search.chip.movies'}], topSearchResult: () => top,
      searchGroupLabel: () => 'search.chip.movies', topResultKindLabel: () => 'search.kind.show'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable'},
    '../../kit/cards/primitives': {ArtFrame: 'ArtFrame', CardFrame: 'CardFrame', Parts: 'Parts', Progress: 'Progress'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => createFocusRegistry()},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/browse/presentation': presentation,
    '../../kit/theme': {useTheme: () => theme}, '../destinations/focus': {nativeDestinationFocusIds}, '../home/model': {homeSubject: (content: unknown) => content},
    './model': {nativeSearchEntryKey}, './TVSearchSection': {TVSearchSection: 'TVSearchSection'}, './SearchGridCard': {SearchGridCard: 'SearchGridCard'}, './TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'},
    '../settings-live/use-tv-voice-search': {useTVVoiceSearch: () => ({start: undefined, begin: undefined, busy: false})},
    '../../kit/focus/recognizer-return': {retainRecognizerReturn}, '../../shell/model': {sameAuthority}, './genre-tiles': {nativeSearchGenreTiles},
  });
}

test('the drawn field, keyboard column and results column are tv.html#f5 geometry and paint', async () => {
  const host = hooks(), {TVSearchScreen} = await screen({...host.react, useId: () => 'gradient'});
  state = {text: 'the l', group: 'all', groups, search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}, recent: []};
  landing = {phase: 'ready', genres, initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'}; top = undefined; modelProgress = undefined;
  const tree = host.render(() => TVSearchScreen(screenProps()));
  assert.equal(theme.gutter, 200);
  const field = style(elements(tree).find(node => node.props.accessibilityLabel === defaultI18n.t('web.shell.search'))!.props.style);
  const {backgroundColor, ...fieldGeometry} = field;
  assert.deepEqual(fieldGeometry, {position: 'absolute', left: 200, top: 64, width: 500, height: 80, borderRadius: 16,
    paddingHorizontal: 24, flexDirection: 'row', alignItems: 'center', gap: 16});
  assert.equal(backgroundColor, alpha(colors.text, .08));
  const typed = style(textNode(tree, 'the l')!.props.style);
  assert.deepEqual({fontSize: typed.fontSize, fontFamily: typed.fontFamily, letterSpacing: typed.letterSpacing}, browseType(theme, 34, 700));
  const caret = style(elements(tree).find(node => style(node.props.style).marginLeft === -12)!.props.style);
  assert.deepEqual(caret, {width: 3, height: 40, marginLeft: -12, borderRadius: 2, backgroundColor: colors.accentStrong});
  const keyboard = elements(tree).find(node => node.type === 'NativeTVKeyboard')!;
  assert.equal(keyboard.props.id, ids.searchKeyboard);
  assert.equal(keyboard.props.leftRegionId, ids.rail);
  assert.equal(keyboard.props.rightRegionId, ids.searchResults);
  const keyboardFrame = style(elements(tree).find(node => (Array.isArray(node.props.children) ? node.props.children : [node.props.children]).includes(keyboard))!.props.style);
  assert.deepEqual(keyboardFrame, {position: 'absolute', left: 200, top: 176});
  // The voice hint is its own drawn stop (.voice); the results keep the right column (.sres).
  const voice = elements(tree).find(node => node.type === 'FocusRegion' && node.props.id === `${ids.searchKeyboard}.voice`)!;
  assert.deepEqual(style(voice.props.style), {position: 'absolute', left: 200, top: 792, width: 500, flexDirection: 'row', alignItems: 'flex-start', gap: 18});
  const results = elements(tree).find(node => node.type === 'FocusRegion' && node.props.id === ids.searchResults)!;
  // The region reaches 40 px left of and above .sres and pads its content by the same, so the
  // drawn cards stay at x 780 / y 64 while a focused card's scale and ring are not clipped.
  assert.deepEqual(style(results.props.style), {position: 'absolute', left: 740, right: 0, top: 24, bottom: 0});
  assert.equal(results.props.entryRegion, ids.searchSection('movies'));
  assert.equal(results.props.edges.left, ids.searchKeyboardRow(0));
  const sections = elements(tree).filter(node => node.type === 'TVSearchSection');
  assert.equal(sections[0]!.props.group.items[0], movie);
  assert.equal(sections[0]!.props.width, 1044);
  assert.equal(sections[0]!.props.keyboardRegion, ids.searchKeyboardRow(0));
  // A title nobody has started draws no progress track (.mprog only rides a resume).
  assert.equal(elements(tree).find(node => node.type === 'Progress'), undefined);
});

test('the drawn top result keeps f5 meta at 24px with 26px gaps, season count as .tag and 160px progress', async () => {
  const host = hooks(), {TVSearchTopResult, TVSearchTag} = await screen({...host.react, useId: () => 'gradient'});
  modelProgress = .22; top = undefined;
  const card = host.render(() => TVSearchTopResult({entry: show, group: 'shows', runtime, actions: {}, disabled: false}));
  assert.deepEqual(style(card.props.style), {marginTop: 44, transformOrigin: 'left center'});
  assert.deepEqual(style(card.props.pressableStyle), {flexDirection: 'row', gap: 36, alignItems: 'center'});
  const renderRow = (Array.isArray(card.props.children) ? card.props.children[0] : card.props.children) as (state: any) => any;
  const row = host.render(() => renderRow({focused: false, hovered: false, pressed: false, holding: false}));
  const art = elements(row).find(node => node.type === 'ArtFrame')!;
  assert.deepEqual({width: art.props.width, height: art.props.height, radius: art.props.radius}, {width: 480, height: 270, radius: 14});
  const label = style(textNode(row, defaultI18n.t('search.topResult'))!.props.style);
  assert.deepEqual(label, {...browseType(theme, 20, 700), color: colors.accentStrong, marginBottom: 10});
  const title = style(elements(row).find(node => node.type === 'Text' && node.props.numberOfLines === 3)!.props.style);
  assert.deepEqual(title, {...browseType(theme, 52, 800, -.04, 52), color: colors.text, marginBottom: 14});
  assert.equal(title.lineHeight, 52);
  const meta = elements(row).find(node => style(node.props.style).rowGap === 10 && style(node.props.style).columnGap === 26)!;
  assert.deepEqual(style(meta.props.style), {flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 26, rowGap: 10, marginBottom: 14});
  const facts = elements(meta).filter(node => node.type === 'Text').map(node => ({text: flatText(node.props.children), style: style(node.props.style)}));
  assert.deepEqual(facts[0], {text: '2023', style: {...browseType(theme, 24, 700), color: colors.text}});
  assert.deepEqual(facts[1], {text: defaultI18n.t('next.search.tvShow'), style: {...browseType(theme, 24, 400), color: colors.textSecondary}});
  assert.equal(facts.filter(fact => fact.text === '2023').length, 1);
  // The season count is the drawn .tv .tag, drawn by the canonical tag spec.
  const tagElement = elements(meta).find(node => typeof node.type === 'function' && node.type.name === 'TVSearchTag')!;
  assert.equal(flatText(tagElement.props.children), '1 season');
  const tag = host.render(() => TVSearchTag({children: '1 season'}));
  assert.deepEqual(style(tag.props.style), {paddingHorizontal: 12, paddingVertical: 3, borderRadius: 8, borderWidth: 1.5, borderColor: alpha(colors.text, .28)});
  const tagText = style(elements(tag).find(node => node.type === 'Text')!.props.style);
  assert.deepEqual(tagText, {...theme.type.tag, color: colors.text});
  assert.equal(tagText.fontSize, 20);
  const bar = elements(row).find(node => style(node.props.style).width === 160)!;
  assert.deepEqual(style(bar.props.style), {width: 160});
  assert.equal(elements(row).find(node => node.type === 'Progress')!.props.value, .22);
  modelProgress = 0;
  const freshCard = host.render(() => TVSearchTopResult({entry: show, group: 'shows', runtime, actions: {}, disabled: false}));
  const renderFresh = (Array.isArray(freshCard.props.children) ? freshCard.props.children[0] : freshCard.props.children) as (state: any) => any;
  const fresh = host.render(() => renderFresh({focused: false, hovered: false, pressed: false, holding: false}));
  assert.equal(elements(fresh).find(node => node.type === 'Progress'), undefined);
});

test('the drawn empty page is states.html#f18 and its genre tiles keep the drawn ring outside the clip', async () => {
  const host = hooks(), {TVSearchScreen, TVSearchGenre} = await screen({...host.react, useId: () => 'gradient'});
  state = {text: 'casablanca', group: 'all', groups: [{id: 'movies' as const, status: 'success' as const, items: [], totalCount: 0, totalCountExact: true}],
    search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}, recent: [{q: 'severance'}]};
  landing = {phase: 'ready', genres, initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'}; top = undefined; modelProgress = undefined;
  const tree = host.render(() => TVSearchScreen(screenProps()));
  const noMatches = style(textNode(tree, defaultI18n.t('next.states.noMatches', {query: 'casablanca'}))!.props.style);
  assert.deepEqual(noMatches, {...browseType(theme, 52, 800, -.04, 54.6), color: colors.text, marginBottom: 14});
  const body = style(textNode(tree, defaultI18n.t('next.states.noMatchesBody'))!.props.style);
  assert.deepEqual(body, {...browseType(theme, 26, 400), color: colors.textSecondary, marginBottom: 54});
  const browse = style(textNode(tree, defaultI18n.t('next.search.browseInstead'))!.props.style);
  assert.deepEqual(browse, {...browseType(theme, 32, 700, -.019), color: colors.text, marginBottom: 22});
  // states.html#f18's .sres ends at --safe-r, so its two drawn columns are (1140 - 96 - 26) / 2 and keep the producer's order.
  // The harness evaluates each screen module once, so the drawn tile is identified by its compiled component.
  const tiles = elements(tree).filter(node => typeof node.type === 'function' && node.type.name === 'TVSearchGenre');
  assert.equal(typeof TVSearchGenre, 'function');
  assert.deepEqual(tiles.map(node => node.props.genre.id), ['Crime', 'Drama', 'Adventure', 'Animation']);
  assert.deepEqual(tiles.map(node => node.props.width), [509, 509, 509, 509]);
  const tile = host.render(() => TVSearchGenre({genre: genres[0], runtime, width: 509, disabled: false, onPress: () => {}}));
  assert.deepEqual(style(tile.props.style), {width: 509, height: 200, borderRadius: 16, backgroundColor: colors.raised});
  assert.equal(tile.props.focusRadius, 16);
  assert.equal(tile.props.accessibilityLabel, 'Crime');
  const clip = elements(tile).find(node => style(node.props.style).overflow === 'hidden')!;
  assert.deepEqual(style(clip.props.style), {position: 'absolute', top: 0, right: 0, bottom: 0, left: 0, borderRadius: 16, overflow: 'hidden'});
  const stops = elements(tile).filter(node => node.type === 'Stop');
  assert.deepEqual(stops.map(node => [node.props.offset, node.props.stopOpacity, node.props.stopColor]), [[ '0', .88, colors.ink], ['0.55', .5, colors.ink], ['1', .15, colors.ink]]);
  const copy = elements(tile).find(node => style(node.props.style).left === 30 && style(node.props.style).bottom === 26)!;
  const [name, count] = elements(copy).filter(node => node.type === 'Text');
  assert.deepEqual({text: flatText(name.props.children), style: style(name.props.style)}, {text: 'Crime', style: {...browseType(theme, 36, 800, -.025), color: colors.text}});
  assert.deepEqual({text: flatText(count.props.children), style: style(count.props.style)}, {text: defaultI18n.t('next.selection.titles', {count: 9}),
    style: {...browseType(theme, 20, 400), color: colors.textSecondary, marginTop: 4}});
  // f18 draws no voice hint and no type chips while the page has no matches; tv.html#f5 draws both with results.
  assert.equal(textOf(tree).includes('voice-title'), false);
  assert.equal(elements(tree).find(node => node.type === 'Chip'), undefined);
  assert.equal(textOf(tree).includes('severance'), false);
});

test('the drawn keyboard is f5 six-column grid with Space, Delete and Clear and exact key geometry', async () => {
  const host = hooks();
  const {NativeTVKeyboard} = await screenModule(new URL('../src-next/screens/search/TVKeyboard.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => createFocusRegistry()}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': presentation, '../../kit/theme': {useTheme: () => theme},
    '../destinations/useEntryFocus': {useDestinationEntryFocus: (_activation: unknown, address: unknown) => {entryRequests.push(address);}},
    '../destinations/focus': {}, './keyboard': {applyNativeTVKeyboardKey, nativeTVKeyboardRows},
  });
  const typed: string[] = [], rows: string[] = [], activation = {visible: true, intent: 'enter' as const, revision: 7};
  const tree = host.render(() => NativeTVKeyboard({id: ids.searchKeyboard, value: 'the l', onChangeText: (value: string) => typed.push(value),
    activation, leftRegionId: ids.rail, rightRegionId: ids.searchResults, onRowFocus: (row: string) => rows.push(row)}));
  assert.deepEqual(entryRequests, [{regionId: `${ids.searchKeyboard}.row.0`, childId: 'a'}]);
  const regions = elements(tree).filter(node => node.type === 'FocusRegion');
  assert.deepEqual(regions.map(node => node.props.id), [ids.searchKeyboard, ...[0, 1, 2, 3, 4, 5, 6].map(row => `${ids.searchKeyboard}.row.${row}`)]);
  const keys = elements(tree).filter(node => node.type === 'Focusable');
  assert.equal(keys.length, 39);
  assert.deepEqual(keys.slice(0, 6).map(key => key.props.focusId), ['a', 'b', 'c', 'd', 'e', 'f']);
  const key = (id: string) => keys.find(value => value.props.focusId === id)!;
  assert.deepEqual(style(key('a').props.style), {width: 75, height: 72, borderRadius: 14, backgroundColor: alpha(colors.text, .05), justifyContent: 'center', alignItems: 'center'});
  assert.deepEqual([key('space').props.style.width, key('delete').props.style.width, key('clear').props.style.width], [245, 75, 160]);
  const keyText = (id: string) => style(elements(key(id)).find(node => node.type === 'Text')!.props.style);
  assert.deepEqual(keyText('a'), {...browseType(theme, 28, 700), color: colors.textSecondary});
  assert.deepEqual(keyText('6'), {...browseType(theme, 28, 700), color: colors.textTertiary});
  assert.deepEqual(keyText('space'), {...browseType(theme, 22, 700), color: colors.textSecondary});
  assert.deepEqual(keyText('clear'), {...browseType(theme, 22, 700), color: colors.textSecondary});
  assert.equal(key('space').props.accessibilityLabel, defaultI18n.t('next.states.key.Space'));
  assert.equal(key('clear').props.accessibilityLabel, defaultI18n.t('filter.clear'));
  const backspace = elements(key('delete')).find(node => node.type === 'Icon')!;
  assert.deepEqual({name: backspace.props.name, size: backspace.props.size, color: backspace.props.color}, {name: 'i-backspace', size: 32, color: colors.textSecondary});
  key('space').props.onPress(); key('delete').props.onPress(); key('clear').props.onPress();
  assert.deepEqual(typed, ['the l ', 'the ', '']);
  // Row edges are the drawn ones: Left opens the rail, Right enters the results.
  const row = regions.find(node => node.props.id === `${ids.searchKeyboard}.row.1`)!;
  assert.deepEqual(row.props.edges, {left: ids.rail, right: ids.searchResults});
  assert.equal(row.props.entry, 'g');
  assert.equal(row.props.orientation, 'horizontal');
  assert.equal(row.props.screenStart.regionId, `${ids.searchKeyboard}.row.0`);
  assert.deepEqual(regions.find(node => node.props.id === `${ids.searchKeyboard}.row.6`)!.props.order, ['space', 'delete', 'clear']);
  // The screen is told which row holds focus, so its results can return to that row's remembered key.
  assert.deepEqual(rows, []);
  row.props.onFocusChange(true); row.props.onFocusChange(false);
  assert.deepEqual(rows, [`${ids.searchKeyboard}.row.1`]);
});

test('Left from a result returns to the last keyboard row, and that key returns Right to the same card', async () => {
  const host = hooks(), {TVSearchScreen} = await screen({...host.react, useId: () => 'gradient'});
  state = {text: 'the l', group: 'all', groups, search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}, recent: []};
  landing = {phase: 'ready', genres, initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'}; top = undefined; modelProgress = undefined;
  let tree = host.render(() => TVSearchScreen(screenProps()));
  const results = () => elements(tree).find(node => node.type === 'FocusRegion' && node.props.id === ids.searchResults)!;
  assert.equal(results().props.edges.left, ids.searchKeyboardRow(0));
  // F-SEARCH-4/27: the keyboard remembers its key, so the results point Left at the row that held it.
  elements(tree).find(node => node.type === 'NativeTVKeyboard')!.props.onRowFocus(`${ids.searchKeyboard}.row.1`);
  tree = host.render(() => TVSearchScreen(screenProps()));
  assert.equal(results().props.edges.left, `${ids.searchKeyboard}.row.1`);
  // F-SEARCH-28: the results region remembers its own element, so Right from that key returns to it.
  assert.equal(results().props.entryRegion, ids.searchSection('movies'));
  assert.equal(results().props.entryPolicy, undefined);
});

test('result chips keep their natural width so no label is ellipsised or blanked', async () => {
  const host = hooks(), {TVSearchScreen} = await screen({...host.react, useId: () => 'gradient'});
  state = {text: 'the l', group: 'all', groups, search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}, recent: []};
  landing = {phase: 'ready', genres, initialHeading: 'next.search.browseByGenre', emptyHeading: 'next.search.browseInstead'}; top = undefined; modelProgress = undefined;
  const tree = host.render(() => TVSearchScreen(screenProps()));
  const chips = elements(tree).filter(node => node.type === 'Chip');
  assert.ok(chips.length >= 2);
  for (const chip of chips) {
    assert.equal(style(chip.props.style).flexShrink, 0);
    assert.equal(style(chip.props.textStyle).flexShrink, 0);
  }
  const row = elements(tree).find(node => node.type === 'FocusRegion' && node.props.id === ids.searchChips)!;
  assert.equal(style(row.props.style).flexWrap, 'wrap');
});
