import * as layoutModel from '../src-next/kit/layout/model.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as homeWindow from '../src-next/screens/home/home-window';
import * as rowState from '../src-next/kit/layout/row-state';

function nodes(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value || typeof value !== 'object' || !('type' in value)) return [];
  return [value, ...nodes(value.props?.children)];
}
const named = (tree: any, name: string) => nodes(tree).find(node => node.type === name);

// This is a shallow composition check of HomeScreen's actual props and row
// spacers. Native scroll delivery, Reanimated and focus reconciliation still
// require the app's local native tests; fixtures are explicit to this module.
async function homeFixture(width: number, height: number) {
  const h = hooks(), layouts: (() => unknown)[] = [];
  const react = {...h.react, useLayoutEffect: (run: () => unknown) => {layouts.push(run);}};
  const theme = {platform: 'tablet', layoutMode: 'phone', isTV: false, textScale: 1, gutter: 24,
    colors: {text: '#fff', textTertiary: '#777', ink: '#000'},
    type: {rowHeading: {fontSize: 21, lineHeight: 27}, lockup: {fontSize: 42, lineHeight: 42.84, letterSpacing: -1.68}},
    spec: {hero: {homeHeight: {tablet: 440}}, shelf: {phonePaddingTop: 6, phonePaddingBottom: 8, headingMargin: {tablet: 12}},
      card: {width: {square: {tablet: 150}}, live: {height: {phone: 138}}}},
  };
  let dimensions = {width, height, fontScale: 1}, posture: any = {status: 'unavailable'};
  const authority = {}, entry = {id: 'movie', kind: 'movie', libraryId: 'movies', title: 'Movie', posterUrl: 'poster', backdropUrl: 'backdrop'};
  const rows = Array.from({length: 8}, (_, index) => ({id: `row-${index}`, kind: 'library', artworkShape: 'portrait', entries: [entry]}));
  const document = {rows, spotlight: [{entry}]}, anchor = {scrollRef: {}, offset: {value: 0}, repeating: false, stop() {}, move() {}, anchor() {}};
  const registry = {subscribe: () => () => {}, getSnapshot: () => 0, capture: () => undefined, isRegionActive: () => false};
  const runtime = {home: {subscribe: () => () => {}, getSnapshot: () => ({document, phase: 'ready'}), load() {}, more() {}},
    getSnapshot: () => ({authority, libraries: [], selectionActive: false}), text: {t: (key: string) => key},
    artwork: (url: unknown) => url, ambient: (url: unknown) => url, selection: () => ({}),
    actions: {customize() {}}, reportError() {}, onArtworkError() {},
  };
  class Paging {
    subscribe = () => () => {};
    getSnapshot = () => 0;
    state() {return undefined;}
  }
  const {HomeScreen} = await screenModule(new URL('../src-next/screens/home/HomeScreen.tsx', import.meta.url), {'./live-expiry': await import('../src-next/screens/home/live-expiry.ts'), '../../shell/boot-timing': {logBootMilestone: () => {}}, '../../shell/home-read-log': {noteHomeRead: () => {}, createHomeReadLog: () => () => 'home-service'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, '../../kit/overlays': {Banner: 'Banner'}, 
    '../../kit/useMotion': {useMotion: () => ({transforms: true, reduce: false, timing: () => ({})})}, 
    react,
    'react-native': {AppState: {currentState: 'active'}, RefreshControl: 'RefreshControl', Text: 'Text', View: 'View', useWindowDimensions: () => dimensions},
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn,
      useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => react.useRef({value}).current},
    '@portico/design': {heroSpec: {thumbnail: {width: 104, height: 58}}},
    '../../../../../packages/i18n/src': {defaultI18n: {has: () => true}},
    '../../../../../packages/client-core/src/presentation/home-spotlight': {
      homeRowPresentation: (row: any) => ({cards: row.entries.map((item: any) => ({entry: item})), title: row.id}),
      homeSpotlightModels: () => [{entry, reason: {text: 'Featured'}}], homeTitleFactParts: () => [],
    },
    '../../../../../packages/client-core/src/home-selection': {homeEntryCapabilities: () => ({})},
    '../../../../../packages/client-core/src/presentation/errors': {},
    '../../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: (_labels: unknown, value: unknown) => value},
    '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../platform/ArtworkPipeline': {}, '../../platform/platformPosture': {usePlatformPosture: () => posture},
    '../../kit/artwork': {AmbientArt: 'AmbientArt', Artwork: 'Artwork'}, '../../kit/controls': {Button: 'Button'},
    '../destinations/useTVPageBack': {useTVPageBack() {}},
    '../../kit/focus': {FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', useFocusRegistry: () => registry},
    '../../kit/focus/initial-entry': {createInitialRegionEntry: () => ({})},
    '../../shell/model': {authorityKey: () => 'scope', sameAuthority: (a: unknown, b: unknown) => a === b},
    '../../shell/tv-focus': {tvFocusIds: () => ({spotlight: 'spotlight', row: (id: string) => id, page: (id: string) => id}),
      tvHomeRegionMap: () => ({rows: new Map()})},
    '../../shell/home-paging': {NativeHomePaging: Paging}, './tv-presentation': {},
    './home-start': {homeStartModel: () => ({actionless: false})},
    '../../kit/cards': {}, '../../kit/layout': {Hero: 'Hero', FocusPreviewHero: 'FocusPreviewHero', Row: 'Row'},
    '../../kit/layout/FocusAnchor': {useFocusAnchor: () => anchor}, '../../kit/layout/types': {},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', StateView: 'StateView', HomeSkeleton: 'HomeSkeleton', SkeletonBlock: 'SkeletonBlock'},
    '../../kit/theme': {useTheme: () => theme, nativeType: ({size, weight, tracking, lineHeight}: any, scale: number) =>
      ({fontSize: size * scale, fontWeight: weight, letterSpacing: tracking * size, lineHeight})},
    '../../platform/home-action-diagnostics': {}, '../destinations/music/MusicMixesRow': {}, './HomeHeroFacts': {HomeHeroFacts: 'HomeHeroFacts'},
    './model': {authorizedHome: (value: unknown) => value, platformRows: () => rows, homeRowHasContent: () => true},
    './TvHomeRecovery': {}, './useTvHomeRecovery': {useTvHomeRecovery: () => ({visible: false})}, './home-window': homeWindow,
  });
  return {
    setSize: (nextWidth: number, nextHeight: number) => {dimensions = {width: nextWidth, height: nextHeight, fontScale: 1};},
    setCrease: () => {posture = {status: 'available', window: dimensions, folds: [{orientation: 'vertical', bounds: {left: width / 2 - 4, right: width / 2 + 4}}]};},
    render: () => {const tree = h.render(() => HomeScreen({runtime, authority})); for (const run of layouts.splice(0)) run(); return tree;},
  };
}

test('upright tablet Home composes the centered 560pt hero and 152pt shelves', async () => {
  const fixture = await homeFixture(834, 1194), tree = fixture.render(), hero = named(tree, 'Hero');
  assert.equal(hero.props.height, 560); assert.equal(hero.props.copySide, undefined);
  assert.deepEqual(hero.props.copyStyle, {left: 56, right: 56, bottom: 44});
  assert.equal(hero.props.lockupStyle.fontSize, 62);
  const artwork = hero.props.renderArtwork(hero.props.candidates[0], {visible: true});
  assert.equal(artwork.type, 'AmbientArt'); assert.equal(artwork.props.foregroundFraction, undefined);
  assert.deepEqual(artwork.props.position, {x: .36, y: .28});
  const shelf = named(tree, 'Row'); assert.equal(shelf.props.itemWidth, 152); assert.equal(shelf.props.gutter, 28);
});

test('a matching upright fold keeps the side-by-side 440pt hero and 140pt shelves', async () => {
  const fixture = await homeFixture(840, 880); fixture.setCrease();
  const tree = fixture.render(), hero = named(tree, 'Hero');
  assert.equal(hero.props.height, 440); assert.deepEqual(hero.props.copySide, {inset: 28, width: 300, bottom: 30});
  assert.equal(hero.props.copyStyle, undefined); assert.equal(hero.props.lockupStyle.fontSize, 42);
  const artwork = hero.props.renderArtwork(hero.props.candidates[0], {visible: true});
  assert.equal(artwork.props.style.left, 424);
  assert.equal(named(artwork, 'AmbientArt').props.foregroundFraction, .6);
  const shelf = named(tree, 'Row'); assert.equal(shelf.props.itemWidth, 140); assert.equal(shelf.props.gutter, 24);
});

test('same-width height and crease changes invalidate measured offscreen row spacers', async () => {
  for (const change of ['height', 'crease']) {
    const fixture = await homeFixture(834, 1194), first = fixture.render();
    const row = nodes(first).find(node => node.type?.name === 'HomeWindow' && node.props.row.id === 'row-2');
    const measured = nodes(row).find(node => node.type === 'View' && node.props.onLayout);
    measured.props.onLayout({nativeEvent: {layout: {y: 1200, height: 888}}});
    const second = fixture.render();
    named(second, 'ScrollView').props.onScroll({contentOffset: {y: 10000}});
    const spacer = (tree: any) => nodes(tree).find(node => node.type === 'View' && node.props.key === 'row-2');
    assert.equal(nodes(spacer(fixture.render()))[1].props.style.height, 888);
    if (change === 'height') fixture.setSize(834, 800); else fixture.setCrease();
    const nextHeight = nodes(spacer(fixture.render()))[1].props.style.height;
    assert.equal(nextHeight, 27 + 12 + 140 * 1.5 + 48 + 6 + 8, `${change} must replace the old cached height`);
  }
});

test('a restored shelf seeds the anchor offset once and preserves later native scroll offsets', async () => {
  const h = hooks(), layouts: (() => unknown)[] = [];
  const react = {...h.react, useLayoutEffect: (run: () => unknown) => {layouts.push(run);}};
  const anchor = {scrollRef: {}, offset: {value: 0}, anchor() {}, move() {}, stop() {}, repeating: false};
  const theme = {isTV: false, platform: 'phone', gutter: 20, colors: {text: '#fff'}, type: {rowHeading: {}},
    spec: {shelf: {gap: {phone: 10}, phonePaddingTop: 6, phonePaddingBottom: 8, headingGap: {phone: 10}, headingMargin: {phone: 12}}}};
  const {Row} = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {'./model': layoutModel, 
    react, 'react-native': {Pressable: 'Pressable', Text: 'Text', View: 'View'}, 'react-native-svg': {},
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn,
      useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => react.useRef({value}).current},
    '../../../../../packages/i18n/src': {}, '../Icon': {}, '../theme': {useTheme: () => theme},
    '../focus': {FocusProvider: 'FocusProvider', FocusItem: 'FocusItem', useOptionalFocusRegistry: () => undefined},
    './row-state': rowState, '../focus/page-boundary': {useRowPageBoundary() {}},
    './FocusAnchor': {useFocusAnchor: () => anchor}, './MotionItem': {MotionItem: 'MotionItem'}, './types': {itemSelection: () => ({})},
  });
  const memory = {offset: 520}, props = {title: 'Shelf', items: Array.from({length: 20}, (_, id) => ({id: String(id)})),
    getKey: (item: any) => item.id, renderItem: (item: any) => react.createElement('Card', {id: item.id}),
    itemWidth: 100, itemHeight: 150, initialViewportWidth: 390, selection: {}, scrollMemory: memory};
  const render = () => {
    const tree = h.render(() => {const child = Row(props).props.children[0]; return child.type(child.props);});
    for (const run of layouts.splice(0)) run();
    return tree;
  };
  const initial = render();
  assert.deepEqual(named(initial, 'ScrollView').props.contentOffset, {x: 520, y: 0});
  assert.equal(anchor.offset.value, 520, 'focus/arrow calculations start at the restored native position');
  assert.equal(nodes(initial).find(node => node.type === 'Card').props.id, '2', 'the bounded rendered window starts near the restored offset');
  named(initial, 'ScrollView').props.onScroll({contentOffset: {x: 753}});
  assert.equal(anchor.offset.value, 753); assert.equal(memory.offset, 753);
  render(); assert.equal(anchor.offset.value, 753, 'a committed rerender must not replay the mount offset');
  memory.offset = 99;
  render(); assert.equal(anchor.offset.value, 753, 'changing the memory object value does not take over native scroll ownership');
});


test('Home data visibility follows measured row positions ahead of estimates', async () => {
  const fixture = await homeFixture(834, 1194);
  fixture.render();
  const initial = fixture.render();
  named(initial, 'ScrollView').props.onScroll({contentOffset: {y: 600}});
  const rowWindow = (tree: any) => nodes(tree).find(node => node.type?.name === 'HomeWindow' && node.props.row.id === 'row-0');
  const visible = fixture.render();
  assert.equal(rowWindow(visible).props.visible, true);
  const row = nodes(rowWindow(visible)).find(node => node.type === 'View' && node.props.onLayout);
  row.props.onLayout({nativeEvent: {layout: {y: 2000, height: 300}}});
  fixture.render();
  assert.equal(rowWindow(fixture.render()).props.visible, false, 'estimated y must not authorize data for an actually offscreen row');
  named(fixture.render(), 'ScrollView').props.onScroll({contentOffset: {y: 2000}});
  assert.equal(rowWindow(fixture.render()).props.visible, true, 'actual row position re-enables its data window');
});
