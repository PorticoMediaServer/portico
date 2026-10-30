// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {downloadEntryIdentity, downloadGroups, downloadRequestLines, downloadRowText, downloadStorageSegments, downloadTransferProgress, type NativeDownloadsCopy} from '../src-next/screens/downloads/presentation';
import type {OfflineEntry} from '../src-next/platform/offline-downloads';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import * as downloadInventory from '../src-next/screens/downloads/inventory.ts';
import {createTheme} from '../src-next/kit/theme';
import {holdCard} from '../src-next/kit/cards/model';

/** Mounts one shallow component under the real hook driver. React's own hook
 * dispatcher runs the module's effects, so layout effects commit and unmount runs
 * every cleanup and store release. No reconciler draws the tree: the test calls
 * the exported component as a function and inspects its elements, as the other
 * shallow screen fixtures do. */
async function mount(component: (props: any) => any, props: any) {
  let tree: any;
  const host = createFabricHookHost(() => {tree = component(props);});
  await host.mount();
  return {host, get tree() {return tree;}};
}

const copy: NativeDownloadsCopy = {emptyBody: 'downloads.emptyBody', storageLimitHelp: 'downloads.storageLimit', freeOnPhone: 'downloads.used', removeFromPhone: 'action.remove',
  stoppedBody: 'downloads.paused', downloading: 'downloads.paused', needsCheckIn: 'downloads.offline.needsCheckIn', goneFromServer: 'downloads.offline.goneFromServer',
  otherProfiles: 'downloads.used', stoppedAt: 'server.plays.stoppedAt', stoppedHelp: 'downloads.paused'};
const text = {t: defaultI18n.t};
function entry(patch: Partial<OfflineEntry> = {}): OfflineEntry {
  return {id: 'local-copy', itemId: 'actual-item', owner: 'current-viewer', title: 'Stored title: Special Edition', subtitle: 'opaque legacy prose', quality: 'original', artworkUrl: '',
    state: 'ready', error: '', waiting: '', held: false, bytes: 1024 ** 3, bytesDone: 1024 ** 3, addedAt: 1, onServer: true, changedOnServer: false, ...patch};
}

test('Downloads metadata uses actual cached file facts and episode numbering, never quality IDs or legacy prose', () => {
  const episode = entry({info: {kind: 'episode', title: 'Stored episode', year: 2024, durationSeconds: 3000, parentId: 'show', parentTitle: 'Stored show', groupId: 'season', groupTitle: '', groupNumber: 1, number: 2, artist: '', onServer: true}});
  const original = downloadRowText(episode, {resumePositionSeconds: 0}, null, copy, text);
  assert.deepEqual(original.metadata, ['S1 E2', 'Original', defaultI18n.bytes(1024 ** 3)]);
  const converted = {...episode, quality: '720p-high'};
  assert.deepEqual(downloadRowText(converted, undefined, null, copy, text).metadata, ['S1 E2', defaultI18n.bytes(1024 ** 3)]);
  assert.deepEqual(downloadRowText(converted, {media: {width: 1920, height: 800}}, null, copy, text).metadata, ['S1 E2', '1080p', defaultI18n.bytes(1024 ** 3)]);
  assert.equal(original.metadata.some(value => value.includes('opaque')), false);
  assert.deepEqual(downloadRowText({...episode, state: 'downloading'}, undefined, null, copy, text).metadata, ['S1 E2']);
});

test('Downloads reasons preserve truthful native playability, transfer and removed-title states', () => {
  const waiting = downloadRowText(entry({state: 'downloading', waiting: 'wifi'}), undefined, null, copy, text);
  assert.deepEqual(waiting.metadata, []); assert.equal(waiting.reason, defaultI18n.t('downloads.waiting.wifi')); assert.equal(waiting.warning, true);
  const checkIn = downloadRowText(entry(), undefined, 'needs_check_in', copy, text);
  assert.equal(checkIn.reason, defaultI18n.t(copy.needsCheckIn)); assert.deepEqual(checkIn.metadata, []);
  const removed = downloadRowText(entry({onServer: false}), undefined, null, copy, text);
  assert.equal(removed.reason, defaultI18n.t(copy.goneFromServer)); assert.equal(removed.warning, false, 'a removed server title still plays when its actual receipt is usable');
  assert.equal(downloadRowText(entry({state: 'failed'}), undefined, null, copy, text).stopped, true);
  assert.equal(downloadTransferProgress(entry({bytes: 0})), undefined);
  assert.equal(downloadTransferProgress(entry({bytes: 100, bytesDone: 38})), .38);
});

