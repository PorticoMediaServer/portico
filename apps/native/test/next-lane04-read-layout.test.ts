import * as cardModel from '../src-next/kit/cards/model.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as presentation from '../src-next/screens/read/periodical-presentation';

function renderer() {
  const owners = new Map<any, ReturnType<typeof hooks>>(); let active = hooks();
  const react: any = {createElement: (...args: any[]) => active.react.createElement(...args),
    useRef: (...args: any[]) => active.react.useRef(...args), useState: (...args: any[]) => active.react.useState(...args),
    useMemo: (...args: any[]) => active.react.useMemo(...args), useCallback: (fn: any) => fn,
    useEffect() {}, useLayoutEffect() {}, useSyncExternalStore: (_subscribe: any, get: any) => get()};
  react.default = react;
  return {react, render: (component: any, props: any) => {
    let owner = owners.get(component); if (!owner) {owner = hooks(); owners.set(component, owner);}
    const previous = active; active = owner;
    try {return owner.render(() => component(props));} finally {active = previous;}
  }};
}
function nodes(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value || typeof value !== 'object' || !('type' in value)) return [];
  return [value, ...nodes(value.props?.children), ...nodes(value.props?.header), ...nodes(value.props?.hero), ...nodes(value.props?.skeleton)];
}
const named = (tree: any, name: string) => nodes(tree).find(node => node.type === name || node.type?.name === name);
const theme = (tablet: boolean) => ({platform: tablet ? 'tablet' : 'phone', gutter: 20,
  colors: {ink: '#071018', text: '#edf5fa', textSecondary: '#aaa', textTertiary: '#777', raised: '#123', raisedHigh: '#234', accent: '#409fd4', accentStrong: '#5cb3e2', line: '#333'}});
const typography = (_theme: unknown, fontSize: number, fontWeight?: number, tracking?: number) => ({fontSize, fontWeight, tracking});
const issue = (id: string, issueDate: string, read = false) => ({issueDate, item: {id, libraryId: 'library', title: id, inventoryAvailable: true,
  coverUrl: `${id}-cover`, readingMark: {itemId: id, read, revision: 2}}});

test('reading shelf keeps a bounded native window, starts ten cards early and draws a pending tail', async () => {
  const r = renderer(), {ReadingShelf} = await screenModule(new URL('../src-next/screens/read/ReadingShelf.tsx', import.meta.url), {
    react: r.react, 'react-native': {FlatList: 'FlatList', View: 'View'}, '../../kit/states': {SkeletonBlock: 'SkeletonBlock'},
  });
  const data = Array.from({length: 500}, (_, id) => ({id: String(id)})); let more = 0;
  const memory = {offset: 3200}, props = {items: data, getKey: (item: any) => item.id, renderItem: () => null,
    width: 142, height: 279, artHeight: 213, gap: 14, gutter: 32, onNext: () => {more++;}, scrollMemory: memory};
  let tree = r.render(ReadingShelf, props); assert.equal(tree.type, 'FlatList'); assert.equal(tree.props.data, data);
  assert.equal(tree.props.initialNumToRender, 4); assert.equal(tree.props.windowSize, 5);
  tree.props.onLayout({nativeEvent: {layout: {width: 390}}}); tree = r.render(ReadingShelf, props);
  assert.equal(tree.props.onEndReachedThreshold, 10 * (142 + 14) / 390);
  assert.deepEqual(tree.props.getItemLayout(data, 20), {length: 156, offset: 3152, index: 20});
  assert.deepEqual(tree.props.contentOffset, {x: 3200, y: 0});
  tree.props.onScroll({nativeEvent: {contentOffset: {x: 3333}}}); assert.equal(memory.offset, 3333);
  tree.props.onEndReached(); assert.equal(more, 1);
  tree = r.render(ReadingShelf, {...props, pending: true}); assert.equal(tree.props.onEndReached, undefined);
  assert.equal(named(tree.props.ListFooterComponent, 'SkeletonBlock').props.height, 213);
});

test('phone card long press opens its anchored menu and selection tap does not open the book', async () => {
  const r = renderer(), {PhoneReadingBookCard} = await screenModule(new URL('../src-next/screens/read/PhoneReadingBookCard.tsx', import.meta.url), {'../../kit/cards/model': cardModel, 
    react: r.react, 'react-native': {Text: 'Text', View: 'View'}, '@portico/design': {generatedCoverTint: () => ({})},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', GeneratedCover: 'GeneratedCover', MediaArt: 'MediaArt'}, '../../kit/controls': {Focusable: 'Focusable'},
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/browse/presentation': {browseType: typography}, '../../kit/theme': {useTheme: () => theme(false)},
  });
  let opened = 0, selected = 0, anchor: unknown;
  const tree = r.render(PhoneReadingBookCard, {testID: 'one', title: 'One', width: 108, onOpen: () => {opened++;}, onMenu: (ref: unknown) => {anchor = ref;},
    selection: {active: true, selected: true, onToggle: () => {selected++;}}});
  tree.props.onLongPressAction(); assert.equal(anchor, tree.props.ref);
  tree.props.onPress(); assert.equal(opened, 0); assert.equal(selected, 1);
  assert.equal(named(tree, 'SelectionCircle').props.selected, true);
});

