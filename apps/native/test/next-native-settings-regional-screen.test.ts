import {passthroughFormats} from '../src-next/platform/playback/passthrough-formats';
import {nativeEqualizer} from '../src-next/platform/playback/equalizer';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as routes from '../../../packages/contracts/src/routes';
import * as structure from '../../../packages/client-core/src/presentation/settings-structure';
import * as languages from '../../../packages/client-core/src/presentation/language-list';
import * as regionChoices from '../../../packages/client-core/src/presentation/region-choices';
import * as sentenceParts from '../src-next/kit/states/sentence-parts';
import * as settingsActions from '../src-next/screens/settings-live/settings-actions';
import * as settingsConfiguration from '../../../packages/client-core/src/settings-configuration';
import * as settingsBindings from '../../../packages/client-core/src/presentation/settings-bindings';
import * as settingsSpec from '../../../packages/client-core/src/presentation/settings-spec';
import {AppState,NativeModules} from 'react-native';
import {alpha} from '../src-next/kit/shell/visual';
import * as i18n from '../../../packages/i18n/src';
import {parsePreferenceSnapshot} from '../../../packages/client-core/src/preferences';
import {nextIcons} from '../../../packages/design/src/next-icons';
import {createTheme} from '../src-next/kit/theme';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

async function fixture(isTV = false) {
  const host = hooks(), bodyHost = hooks();
  let nested = false, language = 'CA';
  const react: any = {...host.react};
  for (const name of Object.keys(host.react).filter(name => name.startsWith('use'))) react[name] = (...args: any[]) => {const active: any = nested ? bodyHost.react : host.react; return active[name](...args);};
  // The body's layout effects own reconciliation, not this word-for-word render.
  react.useLayoutEffect = () => {}; react.useInsertionEffect = () => {};
  react.default = react;
  const theme = createTheme({width: isTV ? 1920 : 800, isTV});
  const safeMode = await screenModule(new URL('../src-next/screens/settings-live/NativeSafeModeRow.tsx', import.meta.url), {
    react, 'react-native': {View: 'View'}, '../../kit/controls': {Select: 'Select'}, '../../kit/states': {StateView: 'StateView'},
  });
  const module = await screenModule(new URL('../src-next/screens/settings-live/SettingsScreen.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true}, 
    './use-tv-voice-search': {useTVVoiceSearch: () => ({start: undefined, busy: false})},
    './tv-settings-focus': tvSettingsFocus,
    './NativeSafeModeRow': safeMode,
    '../../platform/playback/dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false},
    './NativeDynamicRangeRow': {NativeDynamicRangeRow: 'NativeDynamicRangeRow', nativeDynamicRangeFocusId: 'settings-device-match-dynamic-range'},
    './NativeFrameRateRow': {NativeFrameRateRow: 'NativeFrameRateRow', nativeFrameRateFocusId: 'settings-device-match-frame-rate'},
    '../../platform/playback/passthrough-formats': {passthroughFormats},
    './NativePassthroughRow': {NativePassthroughRow: 'NativePassthroughRow', nativePassthroughFocusId: 'settings-device-audio-passthrough'},
    './NativeApplePassthroughRow': {NativeApplePassthroughRow: 'NativeApplePassthroughRow'},
    './NativeCapabilityRows': {NativeCapabilityRows: 'NativeCapabilityRows'},
    './DashboardScreen': {NativeDashboardScreen: 'NativeDashboardScreen'},
    './server/RemoteAccessPage': {NativeRemoteAccessPage: 'NativeRemoteAccessPage'},
    './NativeLossNoticesRow': {NativeLossNoticesRow: 'NativeLossNoticesRow', nativeLossNoticesFocusId: 'settings-device-loss-notices'},
    react,
    'react-native': {AppState,NativeModules,Dimensions: {get: () => ({width: 800, height: 1024})}, Platform: {OS: 'ios', isTV: false, isPad: true}, ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/contracts/src/routes': routes,
    '../../../../../packages/client-core/src/settings-configuration': settingsConfiguration,
    '../../../../../packages/client-core/src/presentation/settings-bindings': settingsBindings,
    '../../../../../packages/client-core/src/presentation/settings-spec': settingsSpec,
    '../../../../../packages/client-core/src/presentation/settings-structure': structure,
    '../../../../../packages/client-core/src/presentation/language-list': languages,
    '../../../../../packages/client-core/src/presentation/region-choices': regionChoices,
    '../../kit/states/sentence-parts': sentenceParts,
    '../../../../../packages/i18n/src': i18n,
    '../../../../../packages/client-core/src/recommendation-feedback': {resetRecommendations: () => {throw Error('unexpected_mutation');}},
    '../../../../../packages/design/src/next-icons': {nextIcons},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({capture: () => undefined, focus: () => false, entryAddress: () => undefined, subscribe: () => () => {}, getSnapshot: () => 0})},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/shell': {SettingsSheet: 'SettingsSheet', SettingsRow: 'SettingsRow', SettingsSearchResults: 'SettingsSearchResults', LanguageList: 'LanguageList', ShortcutTiles: 'ShortcutTiles'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfileFace: 'ProfileFace'},
    '../../kit/shell/visual': {alpha,shellType: (fontSize: number, fontWeight: number) => ({fontSize, fontWeight})},
    '../../kit/theme': {useTheme: () => theme},
    '../../kit/states': {StateView: 'StateView'},
    '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
    '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})},
    '../destinations': {NativeTVKeyboard: 'NativeTVKeyboard'},
    '../destinations/useEntryFocus': {useDestinationEntryFocus: () => {}},
    './settings-actions': settingsActions,
    './Screen': {Screen: 'Screen', useScreenEntry: () => undefined, useScreenBack: () => {}},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    'react-native-qrcode-svg': {default: 'QRCode'},
    '../../shell/chrome': {setNativeScreenChromeHidden: () => {}},
    '../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'},
    './owner-libraries/OwnerLibrariesPanel': {NativeOwnerLibrariesPanel: 'NativeOwnerLibrariesPanel'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'NativeLiveOwnerScreen'},
    './live-owner-controller': {NativeLiveOwnerController: function NativeLiveOwnerController() {}},
    './recordings-controller': {createNativeRecordingsController: () => ({dispose: () => {}})},
    '../../platform/server-connections': {nativeRouteCrypto: {random: (size: number) => new Uint8Array(size)}},
    './server/TroubleshootingPage': {NativeServerTroubleshooting: 'NativeServerTroubleshooting'},
    './server/StoragePage': {NativeServerStorage: 'NativeServerStorage'},
    './server/IntegrationsPage': {NativeServerIntegrations: 'NativeServerIntegrations'},
    './server/GeneralPage': {NativeServerGeneral: 'NativeServerGeneral'},
    './server/StreamingPage': {NativeServerStreaming: 'NativeServerStreaming'},
    './server/SegmentsPage': {NativeServerSegments: 'NativeServerSegments'},
    './server/PeoplePage': {NativeServerPeople: 'NativeServerPeople'},
    './server/SignInPage': {NativeServerSignIn: 'NativeServerSignIn'},
    './server/MetadataPage': {NativeServerMetadata: 'NativeServerMetadata'},
    './server/HistoryPage': {NativeServerHistory: 'NativeServerHistory'},
    './server/PhoneServerPageHeader': {PhoneServerPageHeader: 'PhoneServerPageHeader'},
    '../settings/remote-sources/NativeRemoteSourcesScreen': {NativeRemoteSourcesScreen: 'NativeRemoteSourcesScreen'},
    '../../platform/playback/equalizer': {nativeEqualizer},
    '../../tv/auth/TVHandoff': {publicTVHandoffAddress: (value: string) => ({url: value, display: value, pathname: '/settings/profile', search: ''})},
  });
  const preferences = parsePreferenceSnapshot({deviceClass: 'mobile', registryRevision: 'regional-screen', registry: {revision: 'regional-screen', fields: [{key: 'region.locale', type: 'locale', default: 'en-US', group: 'region', labelKey: 'preferences.region.locale', scopes: ['profile-server']}]} ,
    documents: ['profile-server', 'profile-device-class'].map(scope => ({scope, revision: 1, activeRevision: 1, digest: 'd'.repeat(64), values: {}})), effective: {'region.locale': 'en-US'}, effectiveSource: {'region.locale': 'default'}, clampedFields: []});
  const shellSnapshot = {profile: {id: 'profile', name: 'Example', initial: 'E', color: 'blue' as const}, account: {actions: [] as readonly unknown[]}};
  const runtime = {text: {get t() {const prefix = language; return (key: string) => `${prefix}/${key}`;}}, reportError: (error: unknown) => {throw error;}, subscribe: () => () => {}, getSnapshot: () => shellSnapshot};
  const settingsRoute = {revision: 0, route: {kind: 'shell', destination: {kind: 'settings'}}};
  const props = {runtime, ports: {context: {current: () => true, account: {account: {role: 'member'}, canManage: false}, serverName: 'Example', authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'}), navigation: {subscribe: () => () => {}, getSnapshot: () => settingsRoute, isCurrent: () => true}}, settingsContext: () => ({owner: false, platform: 'phone', capabilities: {localAccount: true}}), serverName: () => 'Example', renderSettingsPanel: () => null},
    controller: {subscribe: () => () => {}, getSnapshot: () => ({preferences, busy: false, saved: 0}), lifetime: {authority: {}, current: () => true}},
    devicePreferences: {subscribe: () => () => {}, read: () => undefined}};
  return {setLanguage: (next: string) => {language = next;},
    render: () => {nested = false; return host.render(() => module.NativeSettingsScreen(props));},
    body: (node: any) => {nested = true; try {return bodyHost.render(() => node.type(node.props));} finally {nested = false;}}};
}

test('Settings headings and search consume the current runtime translator after its confirmed replacement', async () => {
  const f = await fixture();
  let tree = f.render();
  assert.ok(nodes(tree).some(node => node.type === 'Text' && node.props.children.includes('CA/next.shell.settings')));
  const search = nodes(tree).find(node => node.type === 'TextField');
  assert.equal(search.props.placeholder, 'CA/next.shell.searchSettings');
  f.setLanguage('GB'); tree = f.render();
  assert.ok(nodes(tree).some(node => node.type === 'Text' && node.props.children.includes('GB/next.shell.settings')));
  const nextSearch = nodes(tree).find(node => node.type === 'TextField');
  assert.equal(nextSearch.props.placeholder, 'GB/next.shell.searchSettings');
  nextSearch.props.onChangeText('GB/settings.page'); tree = f.render();
  const results = nodes(tree).find(node => node.type === 'SettingsSearchResults');
  assert.ok(results.props.matches.length > 0, 'the canonical search indexes the same translated page words');
});

test('Settings page body uses the same live translator for its own control and confirmation labels', async () => {
  const f = await fixture(), tree = f.render();
  const body = nodes(tree).find(node => typeof node.type === 'function' && node.type.name === 'SettingsPageBody');
  assert.ok(body);
  let rendered = f.body(body);
  assert.ok(nodes(nodes(rendered).find(node => node.type === 'Dialog').props.footer).some(node => node.type === 'Button' && node.props.children.includes('CA/action.cancel')));
  f.setLanguage('GB'); rendered = f.body(body);
  assert.ok(nodes(nodes(rendered).find(node => node.type === 'Dialog').props.footer).some(node => node.type === 'Button' && node.props.children.includes('GB/action.cancel')));
});

test('TV page column has an explicit Up crossing to Search and legible directional hints', async () => {
  const f = await fixture(true), tree = f.render();
  const column = nodes(tree).find(node => node.type === 'FocusRegion' && node.props.id === 'settings.pages');
  const search = nodes(tree).find(node => node.type === 'FocusRegion' && node.props.id === 'settings.search');
  assert.equal(column.props.edges.up, search.props.id);
  assert.equal(search.props.edges.down, column.props.id);
  const hints = nodes(nodes(tree).find(node => node.props.testID === 'settings-key-hints')).filter(node => node.type === 'Text');
  assert.equal(hints.length, 3);
  assert.ok(hints.every(node => node.props.style[0].fontSize === 20 && node.props.style[0].fontWeight === 600));
});
