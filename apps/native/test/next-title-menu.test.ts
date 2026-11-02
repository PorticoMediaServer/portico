import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {entryActions, type EntryCapabilities} from '../../../packages/client-core/src/presentation/entry-actions.ts';
import {homeEntryCapabilities} from '../../../packages/client-core/src/home-selection.ts';
import {titleMenuItems, titleMenuRowIds} from '../src-next/kit/overlays/title-menu.ts';
import {nativeWatchMenuCapabilities, nativeWatchMenuGroups} from '../src-next/screens/watch/menu-model.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// FX-S3/FX-S4: one title action menu on every surface. The model decides the rows; one renderer draws them.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const t = defaultI18n.t;
const row = (id: string): any => ({id, label: id, icon: 'i-film', onSelect() {}});
const ids = (items: readonly any[]) => items.map(item => item.id);

const entry = {id: 'film', kind: 'movie', title: 'Actual film', libraryId: 'library'} as any;
const everyGrant = ['play', 'watchlist', 'favorite', 'watched', 'rating', 'add_to_collection', 'metadata_edit', 'delete'];
const detail = (grants = everyGrant, personal: Record<string, unknown> = {}): any => ({
  personal: {watchlisted: false, favorite: false, watched: false, rating: null, progressSeconds: 0, ...personal},
  actions: grants.map(id => ({id, enabled: true, ...(id === 'play' ? {playback: {itemId: 'film', startSeconds: 0}} : {})})),
});
const noop = async (): Promise<void> => {};
const fullMenu = (without: string[] = []): any => Object.fromEntries(Object.entries({play: noop, enqueue: noop, addToPlaylist() {}, addToCollection() {}, rate() {}, download() {}, details() {},
  editMetadata() {}, refreshMetadata: noop, deleteTitle() {}, mutate: noop, dispose() {}, subscribe: () => () => {}, current: (): undefined => undefined}).filter(([key]) => !without.includes(key)));

test('the Watch menu draws the shared model in the mock-ups\' order on touch: personal, organize, host rows, Details, then the Owner group', () => {
  const groups = nativeWatchMenuGroups({entry, detail: detail(), menu: fullMenu()});
  const items = titleMenuItems(groups, {tv: false, run() {}, afterPlay: [row('start-radio')], surface: [row('different-version'), row('select'), row('file-info')], trailing: [row('share')]});
  assert.deepEqual(ids(items), ['play', 'start-radio', 'watchlist', 'watched', 'favorite', 'rate', 'addToPlaylist', 'addToCollection', 'download',
    'different-version', 'select', 'file-info', 'details', 'share', 'group:manage', 'editMetadata', 'refreshMetadata', 'delete'], 'select.html 17: Select above File info, the Owner group last');
  const owner = items.find(item => item.id === 'group:manage') as any;
  assert.equal(owner.label, t('next.selection.owner')); assert.equal(owner.tone, 'owner');
  const remove = items.find(item => item.id === 'delete') as any;
  assert.equal(remove.destructive, true); assert.equal(remove.tone, 'owner');
  assert.equal((items.find(item => item.id === 'play') as any).label, t('entry.play'));
});

test('the Watch menu keeps the model\'s groups on TV, with owner rows and no Download', () => {
  const groups = nativeWatchMenuGroups({entry, detail: detail(), menu: fullMenu(['download']), together: {queue: true, watch: true}});
  const items = titleMenuItems(groups, {tv: true, run() {}, afterPlay: [row('start-radio')], surface: [row('different-version'), row('file-info'), row('select')]});
  assert.deepEqual(ids(items).filter(id => !id.startsWith('separator') && id !== 'surface-separator' && id !== 'group:together'), ['play', 'start-radio', 'playNext', 'addToQueue', 'watchWithGroup', 'addToGroupQueue',
    'watchlist', 'favorite', 'watched', 'rate', 'addToPlaylist', 'addToCollection', 'different-version', 'file-info', 'select', 'details', 'group:manage', 'editMetadata', 'refreshMetadata', 'delete']);
  assert.equal(ids(items).includes('download'), false, 'TV has no Download (select.html 23)');
  assert.ok(ids(items).includes('delete'), 'owner rows are offered on TV (DESIGN/Q45 overrides select.html 23\'s "no owner area")');
  assert.equal((items.find(item => item.id === 'group:manage') as any).label, t('entry.group.manage'));
});

