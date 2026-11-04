import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as tvSettingsFocus from '../src-next/screens/settings-live/tv-settings-focus.ts';

const nodes = (tree: any): any[] => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children), ...nodes(tree.props?.headerContent)] : [];
const find = (tree: any, id: string) => nodes(tree).find(node => node.props?.testID === id);
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
  const structure = {headings: [{id: 'account', pages: ['profile', 'playback', 'region'].map(page)}, {id: 'server', pages: ['dashboard', 'history', 'metadata', 'people', 'remote-sources'].map(page)}]};
  const theme: any = {isTV: false, width: initialWidth, platform: initialWidth < 600 ? 'phone' : 'tablet', colors: new Proxy({}, {get: (_t, key) => String(key)})};
  const {NativeSettingsScreen} = await screenModule(new URL('../src-next/screens/settings-live/SettingsScreen.tsx', import.meta.url), {'../../platform/keyboard': {FormScrollView: 'ScrollView', KeyboardProvider: ({children}: {children: unknown}) => children, useKeyboardHeight: () => 0, touchKeyboard: () => true},  './tv-settings-focus': tvSettingsFocus,
    react: host.react,
    'react-native': {AppState: {}, BackHandler: {addEventListener: (_name: string, listener: () => boolean) => {hardwareBack.push(listener); return {remove: () => {hardwareBack.splice(hardwareBack.indexOf(listener), 1);}};}}, Dimensions: {get: () => ({width: theme.width, height: 844})}, NativeModules: {}, Platform: {OS: 'ios'}, ScrollView: 'ScrollView', StatusBar: {}, Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, left: 0, right: 0, bottom: 0})}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '../../../../../packages/client-core/src/presentation/settings-structure': {settingsStructure: () => structure, searchSettings: () => [], findSettingsPage: () => undefined},
    '../../../../../packages/client-core/src/presentation/language-list': {}, '../../../../../packages/client-core/src/presentation/region-choices': {},
    '../../../../../packages/i18n/src': {defaultI18n: {number: String}}, '../../../../../packages/client-core/src/recommendation-feedback': {}, '../../../../../packages/design/src/next-icons': {nextIcons: {}},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'}, '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({capture: () => undefined, focus: () => true})},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/shell': {SettingsRow: 'SettingsRow', SettingsSheet: 'SettingsSheet', SettingsSearchResults: 'SettingsSearchResults', LanguageList: 'LanguageList', ShortcutTiles: 'ShortcutTiles'},
    '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfileFace: 'ProfileFace'}, '../../kit/shell/visual': {shellType: () => ({}), alpha: (value: string) => value}, '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateView: 'StateView'}, '../../kit/states/sentence-parts': {}, '../../kit/overlays': {Dialog: 'Dialog', Menu: 'Menu'},
    '../../shell/tv-focus': {tvFocusIds: () => ({surface: (id: string) => id, rail: 'rail'})}, '../destinations': {NativeTVKeyboard: 'NativeTVKeyboard'}, '../destinations/useEntryFocus': {useDestinationEntryFocus() {}},
    '../../shell/chrome': {setNativeScreenChromeHidden() {}}, './Screen': {Screen: 'Screen', useScreenBack() {}, useScreenEntry() {}}, './settings-actions': {},
    '../../../../../packages/contracts/src/routes': {}, '../../../../../packages/client-core/src/settings-configuration': {}, '../../../../../packages/client-core/src/presentation/settings-bindings': {}, '../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec: [{id: 'signin', scope: 'server', title: 'signin', icon: 'settings'}]},
    './owner-libraries/OwnerLibrariesPanel': {NativeOwnerLibrariesPanel: 'Libraries'}, '../../kit/shell/ServerIdentity': {ServerBand: 'ServerBand', ServerHeader: 'ServerHeader'},
    './LiveOwnerScreen': {NativeLiveOwnerScreen: 'LiveOwner'}, './live-owner-controller': {NativeLiveOwnerController: class {}}, './recordings-controller': {createNativeRecordingsController: () => ({})}, '../../platform/server-connections': {nativeRouteCrypto: {}}, '../../../../../packages/client-core/src/administration': {parseUpdateReport: () => ({updateAvailable: false})},
    './server/TroubleshootingPage': {NativeServerTroubleshooting: 'Troubleshooting'}, './server/StoragePage': {NativeServerStorage: 'Storage'}, './server/IntegrationsPage': {NativeServerIntegrations: 'Integrations'}, './server/GeneralPage': {NativeServerGeneral: 'General'}, './server/RemoteAccessPage': {NativeRemoteAccessPage: 'RemoteAccess'}, './server/StreamingPage': {NativeServerStreaming: 'Streaming'}, './server/SegmentsPage': {NativeServerSegments: 'Segments'}, './server/PeoplePage': {NativeServerPeople: 'People'}, './server/SignInPage': {NativeServerSignIn: 'SignIn'}, './server/MetadataPage': {NativeServerMetadata: 'Metadata'}, './server/HistoryPage': {NativeServerHistory: 'History'},
    './server/PhoneServerPageHeader': {PhoneServerPageHeader: 'PhoneHeader'}, '../settings/remote-sources/NativeRemoteSourcesScreen': {NativeRemoteSourcesScreen: 'RemoteSources'}, './DashboardScreen': {NativeDashboardScreen: 'Dashboard'},
    '../../platform/playback/equalizer': {}, '../../tv/auth/TVHandoff': {},
    './use-tv-voice-search': {useTVVoiceSearch: () => ({})}, '../../platform/playback/passthrough-formats': {}, './NativeDynamicRangeRow': {}, '../../platform/playback/dynamic-range-matching': {}, './NativeApplePassthroughRow': {}, './NativeCapabilityRows': {}, './NativeFrameRateRow': {}, './NativePassthroughRow': {}, './NativeLossNoticesRow': {}, './NativeServerOfflineNoticeRow': {}, './NativeSafeModeRow': {},
  });
  const runtime: any = {retirementSignal: retirement.signal, text: {t: (id: string) => id}, subscribe: () => () => {}, getSnapshot: () => guarded({profile: {name: 'Profile'}, account: {actions: [{id: 'saved', label: 'Saved', onPress: () => shortcutCalls.push('saved')}]}})};
  const props = {runtime, ports: {context, settingsContext: () => ({}), serverName: () => 'Server'}, controller: {subscribe: () => () => {}, getSnapshot: () => ({}), lifetime: {authority: {}, current: () => lifetime}}, devicePreferences: {}, ...(initialPage ? {initialPage} : {})};
  let tree: any;
  const render = () => {tree = host.render(() => NativeSettingsScreen(props)); for (const effect of pending.splice(0)) effect(); return tree;};
  render();
  return {render, tree: () => tree, find: (id: string) => find(tree, id), nodes: () => nodes(tree), backs: () => backs, opens, shortcutCalls,
    header: () => nodes(tree).find(node => node.type === 'PhoneHeader'),
    routeAway: () => {navigation = {revision: navigation.revision + 1, route: {kind: 'together'}};},
    replace: () => {navigation = {...navigation, revision: navigation.revision + 1, route: {kind: 'shell', destination: {kind: 'settings'}}};},
    retire: () => retirement.abort(), endLifetime: () => {lifetime = false;}, endContext: () => {contextCurrent = false;}, retiredReads: () => retiredReads,
    resize: (width: number) => {theme.width = width; theme.platform = width < 600 ? 'phone' : 'tablet';},
    systemBack: () => [...hardwareBack].reverse().some(listener => listener()),
    remote: (handled: boolean) => nodes(tree).find(node => node.type === 'RemoteSources').props.onBackHandlerChange(() => {remoteBacks++; return handled;}), remoteBacks: () => remoteBacks,
    unmount: () => {for (const effect of effects) effect.cleanup?.();},
  };
}