async function periodical(tablet: boolean, magazine: boolean, phase = 'ready') {
  const r = renderer(), selected = issue('selected', '2026-10-01', true), latest = issue('latest', '2026-10-28');
  const state = {phase, publicationType: magazine ? 'magazine' : 'newspaper', latest, groupName: 'Publication', issueCount: 3,
    items: [latest, selected], years: [{value: '2026', count: 2}, {value: '2025', count: 1}],
    yearSections: [{year: '2026', phase: 'ready', items: [latest, selected]}], selectedYear: '2026',
    days: [{value: selected.issueDate, count: 1}, {value: latest.issueDate, count: 1}], months: ['2026-10'], selectedMonth: '2026-10', selectedDate: selected.issueDate};
  const opens: unknown[] = [], menus: unknown[] = [], life = new AbortController();
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, itemLifetime: () => ({signal: life.signal, isCurrent: () => true}),
    open: async (_state: unknown, value: unknown) => {opens.push(value);}, selectDate: async () => {}, ensureYear: async () => {}, selectYear: async () => {}};
  const actions = {state: {selecting: false, selected: []}, actions: {show: (value: unknown) => menus.push(value), inspect: async () => undefined},
    selection: () => ({active: false, selected: false})};
  const runtime = {artwork: (uri?: string) => uri ? {uri, cacheKey: uri} : undefined, ambient: () => undefined, reportError: (error: unknown) => {throw error;}};
  const {ReadPeriodicalScreen} = await screenModule(new URL('../src-next/screens/read/ReadPeriodicalScreen.tsx', import.meta.url), {
    react: r.react, 'react-native': {FlatList: 'FlatList', ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: tablet ? 932 : 390, height: 844})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 0})}, '@portico/design': {generatedCoverTint: () => ({})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, number: String}, calendarDate: (_i18n: unknown, value: string) => value,
      calendarMonth: () => ({label: 'October 2026', offset: 4, weekdays: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'], dates: Array.from({length: 31}, (_, index) => `2026-10-${String(index + 1).padStart(2, '0')}`)})},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork', AmbientArt: 'AmbientArt', GeneratedCover: 'GeneratedCover'}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/states': {SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, '../../kit/cards/primitives': {Scrim: 'Scrim'},
    '../../kit/browse/presentation': {alpha: (color: string, opacity: number) => `${color}/${opacity}`, browseType: typography}, '../../kit/theme': {useTheme: () => theme(tablet)},
    '../downloads/request': {askNativeDownload: () => {}}, '../../../platform/offline-reading': {nativePublicationReadsAvailable: () => true},
    './ReadingLoading': {ReadingLoading: 'ReadingLoading'}, './ReadingShelf': {ReadingShelf: 'ReadingShelf'},
    './ReadingActions': {ReadingItemMenu: 'ReadingItemMenu', ReadingSelectionToolbar: 'ReadingSelectionToolbar', useReadingActions: () => actions},
    './periodical-presentation': presentation,
  });
  const tree = r.render(ReadPeriodicalScreen, {controller, route: {groupId: 'group', publicationType: state.publicationType}, context: {}, runtime, onBack() {}});
  return {r, tree, state, selected, latest, opens, menus};
}

test('magazine uses virtual year rows, a 190pt tablet hero and 44pt year chips', async () => {
  const f = await periodical(true, true), rowNode = named(f.tree, 'MagazineIssues'), list = f.r.render(rowNode.type, rowNode.props);
  assert.equal(list.type, 'FlatList'); assert.equal(list.props.initialNumToRender, 4);
  assert.deepEqual(list.props.data.map((row: any) => [row.year, row.kind]), [['2026', 'heading'], ['2026', 'issues'], ['2025', 'heading'], ['2025', 'issues']]);
  const heroNode = named(rowNode.props.hero, 'PeriodicalHero'), hero = f.r.render(heroNode.type, heroNode.props);
  assert.equal(named(hero, 'IssueArtwork').props.width, 190);
  const chips = nodes(list.props.ListHeaderComponent).find(node => node.type === 'FlatList' && node.props.horizontal);
  const chip = chips.props.renderItem({item: f.state.years[0]}); assert.equal(chip.props.style.height, 44);
  assert.equal(chip.props.style.backgroundColor, '#409fd4/0.2');
  const shelf = list.props.renderItem({item: list.props.data[1]}); assert.equal(shelf.type, 'ReadingShelf');
  assert.equal(shelf.props.width, 150); assert.equal(shelf.props.height, 266);
});