test('collection, Rate and the owner rows appear only when the menu can run them and the title grants them', () => {
  const rowsFor = (grants: string[], menu: any) => ids(titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(grants), menu}), {tv: false, run() {}}));
  assert.deepEqual(rowsFor(['play'], fullMenu(['addToCollection', 'rate', 'editMetadata', 'refreshMetadata', 'deleteTitle', 'download'])), ['play', 'addToPlaylist', 'details']);
  assert.equal(rowsFor(everyGrant, fullMenu(['addToCollection'])).includes('addToCollection'), false, 'no executor, no row');
  assert.equal(rowsFor(['play', 'watchlist'], fullMenu()).includes('rate'), false, 'Rate needs the title\'s rating grant');
  const member = rowsFor(everyGrant, fullMenu(['editMetadata', 'refreshMetadata', 'deleteTitle']));
  assert.ok(member.includes('addToCollection') && member.includes('rate'));
  assert.equal(member.some(id => ['group:manage', 'editMetadata', 'refreshMetadata', 'delete'].includes(id)), false, 'a member has no Owner group');
  assert.deepEqual(rowsFor(everyGrant, fullMenu()).slice(-4), ['group:manage', 'editMetadata', 'refreshMetadata', 'delete']);
});

test('the Watch menu\'s capabilities carry personal state, progress and the toggles\' current values into the shared model', () => {
  const caps = nativeWatchMenuCapabilities({entry, detail: detail(everyGrant, {watchlisted: true, watched: true, rating: 8, progressSeconds: 90}), menu: fullMenu()});
  assert.equal(caps.watchlisted, true); assert.equal(caps.watched, true); assert.equal(caps.rating, 8); assert.equal(caps.progressSeconds, 90);
  const groups = nativeWatchMenuGroups({entry, detail: detail(everyGrant, {progressSeconds: 90}), menu: fullMenu()});
  assert.equal(groups.flatMap(group => group.actions).some(action => action.id === 'playFromStart'), false, 'Play from beginning needs the server\'s start-over target');
  assert.equal(groups.flatMap(group => group.actions).some(action => action.id === 'resume'), true);
  const labels = titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(everyGrant, {watchlisted: true, watched: true, rating: 8}), menu: fullMenu()}), {tv: false, run() {}});
  assert.equal((labels.find(item => item.id === 'watchlist') as any).selected, true);
  assert.equal((labels.find(item => item.id === 'rate') as any).label, t('entry.rate'), 'phone and tablet say Rate…, not the rated state');
});

test('a personal toggle is disabled while a write is under way, and every row runs the model action it was drawn from', () => {
  const calls: string[] = [];
  const items = titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(), menu: fullMenu()}), {tv: false, run: id => calls.push(id), busy: true});
  assert.deepEqual(items.filter((item: any) => item.disabled).map(item => item.id), ['watchlist', 'watched', 'favorite']);
  for (const item of items as any[]) item.onSelect?.();
  assert.deepEqual(calls, titleMenuRowIds(items));
});

// The same capabilities draw the same rows in Home's More menu and in the Watch menu behind every other card.
const homeActions = {play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, played: true, rating: true,
  editMetadata: true, refreshMetadata: true, delete: true, download: true, details: true,
  state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 1, containerRevision: 1},
  selection: {play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, download: true, edit: true, refresh: true, delete: true}};
const theme = (tv: boolean) => ({isTV: tv, layoutMode: tv ? 'tv' : 'phone', platform: tv ? 'tv' : 'phone', type: {caption: {}, body: {}}, colors: {}, textScale: 1});