for (const reason of ['route', 'revision', 'retire', 'lifetime', 'context', 'unmount'] as const) test(`Retained Settings close, choose, shortcut and sheet callbacks refuse after ${reason}`, async () => {
  const f = await fixture();
  const done = f.find('settings-sheet-done').props.onPress, choose = f.find('settings-page-playback').props.onPress;
  const sheet = f.find('settings-sheet').props.onOpenChange, shortcut = f.find('settings-shortcuts').props.actions[0].onPress;
  if (reason === 'route') f.routeAway(); else if (reason === 'revision') f.replace(); else if (reason === 'retire') f.retire(); else if (reason === 'lifetime') f.endLifetime(); else if (reason === 'context') f.endContext(); else f.unmount();
  done(); choose(); sheet(false, 'back'); shortcut();
  assert.equal(f.backs(), 0); assert.deepEqual(f.opens, []); assert.deepEqual(f.shortcutCalls, []); assert.equal(f.retiredReads(), 0);
  if (reason === 'route' || reason === 'revision') {f.render(); assert.equal(f.header(), undefined, 'old choose did not push a page into the replacement opening');}
  f.unmount();
});

test('A phone page owns Back before sheet dismissal; stale hub and page callbacks do not dismiss its successor', async () => {
  const f = await fixture();
  try {
    const hubDone = f.find('settings-sheet-done').props.onPress, hubSheet = f.find('settings-sheet').props.onOpenChange;
    f.find('settings-page-playback').props.onPress(); hubDone(); hubSheet(false, 'outside'); f.render();
    assert.ok(f.header()); assert.equal(f.backs(), 0);
    const oldHeader = f.header().props.onBack; oldHeader(); f.render(); assert.equal(f.header(), undefined);
    f.find('settings-page-region').props.onPress(); f.render(); oldHeader(); f.render(); assert.ok(f.header(), 'old Playback Back cannot pop Region');
    f.find('settings-sheet').props.onOpenChange(false, 'back'); f.render(); assert.equal(f.header(), undefined); assert.equal(f.backs(), 0);
    f.find('settings-sheet-done').props.onPress(); f.render();
    assert.equal(f.find('settings-sheet').props.open, false, 'Done first plays the sheet exit'); assert.equal(f.backs(), 0);
    f.find('settings-sheet-done').props.onPress(); f.find('settings-sheet').props.onOpenChange(false, 'outside'); f.render(); assert.equal(f.backs(), 0);
    f.find('settings-sheet').props.onExited(); assert.equal(f.backs(), 1, 'Back follows the finished exit once');
  } finally {f.unmount();}
});

