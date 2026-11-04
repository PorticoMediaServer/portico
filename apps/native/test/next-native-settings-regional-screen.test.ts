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
    './NativeServerOfflineNoticeRow': {NativeServerOfflineNoticeRow: 'NativeServerOfflineNoticeRow'},
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
    '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({capture: () => focus.region ? {regionId: focus.region, childId: 'control'} : undefined, focus: (address: unknown) => {focus.calls.push(address); return true;}, entryAddress: () => undefined, subscribe: () => () => {}, getSnapshot: () => 0})},
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
    './Screen': {Screen: 'Screen', useScreenEntry: () => undefined, useScreenBack: (runtime: any, handler: () => boolean, isTV: boolean) => {if (isTV) runtime.registerTVPageBack?.(handler);}},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    'react-native-qrcode-svg': {default: 'QRCode'},
    '../../shell/chrome': {setNativeScreenChromeHidden: () => {}},
    '../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'},
    './owner-libraries/OwnerLibrariesPanel': {NativeOwnerLibrariesPanel: 'NativeOwnerLibrariesPanel'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'NativeLiveOwnerScreen'},
    './live-owner-controller': {NativeLiveOwnerController: function NativeLiveOwnerController() {}},
    './recordings-controller': {createNativeRecordingsController: () => ({dispose: () => {}})},
    '../../platform/server-connections': {nativeRouteCrypto: {random: (size: number) => new Uint8Array(size)}}, '../../../../../packages/client-core/src/administration': {parseUpdateReport: () => ({updateAvailable: false})},
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
  const runtime = {text: {get t() {const prefix = language; return (key: string) => `${prefix}/${key}`;}}, reportError: (error: unknown) => {throw error;}, subscribe: () => () => {}, getSnapshot: () => shellSnapshot,
    registerTVPageBack: (handler: () => boolean) => {focus.back.push(handler); return () => {focus.back.splice(focus.back.indexOf(handler), 1);};}};
  const settingsRoute = {revision: 0, route: {kind: 'shell', destination: {kind: 'settings'}}};
  const props = {runtime, ports: {context: {current: () => true, account: {account: {role: owner ? 'owner' : 'member'}, canManage: owner, capabilities: {}}, ...(io ? {api: io.api} : {}), serverName: 'Example', authority: () => ({serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'}), navigation: {subscribe: () => () => {}, getSnapshot: () => settingsRoute, isCurrent: () => true}}, settingsContext: () => ({owner, platform: 'phone', capabilities: {localAccount: true}}), serverName: () => 'Example', renderSettingsPanel: () => null},
    controller: {subscribe: () => () => {}, getSnapshot: () => ({preferences, busy: false, saved: 0}), lifetime: io?.lifetime ?? {authority: {}, current: () => true, request: (run: (signal?: AbortSignal) => Promise<unknown>) => run(undefined)}},
    devicePreferences: {subscribe: () => () => {}, read: () => undefined}};
  return {focus, setLanguage: (next: string) => {language = next;},
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

test('W-14: on TV, Left from any page content lands on its page in the column, and Back returns there from a custom page\'s own panel', async () => {
  const f = await fixture(true), tree = f.render();
  // TV focus step 4 (D11): no geometric gate. The rows region declares Left to the page column, and every nested
  // oriented region inherits it, so geometry never decides.
  assert.equal(nodes(tree).some(node => node.type === 'FocusGate'), false);
  const column = nodes(tree).find(node => node.type === 'FocusRegion' && node.props.id === 'settings.pages');
  assert.equal(column.props.edges.right, 'settings.rows.profile', 'Right from the column enters the page rows (whose own Left edge is the column; next-tv-focus-sequences D11)');
  // "Profile & account" draws its own panels, outside the generic rows region.
  f.focus.region = 'settings.panel.profiles';
  const back = f.focus.back.at(-1);
  assert.ok(back, 'TV Settings registers its own Back step');
  assert.equal(back!(), true);
  assert.equal((f.focus.calls.at(-1) as {regionId: string}).regionId, 'settings.pages', 'Back from the panel returns to the page column');
});

test('N5-5: phone and tablet Settings search finds a Server page this owner has, and a result opens it', async () => {
  const f = await fixture(false, true);
  const general = structure.settingsStructure({owner: true, platform: 'phone', capabilities: {localAccount: true}} as never).headings
    .find(heading => heading.id === 'server')!.pages.find(page => page.id === 'general')!;
  let tree = f.render();
  nodes(tree).find(node => node.type === 'TextField').props.onChangeText(`CA/${general.title}`); tree = f.render();
  const results = nodes(tree).find(node => node.type === 'SettingsSearchResults');
  const match = results.props.matches.find((value: {address: string}) => value.address === 'server-general');
  assert.ok(match, 'the Server page is in the results');
  results.props.onNavigate(match.address); tree = f.render();
  assert.ok(nodes(tree).some(node => node.type === 'NativeServerGeneral'), 'and its result opens that page');
});

test('N5-5: a member\'s search finds no Server page they can\'t open', async () => {
  const f = await fixture();
  const general = structure.settingsStructure({owner: true, platform: 'phone', capabilities: {localAccount: true}} as never).headings
    .find(heading => heading.id === 'server')!.pages.find(page => page.id === 'general')!;
  let tree = f.render();
  nodes(tree).find(node => node.type === 'TextField').props.onChangeText(`CA/${general.title}`); tree = f.render();
  const results = nodes(tree).find(node => node.type === 'SettingsSearchResults');
  assert.equal(results?.props.matches.some((value: {address: string}) => value.address === 'server-general') ?? false, false);
});

test('#595 rule on native, through the real path: the owner\'s away-limit read goes through the screen lifetime to HttpLocalApi as optional, so a management refusal never asks for the PIN and leaves only that row out', async () => {
  const {HttpLocalApi} = await import('../../../packages/client-core/src/index.ts');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime.ts');
  const refusal = () => new Response(JSON.stringify({error: {code: 'profile_pin_manage', message: 'Enter this profile\'s PIN to manage this account.'}}), {status: 403, headers: {'content-type': 'application/json'}});
  const paths: string[] = [];
  const api = new HttpLocalApi('https://portico.test', 'token', async input => {paths.push(new URL(String(input)).pathname); return refusal();});
  const gate: unknown[] = []; api.subscribeManageGate(value => {gate.push(value);});
  const shellAuthority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
  const lifetime = new NativeScreenLifetime({authority: () => shellAuthority, current: () => true, subscribeAuthority: () => () => {}});
  const io = {effects: [] as (() => unknown)[], api, lifetime};
  const f = await fixture(false, true, io);
  let tree = f.render();
  for (const effect of io.effects.splice(0)) {try {effect();} catch {/* other effects need native modules this fixture leaves out */}}
  for (let i = 0; i < 10; i++) await new Promise(resolve => setImmediate(resolve));
  assert.ok(paths.includes('/v1/admin/settings/configuration'), 'the away-limit read reached the server');
  assert.deepEqual(gate, [], 'its refusal never asked for the management PIN');
  tree = f.render();
  assert.ok(nodes(tree).some(node => node.type === 'TextField'), 'the page is still drawn');
  assert.equal(nodes(tree).some(node => node.props?.testID === 'settings-away-limit'), false, 'only the away-limit row is left out');
  // Control: the same refusal to an ordinary read through this api does ask, so a dropped option would fail above.
  await api.request('/v1/admin/settings/configuration').catch(() => {});
  assert.equal(gate.length, 1);
  lifetime.dispose();
});
