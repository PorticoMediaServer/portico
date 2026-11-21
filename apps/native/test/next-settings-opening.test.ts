import {settingsFixture, settingsFixtureChildren} from './helpers/settings-fixture.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(settingsFixtureChildren(tree)), ...nodes(tree.props?.headerContent)] : [];
const find = (tree: any, id: string) => nodes(tree).find(node => node.props?.testID === id);
// Settings search seams: the matches, the Covered flag, TV, and every destination entry ask the screen makes.
const search = {matches: [] as any[], covered: false, tv: false, entries: [] as {activation: any; address: any}[]};
async function fixture(initialRoute: any = {kind: 'shell', destination: {kind: 'settings'}}, initialWidth = 390, role = 'owner', initialPage?: 'live' | 'profile') {
  const host = hooks(), pending: (() => void)[] = [], effects = new Set<{cleanup?: () => void}>();
  const useLayoutEffect = (fn: () => void | (() => void), deps: any[]) => {
    const slot = host.react.useRef(undefined);
    if (!slot.current || deps.some((value, index) => value !== slot.current.deps[index])) {
      const before = slot.current, next = {deps, cleanup: undefined as (() => void) | undefined}; slot.current = next; effects.add(next);
      pending.push(() => {before?.cleanup?.(); next.cleanup = fn() || undefined;});
    }
  };
  Object.assign(host.react, {useLayoutEffect}); Object.assign(host.react.default, {useLayoutEffect});
  let navigation: any = {revision: 1, route: initialRoute}, lifetime = true, contextCurrent = true, backs = 0, retiredReads = 0, remoteBacks = 0;
  const hardwareBack: (() => boolean)[] = [];
  const retirement = new AbortController(), opens: any[] = [], shortcutCalls: string[] = [];
  const guarded = <T,>(value: T) => {if (retirement.signal.aborted) {retiredReads++; throw new Error('retired accessor');} return value;};
  const context: any = {current: () => guarded(contextCurrent), account: {account: {role, username: 'Viewer'}, canManage: true, capabilities: {manageSignIn: role === 'owner', managePeople: role === 'owner' || role === 'admin'}}, authority: () => guarded({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'}),
    navigation: {subscribe: () => () => {}, getSnapshot: () => guarded(navigation), isCurrent: () => guarded(true),
      back: () => {backs++; navigation = {revision: navigation.revision + 1, route: {kind: 'shell', destination: {kind: 'home'}}};},
      open: (route: any) => {opens.push(route); navigation = {revision: navigation.revision + 1, route};}}};
  const page = (id: string) => ({id, title: id, icon: 'settings', sections: []});
  const structure = {headings: [{id: 'account', pages: ['profile', 'playback', 'region'].map(page)}, {id: 'server', pages: ['dashboard', 'history', 'metadata', 'segments', 'people', 'remote-sources'].map(page)}]};
  const theme: any = {isTV: search.tv, width: initialWidth, platform: initialWidth < 600 ? 'phone' : 'tablet', colors: new Proxy({}, {get: (_t, key) => String(key)})};
  const {NativeSettingsScreen} = await settingsFixture({'../../../../../packages/client-core/src/home-selection/list-destinations': {withGuardedRaw: (api: unknown) => api}, '../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true},  './tv-settings-focus': tvSettingsFocus,
    react: host.react,
    'react-native': {AppState: {}, BackHandler: {addEventListener: (_name: string, listener: () => boolean) => {hardwareBack.push(listener); return {remove: () => {hardwareBack.splice(hardwareBack.indexOf(listener), 1);}};}}, Dimensions: {get: () => ({width: theme.width, height: 844})}, NativeModules: {}, Platform: {OS: 'ios'}, ScrollView: 'ScrollView', StatusBar: {}, Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '../../../../../packages/client-core/src/presentation/settings-structure': {settingsStructure: () => structure, searchSettings: () => search.matches, findSettingsPage: (searchable: any, address: string) => {for (const heading of searchable.headings) {const found = heading.pages.find((value: any) => value.id === address); if (found) return {heading, page: found};} return undefined;}},
    '../../../../../packages/client-core/src/presentation/language-list': {}, '../../../../../packages/client-core/src/presentation/region-choices': {},
    '../../../../../packages/i18n/src': {defaultI18n: {number: String}}, '../../../../../packages/client-core/src/recommendation-feedback': {}, './settings-icon': await import('../src-next/screens/settings-live/settings-icon.ts'), '../../../../../packages/design/src/next-icons': {nextIcons: {}},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({capture: () => undefined, focus: () => true, entryAddress: () => undefined, subscribe: () => () => {}, getSnapshot: () => 0}), useCovered: () => search.covered},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/shell': {SettingsRowTargetContext: {Provider: 'TargetProvider'}, SettingsRow: 'SettingsRow', SettingsSheet: 'SettingsSheet', SettingsSearchResults: 'SettingsSearchResults', LanguageList: 'LanguageList', ShortcutTiles: 'ShortcutTiles', SettingsSectionLabel: 'SettingsSectionLabel'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfileFace: 'ProfileFace'}, '../../kit/shell/visual': {shellType: () => ({}), alpha: (value: string) => value}, '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateView: 'StateView'}, '../../kit/states/sentence-parts': {}, '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
    '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})}, '../destinations': {NativeTVKeyboard: 'NativeTVKeyboard'}, '../destinations/useEntryFocus': {useDestinationEntryFocus: (activation: any, address: any) => {search.entries.push({activation, address});}},
    '../../shell/chrome': {setNativeScreenChromeHidden() {}}, './Screen': {Screen: 'Screen', useScreenBack() {}, useScreenEntry() {}}, './settings-actions': {},
    '../../../../../packages/contracts/src/routes': {}, '../../../../../packages/client-core/src/settings-configuration': {}, '../../../../../packages/client-core/src/presentation/settings-bindings': {}, '../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec: [{id: 'signin', scope: 'server', title: 'signin', icon: 'settings'}]},
    './owner-libraries/OwnerLibrariesPanel': {NativeOwnerLibrariesPanel: 'Libraries'}, '../../kit/shell/ServerIdentity': {ServerBand: 'ServerBand', ServerHeader: 'ServerHeader'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'LiveOwner'}, './live-owner-controller': {NativeLiveOwnerController: class {}}, './recordings-controller': {createNativeRecordingsController: () => ({})}, '../../platform/server-connections': {nativeRouteCrypto: {}},
    './server/TroubleshootingPage': {NativeServerTroubleshooting: 'Troubleshooting'}, './server/StoragePage': {NativeServerStorage: 'Storage'}, '../../touch/settings/ServerIntegrationsPage': {ServerIntegrationsPage: 'Integrations'}, './ServerGeneralPage': {ServerGeneralPage: 'General'}, './ServerRemoteAccessPage': {ServerRemoteAccessPage: 'RemoteAccess'}, '../../touch/settings/ServerStreamingPage': {ServerStreamingPage: 'Streaming'}, '../../touch/settings/ServerSegmentsPage': {ServerSegmentsPage: 'Segments'}, '../../touch/settings/ServerPeoplePage': {ServerPeoplePage: 'People'}, './server/SignInPage': {NativeServerSignIn: 'SignIn'}, './server/MetadataPage': {NativeServerMetadata: 'Metadata'}, '../../touch/settings/ServerHistoryPage': {ServerHistoryPage: 'History'},
