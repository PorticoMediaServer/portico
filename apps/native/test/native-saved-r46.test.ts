// @fabric-react
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {createTheme, fontFamilies} from '../src-next/kit/theme.ts';
import {browseType} from '../src-next/kit/browse/presentation.ts';

type Element = {type: any; props: Record<string, any>};
const elements = (node: any): Element[] => Array.isArray(node) ? node.flatMap(elements) : node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
const flat = (style: any): Record<string, any> => Array.isArray(style) ? style.reduce((out, part) => Object.assign(out, flat(part)), {}) : {...(style ?? {})};
const text = (node: Element | undefined): string => ([] as any[]).concat(node?.props.children ?? []).join('');
const theme = createTheme({width: 390, isTV: false});
const childrenOf = (node: Element): any[] => ([] as any[]).concat(node.props.children ?? []);

/** Mounts one shallow component under the real hook driver. React's own hook
 * dispatcher runs the screen's effects, so layout effects commit and unmount runs
 * every cleanup and store release. No reconciler draws the tree. */
async function mount(component: (props: any) => any, props: any) {
  let tree: any;
  const host = createFabricHookHost(() => {tree = component(props);});
  await host.mount();
  return {host, get tree() {return tree;}};
}
const drawn = {
  largeTitle: 32,
  largeTitleLine: 36.8,
  trackPadTop: 8,
  trackPadBottom: 10,
  trackGap: 12,
  rowMargin: 28,
  firstRowMargin: 2,
  headingMarginBottom: 4,
  inlineRowCaption: 12.5,
};

const t = (id: string, values?: Record<string, any>) => id === 'title.kind' ? ({collection: 'Collection', playlist: 'Playlist'} as Record<string, string>)[values?.kind as string] : id;

async function savedScreen() {
  const opened: any[] = [];
  const entry = {id: 'sev', kind: 'show', title: 'Severance', displayTitle: '', posterUrl: '/sev.jpg'};
  const projection = {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: '', viewerFence: 'actual-fence'},
    revision: {catalog: 1, viewer: 1}, navigation: [], sorts: [], filters: [], query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'},
    sections: [{id: 'items', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [entry], totalCount: 1, nextCursor: ''}]};
  const snapshot = {phase: 'ready', myList: {projection}, myListSquares: {projection: undefined}, favorites: {}, playlists: {}, collections: {}, views: {},
    pins: [{kind: 'collection', id: 'toy', name: 'Toy Story', pinRevision: 4}],
    summary: {myList: {posters: 1, squares: 0}, favorites: {posters: 24, squares: 0}, playlists: 3, collections: 1, savedViews: 2}};
  const downloads = {supported: true, entries: [], usedBytes: 3.4e9};
  const {SavedScreen} = await screenModule(new URL('../src-next/screens/saved/SavedScreen.tsx', import.meta.url), {
    '../../kit/selection': {SelectionCircle: 'SelectionCircle'}, './SavedLoading': {SavedLoading: 'SavedLoading', SavedTail: 'SavedTail'},
    './paging': {savedNearEnd: () => false},
    react: {...React, default: React}, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 24})},
    '../../../../../packages/i18n/src': {defaultI18n: {t, bytes: () => '3.4 GB', number: (value: number) => String(value)}},
    '../../kit/cards': {PosterCard: 'PosterCard', SquareCard: 'SquareCard'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame', Parts: 'Parts'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/overlays': {Menu: 'Menu'},
    '../../kit/controls': {Button: 'Button', Chip: 'Chip', Focusable: 'Focusable', Tabs: 'Tabs'},
    '../../kit/states/usePullRefresh': {usePullRefresh: () => undefined}, '../../kit/dock/DockClearance': {useDockClearance: () => 0}, '../../kit/states': {StateView: 'StateView'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType, alpha: (color: string) => color},
    '../../kit/theme': {useTheme: () => theme},
    '../../platform/offline-downloads': {groupOfflineEntries: () => []},
    '../home/model': {homeCardText: () => ({caption: []})},
    './model': {NativeSavedDestination: class {}}, './SavedDownloadCard': {SavedDownloadCard: 'SavedDownloadCard'},
    '../downloads/playback': {openNativeSavedDownload: () => {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError},
  });
  const actions = {openView: (view: unknown) => opened.push(view), openEntry: (item: unknown) => opened.push(item), menuEntry: () => {}, openDownloads: () => opened.push('downloads'),
    openPin: (pin: unknown) => opened.push(pin), editPins: () => opened.push('edit')};
  const screen = await mount(SavedScreen, {destination: {subscribe() {}, getSnapshot: () => snapshot, load: async () => {}}, runtime: {text: {}, artwork: () => undefined, reportError() {}, registerScrollToTop() {}},
    device: {subscribe() {}, getSnapshot: () => downloads, open: async () => {}}, actions, onThisPhone: 'next.saved.phone.onThisPhone', downloadsCopy: {downloading: 'next.saved.phone.downloading'},
    activation: {subscribe() {}, getSnapshot: (() => {const snapshot = {visible: true}; return () => snapshot;})()}});
  return {screen, tree: screen.tree, entry, opened};
}

