import test from 'node:test';
import assert from 'node:assert/strict';
import * as layoutModel from '../src-next/kit/layout/model.ts';
import * as rowState from '../src-next/kit/layout/row-state.ts';
import {tvScrollEnabled} from '../src-next/kit/layout/tv-scroll.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// W-17 sweep. On Android a horizontal ScrollView is focusable itself, and react-native-tvos ties
// that to `scrollEnabled` (there is no `focusable` prop on a scroll view). So on TV a horizontal
// scroller with nothing focusable in it takes D-pad focus and nothing is drawn as focused. The rule:
// on TV a horizontal scroller is scroll-enabled only while it holds at least one focusable item;
// touch is always enabled. PR #329 did this for the title tabs; this covers every other scroller.
const kids = (node: any): any[] => Array.isArray(node) ? node.flatMap(kids) : node?.props ? [node, ...kids(node.props.children)] : [];
const scroller = (tree: any) => {
  const found = kids(tree).filter(node => node.type === 'ScrollView');
  assert.equal(found.length, 1, 'exactly one horizontal scroller is drawn');
  return found[0];
};
// The real helper is linked in everywhere, so these tests exercise the rule itself.
const tvScroll = {tvScrollEnabled};
const view = (isTV: boolean) => ({isTV, platform: isTV ? 'tv' : 'phone'});

test('the helper: TV needs a focusable item, touch is always enabled', () => {
  assert.equal(tvScrollEnabled(true, false), false, 'nothing focusable on TV: the scroller must not be a focus stop');
  assert.equal(tvScrollEnabled(true, true), true, 'a focusable item on TV: focus-follow scrolling stays on');
  assert.equal(tvScrollEnabled(false, false), true, 'touch scrolls whatever it holds');
  assert.equal(tvScrollEnabled(false, true), true);
});

// ---- kit/layout/Row.tsx (also TVShelf, and every Home, Library, Search, Title, Person and Music shelf) ----
async function row(isTV: boolean) {
  const h = hooks(), anchor = {scrollRef: {}, offset: {value: 0}, anchor() {}, move() {}, stop() {}, repeating: false};
  const react = {...h.react, useLayoutEffect: () => {}, useId: () => 'id'};
  const theme = {...view(isTV), gutter: 20, fontScale: 1, colors: {text: '#fff'}, type: {rowHeading: {}}, baseRadii: {control: 8}, layers: {},
    layout: {gutter: {tvRight: 96}},
    spec: {focus: {scale: {card: {tv: 1.08}}, offsetOuter: {tv: 4}, ringWidth: {tv: 3}}, shelf: {gap: {tv: 28, phone: 10}, phonePaddingTop: 6, phonePaddingBottom: 8, headingGap: {tv: 10, phone: 10}, headingMargin: {tv: 10, phone: 12}}}};
  const {Row} = await screenModule(new URL('../src-next/kit/layout/Row.tsx', import.meta.url), {'./model': layoutModel,
    react, 'react-native': {Pressable: 'Pressable', Text: 'Text', View: 'View'}, 'react-native-svg': {},
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => react.useRef({value}).current},
    '../../../../../packages/i18n/src': {}, '../Icon': {}, '../theme': {useTheme: () => theme},
    '../focus': {FocusProvider: 'FocusProvider', FocusItem: 'FocusItem', FocusRegion: 'FocusRegion', focusTestID: (a: string, b: string) => `${a}.${b}`, useOptionalFocusRegistry: () => undefined},
    './row-state': rowState, './tv-scroll': tvScroll, '../focus/page-boundary': {useRowPageBoundary() {}},
    './FocusAnchor': {useFocusAnchor: () => anchor}, './MotionItem': {MotionItem: 'MotionItem'}, './types': {itemSelection: () => ({})},
  });
  return (extra: Record<string, unknown>) => {
    const props = {title: 'Shelf', showHeading: false, items: [] as {id: string}[], getKey: (item: any) => item.id, renderItem: (item: any) => react.createElement('Card', {id: item.id}),
      itemWidth: 100, itemHeight: 150, initialViewportWidth: 800, selection: {}, ...extra};
    return h.render(() => {const child = Row(props as any).props.children[0]; return child.type(child.props);});
  };
}
const cards = (count: number) => Array.from({length: count}, (_, index) => ({id: `c${index}`}));
const waitingPage = {hasMore: true, loading: false, waitingId: 'page:2', waitingItem: {id: 'page:2'}, requestNext() {}};

