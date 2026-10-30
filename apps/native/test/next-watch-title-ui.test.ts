import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {cardText, episodeLabel, playbackIdentity, titleFactParts} from '../../../packages/client-core/src/presentation/kit-model/facts.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {tvTitleRegionMap} from '../src-next/screens/watch/tv-title-focus.ts';

const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : node?.props ? [node, ...descendants(node.props.children)] : [];
const style = (node: any) => Object.assign({}, ...[node.props.style].flat(Infinity).filter(Boolean));
const find = (tree: any, id: string) => descendants(tree).find(node => node.props.testID === id || node.props.focusId === id);
const t = (id: string, values?: any) => id === 'next.kit.duration.minutes' ? `${values.minutes} min` : id;

async function fixture() {
  const host = hooks();
  const react = {...host.react, useLayoutEffect() {}, createContext: (value: unknown) => ({_currentValue: value})};
  const fontFamilies = {regular: 'Manrope-Regular', medium: 'Manrope-Medium', semibold: 'Manrope-SemiBold', bold: 'Manrope-Bold', extraBold: 'Manrope-ExtraBold'};
  const {nativeType} = await screenModule(new URL('../src-next/kit/theme.ts', import.meta.url), {
    react: {...react, default: react}, 'react-native': {}, '@portico/design': {typography: fontFamilies},
  });
  const viewport = {width: 390, height: 844}, insets = {top: 50, bottom: 34, left: 0, right: 0};
  const theme = {isTV: false, platform: 'phone', textScale: 1, colors: {ink: '#071018', text: '#edf5fa', textSecondary: '#bdccd6', textTertiary: '#96a9b5', line: '#28404f', lineStrong: '#3b5668'},
    type: {body: {}, bodyStrong: {}, heading: {}, caption: {}, captionStrong: {}, meta: {}, lockup: {}}};
  const entry = {id: 'film', kind: 'movie', libraryId: 'library', title: 'A film title', overview: 'The actual synopsis.', year: 2026, duration: 5400};
  const snapshot: any = {retired: false, entry, place: {tab: 'related', anchor: {itemId: null, offset: 0}}, seasons: [], episodes: [],
    detail: {phase: 'ready', data: {item: entry, facts: {contentRating: 'PG'}, personal: {watchlisted: false, watched: false},
      actions: [{id: 'play', enabled: true, playback: {itemId: entry.id}}, {id: 'watchlist', enabled: true}, {id: 'watched', enabled: true}],
      metadata: {credits: [], genres: [], ratings: []}, related: {rows: [{id: 'related', heading: 'Related', entries: [
        {id: 'one', kind: 'movie', title: 'One', year: 2001}, {id: 'two', kind: 'movie', title: 'Two'},
      ]}]}}}};
  const calls: any[] = [];
  const controller = {subscribe: () => () => {}, getSnapshot: () => snapshot, memory: {getSnapshot: () => snapshot.place},
    episode: {subscribe: () => () => {}, getSnapshot: () => ({phase: 'idle'}), close() {}}, pageLease: () => undefined,
    open: async () => {}, play: async () => {calls.push('play');}, personal: async () => {}, remember() {}, moreEpisodes: async () => {}};
  const modules = {
    '../../kit/useMotion': {useMotion: () => ({reduce: false})},
    './TVTitleChrome': {TVTitleBackdrop: 'TVTitleBackdrop', TVTitleCompactHeader: 'TVTitleCompactHeader', TVTitleHeroCopy: 'TVTitleHeroCopy'},
    react: {...react, default: react}, 'react-native': {View: 'View', Text: 'Text', ScrollView: 'Scroll', useWindowDimensions: () => viewport},
    'react-native-safe-area-context': {useSafeAreaInsets: () => insets}, '../../platform/native-blur': {BlurView: 'BlurView'},
    '../../../../../packages/client-core/src/presentation/kit-model': {cardText, episodeLabel, playbackIdentity, titleFactParts},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText}, '../../../../../packages/i18n/src': {defaultI18n: {t, list: (names: string[]) => names.join(', ')}},
    '../../../../../packages/client-core/src/presentation/errors': {}, '../../../../../packages/player-core/src': {sameFence: () => true},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', AmbientArt: 'Ambient', Artwork: 'Artwork'}, '../../kit/cards': {EpisodeTile: 'Episode', PosterCard: 'Poster'},
    '../../kit/controls': {Button: 'Button', ResumeButton: 'Resume', Chip: 'Chip', Tabs: 'Tabs', Focusable: 'Focusable', Switch: 'Switch'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/layout/Hero': {HeroMetadata: 'Facts'}, '../../kit/states': {TitleSkeleton: 'Skeleton'},
    '../../kit/theme': {nativeType, useTheme: () => theme}, '../../kit/focus': {FocusRegion: 'FocusRegion', useOptionalFocusRegistry: () => undefined},
    '../../tv/layout/TVShelf': {TVShelf: 'TVShelf', tvItemKey: (item: any) => item.id}, './tv-title-focus': {tvTitleRegionMap}, '../../shell/tv-focus': {},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable', window: null, folds: []})}, '../../shell/model': {mediumRailMetrics: {width: 84}},
    './TitlePeople': {TitlePeople: 'TitlePeople'}, './TitleEpisodePlate': {NativeTitleEpisodePlate: 'EpisodePlate'}, './SeasonsSheet': {NativeSeasonsSheet: 'Seasons'},
    './TVEpisodeBrowser': {TVEpisodeBrowser: 'TVEpisodeBrowser'}, './ShowSettingsSheet': {NativeShowSettingsSheet: 'Settings'},
  };
  const {NativeTitleScreen, TitleRelated} = await screenModule(new URL('../src-next/screens/watch/TitleScreen.tsx', import.meta.url), modules);
  const {NativeTitleEpisodePlate} = await screenModule(new URL('../src-next/screens/watch/TitleEpisodePlate.tsx', import.meta.url), modules);
  const props = {controller, text: {t}, ambient: () => undefined, artwork: () => undefined, aboutLabel: 'About',
    back: () => {calls.push('back');}, navigate() {}, more() {}, reportError: assert.fail, renderError: assert.fail,
    download() {}, onPlayOn: (anchor: any) => {calls.push(anchor);}};
  return {host, viewport, insets, theme, snapshot, calls, TitleRelated, NativeTitleEpisodePlate, render: () => host.render(() => NativeTitleScreen(props))};
}