test('Saved phone body keeps the drawn large title, shelf and track rhythm', async () => {
  const {screen, tree} = await savedScreen();
  const mounted: Array<Awaited<ReturnType<typeof mount>>> = [];
  try {
    const scroll = elements(tree).find(node => node.type === 'ScrollView' && node.props.contentContainerStyle?.paddingTop !== undefined)!;
    assert.equal(scroll.props.contentContainerStyle.paddingTop, 58, 'phone-app9 .lt starts 58 below the top');
    const title = elements(tree).find(node => node.type === 'Text' && node.props.accessibilityRole === 'header' && text(node) === 'saved.title')!;
    const titleStyle = flat(title.props.style);
    assert.equal(titleStyle.fontSize, drawn.largeTitle, '.lt h1 is 32');
    assert.equal(titleStyle.letterSpacing, -.035 * drawn.largeTitle);
    assert.equal(titleStyle.lineHeight, drawn.largeTitleLine, '.lt h1 line-height 1.15');
    const titleBlock = elements(tree).find(node => node.type === 'View' && childrenOf(node).some(child => child === title))!;
    assert.deepEqual({paddingHorizontal: flat(titleBlock.props.style).paddingHorizontal, paddingBottom: flat(titleBlock.props.style).paddingBottom}, {paddingHorizontal: 20, paddingBottom: 12});

    const drawnSections = elements(tree).filter(node => node.type?.name === 'SavedSection');
    const sections = await Promise.all(drawnSections.map(async node => {const rendered = await mount(node.type, node.props); mounted.push(rendered); return flat(rendered.tree.props.style);}));
    assert.deepEqual(sections.map(style => style.marginTop), [drawn.firstRowMargin, drawn.rowMargin], 'My List follows the title by 2; Downloads is a 28 shelf');

    const tracks = elements(tree).filter(node => node.type === 'ScrollView' && node.props.horizontal === true);
    assert.equal(tracks.length, 1, 'the drawn My List track');
    assert.deepEqual({paddingHorizontal: tracks[0].props.contentContainerStyle.paddingHorizontal, paddingTop: tracks[0].props.contentContainerStyle.paddingTop,
      paddingBottom: tracks[0].props.contentContainerStyle.paddingBottom, gap: tracks[0].props.contentContainerStyle.gap},
      {paddingHorizontal: 20, paddingTop: drawn.trackPadTop, paddingBottom: drawn.trackPadBottom, gap: drawn.trackGap});
    const card = elements(tree).find(node => node.type?.name === 'SavedMyListCard')!;
    const rendered = await mount(card.type, card.props); mounted.push(rendered);
    assert.equal(elements(rendered.tree).find(node => node.type === 'PosterCard')!.props.width, undefined, 'cards use the canonical 118 phone width, not a local override');
  } finally {
    for (const value of mounted) await value.host.unmount();
    await screen.host.unmount();
  }
});

test('Saved row headings and links match the drawn phone parts', async () => {
  const {screen, tree} = await savedScreen();
  const mounted: Array<Awaited<ReturnType<typeof mount>>> = [];
  try {
    const heading = elements(tree).find(node => node.type?.name === 'SavedRowHeading' && node.props.testID === 'saved-my-list')!;
    const headingMount = await mount(heading.type, heading.props); mounted.push(headingMount);
    const view = headingMount.tree;
    const headingStyle = flat(view.props.style);
    assert.equal(headingStyle.gap, 14, '.row-head gap');
    assert.equal(headingStyle.marginBottom, drawn.headingMarginBottom, 'phone .row-head margin-bottom 4');
    assert.equal(headingStyle.marginHorizontal, 20);
    const headingText = elements(view).find(node => node.type === 'Text' && node.props.accessibilityRole === 'header')!;
    assert.equal(flat(headingText.props.style).fontSize, 19, 'phone .row-head h2 is 19');
    const detail = elements(view).find(node => node.type === 'Text' && text(node) === 'next.selection.titles')!;
    assert.deepEqual({size: flat(detail.props.style).fontSize, family: flat(detail.props.style).fontFamily}, {size: 13, family: fontFamilies[400]}, 'the count is 13 regular');

    const link = elements(tree).find(node => node.props.testID === 'saved-view-favorites')!;
    const linkMount = await mount(link.type, link.props); mounted.push(linkMount);
    const row = linkMount.tree;
    const rowStyle = flat(elements(row).find(node => node.type === 'Focusable')!.props.style);
    assert.deepEqual({minHeight: rowStyle.minHeight, paddingVertical: rowStyle.paddingVertical, paddingHorizontal: rowStyle.paddingHorizontal, gap: rowStyle.gap},
      {minHeight: 48, paddingVertical: 8, paddingHorizontal: 14, gap: 12});
    const tile = flat(elements(row).find(node => node.type === 'View' && node.props.style?.width === 30)!.props.style);
    assert.deepEqual({width: tile.width, height: tile.height, borderRadius: tile.borderRadius}, {width: 30, height: 30, borderRadius: 8});

    const pin = elements(tree).find(node => node.props.testID === 'saved-pin-collection-toy')!;
    const pinMount = await mount(pin.type, pin.props); mounted.push(pinMount);
    const caption = elements(pinMount.tree).find(node => node.type === 'Text' && text(node) === 'Collection')!;
    assert.deepEqual({size: flat(caption.props.style).fontSize, family: flat(caption.props.style).fontFamily, marginTop: flat(caption.props.style).marginTop},
      {size: drawn.inlineRowCaption, family: fontFamilies[500], marginTop: 2}, 'the drawn Collection caption under a pinned name');
  } finally {
    for (const value of mounted) await value.host.unmount();
    await screen.host.unmount();
  }
});