test('a TV row or shelf with no items is not a focus stop; with items it keeps focus-follow scrolling', async () => {
  const render = await row(true);
  assert.equal(scroller(render({items: []})).props.scrollEnabled, false, 'an empty shelf');
  assert.equal(scroller(render({items: cards(3)})).props.scrollEnabled, true, 'a shelf of cards');
});

test('a TV row whose every item is inert is not a focus stop, but a waiting page host or one live card is', async () => {
  const render = await row(true), inert = () => false;
  assert.equal(scroller(render({items: cards(3), isFocusable: inert})).props.scrollEnabled, false, 'all passive');
  assert.equal(scroller(render({items: cards(3), isFocusable: inert, paging: waitingPage})).props.scrollEnabled, true, 'the page cursor is a real stop');
  assert.equal(scroller(render({items: cards(3), isFocusable: (item: any) => item.id === 'c2'})).props.scrollEnabled, true, 'one card can take focus');
});

test('a touch row scrolls whatever it holds', async () => {
  const render = await row(false);
  assert.equal(scroller(render({items: []})).props.scrollEnabled, true);
  assert.equal(scroller(render({items: cards(3), isFocusable: () => false})).props.scrollEnabled, true);
});

// ---- kit/layout/WindowedRow.tsx (the TV Collection row) ----
async function windowedRow(isTV: boolean) {
  const h = hooks(), anchor = {scrollRef: {}, offset: {value: 0}, anchor() {}, move() {}, stop() {}, repeating: false};
  const react = {...h.react, useLayoutEffect: () => {}};
  const theme = {...view(isTV), gutter: 20, fontScale: 1,
    spec: {focus: {scale: {card: {tv: 1.08}}, offsetOuter: {tv: 4}, ringWidth: {tv: 3}}}};
  const {WindowedRow} = await screenModule(new URL('../src-next/kit/layout/WindowedRow.tsx', import.meta.url), {'./model': layoutModel,
    react, 'react-native': {View: 'View'},
    'react-native-reanimated': {default: {ScrollView: 'ScrollView'}, runOnJS: (fn: unknown) => fn, useAnimatedScrollHandler: (fn: unknown) => fn, useSharedValue: (value: unknown) => react.useRef({value}).current},
    '../theme': {useTheme: () => theme}, './FocusAnchor': {useFocusAnchor: () => anchor}, './tv-scroll': tvScroll, './types': {itemSelection: () => ({})},
  });
  return (loaded: ReadonlySet<number>, total?: number) => {
    const collection = {subscribe: () => () => {}, getSnapshot: () => ({total}), pageOf: () => 0, subscribePage: () => () => {}, getPageVersion: () => 0, ensureRange() {}, pin() {},
      slotAt: (index: number) => loaded.has(index) ? {kind: 'item', item: {id: `m${index}`}} : {kind: 'placeholder', key: `placeholder:${index}`}};
    return h.render(() => WindowedRow({collection: collection as any, getKey: (item: any) => item.id, renderItem: () => react.createElement('Card', {}), renderPlaceholder: () => react.createElement('Placeholder', {}),
      itemWidth: 214, itemHeight: 321}));
  };
}

test('a TV window of placeholders is not a focus stop; once a real card is in the window it is', async () => {
  const render = await windowedRow(true);
  assert.equal(scroller(render(new Set(), undefined)).props.scrollEnabled, false, 'before the first page answers');
  assert.equal(scroller(render(new Set(), 40)).props.scrollEnabled, false, 'a known total, nothing loaded yet');
  assert.equal(scroller(render(new Set([0, 1]), 40)).props.scrollEnabled, true, 'real cards in the window');
});

test('a touch window of placeholders still scrolls', async () => {
  const render = await windowedRow(false);
  assert.equal(scroller(render(new Set(), 40)).props.scrollEnabled, true);
});