test('Remote source Back belongs to the current Settings route and is offered before popping its page', async () => {
  const f = await fixture();
  try {
    f.find('settings-page-remote-sources').props.onPress(); f.render(); f.remote(true);
    const back = f.header().props.onBack; back(); f.render(); assert.ok(f.header()); assert.equal(f.remoteBacks(), 1);
    f.routeAway(); back(); assert.equal(f.remoteBacks(), 1); assert.equal(f.backs(), 0);
  } finally {f.unmount();}
});

test('NT-4: on a tablet, where Settings is a page, system Back takes the Remote sources wizard step before leaving Settings', async () => {
  const f = await fixture(undefined, 800);
  try {
    assert.equal(f.find('settings-sheet').props.presentation, 'page');
    f.find('settings-page-remote-sources').props.onPress(); f.render(); f.remote(true);
    assert.equal(f.systemBack(), true, 'the page owns this Back'); assert.equal(f.remoteBacks(), 1); assert.equal(f.backs(), 0, 'Settings stays open');
    f.routeAway(); assert.equal(f.systemBack(), false, 'a page that is no longer on screen never takes Back'); assert.equal(f.remoteBacks(), 1);
  } finally {f.unmount();}
});

test('Trusted denied-server Settings fallback retains ordinary internal navigation and Done', async () => {
  const f = await fixture({kind: 'server-settings'});
  try {
    f.find('settings-page-playback').props.onPress(); f.render(); assert.ok(f.header());
    f.header().props.onBack(); f.render(); f.find('settings-sheet-done').props.onPress(); f.render(); assert.equal(f.backs(), 0);
    f.find('settings-sheet').props.onExited(); assert.equal(f.backs(), 1);
  } finally {f.unmount();}
});

