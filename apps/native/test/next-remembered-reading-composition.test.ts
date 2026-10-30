import test, {after} from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createReadingHost} from '../../../packages/client-core/src';
import {routeBase64} from '../../../packages/client-core/src/route-identity';
import {appleReaderEntry, readerAssetEntry, readerNavigationAllowed, readerDelivery} from '../src-next/screens/read/asset-origin';
import {closeReadingResource, createReadingSessionOwner} from '../src-next/screens/read/reading-session-owner';
import {rememberedReadingHistoryPaused} from '../src-next/shell/remembered-reading-settings';
import {screenModule, stackSlotFixture} from './helpers/screen-harness.mjs';
import {React as fabricReact} from './helpers/fabric-host.mjs';
import {sceneHost} from './helpers/scene-host.mjs';

const previousDev = Object.getOwnPropertyDescriptor(globalThis, '__DEV__');
Object.defineProperty(globalThis, '__DEV__', {configurable: true, value: false});
after(() => {if (previousDev) Object.defineProperty(globalThis, '__DEV__', previousDev); else Reflect.deleteProperty(globalThis, '__DEV__');});
type Node = {type: unknown; props: Record<string, any>};
/** `type` is the element type itself: the real guest component is a function, while stubbed kit elements are their tag string. */
function find(value: unknown, type: unknown): Node | undefined {
  if (Array.isArray(value)) {for (const child of value) {const result = find(child, type); if (result) return result;} return;}
  if (!value || typeof value !== 'object' || !('props' in value)) return;
  const node = value as Node; return node.type === type ? node : find(node.props.children, type);
}

/** Controlled session/native ports verify root composition, not signatures,
 * archive decoding, native files, receipt cryptography or a real cold launch. */
