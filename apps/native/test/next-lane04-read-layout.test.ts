import {createTheme, textLine} from '../src-next/kit/theme';
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
  const r = renderer(), {ReadingShelf} = await screenModule(new URL('../src-next/kit/reading/ReadingShelf.tsx', import.meta.url), {
    react: r.react, 'react-native': {FlatList: 'FlatList', View: 'View'}, '../states': {SkeletonBlock: 'SkeletonBlock'},
  });
  const data = Array.from({length: 500}, (_, id) => ({id: String(id)})); let more = 0;
  const memory = {offset: 3200}, props = {items: data, getKey: (item: any) => item.id, renderItem: () => null,
    width: 142, height: 279, artHeight: 213, gap: 14, gutter: 32, onNext: () => {more++;}, scrollMemory: memory};
  // The shelf's list sits in its TVRow (TV focus step 5).
  const list = (node: any) => node.type === 'FlatList' ? node : [node.props.children].flat()[0];
  let tree = list(r.render(ReadingShelf, props)); assert.equal(tree.type, 'FlatList'); assert.equal(tree.props.data, data);
  assert.equal(tree.props.initialNumToRender, 4); assert.equal(tree.props.windowSize, 5);
  tree.props.onLayout({nativeEvent: {layout: {width: 390}}}); tree = list(r.render(ReadingShelf, props));
  assert.equal(tree.props.onEndReachedThreshold, 10 * (142 + 14) / 390);
  assert.deepEqual(tree.props.getItemLayout(data, 20), {length: 156, offset: 3152, index: 20});
  assert.deepEqual(tree.props.contentOffset, {x: 3200, y: 0});
  tree.props.onScroll({nativeEvent: {contentOffset: {x: 3333}}}); assert.equal(memory.offset, 3333);
  tree.props.onEndReached(); assert.equal(more, 1);
  tree = list(r.render(ReadingShelf, {...props, pending: true})); assert.equal(tree.props.onEndReached, undefined);
  assert.equal(named(tree.props.ListFooterComponent, 'SkeletonBlock').props.height, 213);
});