test('Callbacks from the compact presentation retire when the window changes size', async () => {
  const f = await fixture();
  try {const done = f.find('settings-sheet-done').props.onPress; f.resize(800); f.render(); done(); assert.equal(f.backs(), 0);}
  finally {f.unmount();}
});

// Compare element type/key/index all the way to the real page-body component.
// Native mounting is outside this shallow harness; this proves the React ancestry
// no longer switches between a sheet wrapper and an unwrapped page at 600 points.
function ancestry(tree: any, match: (node: any) => boolean, path: any[] = []): any[] | undefined {
  if (Array.isArray(tree)) {for (let index = 0; index < tree.length; index++) {const found = ancestry(tree[index], match, [...path, index]); if (found) return found;} return undefined;}
  if (!tree || typeof tree !== 'object') return undefined;
  const next = [...path, {type: tree.type, key: tree.props?.key}];
  return match(tree) ? next : ancestry(tree.props?.children, match, next);
}

test('An open Settings page keeps its component ancestry and selection through phone-tablet-phone resize', async () => {
  const f = await fixture();
  try {
    f.find('settings-page-playback').props.onPress(); f.render();
    const body = (node: any) => node.props?.page?.id === 'playback';
    const path = ancestry(f.tree(), body); assert.ok(path);
    assert.equal(f.find('settings-sheet').props.host, 'inline'); assert.equal(f.find('settings-sheet').props.presentation, 'sheet');
    for (const width of [800, 390]) {
      f.resize(width); f.render(); f.render();
      assert.deepEqual(ancestry(f.tree(), body), path);
      assert.equal(f.find('settings-sheet').props.host, 'inline');
      assert.equal(f.find('settings-sheet').props.presentation, width < 600 ? 'sheet' : 'page');
    }
    assert.equal(f.header().props.title, 'playback'); assert.equal(f.backs(), 0);
  } finally {f.unmount();}
});

test('J-32: a notice opens Profile & account directly for any profile; a server page still needs a manager', async () => {
  for (const role of ['owner', 'member'] as const) {
    const f = await fixture(undefined, 390, role, 'profile');
    try {assert.equal(f.header()?.props.title, 'profile', `${role}: Review security / Review device lands on Profile & account`);}
    finally {f.unmount();}
  }
  const member = await fixture(undefined, 390, 'member', 'live');
  try {assert.notEqual(member.header()?.props.title, 'live', 'a member is not taken to a server page');}
  finally {member.unmount();}
});

test('A page selected in the wide pane stays pushed when the viewport becomes compact', async () => {
  const f = await fixture(undefined, 800);
  try {
    f.find('settings-page-region').props.onPress(); f.render();
    const path = ancestry(f.tree(), node => node.props?.page?.id === 'region'); assert.ok(path);
    f.resize(390); f.render();
    assert.equal(f.header().props.title, 'region'); assert.deepEqual(ancestry(f.tree(), node => node.props?.page?.id === 'region'), path);
    f.header().props.onBack(); f.render(); assert.equal(f.header(), undefined, 'an explicit Back still releases the pushed page');
  } finally {f.unmount();}
});