for (const format of ['epub', 'pdf', 'cbz'] as const)
for (const mixed of [false, true])
for (const ending of ['back', 'entry', 'expiry', 'owner', 'unmount'] as const)
test(`remembered ${format} composition: ${mixed ? 'mixed' : 'publication only'}, ${ending}`, async () => {
  let root = true, retained = true, opens = 0, closes = 0, online = 0, paused: boolean | undefined, backs = 0;
  const listeners = new Set<() => void>(), abort = new AbortController();
  const book = {id: `entry-${format}`, itemId: `item-${format}`, kind: format === 'cbz' ? 'comic' : 'ebook', state: 'ready', title: format};
  const movie = {id: 'entry-movie', itemId: 'movie', kind: 'movie', state: 'ready', title: 'movie'};
  const sourceId = `source-${format}`, revision = 'a'.repeat(64);
  const session = {manifest: {itemId: book.itemId, sourceId, revision, format}, signal: abort.signal, progress: async () => ({progress: null}),
    close: async () => {closes++;}, headPDF: async () => ({}), readRange: async () => new Uint8Array(), readAsset: async () => new Uint8Array(),
    page: async () => ({}), saveProgress: async () => ({saved: false, progress: null})};
  const identity = {current: () => root, subscribe: () => () => {}, dispose() {root = false;}};
  const device = {current: () => root, publicationSource: (entry: typeof book) => {if (!retained || entry !== book) throw new Error('removed'); return {sourceId, revision};},
    device: {getSnapshot: () => ({supported: true, ready: true, entries: mixed ? [movie, book] : [book]}),
      playabilityReason: (entry: typeof book) => entry === book && !retained ? 'unavailable' : null,
      subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}},
    readingSession: async (entry: typeof book, isPaused: () => boolean) => {assert.equal(entry, book); opens++; paused = isPaused(); return session;},
    connect: () => {online++; throw new Error('network forbidden');}, open: async () => {}, dispose() {}};
  const {createRememberedReadingOwner} = await screenModule(new URL('../src-next/shell/remembered-reading-owner.ts', import.meta.url), {
    react: {...fabricReact, default: fabricReact}, '../screens/read/reading-session-owner': {closeReadingResource, createReadingSessionOwner},
    './remembered-reading-settings': {rememberedReadingHistoryPaused},
  });
  const {createRememberedDownloadsOpening} = await screenModule(new URL('../src-next/shell/remembered-downloads-opening.ts', import.meta.url), {
    react: {...fabricReact, default: fabricReact}, 'react-native': {Platform: {isTV: false}}, '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    './remembered-downloads-viewer': {captureRememberedDownloadsViewer: async () => identity}, './remembered-downloads-device': {createRememberedDownloadsDevice: () => device},
    './remembered-playback-settings': {readRememberedPlaybackSettings: async () => mixed ? {} : undefined},
    './remembered-reading-settings': {readRememberedReadingPreferences: async () => ({effective: {}})}, './remembered-reading-owner': {createRememberedReadingOwner},
    '../platform/playback/receipt-only': {createNativeReceiptOnlyPlayback: () => ({current: () => root, dispose() {}})},
    '../screens/accounts/downloads-entry-model': {noAccountDownloads: {available: false}},
  });
  const {ReadingGuestView, createReadingGuestBridge} = await screenModule(new URL('../src-next/screens/read/ReadingGuestView.tsx', import.meta.url), {
    react: {...fabricReact, default: fabricReact}, 'react-native-webview': {default: 'WebView'}, '../../../../../packages/client-core/src': {createReadingHost},
    '../../../../../packages/client-core/src/route-identity': {routeBase64}, '../../platform/server-connections': {nativeRouteCrypto: {random: () => new Uint8Array(24).fill(7)}},
    '../../platform/reading-highlights-save': {readingHighlightsSavePort: () => async () => 'cancelled'},
    './asset-origin': {readerNavigationAllowed, readerDelivery}, './startup-diagnostics': {readerStartupDiagnostic: () => undefined, readerStartupProbe: ''},
  });
  const {NativeReadingGuestHost} = await screenModule(new URL('../src-next/screens/read/NativeReadingGuestHost.tsx', import.meta.url), {...stackSlotFixture, 
    react: {...fabricReact, default: fabricReact}, 'react-native': {View: 'View', NativeModules: {PorticoReaderAssets: {bundle: async () => ({url: appleReaderEntry})}}, useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-webview': {default: 'WebView'}, 'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../kit/controls/Button': {Button: 'Button'},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'}, '../../kit/theme': {useTheme: () => ({colors: {ink: '#000'}})},
    '../../platform/platformPosture': {usePlatformPosture: () => ({status: 'unavailable'})}, './asset-origin': {readerAssetEntry}, './ReadingGuestView': {ReadingGuestView, createReadingGuestBridge},
  });
  const {RememberedReadingScreen} = await screenModule(new URL('../src-next/screens/downloads/RememberedReadingScreen.tsx', import.meta.url), {
    react: {...fabricReact, default: fabricReact}, 'react-native': {View: 'View', useWindowDimensions: () => ({width: 390, height: 844})},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 47})}, '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../kit/overlays/surface': {Dialog: 'Dialog'}, '../../kit/controls': {Button: 'Button'},
    '../../kit/states': {LoadingPaint: 'LoadingPaint', SkeletonBlock: 'SkeletonBlock', StateView: 'StateView'},
    '../read/NativeReadingGuestHost': {NativeReadingGuestHost: 'NativeReadingGuestHost'},
  });
  let opened: any;
  const original = await createRememberedDownloadsOpening({current: () => root}, {signal: new AbortController().signal, isCurrent: () => root,
    onOpen: (value: unknown) => {opened = value;}, onRetired() {}});
  assert.ok(original); assert.equal(original.entry.getSnapshot().available, true); original.entry.open(); assert.equal(opened, original);
  assert.equal(original.canRead(book), true); const reading = original.reading(book); let tree: unknown;
  // Both the page and the accepted guest run on the Fabric renderer, so the
  // session close, guest retirement and stale-guest refusal are React cleanups.
  const page = await sceneHost(() => {tree = RememberedReadingScreen({reading, noticeCopy: original.noticeCopy, current: original.current, onBack: () => {backs++;}}); return tree;});
  let guestHost: Awaited<ReturnType<typeof sceneHost>> | undefined, guestTree: unknown;
  try {
    const guest = find(tree, 'NativeReadingGuestHost'); assert.ok(guest, 'accepted original reaches the guest component');
    assert.equal(guest!.props.owner.snapshot().manifest, session.manifest); assert.equal(paused, true); assert.equal(opens, 1); assert.equal(online, 0);
    guestHost = await sceneHost(() => {guestTree = NativeReadingGuestHost(guest!.props); return guestTree;});
    const view = find(guestTree, ReadingGuestView); assert.ok(view); const oldView = ReadingGuestView(view!.props); assert.equal(oldView.type, 'WebView');
    if (ending === 'back') guest!.props.onBack();
    else if (ending === 'entry') {retained = false; for (const listener of [...listeners]) listener();}
    else if (ending === 'expiry') abort.abort();
    else if (ending === 'owner') {root = false; for (const listener of [...listeners]) listener();}
    else {await guestHost.unmount(); await page.unmount();}
    await page.host.settle(); if (guestHost) await guestHost.host.settle();
    if (ending === 'back') {assert.equal(backs, 1); assert.equal(original.current(), true);}
    assert.equal(closes, 1); assert.equal(opens, 1);
    oldView.props.onError(); oldView.props.onMessage({nativeEvent: {url: appleReaderEntry, data: 'stale'}});
    assert.equal(reading.owner.current(), false, 'the retired original cannot be revived by the old guest');
  } finally {await guestHost?.unmount(); await page.unmount(); original.dispose();}
  assert.equal(online, 0);
});