test('phone card long press opens its anchored menu and selection tap does not open the book', async () => {
  const r = renderer(), {PhoneReadingBookCard} = await screenModule(new URL('../src-next/kit/reading/PhoneReadingBookCard.tsx', import.meta.url), {
    '../live/GuideProgrammeTitle': {GuideProgrammeTitle: 'GuideProgrammeTitle'}, '../layout/PreviewText': {PreviewText: 'PreviewText'},'../cards/model': cardModel,
    react: r.react, 'react-native': {Text: 'Text', View: 'View'}, '@portico/design': {generatedCoverTint: () => ({})},
    '../artwork': {StandardBackdrop: 'StandardBackdrop', GeneratedCover: 'GeneratedCover', MediaArt: 'MediaArt'}, '../controls': {Focusable: 'Focusable'},
    '../selection': {SelectionCircle: 'SelectionCircle'}, '../browse/presentation': {browseType: typography}, '../theme': {useTheme: () => createTheme({isTV: false, width: 390})},
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
  const width = tablet ? 932 : 390;
  const i18n = {defaultI18n: {t: (key: string) => key, number: String}, calendarDate: (_i18n: unknown, value: string) => value,
    calendarMonth: () => ({label: 'October 2026', offset: 4, weekdays: ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'], dates: Array.from({length: 31}, (_, index) => `2026-10-${String(index + 1).padStart(2, '0')}`)})};
  const native = {FlatList: 'FlatList', ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width, height: 844})};
  const useTheme = () => ({...createTheme({isTV: false, width}), ...theme(tablet)});
  const states = {SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'};
  const art = {Artwork: 'Artwork', AmbientArt: 'AmbientArt', GeneratedCover: 'GeneratedCover'};
  const parts = await screenModule(new URL('../src-next/kit/reading/PeriodicalParts.tsx', import.meta.url), {
    react: r.react, 'react-native': native, '@portico/design': {generatedCoverTint: () => ({})}, '../../../../../packages/i18n/src': i18n,
    '../artwork': art, '../controls': {Button: 'Button', Focusable: 'Focusable'}, '../Icon': {Icon: 'Icon'},
    '../overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'}, '../states': states, '../selection': {SelectionCircle: 'SelectionCircle'},
    '../cards/primitives': {Scrim: 'Scrim'}, '../browse/presentation': {alpha: (color: string, opacity: number) => `${color}/${opacity}`},
    '../theme': {useTheme, textLine}, '../../screens/read/periodical-presentation': presentation,
    '../../pages/shared/read/usePeriodicalActions': {usePeriodicalActions: () => ({})},
    './ReadingShelf': {ReadingShelf: 'ReadingShelf'}, './ReadingActions': {ReadingSelectionToolbar: 'ReadingSelectionToolbar'},
    '../live/GuideProgrammeTitle': {GuideProgrammeTitle: 'GuideProgrammeTitle'}, '../layout/PreviewText': {PreviewText: 'PreviewText'},
  });
  const frame = await screenModule(new URL('../src-next/kit/frames/ReadingPeriodicalFrame.tsx', import.meta.url), {
    react: r.react, 'react-native': native, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 44, bottom: 0})},
    '../theme': {useTheme}, '../artwork': art, '../states': states, '../reading/ReadingLoading': {ReadingLoading: 'ReadingLoading'},
    '../reading/PeriodicalParts': parts, '../layout/TopBar': {TopBar: 'TopBar'}, '../layout/page-inset': {usePageWidth: () => width},
  });
  const model = await screenModule(new URL('../src-next/pages/shared/read/useReadPeriodical.ts', import.meta.url), {
    react: r.react, '../../../../platform/offline-reading': {nativePublicationReadsAvailable: () => true},
    '../../../screens/downloads/request': {askNativeDownload: () => {}}, '../../../screens/read/periodical-presentation': presentation,
    './useReadingActions': {useReadingActions: () => actions},
  });
  const {ReadPeriodicalPage} = await screenModule(new URL('../src-next/pages/touch/read/ReadPeriodicalPage.tsx', import.meta.url), {
    react: r.react, '../../../../../../packages/i18n/src': i18n, '../../../kit/frames/ReadingPeriodicalFrame': frame,
    '../../../kit/reading/PeriodicalParts': parts, '../../../kit/reading/ReadingActions': {ReadingItemMenu: 'ReadingItemMenu'},
    '../../../kit/states': states, '../../shared/read/useReadPeriodical': model,
    '../../../kit/artwork': art, '../../../kit/frames': {TitleFrame: 'TitleFrame'}, '../../../kit/layout': {HeroMetadata: 'HeroMetadata'},
    '../../../kit/layout/page-inset': {usePageWidth: () => width}, '../../../kit/theme': {useTheme},
    '../../shared/read/useMagazineHead': {useMagazineHead: () => ({facts: [], primary: {label: 'Read', onPress() {}}})},
  });
  const page = r.render(ReadPeriodicalPage, {controller, route: {groupId: 'group', libraryId: 'books', publicationType: state.publicationType},
    context: {libraries: () => [{id: 'books', name: 'Books'}]}, runtime: {...runtime, onDestination: (value: unknown) => opens.push(value)}, onBack() {}});
  // A magazine's title page is a fragment (the frame and its item menu); the archive frame is a component to render.
  const tree = typeof page.type === 'function' ? r.render(page.type, page.props) : page;
  return {r, tree, state, selected, latest, opens, menus};
}

/** A magazine on a phone or tablet is a title page (phone-read 24): its issues are the TitleFrame's scroller. */
function magazineIssues(f: Awaited<ReturnType<typeof periodical>>) {
  const frame = named(f.tree, 'TitleFrame'), rowNode = frame.props.scroller('head', {paddingBottom: 1}, {at: 100, onChange() {}});
  return {frame, rowNode, list: f.r.render(rowNode.type, rowNode.props)};
}

test('magazine uses virtual year rows, the title page poster cover and 44pt year chips', async () => {
  const f = await periodical(true, true), {frame, rowNode, list} = magazineIssues(f);
  assert.equal(list.type, 'FlatList'); assert.equal(list.props.initialNumToRender, 4);
  assert.deepEqual(list.props.data.map((row: any) => [row.year, row.kind]), [['2026', 'heading'], ['2026', 'issues'], ['2025', 'heading'], ['2025', 'issues']]);
  assert.equal(rowNode.props.hero, 'head'); assert.equal(frame.props.cover.shape, 'poster');
  // phone-read 24: "Magazine · Books", the library a link to it.
  assert.equal(frame.props.kicker.kind, 'next.reader.magazine'); assert.equal(frame.props.kicker.label, 'Books');
  frame.props.kicker.onPress(); assert.deepEqual(f.opens.at(-1), {kind: 'library', libraryId: 'books'});
  const cover = frame.props.cover.node(190); assert.equal(cover.props.width, 190); assert.equal(cover.props.issue, f.latest);
  const chips = nodes(list.props.ListHeaderComponent).find(node => node.type === 'FlatList' && node.props.horizontal);
  const chip = chips.props.renderItem({item: f.state.years[0]}); assert.equal(chip.props.style.height, 44);
  assert.equal(chip.props.style.backgroundColor, '#409fd4/0.2');
  const shelf = list.props.renderItem({item: list.props.data[1]}); assert.equal(shelf.type, 'ReadingShelf');
  assert.equal(shelf.props.width, 150); assert.ok(shelf.props.height > shelf.props.artHeight);
});

test('newspaper phone renders real day choices and its 446pt front page opens the selected edition', async () => {
  const f = await periodical(false, false), bodyNode = named(f.tree, 'NewspaperEditions'), body = f.r.render(bodyNode.type, bodyNode.props);
  const strip = nodes(body).find(node => node.type === 'FlatList' && node.props.horizontal);
  // Every day is drawn: no initialScrollIndex (a strip shorter than the screen would never draw the days before it);
  // the chosen day is brought into view by scrolling, which getItemLayout makes exact.
  assert.equal(strip.props.data, f.state.days); assert.equal(strip.props.initialScrollIndex, undefined); assert.equal(typeof strip.props.getItemLayout, 'function');
  const front = nodes(body).find(node => node.type === 'Focusable' && node.props.accessibilityLabel === 'selected');
  assert.equal(front.props.style.height, 446); front.props.onPress(); assert.deepEqual(f.opens, [f.selected]);
  front.props.onLongPressAction(); assert.equal((f.menus[0] as any).item, f.selected.item);
  assert.equal((f.menus[0] as any).anchorRef, front.props.ref);
  assert.ok(named(body, 'BottomSheet'), 'Go to date uses a calendar sheet');
});

test('read issue artwork uses the approved dimming while preserving the selected ring independently', async () => {
  const f = await periodical(true, true), {list} = magazineIssues(f);
  const shelf = list.props.renderItem({item: list.props.data[1]}), cardNode = shelf.props.renderItem(f.selected), card = f.r.render(cardNode.type, cardNode.props);
  const artworkNode = named(card, 'IssueArtwork'), artwork = f.r.render(artworkNode.type, artworkNode.props), image = named(artwork, 'Artwork');
  assert.equal(image.props.saturation, .6); assert.equal(image.props.brightness, .66);
  // phone-read 24: under its year an issue is titled by its date alone, not the magazine's name again.
  assert.equal(named(card, 'GuideProgrammeTitle').props.title, 'October 1');
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
  const {ReadingLoading} = await screenModule(new URL('../src-next/kit/reading/ReadingLoading.tsx', import.meta.url), {
    react: r.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../states': {LoadingBoundary: 'LoadingBoundary', SkeletonBlock: 'SkeletonBlock', skeletonTiming: {slowMs: 8000}},
    '../browse/presentation': {browseType: typography}, '../theme': {useTheme: () => theme(false)},
  });
  t.mock.timers.enable({apis: ['setTimeout']});
  const props = {ready: false, skeleton: {type: 'GridOutline', props: {}}};
  let tree = r.render(ReadingLoading, props); assert.equal(named(tree, 'LoadingBoundary').props.immediate, false);
  const cleanup = effects[0]!();
  t.mock.timers.tick(7999); tree = r.render(ReadingLoading, props); assert.equal(named(tree, 'Text'), undefined);
  t.mock.timers.tick(1); tree = r.render(ReadingLoading, props); assert.deepEqual(named(tree, 'Text').props.children, ['next.states.stillLoading']);
  cleanup?.(); tree = r.render(ReadingLoading, {...props, ready: true}); assert.equal(named(tree, 'Text'), undefined);
});

// N5-4: an item the server can't serve now (`inventoryAvailable: false`) is drawn but takes no open, menu or selection, as on web.
const gone = (id: string, issueDate: string) => ({issueDate, item: {...issue(id, issueDate).item, inventoryAvailable: false}});

test('N5-4: an issue the server can\'t serve is drawn but opens nothing, and offers no menu or selection', async () => {
  const f = await periodical(true, true), {list} = magazineIssues(f);
  const shelf = list.props.renderItem({item: list.props.data[1]});
  const card = (value: any) => {const element = shelf.props.renderItem(value); return f.r.render(element.type, element.props);};
  const available = card(issue('here', '2026-10-02')), missing = card(gone('missing', '2026-10-03'));
  assert.notEqual(available.props.disabled, true);
  assert.equal(missing.props.disabled, true, 'not pressable');
  assert.deepEqual(missing.props.accessibilityState, {disabled: true}, 'and said so');
  assert.equal(missing.props.onLongPressAction, undefined, 'no menu');
});

test('N5-4: a newspaper front page the server can\'t serve takes no press', async () => {
  const f = await periodical(false, false);
  f.selected.item.inventoryAvailable = false;
  const tree = f.r.render(named(f.tree, 'NewspaperEditions').type, named(f.tree, 'NewspaperEditions').props);
  const front = nodes(tree).find(node => node.type === 'Focusable' && node.props.accessibilityLabel === 'selected');
  assert.equal(front.props.disabled, true);
});

test('N5-4: a series volume the server can\'t serve takes no open, menu or selection', async () => {
  const r = renderer();
  const {VolumeCard} = await screenModule(new URL('../src-next/kit/reading/VolumeCard.tsx', import.meta.url), {
    react: r.react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0})},
    '@portico/design': {generatedCoverTint: () => ({})}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, number: String}},
    '../artwork': {Artwork: 'Artwork', AmbientArt: 'AmbientArt', GeneratedCover: 'GeneratedCover', StandardBackdrop: 'StandardBackdrop'},
    '../controls': {Button: 'Button', Focusable: 'Focusable'}, '../Icon': {Icon: 'Icon'}, '../layout': {WindowedGrid: 'WindowedGrid'},
    './reading-window': {NativeReadingWindow: class {}}, './ReadingActions': {ReadingItemMenu: 'ReadingItemMenu', ReadingSelectionToolbar: 'ReadingSelectionToolbar', useReadingActions: () => ({})},
    '../selection': {SelectionCircle: 'SelectionCircle'}, '../downloads/request': {askNativeDownload: () => {}}, '../../../platform/offline-reading': {nativePublicationReadsAvailable: () => true},
    '../states': {StateView: 'StateView', SkeletonBlock: 'SkeletonBlock', LoadingPaint: 'LoadingPaint'},
    '../browse/presentation': {alpha: (color: string, opacity: number) => `${color}/${opacity}`, browseType: typography}, '../theme': {textLine: () => 20, useTheme: () => theme(false)},
    '../live/GuideProgrammeTitle': {GuideProgrammeTitle: 'GuideProgrammeTitle'}, '../layout/PreviewText': {PreviewText: 'PreviewText'},
    '../layout/page-inset': {usePageWidth: () => 390},
  });
  const opens: string[] = [], menus: string[] = [], toggles: string[] = [];
  const card = (inventoryAvailable: boolean) => r.render(VolumeCard, {item: {id: 'volume', libraryId: 'books', title: 'Volume', inventoryAvailable}, width: 100, height: 150, comic: false,
    theme: {...theme(false), type: {}, space: {xs: 8}}, runtime: {artwork: () => undefined}, onOpen: () => opens.push('open'), onMenu: () => menus.push('menu'),
    selection: {active: true, selected: false, onToggle: () => toggles.push('toggle')}});
  const here = card(true), missing = card(false);
  here.props.onPress(); assert.deepEqual(toggles, ['toggle'], 'an available volume toggles in a selection');
  assert.equal(missing.props.disabled, true);
  assert.deepEqual(missing.props.accessibilityState, {disabled: true});
  assert.equal(missing.props.onLongPressAction, undefined);
  assert.equal(nodes(missing).some(node => node.type === 'SelectionCircle'), false, 'never part of a selection');
});