test('newspaper phone renders real day choices and its 446pt front page opens the selected edition', async () => {
  const f = await periodical(false, false), bodyNode = named(f.tree, 'NewspaperEditions'), body = f.r.render(bodyNode.type, bodyNode.props);
  const strip = nodes(body).find(node => node.type === 'FlatList' && node.props.horizontal);
  assert.equal(strip.props.data, f.state.days); assert.equal(strip.props.initialScrollIndex, 0);
  const front = nodes(body).find(node => node.type === 'Focusable' && node.props.accessibilityLabel === 'selected');
  assert.equal(front.props.style.height, 446); front.props.onPress(); assert.deepEqual(f.opens, [f.selected]);
  front.props.onLongPressAction(); assert.equal((f.menus[0] as any).item, f.selected.item);
  assert.equal((f.menus[0] as any).anchorRef, front.props.ref);
  assert.ok(named(body, 'BottomSheet'), 'Go to date uses a calendar sheet');
});

test('read issue artwork uses the approved dimming while preserving the selected ring independently', async () => {
  const f = await periodical(true, true), rowNode = named(f.tree, 'MagazineIssues'), list = f.r.render(rowNode.type, rowNode.props);
  const shelf = list.props.renderItem({item: list.props.data[1]}), cardNode = shelf.props.renderItem(f.selected), card = f.r.render(cardNode.type, cardNode.props);
  const artworkNode = named(card, 'IssueArtwork'), artwork = f.r.render(artworkNode.type, artworkNode.props), image = named(artwork, 'Artwork');
  assert.equal(image.props.saturation, .6); assert.equal(image.props.brightness, .66);
  assert.ok(nodes(card).some(node => node.type === 'Icon' && node.props.name === 'i-check'));
});

test('expanded newspaper has a side-by-side thumbnail calendar whose selected ring stays undimmed', async () => {
  const f = await periodical(true, false), bodyNode = named(f.tree, 'NewspaperEditions'), body = f.r.render(bodyNode.type, bodyNode.props);
  assert.ok(nodes(body).some(node => node.type === 'View' && node.props.style?.flexDirection === 'row' && node.props.style?.gap === 28));
  const calendarNode = nodes(body).find(node => node.type?.name === 'PeriodicalCalendar' && node.props.thumbnails);
  let calendar = f.r.render(calendarNode.type, calendarNode.props);
  calendar.props.onLayout({nativeEvent: {layout: {width: 448}}}); calendar = f.r.render(calendarNode.type, calendarNode.props);
  const chosen = nodes(calendar).find(node => node.type === 'Focusable' && node.props.accessibilityState?.selected);
  const ring = nodes(chosen).find(node => node.type === 'View' && node.props.style?.borderWidth === 3);
  assert.ok(ring); assert.equal(ring.props.style.opacity, undefined);
  assert.ok(nodes(ring).some(node => node.type === 'View' && node.props.style?.opacity === .5));
});

test('periodical loading outline uses the same tablet cover and phone front-page geometry', async () => {
  const magazine = await periodical(true, true, 'loading'), outline = named(magazine.tree, 'PeriodicalSkeleton');
  const shaped = magazine.r.render(outline.type, outline.props);
  assert.ok(nodes(shaped).some(node => node.type === 'SkeletonBlock' && node.props.width === 190 && node.props.height === 190 * 4 / 3));
  const paper = await periodical(false, false, 'loading'), paperOutline = named(paper.tree, 'PeriodicalSkeleton');
  assert.ok(nodes(paper.r.render(paperOutline.type, paperOutline.props)).some(node => node.type === 'SkeletonBlock' && node.props.width === 358 && node.props.height === 446));
});

test('reading loading delegates the quiet skeleton and adds a notice only after eight seconds', async t => {
  const r = renderer(), effects: (() => (() => void) | undefined)[] = [];
  r.react.useEffect = (effect: () => (() => void) | undefined) => effects.push(effect);
  const {ReadingLoading} = await screenModule(new URL('../src-next/screens/read/ReadingLoading.tsx', import.meta.url), {
    react: r.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/states': {LoadingBoundary: 'LoadingBoundary', SkeletonBlock: 'SkeletonBlock', skeletonTiming: {slowMs: 8000}},
    '../../kit/browse/presentation': {browseType: typography}, '../../kit/theme': {useTheme: () => theme(false)},
  });
  t.mock.timers.enable({apis: ['setTimeout']});
  const props = {ready: false, skeleton: {type: 'GridOutline', props: {}}};
  let tree = r.render(ReadingLoading, props); assert.equal(named(tree, 'LoadingBoundary').props.immediate, false);
  const cleanup = effects[0]!();
  t.mock.timers.tick(7999); tree = r.render(ReadingLoading, props); assert.equal(named(tree, 'Text'), undefined);
  t.mock.timers.tick(1); tree = r.render(ReadingLoading, props); assert.deepEqual(named(tree, 'Text').props.children, ['next.states.stillLoading']);
  cleanup?.(); tree = r.render(ReadingLoading, {...props, ready: true}); assert.equal(named(tree, 'Text'), undefined);
});
