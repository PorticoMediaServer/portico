import {settingsFixture, settingsFixtureChildren} from './helpers/settings-fixture.mjs';
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
import {serverPath} from '../../../packages/client-core/src/query/server-api';
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

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(settingsFixtureChildren(node))] : [];

async function fixture(isTV = false, owner = false, io?: {effects: (() => unknown)[]; api: unknown; lifetime?: unknown}) {
  const host = hooks(), bodyHost = hooks();
  let nested = false, language = 'CA';
  const focus = {region: undefined as string | undefined, calls: [] as unknown[], back: [] as (() => boolean)[]};
  const react: any = {...host.react};
  for (const name of Object.keys(host.react).filter(name => name.startsWith('use'))) react[name] = (...args: any[]) => {const active: any = nested ? bodyHost.react : host.react; return active[name](...args);};
  // The body's layout effects own reconciliation, not this word-for-word render.
  react.useLayoutEffect = () => {}; react.useInsertionEffect = () => {};
  if (io) react.useEffect = (effect: () => unknown) => {io.effects.push(effect);};
  react.default = react;
  const theme = createTheme({width: isTV ? 1920 : 800, isTV});
  const safeMode = await screenModule(new URL('../src-next/screens/settings-live/NativeSafeModeRow.tsx', import.meta.url), {
    react, 'react-native': {View: 'View'}, '../../kit/controls': {Select: 'Select'}, '../../kit/states': {StateView: 'StateView'},
  });
  const module = await settingsFixture({'../../../../../packages/client-core/src/home-selection/list-destinations': {withGuardedRaw: (api: unknown) => api}, '../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true},
    './use-tv-voice-search': {useTVVoiceSearch: () => ({start: undefined, busy: false})},
    './tv-settings-focus': tvSettingsFocus,
    './NativeSafeModeRow': safeMode,
    '../../platform/playback/dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false},
    './NativeDynamicRangeRow': {NativeDynamicRangeRow: 'NativeDynamicRangeRow', nativeDynamicRangeFocusId: 'settings-device-match-dynamic-range'},
    './NativeFrameRateRow': {NativeFrameRateRow: 'NativeFrameRateRow', nativeFrameRateFocusId: 'settings-device-match-frame-rate'},
    '../../platform/playback/passthrough-formats': {passthroughFormats},
    './NativePassthroughRow': {NativePassthroughRow: 'NativePassthroughRow', nativePassthroughFocusId: 'settings-device-audio-passthrough'},
    './NativeApplePassthroughRow': {NativeApplePassthroughRow: 'NativeApplePassthroughRow'},
    '../../kit/settings/DeviceCapabilityRows': {DeviceCapabilityRows: 'DeviceCapabilityRows'},
    './DashboardScreen': {NativeDashboardScreen: 'NativeDashboardScreen'},
    './ServerRemoteAccessPage': {ServerRemoteAccessPage: 'ServerRemoteAccessPage'},
    './NativeLossNoticesRow': {NativeLossNoticesRow: 'NativeLossNoticesRow', nativeLossNoticesFocusId: 'settings-device-loss-notices'},
    './NativeServerOfflineNoticeRow': {NativeServerOfflineNoticeRow: 'NativeServerOfflineNoticeRow'},
    react,
    'react-native': {AppState,NativeModules,Dimensions: {get: () => ({width: 800, height: 1024})}, Platform: {OS: 'ios', isTV: false, isPad: true}, ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../packages/contracts/src/routes': routes,
    '../../../../../packages/client-core/src/presentation/settings-bindings': settingsBindings,
    '../../../../../packages/client-core/src/presentation/settings-spec': settingsSpec,
    '../../../../../packages/client-core/src/presentation/settings-structure': structure,
    '../../../../../packages/client-core/src/presentation/language-list': languages,
    '../../../../../packages/client-core/src/presentation/region-choices': regionChoices,
    '../../kit/states/sentence-parts': sentenceParts,
    '../../../../../packages/i18n/src': i18n,
    '../../../../../packages/client-core/src/recommendation-feedback': {resetRecommendations: () => {throw Error('unexpected_mutation');}},
    './settings-icon': await import('../src-next/screens/settings-live/settings-icon.ts'), '../../../../../packages/design/src/next-icons': {nextIcons},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/focus': {FocusRegion: 'FocusRegion', useCovered: () => false, useFocusRegistry: () => ({capture: () => focus.region ? {regionId: focus.region, childId: 'control'} : undefined, focus: (address: unknown) => {focus.calls.push(address); return true;}, entryAddress: () => undefined, subscribe: () => () => {}, getSnapshot: () => 0})},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/shell': {SettingsRowTargetContext: {Provider: 'TargetProvider'}, SettingsSheet: 'SettingsSheet', SettingsRow: 'SettingsRow', SettingsSearchResults: 'SettingsSearchResults', LanguageList: 'LanguageList', ShortcutTiles: 'ShortcutTiles', SettingsSectionLabel: 'SettingsSectionLabel'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfileFace: 'ProfileFace'},
    '../../kit/shell/visual': {alpha,shellType: (fontSize: number, fontWeight: number) => ({fontSize, fontWeight})},
    '../../kit/theme': {useTheme: () => theme},
    '../../kit/states': {StateView: 'StateView'},
    '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
    '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})},
    '../destinations': {NativeTVKeyboard: 'NativeTVKeyboard'},
    '../destinations/useEntryFocus': {useDestinationEntryFocus: () => {}},
    './settings-actions': settingsActions,
    './Screen': {Screen: 'Screen', useScreenEntry: () => undefined, useScreenBack: (runtime: any, handler: () => boolean, isTV: boolean) => {if (isTV) runtime.registerTVPageBack?.(handler);}},
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
    '../../touch/settings/ServerIntegrationsPage': {ServerIntegrationsPage: 'NativeServerIntegrations'},
    './ServerGeneralPage': {ServerGeneralPage: 'ServerGeneralPage'},
    '../../touch/settings/ServerStreamingPage': {ServerStreamingPage: 'NativeServerStreaming'},
    '../../touch/settings/ServerSegmentsPage': {ServerSegmentsPage: 'NativeServerSegments'},
    '../../touch/settings/ServerPeoplePage': {ServerPeoplePage: 'NativeServerPeople'},