test('series page: a title page with Back, the reading direction, the server-selected Read action, and loading until the first volumes', async () => {
  const r = renderer();
  const usable = {id: 'readable', title: 'Volume 2', inventoryAvailable: true};
  const events: unknown[] = [];
  const state = {phase: 'loading', items: [] as unknown[], direction: 'rtl'};
  const model = {state, actions: {actions: {show() {}}, selection() {}}, title: 'Series', facts: ['Author'], readable: usable, readLabel: 'Read volume 2',
    covers: [usable], collection: {}, offer() {}, open: (item: unknown) => events.push(item), retry() {}, more() {}};
  const component = (name: string) => name;
  const {ReadSeriesPage} = await screenModule(new URL('../src-next/pages/touch/read/ReadSeriesPage.tsx', import.meta.url), {
    react: r.react, '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, number: String}},
    '../../../../../../packages/client-core/src': {},
    '../../../kit/artwork': {AmbientArt: component('AmbientArt'), Artwork: component('Artwork'), missingArt: () => null},
    '../../../kit/frames': {PanelBlock: 'PanelBlock', PanelHeading: 'PanelHeading', StateRow: 'StateRow', TitleFrame: 'TitleFrame',
      posterGridScroller: () => 'grid', titleRowsScroller: () => 'rows'},
    '../../../kit/layout': {HeroMetadata: 'HeroMetadata'},
    '../../../kit/reading/ReadingActions': {ReadingSelectionToolbar: 'Toolbar', ReadingItemMenu: 'Menu'},
    '../../../kit/theme': {useTheme: () => theme(false)}, '../../shared/read/useReadSeries': {useReadSeries: () => model, readingVolumeKey: () => 'k'},
  });
  const props = {route: {seriesId: 'one'}, comic: true, runtime: {artwork: () => undefined, error: () => ({title: 'error'})}, onBack: () => events.push('back')};
  const frame = (tree: any) => [tree, ...[tree.props?.children].flat()].find((node: any) => node?.type === 'TitleFrame');
  assert.deepEqual(frame(r.render(ReadSeriesPage, props)).props.state, {kind: 'loading'});
  state.phase = 'ready';
  const page = frame(r.render(ReadSeriesPage, props));
  assert.equal(page.props.state, undefined);
  assert.equal(page.props.scroller, 'grid', 'a comic draws its volumes as a grid');
  assert.deepEqual(page.props.metadata.props.facts.map((fact: any) => fact.text), ['Author', 'next.reader.directionRtl']);
  assert.equal(page.props.primary.label, 'Read volume 2');
  page.props.primary.onPress();
  page.props.back.onPress();
  assert.deepEqual(events, [usable, 'back']);
});