test('Storage segments require actual volume allocation and never infer capacity from quota or free bytes', () => {
  assert.equal(downloadStorageSegments({freeBytes: 100}), undefined);
  assert.equal(downloadStorageSegments({totalBytes: 1000, freeBytes: 500}), undefined);
  assert.equal(downloadStorageSegments({totalBytes: 1000, freeBytes: 500, allocatedBytes: 501}), undefined);
  assert.equal(downloadStorageSegments({totalBytes: 1000, freeBytes: -1, allocatedBytes: 100}), undefined);
  assert.deepEqual(downloadStorageSegments({totalBytes: 1000, freeBytes: 550, allocatedBytes: 70}), {downloads: .07, other: .38});
});

test('Menu and artwork identity follow receipt viewer ownership when no legacy owner field exists', () => {
  const receipt: NonNullable<OfflineEntry['receipt']> = {receiptId: 'receipt', algorithm: 'ed25519', keyId: 'key', payload: 'signed', signature: 'signature', revision: 1,
    claims: {kind: 'portico.download-receipt', version: '1', receiptId: 'receipt', keyId: 'key', viewer: {authority: 'authority', serverId: 'server', accountId: 'account', profileId: 'profile1'},
      itemId: 'actual-item', preparationId: 'preparation', quality: 'original', qualityLabel: 'Original', artifact: {sha256: 'actual-artifact', bytes: 100}, issuedAt: '', expiresAt: ''}};
  const first = entry({owner: undefined, receipt});
  const other = {...first, receipt: {...receipt, claims: {...receipt.claims, viewer: {...receipt.claims.viewer, profileId: 'profile2'}}}};
  assert.notEqual(downloadEntryIdentity(first), downloadEntryIdentity(other));
  assert.equal(downloadEntryIdentity(first), downloadEntryIdentity({...first, bytesDone: 75}), 'transfer updates retain the same actual artifact');
});

test('Local groups count actual downloaded episodes and group genuine movies without classifying unknown entries', () => {
  const episode = entry({id: 'e1', info: {kind: 'episode', title: 'Episode', year: 2024, durationSeconds: 10, parentId: 'show', parentTitle: 'Show', groupId: 'season', groupTitle: '', groupNumber: 1, number: 1, artist: '', onServer: true}});
  const groups = downloadGroups([entry({id: 'movie1', kind: 'movie', addedAt: 5}), episode, {...episode, id: 'e2'}, entry({id: 'movie2', kind: 'movie'}), entry({id: 'unknown'})]);
  assert.deepEqual(groups.find(group => group.key === 'kind:movie')?.entries.map(value => value.id), ['movie1', 'movie2']);
  assert.equal(groups.find(group => group.key === 'parent:show')?.episodeCount, 2);
  assert.deepEqual(groups.find(group => group.key === 'title:unknown')?.entries.map(value => value.id), ['unknown']);
});

test('Cached artwork consumer rejects late retired identities and does not reread bundle files for byte progress', async () => {
  const host = hooks(), effects: Array<{effect: () => (() => void); deps: unknown[]; cleanup?: () => void}> = [];
  let effectCursor = 0;
  const react = {...host.react, useEffect(effect: () => (() => void), deps: unknown[]) {
    const index = effectCursor++, previous = effects[index];
    if (previous && deps.every((value, i) => value === previous.deps[i])) return;
    previous?.cleanup?.(); effects[index] = {effect, deps};
  }};
  const {useDownloadArtwork} = await screenModule(new URL('../src-next/screens/downloads/useDownloadArtwork.ts', import.meta.url), {react, './presentation': {downloadEntryIdentity}});
  const pending: Array<(facts: unknown) => void> = [];
  const device = {displayFacts: () => new Promise(resolve => pending.push(resolve))};
  const render = (value: OfflineEntry) => {effectCursor = 0; return host.render(() => useDownloadArtwork(device, value));};
  const flush = () => effects.forEach(value => {if (!value.cleanup) value.cleanup = value.effect();});
  const original = entry();
  assert.equal(render(original), undefined); flush(); assert.equal(pending.length, 1);
  render({...original, bytesDone: 100}); flush(); assert.equal(pending.length, 1);
  const other = {...original, owner: 'different-viewer'};
  assert.equal(render(other), undefined); flush(); assert.equal(pending.length, 2);
  pending[0]({backdropUrl: 'file:///retired-viewer-art'}); await Promise.resolve();
  assert.equal(render(other), undefined);
  pending[1]({backdropUrl: 'file:///current-viewer-art', media: {width: 1280, height: 720}, resumePositionSeconds: 0}); await Promise.resolve();
  assert.deepEqual(render(other), {backdropUrl: 'file:///current-viewer-art', media: {width: 1280, height: 720}});
  effects.forEach(value => value.cleanup?.());
});

