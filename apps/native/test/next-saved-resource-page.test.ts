import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const t = (key: string) => key;
const playlist = {pinned: false, summary: 'Films we always come back to.', actions: ['pin', 'summary']};
const song = {id: 'song-1', kind: 'song', title: 'Come Together', year: 1969, duration: 259};

function view(overrides: Record<string, unknown> = {}) {
  const calls: string[] = [];
  return {calls, value: {
    active: {visible: true}, phase: 'ready', playlist, resource: null, personal: undefined, isPlaylist: true,
    rows: [{id: 'row-1', media: song}], media: [song], canPlayResource: true, count: 1, countText: '1 item', duration: 259,
    artwork: ['/art/abbey.jpg'], name: 'Weekend', eyebrow: 'Playlist', query: {}, manual: true,
    offers: {sorts: [], filters: []}, chooseQuery() {}, reorderGranted: true, canReorder: true, visibility: undefined,
    dragging: false, setDragging() {}, reordering: false, setReordering: (value: boolean) => calls.push(`reorder:${value}`),
    infoOpen: false, setInfoOpen() {}, busy: false, problem: '', clearProblem() {}, stateError: undefined, retry() {},
    next: () => calls.push('next'), play: (shuffle?: boolean) => calls.push(shuffle ? 'shuffle' : 'play'), pin: () => calls.push('pin'),
    setVisibility() {}, openEntryMenu: () => calls.push('row-menu'), moveRow() {}, placeRow() {}, openPageMenu: () => calls.push('page-menu'),
    retryPersonal() {}, filtersOpen: false, setFiltersOpen() {}, filtersAnchor: {current: null}, pageMenu: false, menuItems: [],
    setPageMenu() {}, rowMenu: undefined, rowMenuItems: [], closeRowMenu() {}, renaming: undefined, saveName() {}, nameDraft: undefined,
    closeName() {}, deleting: undefined, confirmDelete() {}, closeDelete() {}, toast: null, clearToast() {},
    ...overrides,
  }};
}

async function page(current: ReturnType<typeof view>) {
  const hook = {
    useSavedResource: () => current.value,
    resourceSortKeys: {},
    savedResourceRowItem: (row: any, _runtime: any, _actions: any, onMore: () => void) => ({id: row.id, title: row.media.title, facts: [], onMore}),
    savedViewGridItem: (entry: any) => ({id: entry.id, title: entry.title}),
  };
  const component = (name: string) => name;
  return screenModule(new URL('../src-next/pages/touch/saved/SavedResourcePage.tsx', import.meta.url), {
    react: await import('react'),
    '../../../../../../packages/i18n/src': {defaultI18n: {t, number: String}},
    '../../../../../../packages/client-core/src/saved': {},
    '../../../../../../packages/client-core/src/presentation/kit-model/text': {durationText: () => '4 min'},
    '../../../kit/artwork': {AmbientArt: component('AmbientArt'), CoverMosaic: component('CoverMosaic')},
    '../../../kit/browse': {ChipRail: component('ChipRail')},
    '../../../kit/controls': {Chip: component('Chip')},
    '../../../kit/frames': {musicScroller: () => 'scroller', PanelBlock: component('PanelBlock'), PosterGridWindow: component('PosterGridWindow'),
      SheetFrame: component('SheetFrame'), StateRow: component('StateRow'), TitleFrame: component('TitleFrame'), TitleRowWindow: component('TitleRowWindow')},
    '../../../kit/layout': {HeroMetadata: component('HeroMetadata')},
    '../../../kit/overlays': {Banner: component('Banner'), BottomSheet: component('BottomSheet'), Menu: component('Menu')},
    '../../../kit/saved/SavedForms': {SavedConfirmSheet: component('SavedConfirmSheet'), SavedNameSheet: component('SavedNameSheet')},
    '../../../kit/saved/SavedReorderList': {SavedReorderList: component('SavedReorderList')},
    '../../../kit/saved/SavedResourceParts': {SavedResourceMenuFacts: component('Facts'), SavedResourceUndo: component('Undo'),
      SavedResourceViewInfo: component('ViewInfo'), SavedResourceVisibility: component('Visibility'), viewSortLabel: () => 'Newest first'},
    '../../../kit/saved/SavedSortMenu': {SavedSortMenu: component('SavedSortMenu')},
    '../../../screens/saved/presentation': {savedLibraryName: () => undefined},
    '../../shared/library/useLibrary': {browseFailure: (value: unknown) => ({failed: value})},
    '../../shared/saved/useSavedResource': hook,
  });
}

const props: any = {runtime: {artwork: (path: string) => ({uri: path}), ambient: (path: string) => ({uri: path}), error: () => ({title: 'Failed'})},
  actions: {}, libraries: [], back() {}};
const flat = (node: any): any[] => !node || typeof node !== 'object' ? []
  : [node, ...[node.props?.children].flat(Infinity).flatMap(flat)];

test('a playlist is a title page with Play, Shuffle and Pin, its rows, and Reorder opening its own full page', async () => {
  const current = view(), {SavedResourcePage} = await page(current);
  const tree = SavedResourcePage(props), nodes = flat(tree);
  const frame = nodes.find(node => node.type === 'TitleFrame');
  assert.equal(frame.props.title, 'Weekend');
  assert.equal(frame.props.kicker.label, 'Playlist');
  frame.props.primary.onPress(); frame.props.actions[0].onPress(); frame.props.actions[1].onPress(); frame.props.topAction.onPress();
  assert.deepEqual(current.calls, ['play', 'shuffle', 'pin', 'page-menu']);
  const rows = nodes.find(node => node.type === 'TitleRowWindow');
  assert.equal(rows.props.rows[0].title, 'Come Together');
  rows.props.rows[0].onMore(); assert.equal(current.calls.at(-1), 'row-menu');
  assert.equal(nodes.some(node => node.type === 'SheetFrame'), false, 'the reorder page is mounted only while reordering');
  nodes.find(node => node.type === 'Chip' && node.props.testID === 'saved-reorder').props.onPress();
  assert.equal(current.calls.at(-1), 'reorder:true');
});

test('a saved view draws its titles as a poster grid; an empty playlist says so and cannot play', async () => {
  const savedView = view({isPlaylist: false, playlist: null, resource: {kind: 'view', actions: [], definition: {sort: [{field: 'added'}]}}});
  let nodes = flat((await page(savedView)).SavedResourcePage(props));
  assert.ok(nodes.some(node => node.type === 'PosterGridWindow'));
  assert.equal(nodes.some(node => node.type === 'TitleRowWindow'), false);
  const empty = view({rows: [], media: [], canPlayResource: false, count: 0, countText: '0 items'});
  nodes = flat((await page(empty)).SavedResourcePage(props));
  assert.equal(nodes.find(node => node.type === 'TitleFrame').props.primary.disabled, true);
  assert.equal(nodes.find(node => node.type === 'StateRow').props.title, 'web.saved.playlistEmpty');
});