test('title chrome allocates the current safe area and keeps real Back and Play on actions', async () => {
  const f = await fixture();
  for (const top of [0, 50, 59]) {
    f.insets.top = top;
    const tree = f.render(), header = find(tree, 'watch-title-header');
    assert.equal(style(header).height, 48 + top);
    assert.equal(style(header).paddingTop, top);
    const back = find(header, 'watch-title-back'), cast = find(header, 'watch-title-play-on');
    assert.deepEqual([style(back).width, style(back).height, style(back).borderRadius], [38, 38, 19]);
    const blur = descendants(back).find(node => node.type === 'BlurView');
    assert.equal(blur.props.blurAmount, 18);
    assert.equal(style(blur.props.children[0]).backgroundColor, 'rgba(7,16,24,.45)');
    back.props.onPress(); assert.equal(f.calls.pop(), 'back');
    cast.props.ref.current = {nativeTag: 18}; cast.props.onPress();
    assert.equal(f.calls.pop(), cast.props.ref, 'Play on receives the actual opener ref');
  }
});

test('the compact phone hero centers its copy and puts wide Play, actions and synopsis below the artwork', async () => {
  const f = await fixture(), tree = f.render(), hero = find(tree, 'watch-title-hero');
  assert.equal(style(hero).height, 396);
  const lockup = descendants(hero).find(node => node.type === 'Text' && node.props.accessibilityRole === 'header');
  assert.equal(style(lockup).fontSize, 46); assert.equal(style(lockup).textAlign, 'center'); assert.equal(style(lockup).marginBottom, 10);
  assert.equal(style(descendants(hero).find(node => node.type === 'Text' && node.props.children[0] === '2026')).fontSize, 13);
  assert.equal(find(hero, 'watch-title-play'), undefined);
  assert.equal(descendants(hero).some(node => node.type === 'Text' && node.props.children[0] === 'The actual synopsis.'), false);
  const play = find(tree, 'watch-title-play'); assert.equal(style(play).flex, 1); play.props.onPress(); assert.equal(f.calls.pop(), 'play');
  const synopsis = descendants(tree).find(node => node.type === 'Text' && node.props.children[0] === 'The actual synopsis.');
  assert.deepEqual([style(synopsis).fontSize, style(synopsis).lineHeight, style(synopsis).marginTop], [14, 21, 16]);
  for (const icon of ['i-plus', 'i-dl', 'i-more']) {
    const secondary = descendants(tree).find(node => node.type === 'Button' && node.props.icon === icon);
    assert.equal(style(secondary).borderRadius, 10);
  }
  const scroll = descendants(tree).find(node => node.type === 'Scroll' && node.props.onScroll);
  const move = (y: number) => scroll.props.onScroll({nativeEvent: {contentOffset: {y}, contentSize: {height: 2000}, layoutMeasurement: {height: 844}}});
  move(297); assert.equal(style(find(f.render(), 'watch-title-header')).backgroundColor, 'transparent');
  move(299); assert.equal(style(find(f.render(), 'watch-title-header')).backgroundColor, '#071018');
});