// ---- screens/destinations/music/MusicMixesRow.tsx ----
async function mixes(isTV: boolean) {
  const h = hooks(), caller = {lifetime: {isCurrent: () => true}, dispose() {}};
  // A layout effect runs at once, so the caller is captured on the next render, as on a device.
  const react = {...h.react, useLayoutEffect: (run: () => unknown) => {run();}};
  const theme = {...view(isTV), layoutMode: isTV ? 'tv' : 'phone', type: {rowHeading: {}}, colors: {text: '#fff', textTertiary: '#888'}};
  const {MusicMixesRow} = await screenModule(new URL('../src-next/screens/destinations/music/MusicMixesRow.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', StyleSheet: {create: (value: unknown) => value, absoluteFill: {}}, Text: 'Text', View: 'View'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../../kit/artwork': {Artwork: 'Artwork'}, '../../../kit/controls': {Focusable: 'Focusable'},
    '../../../kit/theme': {useTheme: () => theme}, '../../../kit/browse/presentation': {browseType: () => ({})}, '../../../kit/focus': {FocusRegion: 'FocusRegion'},
    '../../../kit/overlays': {MenuRow: 'MenuRow', SidePanel: 'SidePanel'}, '../../../kit/layout/tv-scroll': tvScroll,
  });
  const port = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', rows: [{id: 'a', name: 'A'}, {id: 'b', name: 'B'}]}), subscribeRevision: () => () => {}, getRevision: () => 1,
    captureCaller: () => caller, load: async () => {}, play: async () => {}, save: async () => {}};
  return (active: boolean, settle = true) => {
    const props = {port: port as any, artwork: () => undefined, reportError() {}, active};
    h.render(() => MusicMixesRow(props));
    return settle ? h.render(() => MusicMixesRow(props)) : undefined;
  };
}

test('TV Mixes tiles that are disabled (inactive row, or caller not captured yet) leave no focus stop; enabled ones do', async () => {
  const render = await mixes(true);
  assert.equal(scroller(render(true)).props.scrollEnabled, true, 'active with a captured caller');
  assert.equal(scroller(render(false)).props.scrollEnabled, false, 'an inactive row: every tile is disabled');
});

test('TV Mixes tiles are disabled before the caller is captured', async () => {
  const h = hooks(), react = {...h.react, useLayoutEffect: () => {}};
  const {MusicMixesRow} = await screenModule(new URL('../src-next/screens/destinations/music/MusicMixesRow.tsx', import.meta.url), {
    react, 'react-native': {ScrollView: 'ScrollView', StyleSheet: {create: (value: unknown) => value, absoluteFill: {}}, Text: 'Text', View: 'View'}, 'react-native-svg': {default: 'Svg', Defs: 'Defs', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../../kit/artwork': {Artwork: 'Artwork'}, '../../../kit/controls': {Focusable: 'Focusable'},
    '../../../kit/theme': {useTheme: () => ({isTV: true, platform: 'tv', layoutMode: 'tv', type: {rowHeading: {}}, colors: {text: '#fff', textTertiary: '#888'}})}, '../../../kit/browse/presentation': {browseType: () => ({})},
    '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/overlays': {MenuRow: 'MenuRow', SidePanel: 'SidePanel'}, '../../../kit/layout/tv-scroll': tvScroll,
  });
  const port = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'ready', rows: [{id: 'a', name: 'A'}]}), subscribeRevision: () => () => {}, getRevision: () => 1, captureCaller: () => undefined, load: async () => {}};
  assert.equal(scroller(h.render(() => MusicMixesRow({port: port as any, artwork: () => undefined, reportError() {}}))).props.scrollEnabled, false);
});

test('the touch Mixes row scrolls even while its tiles are disabled', async () => {
  const render = await mixes(false);
  assert.equal(scroller(render(false)).props.scrollEnabled, true);
  assert.equal(scroller(render(true)).props.scrollEnabled, true);
});

// ---- kit/artwork/Profiles.tsx (the TV profile picker, and the tablet row) ----
async function profiles(isTV: boolean) {
  const h = hooks();
  const {ProfilePicker} = await screenModule(new URL('../src-next/kit/artwork/Profiles.tsx', import.meta.url), {
    '../focus/registry': {focusTestID: (a: string, b: string) => `${a}.${b}`}, '../focus/ProfileEntryRegion': {ProfileEntryRegion: 'ProfileEntryRegion'},
    react: h.react, 'react-native': {ScrollView: 'ScrollView', StyleSheet: {create: (value: unknown) => value, absoluteFill: {}}, Text: 'Text', View: 'View'},
    'react-native-reanimated': {default: {View: 'AnimatedView'}, useAnimatedStyle: () => ({}), useSharedValue: (value: unknown) => ({value}), withTiming: (value: unknown) => value},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, '@portico/design': {profileArtColors: {}},
    '../../../../../packages/i18n/src/index': {defaultI18n: {t: (key: string) => key}}, '../controls/index': {Button: 'Button', Focusable: 'Focusable'}, '../controls/control-model': {colorAlpha: (c: string) => c},
    '../Icon': {Icon: 'Icon'}, '../theme': {fontFamilies: {}, useTheme: () => ({...view(isTV), textScale: 1, colors: {text: '#fff', textSecondary: '#ccc', textTertiary: '#888'}, type: {body: {}}})},
    '../useMotion': {useMotion: () => ({transforms: true, timing: () => ({})})}, './Artwork': {Artwork: 'Artwork'}, './model': {mixColour: () => '#000'}, '../layout/tv-scroll': tvScroll,
  });
  const profile = (id: string, extra: Record<string, unknown> = {}) => ({id, name: id, initial: id[0], color: 'red', tint: 'red', ...extra});
  return (list: any[], extra: Record<string, unknown> = {}) => h.render(() => ProfilePicker({profiles: list, onSelect() {}, focusScope: 'profiles', ...extra} as any));
}

