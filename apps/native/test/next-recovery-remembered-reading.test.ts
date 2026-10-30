import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import {downloadEntryIdentity} from '../src-next/screens/downloads/presentation';
import * as startupRecovery from '../src-next/shell/startup-recovery';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

type NativeNode = {type: string; props: Record<string, any>; children: NativeNode[]};
const flatten = (nodes: NativeNode[]): NativeNode[] => nodes.flatMap(node => [node, ...flatten(node.children ?? [])]);

/** Actual RestoredShell -> CompactRecovery -> RecoveryDownloads, with controlled
 * root/receipt ports. This does not claim native signed-publication admission. */
for (const category of ['offline', 'unreachable', 'timeout'] as const)
for (const condition of ['publication-only', 'mixed', 'refused', 'missing-cache', 'av-without-player', 'retired'] as const)
test(`compact ${category} recovery admits only original usable downloads: ${condition}`, async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const host = await fabricHost(); let live = true, opened = 0, expectedSource: any, callback: any;
  const listeners = new Set<() => void>(), book = {id: 'publication', itemId: 'book', kind: 'ebook', state: 'ready', title: 'Saved book', artworkUrl: ''};
  const movie = {id: 'movie', itemId: 'film', kind: 'movie', state: 'ready', title: 'Saved film', artworkUrl: ''};
  const entries = condition === 'mixed' ? [book, movie] : condition === 'av-without-player' ? [movie] : [book];
  const snapshot = Object.freeze({supported: true, ready: true, entries});
  const availability = Object.freeze({available: condition !== 'refused' && condition !== 'missing-cache' && condition !== 'av-without-player'});
  const source: any = {current: () => live,
    device: {getSnapshot: () => snapshot, playabilityReason: () => live ? null : 'other_profile',
      subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}},
    entry: {getSnapshot: () => availability, subscribe: () => () => {}, open: () => {if (live && availability.available) {opened++; callback.onOpen(source);}}},
    ...(condition === 'mixed' ? {player: {current: () => live}} : {}),
    ...(condition !== 'missing-cache' ? {canRead: (entry: unknown) => entry === book && live && condition !== 'refused'} : {}),
    dispose() {live = false; for (const listener of [...listeners]) listener();}};
  const theme = {platform: 'phone', isTV: false, colors: {ink: '#000', raised: '#111', text: '#fff', textTertiary: '#888'}, type: {bodyStrong: {}, caption: {}}};
  const native = {Platform: {OS: 'ios'}, View: 'RCTView', Text: 'RCTText', AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}};
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), {'../screens/accounts/ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../platform/negotiation': {negotiationRefusal: () => undefined}, './boot-timing': {logBootMilestone: () => {}}, './restore-trace': {restoreTrace: () => ({waiting: 'start', elapsedMs: 0, marks: []})}, '../screens/share/NativeShareLinks': {NativeShareLinks: () => null}, '../platform/share-links': {nativeShareLinks: {acquire: () => () => {}, subscribe: () => () => {}, getSnapshot: () => undefined, take: () => false}}, 
    react: {...React, default: React}, 'react-native': native,
    '../kit/states': {HomeSkeleton: 'HomeSkeleton', StateView: 'StateView', StateLayout: ({beforeActions}: any) => React.createElement('StateLayout', null, beforeActions)},
    '../kit/artwork': {StandardBackdrop: 'StandardBackdrop', CoverFan: 'CoverFan'}, '../kit/controls': {Focusable: 'Focusable'}, '../kit/Icon': {Icon: 'Icon'}, '../kit/theme': {useTheme: () => theme},
    '../kit/overlays/TVDesignCanvas': {TVDesignCanvas: ({children}: any) => children},
    '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({category, action: 'try-again', title: 'Unavailable', body: 'Unavailable', actionLabel: 'Retry'})},
    '../platform/server-connections': {deviceOnline: () => category !== 'offline', onNetworkChange: () => () => {}},
    './assembly': {createNativeShellBindings: () => ({})}, './bootstrap': {restoreNativeShellRuntime: async () => {throw new Error(category);}},
    './NativeShell': {NativeShell: 'NativeShell'}, '../platform/accounts': {nativeAccounts: {snapshot: async () => ({servers: []}), onSettled: () => () => {}, onFence: () => () => {}}},
    '../platform/app-session-storage': {readNativeSelectedViewer: async () => undefined, subscribeNativeAppSessions: () => () => {}},
    '../screens/accounts/AccountEntry': {AccountEntry: 'AccountEntry'}, '../screens/accounts/AccountRoutes': {ServersRoute: 'ServersRoute'},
    '../../../../packages/i18n/src': {defaultI18n}, '../platform/sign-in-ended': {NativeSignInEnded: class extends Error {}},
    '../platform/identity-failure': {NativeIdentityFailure: class extends Error {}}, '../screens/accounts/PhoneFirstLibrary': {PhoneFirstLibrary: 'PhoneFirstLibrary'},
    '../screens/accounts/ServerIdentityFailure': {ServerIdentityFailure: 'ServerIdentityFailure'},
    '../platform/invitation-links': {nativeInvitationLinks: {subscribe: () => () => {}, getSnapshot: () => undefined, acquire: () => () => {}}},
    '../platform/installation': {beginNativeInstallation: () => ({current: () => live, dispose() {}})},
    './remembered-downloads-opening': {createRememberedDownloadsOpening: async (_installation: unknown, options: unknown) => {callback = options; expectedSource = source; return source;}},
    '../screens/downloads/RememberedDownloadsScreen': {RememberedDownloadsScreen: 'RememberedDownloadsScreen', RememberedDownloadsShortcut: 'RememberedDownloadsShortcut'},
    '../screens/downloads/presentation': {downloadEntryIdentity}, '../screens/settings/NativeSettingsAppearance': {NativeSettingsAppearance: 'NativeSettingsAppearance'},
    '../platform/home-action-diagnostics': {homeActionDiagnostics: {error() {}, lifetime() {}}},
    '../platform/secure-records': {acknowledgeSecureRecordsRecovered() {}, isSecureRecordsLost: () => false, secureRecordsRecoveredNotice: () => undefined},
    './SecureRecordsRecovery': {SecureRecordsRecovery: 'SecureRecordsRecovery'}, '../platform/boot-host': {takeLaunchStartedAt: (now = Date.now()) => now},
    './StartupBoot': {StartupBoot: ({children}: any) => children}, './startup-recovery': startupRecovery,
  });
  try {
    await host.render(React.createElement(RestoredShell));
    assert.equal(flatten(host.root).some(node => node.props?.testID === 'recovery-downloads'), false, 'the first connection failure stays behind Boot');
    t.mock.timers.tick(category === 'offline' ? 1_000 : 15_000); await host.settle();
    const card = flatten(host.root).find(node => node.props?.testID === 'recovery-downloads');
    const expected = condition === 'publication-only' || condition === 'mixed' || condition === 'retired';
    assert.equal(!!card, expected);
    if (condition === 'retired') {live = false; for (const listener of [...listeners]) listener(); card!.props.onPress(); await host.settle(); assert.equal(opened, 0);}
    else if (expected) {card!.props.onPress(); await host.settle(); assert.equal(opened, 1);
      const screen = flatten(host.root).find(node => node.type === 'RememberedDownloadsScreen'); assert.ok(screen); assert.equal(screen!.props.source, expectedSource);}
  } finally {await host.unmount();}
});