test('Opening a wide pane from the compact hub keeps its visible page when shrinking again', async () => {
  const f = await fixture();
  try {
    assert.equal(f.header(), undefined);
    f.resize(800); f.render(); f.render();
    const page = f.nodes().find(node => node.props?.page)?.props.page; assert.ok(page);
    const path = ancestry(f.tree(), node => node.props?.page?.id === page.id);
    f.resize(390); f.render();
    assert.equal(f.header().props.title, page.id); assert.deepEqual(ancestry(f.tree(), node => node.props?.page?.id === page.id), path);
  } finally {f.unmount();}
});

test('Owner Settings mounts each newly available native server page', async () => {
  const f = await fixture();
  try {
    for (const [id, component] of [['history', 'History'], ['metadata', 'Metadata'], ['people', 'People'], ['signin', 'SignIn']]) {
      const row = f.find(`settings-page-${id}`); assert.ok(row, id);
      row.props.onPress(); f.render();
      assert.ok(f.nodes().some(node => node.type === component), `${id} mounts its real renderer slot`);
      assert.equal(f.header().props.title, id);
      f.header().props.onBack(); f.render();
    }
  } finally {f.unmount();}
});


test('Administrator Settings exposes People on phone and tablet without exposing owner pages', async () => {
  for (const width of [390, 800]) {
    const f = await fixture(undefined, width, 'admin');
    try {
      assert.ok(f.find('settings-page-people'));
      assert.equal(f.find('settings-page-history'), undefined);
      assert.equal(f.find('settings-page-metadata'), undefined);
      f.find('settings-page-people').props.onPress(); f.render();
      assert.ok(f.nodes().some(node => node.type === 'People'));
    } finally {f.unmount();}
  }
});

// owner-dashboard 1b/1c (lead-approved): on a phone the Dashboard is a card above the shortcuts, tinted like the Server
// group, and the Server group has no Dashboard row; a tablet keeps the row (the Dashboard is a Settings page there).
const card = (f: Awaited<ReturnType<typeof fixture>>) => f.nodes().find(node => node.type?.name === 'PhoneDashboardCard');
test('Phone owner Settings reaches the Dashboard from a card above the shortcuts, with no Server row for it', async () => {
  const f = await fixture();
  try {
    const shown = card(f);
    assert.ok(shown, 'the card');
    assert.equal(f.find('settings-page-dashboard'), undefined, 'no Dashboard row in the Server group');
    assert.ok(f.find('settings-page-history'), 'the other server rows stay');
    const order = f.nodes().filter(node => node.type?.name === 'PhoneDashboardCard' || node.props?.testID === 'settings-shortcuts' || node.props?.testID === 'settings-profile-card').map(node => node.type?.name ?? node.props.testID);
    assert.deepEqual(order, ['settings-profile-card', 'PhoneDashboardCard', 'settings-shortcuts'], 'profile, then the card, then the shortcuts');
    shown.props.onPress();
    assert.deepEqual(f.opens, [{kind: 'server-dashboard'}]);
    const body = shown.type({...shown.props, update: true});
    const texts = (node: any): string[] => Array.isArray(node) ? node.flatMap(texts) : typeof node === 'string' ? [node] : node?.props ? texts(node.props.children) : [];
    assert.deepEqual(texts(body), ['next.shell.server', 'settings.server.dashboard', 'next.shell.dashboardSummary', 'next.dashboard.update']);
    assert.equal(texts(shown.type({...shown.props, update: false})).includes('next.dashboard.update'), false, 'Update only while one is waiting');
  } finally {f.unmount();}
});

// Who has server pages at all is the settings structure's call (members have none); the card follows the same pages.
test('A tablet keeps the Dashboard row and draws no card', async () => {
  const tablet = await fixture(undefined, 800);
  try {assert.equal(card(tablet), undefined); assert.ok(tablet.find('settings-page-dashboard'));} finally {tablet.unmount();}
});

test('A stale card press after the route moved on opens nothing', async () => {
  const f = await fixture();
  try {const press = card(f).props.onPress; f.routeAway(); press(); assert.deepEqual(f.opens, []);} finally {f.unmount();}
});