async function overlays(tv: boolean) {
  const h = hooks();
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const shared = await screenModule(new URL('../src-next/kit/overlays/title-menu.ts', import.meta.url), {react: h.react, '../../../../../packages/i18n/src': {defaultI18n}});
  const {HomeSelectionOverlays} = await screenModule(new URL('../src-next/shell/HomeSelectionOverlays.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View', StyleSheet: {create: (styles: object) => styles}}, '../../../../packages/i18n/src': {defaultI18n},
    '../../../../packages/client-core/src/presentation/errors': {}, '../../../../packages/client-core/src/presentation/server-text': {serverTextLabel: () => ''},
    '../kit/shell': {ServerMark: 'ServerMark'}, '../kit/overlays': {Menu: 'Menu'}, '../kit/overlays/title-menu': shared, '../kit/selection': {DeleteMediaDialog: 'Delete', MixedEditExplainer: 'Mixed', RatingDialog: 'Rating'},
    '../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'}, '../kit/artwork': {}, '../kit/Icon': {}, '../kit/theme': {useTheme: () => theme(tv)},
    '../platform/haptics': {selectionHaptic: async () => {}}, './selection-display': {memberTitle: () => 'Actual film'}, './HomeDestinationPicker': {HomeDestinationPicker: 'Picker'},
    './HomeBulkEditor': {HomeBulkEditor: 'Editor'}, './tv-focus': {tvFocusIds: () => ({menu: () => 'menu', surface: () => 'surface'})},
  });
  const {NativeWatchOverlays} = await screenModule(new URL('../src-next/screens/watch/WatchOverlays.tsx', import.meta.url), {
    react: h.react, 'react-native': {Modal: 'Modal', Platform: {isTV: tv}, ScrollView: 'ScrollView', StyleSheet: {}, Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/administration': {}, '../../../../../packages/client-core/src/title-identity': {},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {}, '../../shell/model': {}, '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', Menu: 'Menu'},
    '../../kit/overlays/title-menu': shared, './menu-model': {nativeWatchMenuGroups}, '../../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'}, '../../kit/overlays/surface': {},
    '../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})}, '../../kit/overlays/OverlayHost': {}, '../../kit/theme': {nativeType: () => ({}), useTheme: () => theme(tv)},
    './VersionChooser': {}, './DiscTitlesSheet': {}, './FileInfoContent': {}, './file-info': {},
    '../../kit/overlays/SurfacePaintHost': {overlayPaintKey: () => 'opening', overlayPaintPermit: () => ({}), RetainedOverlay: 'RetainedOverlay'},
  });
  const homeEntry = {...entry, homeActions} as any;
  const platform = {platform: tv ? 'tv' as const : 'phone-app' as const, playbackAvailable: true, queueAvailable: true, nativeDownloadsAvailable: true};
  const caps: EntryCapabilities = homeEntryCapabilities(homeEntry, platform);
  const groups = entryActions(caps);
  const state: any = {displayMembers: [], rating: null, mixedEdit: false, deletion: null, selection: {active: false, selectedKeys: []},
    menu: {windowId: 'window', key: 'key', member: {item: {kind: 'movie'}, entry: homeEntry}, groups}};
  const controller = {subscribe: () => () => {}, getSnapshot: () => state, closeMenu() {}, toggle: () => ({ok: true}), executeMenu: async () => {}};
  const runtime = {getSnapshot: () => ({authority, account: {viewerRole: 'owner'}}), homeMenuRow: () => undefined, artwork: () => undefined, actions: {}};
  const home = nodes(h.render(() => HomeSelectionOverlays({controller, runtime, ownerTag: 'Owner', reportError() {}} as any))).find(node => node.type === 'Menu').props.items;
  // The Watch menu for a title whose Detail grants exactly what Home's server grants did, with an executor for each.
  const value: any = {phase: 'menu', capture: {entry}, busy: false, detail: detail(everyGrant),
    actions: {fileInfo() {}, select() {}, entry: fullMenu(tv ? ['download'] : [])}};
  const presenters = {subscribe: () => () => {}, getSnapshot: () => value, overlayCurrent: () => true, current: () => true, togetherActions: () => ({queue: false, watch: false})};
  const watch = nodes(h.render(() => NativeWatchOverlays({presenters, runtime: {artwork: () => undefined}, common: {}} as any))).find(node => node.type === 'Menu').props.items;
  return {home, watch};
}
const shape = (items: any[]) => items.filter(item => !['select', 'file-info', 'different-version', 'see-all'].includes(item.id) && !item.id.startsWith('separator') && item.id !== 'surface-separator')
  .map(item => item.section ? {id: item.id, section: true, label: item.label, tone: item.tone} : {id: item.id, label: item.label, icon: item.icon, selected: item.selected, destructive: item.destructive, tone: item.tone});

for (const tv of [false, true]) test(`Home and the Watch menu draw identical title rows for the same capabilities on ${tv ? 'TV' : 'phone and tablet'}`, async () => {
  const {home, watch} = await overlays(tv);
  assert.ok(shape(home).length >= 8, 'the comparison covers the real rows');
  assert.deepEqual(shape(watch), shape(home));
  if (tv) assert.equal(ids(home).includes('download'), false); else assert.ok(ids(watch).includes('download'));
  assert.ok(ids(watch).includes('delete') && ids(home).includes('delete'), 'owner rows are drawn by both, on TV too');
  assert.ok(ids(watch).includes('select') && ids(home).includes('select'));
});

test('on TV an episode\'s Go to season sits with the personal rows, above the separator that leads to Play a different version and File info (tv-detail.html)', () => {
  const groups = nativeWatchMenuGroups({entry, detail: detail(['play', 'watchlist', 'watched']), menu: fullMenu(['download', 'editMetadata', 'refreshMetadata', 'deleteTitle', 'addToCollection', 'rate', 'details'])});
  const items = titleMenuItems(groups, {tv: true, run() {}, related: [row('go-to-season')], surface: [row('different-version'), row('file-info'), row('select')]});
  assert.deepEqual(ids(items).filter(id => !id.startsWith('separator') && !id.startsWith('group:')), ['play', 'playNext', 'addToQueue', 'watchlist', 'watched', 'addToPlaylist',
    'go-to-season', 'surface-separator', 'different-version', 'file-info', 'select']);
  assert.ok(ids(items).indexOf('go-to-season') < ids(items).indexOf('surface-separator'));
  const touch = titleMenuItems(groups, {tv: false, run() {}, related: [row('go-to-season')], surface: [row('select')]});
  assert.equal(ids(touch).includes('go-to-season'), false, 'Go to season is drawn for TV only');
});