test('a TV profile picker is a focus stop only while a tile or Add can take focus', async () => {
  const render = await profiles(true);
  const open = {id: 'a', name: 'a', initial: 'a', color: 'red', tint: 'red'};
  const disabled = {...open, id: 'b', disabled: true}, unavailable = {...open, id: 'c', unavailableReason: 'restricted'};
  assert.equal(scroller(render([open, disabled])).props.scrollEnabled, true, 'one enabled profile');
  assert.equal(scroller(render([disabled, unavailable])).props.scrollEnabled, false, 'every tile disabled or unavailable');
  assert.equal(scroller(render([])).props.scrollEnabled, false, 'no profiles at all');
  assert.equal(scroller(render([disabled, unavailable], {onAdd() {}})).props.scrollEnabled, true, 'Add is a real stop');
  assert.equal(scroller(render([disabled], {focusBusy: true})).props.scrollEnabled, true, 'a busy disabled tile keeps focus rather than dropping it');
});

test('a tablet profile row scrolls even when every tile is disabled', async () => {
  const render = await profiles(false);
  assert.equal(scroller(render([{id: 'b', name: 'b', initial: 'b', color: 'red', tint: 'red', disabled: true}], {layout: 'row'})).props.scrollEnabled, true);
});

// ---- screens/watch/EpisodePanel.tsx (the TV episodes band over the player) ----
async function episodes(isTV: boolean) {
  const h = hooks();
  const {NativeWatchEpisodes} = await screenModule(new URL('../src-next/screens/watch/EpisodePanel.tsx', import.meta.url), {
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/kit-model': {episodeLabel: () => undefined}, '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText: String},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'}, '../../kit/cards': {EpisodeTile: 'EpisodeTile'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'}, '../../kit/focus': {FocusRegion: 'FocusRegion'},
    '../../kit/overlays/TVPanelMotion': {useTVPanelInteractive: () => true}, '../../kit/theme': {useTheme: () => ({...view(isTV), type: {heading: {}, body: {}}, colors: {text: '#fff', textTertiary: '#888', raised: '#111'}})},
    './SeasonsSheet': {NativeSeasonsSheet: 'Seasons'}, '../../kit/states': {Quiet: 'Quiet'}, '../../kit/layout/tv-scroll': tvScroll,
  });
  return (list: {id: string}[], phase = 'ready') => {
    const state = {workspace: {phase, data: {selected: {seasonId: 's'}}}, seasons: [{id: 's', number: 1}], episodes: list.map(entry => ({...entry, title: entry.id})), place: {hideWatched: false}};
    const controller = {getSnapshot: () => state, subscribe: () => () => {}, open: async () => {}, pageLease: () => ({}), moreEpisodes: async () => {}};
    return h.render(() => NativeWatchEpisodes({controller: controller as any, currentId: 'e1', playing: true, text: {} as any, artwork() {return undefined;}, onPlay() {}, onClose() {}}));
  };
}

test('the TV episodes band is not a focus stop while it only shows Loading or nothing; with tiles it scrolls to follow focus', async () => {
  const render = await episodes(true);
  assert.equal(scroller(render([], 'loading')).props.scrollEnabled, false, 'the loading line');
  assert.equal(scroller(render([])).props.scrollEnabled, false, 'no episodes');
  assert.equal(scroller(render([{id: 'e1'}, {id: 'e2'}])).props.scrollEnabled, true, 'episode tiles');
});