test('the expanded tablet preserves its title layout, labels Watched and insets the pinned bar', async () => {
  const f = await fixture(); f.viewport.width = 1200; f.viewport.height = 900; f.insets.top = 24; f.theme.platform = 'tablet';
  let tree = f.render();
  assert.equal(style(find(tree, 'watch-title-hero')).height, 621);
  assert.equal(find(tree, 'watch-title-watched').props.iconOnly, false);
  const scroll = descendants(tree).find(node => node.type === 'Scroll' && node.props.onScroll);
  scroll.props.onScroll({nativeEvent: {contentOffset: {y: 622}, contentSize: {height: 2000}, layoutMeasurement: {height: 900}}});
  tree = f.render(); const pinned = find(tree, 'watch-title-pinned-bar');
  assert.equal(style(pinned).paddingTop, 24); assert.equal(style(pinned).height, 80);
  assert.equal(find(tree, 'watch-title-header'), undefined);
});

test('mobile related cards show only their supplied year, and keep their own menu anchor', async () => {
  const f = await fixture(), tree = f.render();
  const cards = descendants(tree).filter(node => node.type === f.TitleRelated);
  assert.deepEqual(cards[0].props.caption, [{id: 'year', text: '2001', role: 'supplement'}]);
  assert.equal(cards[1].props.caption, undefined);
  const poster = f.host.render(() => cards[0].type(cards[0].props));
  assert.ok(poster.props.menuAnchorRef);
  assert.deepEqual(poster.props.caption, cards[0].props.caption);
});

test('the inline episode draws its duration and crops a backdrop only when the still is absent', async () => {
  const f = await fixture(), backdrop = {uri: 'backdrop', cacheKey: 'backdrop'}, still = {uri: 'still', cacheKey: 'still'};
  const entry = {id: 'episode', kind: 'episode', title: 'Episode title', airDate: '2026-10-20', duration: 2760, overview: 'Episode synopsis'};
  const props: any = {entry, code: {text: 'S1 E3'}, width: 350, backdrop, locale: 'en-US', snapshot: {phase: 'ready', data: {item: entry,
    facts: {contentRating: 'TV-14'}, personal: {}, actions: [{id: 'play', enabled: true, playback: {itemId: entry.id}}, {id: 'watchlist', enabled: true}],
    metadata: {credits: [{id: 'cast', name: 'Actor', role: 'Character', department: 'acting'}, {id: 'director', name: 'Director', role: '', department: 'directing'}]},
    episodeDetails: {next: {id: 'next', number: 4, title: 'Next title'}}}}, download() {}, more() {}, onClose() {}, onPlay() {}, onPersonal() {}};
  const render = () => f.host.render(() => f.NativeTitleEpisodePlate(props));
  let tree = render(), art = descendants(tree).find(node => node.type === 'Artwork');
  assert.equal(art.props.source, backdrop); assert.deepEqual(style(art).transform, [{scale: 2.2}]);
  const badge = descendants(tree).find(node => node.type === 'Text' && style(node).position === 'absolute');
  assert.deepEqual([style(badge).right, style(badge).top, style(badge).borderRadius], [10, 10, 6]);
  assert.equal(badge.props.children[0], '46 min');
  for (const icon of ['i-plus', 'i-dl', 'i-more']) assert.equal(style(descendants(tree).find(node => node.type === 'Button' && node.props.icon === icon)).borderRadius, 10);
  assert.equal(style(find(tree, 'watch-episode-close-episode')).borderRadius, 17, 'the drawn close control remains a circle');
  const neighbours = descendants(tree).filter(node => typeof node.type === 'function');
  const neighbourText = neighbours.flatMap(node => descendants(f.host.render(() => node.type(node.props)))).filter(node => node.type === 'Text');
  const texts = [...descendants(tree).filter(node => node.type === 'Text'), ...neighbourText];
  assert.ok(texts.length >= 10);
  for (const node of texts) assert.match(style(node).fontFamily, /^Manrope-/, 'each plate and neighbour label selects the bundled family');
  props.still = still; tree = render(); art = descendants(tree).find(node => node.type === 'Artwork');
  assert.equal(art.props.source, still); assert.equal(style(art).transform, undefined);
  props.entry = {...entry, duration: undefined}; props.snapshot = {phase: 'loading'};
  assert.equal(descendants(render()).some(node => node.type === 'Text' && style(node).position === 'absolute'), false, 'unknown duration does not invent a badge');
});