async function downloadCard(entry: Record<string, any>, entries: readonly Record<string, any>[] = [entry]) {
  const host = hooks();
  const {SavedDownloadCard} = await screenModule(new URL('../src-next/screens/saved/SavedDownloadCard.tsx', import.meta.url), {
    react: {...host.react, useId: () => 'gradient'}, 'react-native': {Text: 'Text', View: 'View'},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id, bytes: () => '1.4 GB'}},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/cards/primitives': {ArtFrame: 'ArtFrame', CardFrame: 'CardFrame', Progress: 'Progress'},
    '../../kit/browse/presentation': {browseType}, '../../kit/theme': {useTheme: () => theme},
    '../downloads/presentation': {downloadEntryIdentity: (value: any) => value.id, downloadTransferProgress: () => .64},
    '../downloads/useDownloadArtwork': {useDownloadArtwork: () => undefined},
  });
  const group = {key: 'slow', title: 'Slow Horses', entries};
  return host.render(() => SavedDownloadCard({group, device: {subscribe() {}, getSnapshot: (() => {const snapshot = {entries: []}; return () => snapshot;})()} as any, copy: {downloading: 'next.saved.phone.downloading'}, onOpen: () => {}}));
}

test('Saved downloads keep the drawn 214 wide on-art card and its progress', async () => {
  const ready = {id: 'ep1', title: 'Slow Horses', state: 'ready', waiting: false, bytes: 1.4e9, bytesDone: 0, info: {kind: 'episode'}};
  const card = await downloadCard(ready, [ready, {...ready, id: 'ep2'}]);
  const frame = elements(card).find(node => node.type === 'CardFrame')!;
  assert.equal(frame.props.width, 214, 'the frame overrides the shelf card to 214');
  const artElement = frame.props.children[0]({focused: false, hovered: false, holding: false});
  const art = elements(artElement);
  assert.equal(artElement.props.height, 214 * 9 / 16);
  assert.equal(artElement.props.radius, theme.spec.card.radius.phone, 'the shared card radius, not a local 8');
  const caption = art.find(node => node.type === 'View' && node.props.style?.left === 14 && node.props.style?.bottom === 14)!;
  assert.deepEqual({left: caption.props.style.left, right: caption.props.style.right, bottom: caption.props.style.bottom}, {left: 14, right: 14, bottom: 14});
  const title = art.find(node => node.type === 'Text' && text(node) === 'Slow Horses')!;
  assert.equal(flat(title.props.style).fontSize, 16, '.wide .on-art b is 16');
  const facts = art.find(node => node.type === 'View' && node.props.style?.gap === 12)!;
  assert.equal(facts.props.style.alignItems, 'baseline');

  const downloading = await downloadCard({...ready, state: 'downloading', bytes: 0, bytesDone: 2.1e9});
  const downloadFrame = elements(downloading).find(node => node.type === 'CardFrame')!;
  const downloadingArt = elements(downloadFrame.props.children[0]({focused: false, hovered: false, holding: false}));
  const progress = downloadingArt.find(node => node.type === 'Progress')!;
  assert.deepEqual({value: progress.props.value, height: progress.props.height, marginTop: flat(progress.props.style).marginTop}, {value: .64, height: 4, marginTop: 10});
  assert.ok(downloadingArt.some(node => node.type === 'Text' && text(node) === 'next.saved.phone.downloading'), 'the drawn Downloading, N% line');
});