test('Downloads rows use the shared hold/menu boundary with their actual native anchor and current playability', async () => {
  const theme = createTheme({width: 390, isTV: false});
  const current = entry(), calls: unknown[] = [];
  let opened = 0;
  const snapshot = {supported: true, ready: true, entries: [current], settings: {}, usedBytes: 1, freeBytes: 100, otherBytes: 0};
  const device = {getSnapshot: () => snapshot, subscribe: () => () => {}, open: async () => {opened++;},
    playabilityReason: () => null};
  const {DownloadsScreen} = await screenModule(new URL('../src-next/screens/downloads/DownloadsScreen.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 390})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0})}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', Artwork: 'Artwork'}, '../../kit/controls': {Chip: 'Chip', Focusable: 'Focusable', Switch: 'Switch'},
    '../../kit/cards/primitives': {CardFrame: 'CardFrame'}, '../../kit/overlays': {BottomSheet: 'BottomSheet', Menu: 'Menu'}, '../../kit/states': {StateView: 'StateView'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/browse/presentation': {browseType: () => ({}), alpha: () => ''}, '../../kit/theme': {useTheme: () => theme},
    './inventory': downloadInventory,
    './presentation': {downloadEntryIdentity, downloadGroups, downloadRowText, downloadStorageSegments, downloadTransferProgress}, './useDownloadArtwork': {useDownloadArtwork: () => ({})},
    './useReadingFacts': await screenModule(new URL('../src-next/screens/downloads/useReadingFacts.ts', import.meta.url), {react: {...React, default: React}}),
    '../saved/SavedForms': await screenModule(new URL('../src-next/screens/saved/SavedForms.tsx', import.meta.url), {
      '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => undefined}, '../../kit/browse/presentation': {browseType: () => ({})}, '../search/TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'},
      react: {...React, default: React}, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../packages/i18n/src': {defaultI18n},
      '../../kit/controls': {Button: 'Button', Switch: 'Switch', TextField: 'TextField'}, '../../kit/overlays': {Banner: 'Banner', BottomSheet: 'BottomSheet', Dialog: 'Dialog'},
      '../../kit/theme': {useTheme: () => theme}}),
  });
  function nodes(node: any): any[] {
    if (Array.isArray(node)) return node.flatMap(nodes);
    return node && typeof node === 'object' && 'props' in node ? [node, ...nodes(node.props.children)] : [];
  }
  const screen = await mount(DownloadsScreen, {device, runtime: {text}, copy, back() {}, openTitle() {}});
  try {
    assert.equal(opened, 1, 'the committed screen effect opened the actual device port');
    const window = nodes(screen.tree).find(node => node.type?.type?.name === 'DownloadInventoryWindow');
    const windowScreen = await mount(window.type.type, window.props);
    const live = nodes(windowScreen.tree).find(node => node.type?.type?.name === 'LiveDownloadRow');
    const liveScreen = await mount(live.type.type, live.props);
    const row = nodes(liveScreen.tree).find(node => node.type?.name === 'DownloadRow');
    const rowScreen = await mount(row.type, {...row.props, menuOpen: true, onMenu: (value: unknown) => calls.push(value), onPlay: () => calls.push('local-play')});
    try {
      const card = nodes(rowScreen.tree).find(node => node.type === 'CardFrame');
      assert.equal(card.props.moreExpanded, true);
      const nativeHost = {actual: 'download-card'}; card.props.menuAnchorRef.current = nativeHost;
      holdCard(card.props, false); assert.equal(calls[0], card.props.menuAnchorRef); assert.equal((calls[0] as any).current, nativeHost);
      card.props.onNavigate(); assert.equal(calls[1], 'local-play');
      assert.equal(card.props.onLongPressAction, undefined); assert.equal(card.props.selection, undefined, 'Downloads has no invented selection action');
    } finally {await rowScreen.host.unmount(); await liveScreen.host.unmount(); await windowScreen.host.unmount();}
  } finally {await screen.host.unmount();}
});

test('a whole-container request reports capturing, ready-of-total and failed members in drawn words', () => {
  assert.deepEqual(downloadRequestLines({totalKnown: false, ready: 0, total: 0, failed: 0}), [{key: 'downloads.requestPreparing'}]);
  assert.deepEqual(downloadRequestLines({totalKnown: true, ready: 3, total: 9, failed: 0}),
    [{key: 'downloads.requestProgress', values: {ready: 3, total: 9}}]);
  assert.deepEqual(downloadRequestLines({totalKnown: true, ready: 9, total: 9, failed: 2}), [
    {key: 'downloads.requestProgress', values: {ready: 9, total: 9}},
    {key: 'downloads.requestFailed', values: {failed: 2}},
  ]);
  assert.equal(defaultI18n.t('downloads.requestProgress', {ready: 3, total: 9}), '3 of 9 ready');
  assert.equal(defaultI18n.t('downloads.requestPreparing'), 'Finding everything to download…');
});
