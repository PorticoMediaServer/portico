// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {nativeWatchMenuGroups} from '../src-next/screens/watch/menu-model.ts';
import {titleMenuItems} from '../src-next/kit/overlays/title-menu.ts';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const t = defaultI18n.t;
const text = (tree: any) => nodes(tree).filter(node => node.type === 'Text').map(node => node.props.children).filter(child => typeof child === 'string');

const row = (id: string): any => ({id, label: id, icon: 'i-film', onSelect() {}});
const ids = (items: readonly any[]) => items.map(item => item.id).filter(id => !id.startsWith('separator') && id !== 'surface-separator' && !id.startsWith('group:'));
const entry = {id: 'film', kind: 'movie', title: 'Actual film', libraryId: 'library'} as any;
const detail = (grants: string[]): any => ({personal: {watchlisted: false, favorite: false, watched: false, rating: null, progressSeconds: 0},
  actions: grants.map(id => ({id, enabled: true, ...(id === 'play' ? {playback: {itemId: 'film', startSeconds: 0}} : {})}))});
const noop = async (): Promise<void> => {};
const menu: any = {play: noop, enqueue: noop, addToPlaylist() {}, addToCollection() {}, rate() {}, details() {}, mutate: noop, dispose() {}, subscribe: () => () => {}, current: (): undefined => undefined};

test('the TV menu follows tv-more.html 2 and 3 and tv-detail.html 7; the phone menu keeps its own order', () => {
  const groups = nativeWatchMenuGroups({entry, detail: detail(['play', 'watchlist', 'watched', 'add_to_collection']), menu});
  const tv = ids(titleMenuItems(groups, {tv: true, run() {}, surface: [row('select'), row('go-to-artist'), row('disc-titles')], afterDetails: [row('different-version'), row('file-info')]}));
  assert.deepEqual(tv.slice(tv.indexOf('addToCollection')), ['addToCollection', 'select', 'go-to-artist', 'disc-titles', 'details', 'different-version', 'file-info']);
  const phone = ids(titleMenuItems(groups, {tv: false, run() {}, surface: [row('different-version'), row('select'), row('file-info')]}));
  assert.deepEqual(phone.slice(phone.indexOf('different-version')), ['different-version', 'select', 'file-info', 'details'], 'phone-detail frame 9: version, then File info');
  const song = ids(titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(['play']), menu: {...menu, details: undefined}}), {tv: true, run() {}, surface: [row('select'), row('go-to-artist')], afterDetails: [row('file-info')]}));
  assert.deepEqual(song.slice(-3), ['select', 'go-to-artist', 'file-info'], 'with no Details row, File info follows Select and Go to artist (tv-more.html 3)');
  const episode = titleMenuItems(nativeWatchMenuGroups({entry, detail: detail(['play']), menu: {...menu, details: undefined}}), {tv: true, run() {}, related: [row('go-to-season')], afterDetails: [row('different-version'), row('file-info')]});
  assert.deepEqual(ids(episode).slice(-3), ['go-to-season', 'different-version', 'file-info'], 'tv-detail.html 7: Go to season, then Play a different version and File info');
  assert.ok(episode.findIndex((item: any) => item.id === 'surface-separator') < episode.findIndex((item: any) => item.id === 'different-version'), 'the separator still leads to them');
});

test('the real TV Watch overlay puts Select before Details, then Play a different version and File info', async () => {
  const h = hooks();
  const shared = await screenModule(new URL('../src-next/kit/overlays/title-menu.ts', import.meta.url), {react: h.react, '../../../../../packages/i18n/src': {defaultI18n}});
  const theme = {isTV: true, layoutMode: 'tv', platform: 'tv', type: {caption: {}, body: {}}, colors: {}, textScale: 1};
  const {NativeWatchOverlays} = await screenModule(new URL('../src-next/screens/watch/WatchOverlays.tsx', import.meta.url), {
    '../../../../../packages/client-core/src/presentation/kit-model/technical': {},
    react: h.react, 'react-native': {Modal: 'Modal', Platform: {isTV: true}, ScrollView: 'ScrollView', StyleSheet: {}, Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/administration': {}, '../../../../../packages/client-core/src/title-identity': {},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {}, '../../shell/model': {}, '../../kit/controls': {Button: 'Button', TextField: 'TextField'},
    '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', Menu: 'Menu'},
    '../../kit/overlays/title-menu': shared, './menu-model': {nativeWatchMenuGroups}, '../../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'}, '../../kit/overlays/surface': {},
    '../../kit/overlays/TVPanelMotion': {useTVPanelPresence: () => ({})}, '../../kit/overlays/OverlayHost': {}, '../../kit/theme': {nativeType: () => ({}), useTheme: () => theme},
    '../../kit/player/VersionChooser': {}, '../../kit/player/DiscTitlesSheet': {}, '../../kit/title/FileInfoContent': {}, './file-info': {},
    '../../kit/overlays/SurfacePaintHost': {overlayPaintKey: () => 'opening', overlayPaintPermit: () => ({}), RetainedOverlay: 'RetainedOverlay'},
  });
  const value: any = {phase: 'menu', capture: {entry}, busy: false, detail: detail(['play', 'watchlist', 'watched', 'add_to_collection']),
    actions: {fileInfo() {}, select() {}, differentVersion() {}, entry: {...menu}}};
  const presenters = {subscribe: () => () => {}, getSnapshot: () => value, overlayCurrent: () => true, current: () => true, togetherActions: () => ({queue: false, watch: false})};
  const items = nodes(h.render(() => NativeWatchOverlays({presenters, runtime: {artwork: () => undefined}, common: {}} as any))).find(node => node.type === 'Menu').props.items;
  const order = ids(items);
  assert.deepEqual(order.slice(order.indexOf('addToCollection')), ['addToCollection', 'select', 'details', 'different-version', 'file-info']);
});
