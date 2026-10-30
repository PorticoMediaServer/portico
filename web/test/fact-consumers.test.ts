import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi, parsePreferenceSnapshot} from '@portico/client-core';
import type {ShellFacts, ShellAssemblyContext} from '../src/shell/bootstrap.ts';
import type {ShellIdentity} from '../src/shell/Shell.tsx';
import {createShellFactsSource, forwardFacts, subscribeFacts} from '../src/shell/fact-source.ts';
import {subscribePresentation} from '../src/shell/fact-hooks.ts';
import {forwardIdentity, screenLifetime, type ScreenContext} from '../src/screens/detail/context.ts';
import {createReadBindings} from '../src/screens/read/bindings.ts';
import {profileTargetKey, switchOfferKey} from '../src/shell/profile-offer.ts';
import {libraryChannelAdmission} from '../src/screens/live/owner/library-channel-admission.ts';
import {libraryChannelUpload} from '../src/screens/live/owner/library-channel-upload.ts';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer, tree, one, all} from './helpers/interaction-harness.mjs';
import {defaultI18n} from '@i18n';

const preferences = parsePreferenceSnapshot(JSON.parse(readFileSync(new URL('../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences);
const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', role: 'owner'};
function fixture() {
  let current = true, session = {viewer, sessionFamilyId: 'family', deviceId: 'device'}, calls = 0;
  const identity: ShellIdentity = {viewer, username: 'user', profileName: 'Profile', serverName: 'Server', primaryProfile: true,
    profiles: [{id: 'profile', name: 'Profile', initial: 'P', revision: 1, pinRevision: 1, trustRevision: 1}, {id: 'other', name: 'Other', initial: 'O', pinRequired: true, revision: 2, pinRevision: 3, trustRevision: 4}],
    capabilities: {manageServer: true} as never, canManageProfiles: true, accountFacts: [], libraries: [{id: 'library', name: 'Books', kind: 'book'}], liveAvailable: false, unread: 0, showNotificationDot: true};
  const source = createShellFactsSource({identity, preferences, serverName: 'Server'});
  const api = new HttpLocalApi('https://server.invalid', '', async () => {calls++;throw new Error('Unexpected read');});
  const context = {api, session: () => session, isCurrent: () => current, factsSource: source, get facts() {return source.getSnapshot();},
    artwork: {}, events: {on: () => () => {}}, epoch: 1, viewerFence: 'fence'} as unknown as ShellAssemblyContext & {factsSource: typeof source};
  return {source, context, api, calls: () => calls, retire: () => {current = false;}, replaceSession: () => {session = {...session, sessionFamilyId: 'replacement'};},
    rename() {source.update({identity: {...source.getSnapshot().identity, serverName: 'Renamed server', profileName: 'Renamed profile', profileArt: 'green', libraries: [{id: 'library', name: 'Renamed books', kind: 'book'}]}, serverName: 'Renamed server'});}};
}

/** Use the existing hook harness, with real source subscription effects. Notifications
 * are counted independently of explicitly rendering the next committed projection. */
async function projectionHarness() {
  const h = createRenderer();let notifications = 0;
  h.react.useSyncExternalStore = (subscribe: (changed: () => void) => () => void, read: () => unknown) => {
    h.react.useEffect(() => subscribe(() => {notifications++;}), [subscribe]);return read();
  };
  const hooks = await componentModule(new URL('../src/shell/fact-hooks.ts', import.meta.url), {react: h.react});
  const identity = await componentModule(new URL('../src/screens/detail/context.ts', import.meta.url), {react: h.react});
  return {h, hooks, identity, notifications: () => notifications};
}

test('actual ScreenContext producer and nested copies notify current display without changing original command witnesses', async () => {
  const f = fixture(), {h, hooks, notifications} = await projectionHarness();
  const module = await componentModule(new URL('../src/screens/browse/screen-context.ts', import.meta.url), {
    react: h.react, '../../shell/fact-source': {subscribeFacts}, '../../shell/fact-hooks': hooks,
    '../../platform/browser-download': {createBrowserDownloadPort: () => ({})},
    '../../shell/entry-metadata': {createEntryMetadataEffect: () => {}, retireEntryMetadata() {}},
    '../../shell/entry-actions': {entryActions: {retire() {}}, addToDestinationEffect: () => {}, deleteTitleEffect: () => {}},
  });
  const binding = {scopeKey: 'scope', home: {effects: {reportError() {}}}};
  const captured = {context: f.context, binding};
  const draw = () => h.render(() => module.useLibraryScreenContext(captured));
  const original: ScreenContext = draw(), witness = original.isCurrent, effects = original.effects;
  const first = forwardIdentity(original, {isCurrent: () => witness()}), nested = forwardIdentity(first, {});
  let identityNotifications = 0, preferenceNotifications = 0;
  const off = nested.subscribeIdentity!(() => identityNotifications++), offPreferences = nested.preferences.subscribe(() => preferenceNotifications++);
  f.rename();
  assert.ok(notifications() > 0);assert.equal(identityNotifications, 1);assert.equal(preferenceNotifications, 1);
  assert.strictEqual(draw(), original);assert.strictEqual(nested.effects, effects);assert.strictEqual(original.isCurrent, witness);
  assert.equal(nested.identity?.serverName, 'Renamed server');assert.equal(nested.identity?.libraries[0]?.name, 'Renamed books');
  assert.equal(f.calls(), 0);assert.ok(nested.isCurrent());f.retire();assert.equal(nested.isCurrent(), false);
  off();offPreferences();h.dispose();f.source.dispose();
});

test('actual live lease copies keep their owners and forward identity notifications', async () => {
  for (const kind of ['guide', 'group', 'rules'] as const) {
    const f = fixture(), h = createRenderer();let received: ScreenContext | undefined, disposed = 0, constructed = 0;
    const screen = {api: f.api, events: f.context.events, artwork: {}, scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: f.context.isCurrent,
      get identity() {return f.context.facts.identity;}, subscribeIdentity: (listener: () => void) => subscribeFacts(f.context, listener),
      preferences: {getSnapshot: () => preferences, subscribe: () => () => {}}, effects: {reportError() {}}} as unknown as ScreenContext;
    const make = (context: ScreenContext) => {received = context;constructed++;return {life: screenLifetime(context), load: async () => {}, failure: {subscribe: () => () => {}, getSnapshot: () => undefined}, dispose() {disposed++;}};};
    const file = kind === 'guide' ? 'useLiveLease' : kind === 'group' ? 'useRecordedGroupLease' : 'useSeriesRulesLease';
    const module = await componentModule(new URL(`../src/screens/live/${file}.ts`, import.meta.url), {react: h.react, '@i18n': {defaultI18n},
      '../detail/context': {forwardIdentity, screenLifetime}, './bindings': {createLiveBindings: make}, './recorded-group-reader': {createRecordedGroupReader: make},
      './recorded-group-bindings': {createRecordedGroupReader: make}, './recorded-group': {createRecordedGroupReader: make}, './series-rules-bindings': {createSeriesRulesBindings: make}});
    const draw = () => h.render(() => kind === 'guide' ? module.useLiveLease(screen, '{}', '/live', () => true, undefined, 1)
      : kind === 'group' ? module.useRecordedGroupLease(screen, 'group', 'recent', () => true) : module.useSeriesRulesLease(screen, () => true));
    draw();const lease = draw();assert.ok(lease);assert.ok(received, kind);
    let changes = 0;const off = received!.subscribeIdentity!(() => changes++);
    f.rename();assert.equal(changes, 1);assert.equal(received!.identity?.serverName, 'Renamed server');assert.strictEqual(draw(), lease);assert.equal(constructed, 1);assert.equal(disposed, 0);
    f.retire();assert.equal(received!.isCurrent(), false);off();h.dispose();assert.equal(disposed, 1);f.source.dispose();
  }
});

test('Read preference fallback crosses both shell copies and notifies without replacing its captured client', async () => {
  const f = fixture(), derived = forwardFacts(forwardFacts(f.context, {}), {isCurrent: f.context.isCurrent}), binding = createReadBindings(derived);
  const client = binding.client;let changes = 0;const off = binding.preferences.subscribe(() => changes++);
  const next = {...preferences, effective: {...preferences.effective, 'reading.fontSize': 21}};
  f.source.update({preferences: next});assert.equal(changes, 1);assert.strictEqual(binding.preferences.getSnapshot(), f.source.getSnapshot().preferences);
  assert.strictEqual(binding.client, client);assert.ok(binding.authority.isCurrent());assert.equal(f.calls(), 0);
  f.replaceSession();assert.equal(binding.authority.isCurrent(), false);off();await binding.dispose();f.source.dispose();
});

test('existing PreferencesReader stays primary while display source notifications remain observable', () => {
  const f = fixture(), preferenceListeners = new Set<() => void>(), reader = {subscribe: (listener: () => void) => {preferenceListeners.add(listener);return () => {preferenceListeners.delete(listener);};}};
  let changes = 0;const off = subscribePresentation({...f.context, preferences: reader}, () => changes++);
  f.rename();assert.equal(changes, 1);for (const listener of preferenceListeners) listener();assert.equal(changes, 2);off();f.rename();assert.equal(changes, 2);assert.equal(preferenceListeners.size, 0);f.source.dispose();
});

test('Together republishes display and fallback preferences on the same service, room, runtime and item map', async () => {
  const f = fixture(), serviceListeners = new Set<() => void>(), room = {phase: 'idle'}, runtime = {getSnapshot: () => ({disposed: false}), bindGroup: () => () => {detached++;}};
  let detached = 0, disposed = 0, constructed = 0;
  class Group {constructor() {constructed++;}getSnapshot = () => room;subscribe = (listener: () => void) => {serviceListeners.add(listener);return () => {serviceListeners.delete(listener);};};setI18n() {}dispose() {disposed++;}}
  const module = await componentModule(new URL('../src/screens/together/bindings.ts', import.meta.url), {'@core/group-session.ts': {GroupSessionService: Group}, '../../shell/fact-hooks': {subscribePresentation},
    '../../shell/settings': {shellLanguage: () => defaultI18n}, './item-summaries': {TogetherItemSummaries: class {update() {}dispose() {}invalidate() {}}}});
  const binding = module.createTogetherBindings(f.context, {runtime, artwork: f.context.artwork, environment: defaultI18n, reportError() {}});
  binding.start();const service = binding.service, items = binding.getSnapshot().items;let changes = 0;const off = binding.subscribe(() => changes++);
  f.rename();assert.equal(changes, 1);assert.equal(binding.profileName, 'Renamed profile');assert.equal(binding.profileArt, 'green');assert.strictEqual(binding.service, service);assert.strictEqual(binding.runtime, runtime);
  assert.strictEqual(binding.getSnapshot().room, room);assert.strictEqual(binding.getSnapshot().items, items);assert.equal(constructed, 1);assert.equal(detached, 0);assert.equal(f.calls(), 0);
  off();binding.dispose();assert.equal(detached, 1);assert.equal(disposed, 1);f.source.dispose();
});

test('route library rename is live; tolerant absence retains the original route and actual authority retirement wins', async () => {
  const f = fixture(), {h, hooks} = await projectionHarness();
  const module = await componentModule(new URL('../src/screens/browse/route-library.ts', import.meta.url), {react: h.react, '../../shell/fact-hooks': hooks});
  const draw = (id = 'library') => h.render(() => module.useRouteLibrary(f.context, id));
  assert.equal(draw().name, 'Books');f.rename();const renamed = draw();assert.equal(renamed.name, 'Renamed books');
  f.source.update({identity: {...f.context.facts.identity, libraries: []}});assert.strictEqual(draw(), renamed);assert.equal(draw('other'), undefined);
  f.retire();assert.equal(draw(), undefined);h.dispose();f.source.dispose();
});

test('channel admission notifies current choices while upload keeps its original principal and cannot acquire a display grant', async () => {
  const f = fixture(), owner = {current: f.context.isCurrent, serverId: 'server', api: f.api, reportError() {}};
  const ports = libraryChannelAdmission(f.context, owner as never);let changes = 0;const off = ports.subscribe(() => changes++);
  f.rename();assert.equal(changes, 1);assert.equal(ports.libraries[0]?.name, 'Renamed books');assert.strictEqual(ports.api, f.api);
  const upload = libraryChannelUpload(f.context, f.context.isCurrent, () => {});f.replaceSession();
  await assert.rejects(upload('channel', new File(['x'], 'cover.png', {type: 'image/png'}), 'a'.repeat(48), new AbortController().signal), error => (error as {code: string}).code === 'cancelled');assert.equal(f.calls(), 0);
  off();f.source.dispose();
  const denied = fixture();denied.source.update({identity: {...denied.context.facts.identity, capabilities: {manageServer: false} as never}});
  const refused = libraryChannelUpload(denied.context, denied.context.isCurrent, () => {});
  denied.source.update({identity: {...denied.context.facts.identity, capabilities: {manageServer: true} as never}});
  await assert.rejects(refused('channel', new File(['x'], 'cover.png', {type: 'image/png'}), 'a'.repeat(48), new AbortController().signal), error => (error as {code: string}).code === 'cancelled');
  assert.equal(denied.calls(), 0);denied.source.dispose();
});

test('switch proof excludes display names/art/ordering but includes original target/PIN/trust revisions', () => {
  const f = fixture(), original = switchOfferKey(f.context.facts.identity), target = f.context.facts.identity.profiles[1]!;
  f.rename();assert.equal(switchOfferKey(f.context.facts.identity), original);
  assert.equal(switchOfferKey({...f.context.facts.identity, profiles: [...f.context.facts.identity.profiles].reverse()}), original);
  for (const field of ['revision', 'pinRevision', 'trustRevision'] as const) assert.notEqual(profileTargetKey({...target, [field]: 77}), profileTargetKey(target));
  f.source.dispose();
});

test('mounted Shell retains switch and PIN input through rename and retires the original target on changed proof', async () => {
  const f = fixture(), h = createRenderer(), priorWindow = globalThis.window, location = {pathname: '/', search: {}};
  globalThis.window = {localStorage: {getItem: () => null, setItem() {}}} as never;
  const navigate = () => {};const switches: {id: string; pin: string; signal: AbortSignal}[] = [];
  const selectProfile = async (id: string, pin: string, signal: AbortSignal) => {switches.push({id, pin, signal});};
  const module = await componentModule(new URL('../src/shell/Shell.tsx', import.meta.url), {react: h.react, '@i18n': {defaultI18n},
    './profile-offer': {profileTargetKey, switchOfferKey, switchAuthorityKey: (await import('../src/shell/profile-offer.ts')).switchAuthorityKey},
    '@tanstack/react-router': {useNavigate: () => navigate, useRouterState: ({select}: any) => select({location}), Link: 'Link'},
    '../kit/theme': {useSizeClass: () => 'expanded', useCoarsePointer: () => false, useWindowScrollAnchor() {}}, './viewport': {useShellScrolled: () => false}, './window-chrome': {usePageWindowChrome: () => ({})},
    '../kit/shell': {AccountMenu: 'AccountMenu', ProfileAvatar: 'ProfileAvatar', ProfileAvatarFace: 'ProfileAvatarFace'},
    '../kit/selection': {PhoneSelectionProvider: ({children}: any) => children, usePhoneSelectionPresent: () => false}, '../kit/overlays': {Dialog: 'Dialog'}, '../kit/controls': {Button: 'Button', TextField: 'TextField'},
    './SwitchProfileServerHost': {SwitchProfileServerHost: 'SwitchProfileServerHost', HostedServerOpening: 'HostedServerOpening'}});
  const draw = () => tree(h.render(() => module.Shell({identity: f.context.facts.identity, OwnerActivity: () => null, dockVisible: false, onSignOut() {}, onSwitchProfile: selectProfile})));
  try {
    draw();let ui = draw();one(ui, 'AccountMenu').props.actions.switchServer();ui = draw();const opening = one(ui, 'SwitchProfileServerHost').props.opening;
    f.rename();ui = draw();assert.strictEqual(one(ui, 'SwitchProfileServerHost').props.opening, opening);assert.ok(opening.current());
    one(ui, 'AccountMenu').props.actions.switchProfile(f.context.facts.identity.profiles[1]);ui = draw();assert.ok(one(ui, 'Dialog').props.open);
    one(ui, 'TextField').props.onChange({currentTarget: {value: '1234'}});ui = draw();const originalConfirm = all(tree(one(ui, 'Dialog').props.footer), 'Button')[1].props.onClick;
    f.source.update({identity: {...f.context.facts.identity, serverName: 'Again', profiles: f.context.facts.identity.profiles.map(profile => ({...profile, name: 'Renamed ' + profile.name}))}});
    ui = draw();assert.ok(one(ui, 'Dialog').props.open);assert.equal(one(ui, 'TextField').props.value, '1234');
    f.source.update({identity: {...f.context.facts.identity, profiles: f.context.facts.identity.profiles.map(profile => profile.id === 'other' ? {...profile, pinRevision: 99} : profile)}});
    draw();ui = draw();assert.equal(one(ui, 'Dialog').props.open, false);assert.equal(opening.current(), false);originalConfirm();assert.equal(switches.length, 0);
  } finally {h.dispose();globalThis.window = priorWindow;f.source.dispose();}
});

test('mounted Dashboard updates owned chrome without rebuilding report readers; admission retirement still disposes them', async () => {
  const f = fixture(), {h, hooks} = await projectionHarness();let constructed = 0, disposed = 0;
  const context = {...f.context, preferences: {getSnapshot: () => preferences, subscribe: () => () => {}}};
  // Preserve the real live getter across this test-only composition too.
  Object.defineProperty(context, 'facts', {get: () => f.context.facts});
  const module = await componentModule(new URL('../src/screens/dashboard/entry.phone.tsx', import.meta.url), {react: h.react, '@i18n': {defaultI18n}, '../../shell/fact-hooks': hooks,
    '../../route-registry-view': {useProtectedWebScreenContext: () => ({context})}, '../../shell/settings': {shellLanguage: () => defaultI18n},
    './bindings': {createDashboardBindings: () => {constructed++;return {dispose() {disposed++;}};}}, './Dashboard': {DashboardScreen: 'DashboardScreen'}, './phone': {PhonePage: 'PhonePage'},
    '../../router': {router: {navigate() {}}}});
  const draw = () => tree(h.render(() => module.DashboardPhoneEntry()));
  try {
    draw();let ui = draw();const dashboard = one(ui, 'DashboardScreen').props.bindings, chrome = one(ui, 'DashboardScreen').props.chrome;
    f.rename();ui = draw();assert.strictEqual(one(ui, 'DashboardScreen').props.bindings, dashboard);assert.strictEqual(one(ui, 'DashboardScreen').props.chrome, chrome);assert.equal(chrome.serverName, 'Renamed server');assert.equal(constructed, 1);assert.equal(disposed, 0);
    f.source.update({identity: {...f.context.facts.identity, primaryProfile: false}});draw();assert.equal(disposed, 1);
  } finally {h.dispose();f.source.dispose();}
});

test('mounted first-library opening keeps the original Add Library service and form through a server rename', async () => {
  const f = fixture(), {h, hooks} = await projectionHarness(), location = {href: '/', pathname: '/'};
  f.source.update({identity: {...f.context.facts.identity, libraries: []}});
  const screen = {api: f.api, events: f.context.events, scope: {serverId: 'server', viewerId: 'viewer'}, isCurrent: f.context.isCurrent, effects: {reportError() {}}};
  const module = await componentModule(new URL('../src/screens/home/first-library-state.tsx', import.meta.url), {react: h.react, '@i18n': {defaultI18n}, '../../shell/fact-hooks': hooks,
    '@tanstack/react-router': {useLocation: ({select}: any) => select(location)}, '../../router': {router: {state: {location}, navigate() {}}},
    '../browse/screen-context': {useCommittedLibraryScreenContext: () => screen}, '../detail/context': {screenLifetime},
    '../../shell/viewport': {useShellWidth: () => 1200}, '../../kit/states': {StateLayout: 'StateLayout'}, '../settings/libraries/AddLibrary': {AddLibrary: 'AddLibrary'}});
  const captured = {context: f.context, binding: {}};
  const draw = () => tree(h.render(() => module.FirstLibraryState({captured})));
  try {
    draw();let ui = draw();one(ui, 'StateLayout').props.primary.onPress();draw();ui = draw();const service = one(ui, 'AddLibrary').props.service;
    f.source.update({serverName: 'Renamed empty server', identity: {...f.context.facts.identity, serverName: 'Renamed empty server'}});
    ui = draw();assert.strictEqual(one(ui, 'AddLibrary').props.service, service);assert.equal(one(ui, 'AddLibrary').props.serverName, 'Renamed empty server');assert.equal(f.calls(), 0);
    f.source.update({identity: {...f.context.facts.identity, libraries: [{id: 'new', kind: 'music', name: 'Music'}]}});draw();assert.equal(all(draw(), 'AddLibrary').length, 0);
  } finally {h.dispose();f.source.dispose();}
});

test('Activity explicitly notifies its admitted source and redraws the name without replacing the service', async () => {
  const f = fixture(), {h, hooks} = await projectionHarness();let constructed = 0, loads = 0, sourceChanges = 0, lastSource: unknown;
  class Activity {
    state = {};off: () => void;source: any;
    constructor({source}: any) {constructed++;this.source = source;lastSource = source.getSnapshot();this.off = source.subscribe(() => {sourceChanges++;lastSource = source.getSnapshot();});}
    getSnapshot = () => this.state;subscribe = () => () => {};setActive() {}attach() {}async load() {loads++;}async retry() {}dispose() {this.off();}
  }
  const module = await componentModule(new URL('../src/shell/Activity.tsx', import.meta.url), {react: h.react, './fact-source': {subscribeFacts}, './fact-hooks': hooks,
    '@core/admin/activity-summary/index.ts': {ActivitySummaryService: Activity}, '../kit/shell': {TopBarActivity: 'TopBarActivity'}, './activity-model': {activityPanelView: (_state: unknown, serverName: string) => ({serverName})}});
  const owner = module.createOwnerActivity(f.context, {}, () => {}, () => {}, () => {});
  const draw = () => tree(h.render(() => h.react.createElement(owner.OwnerActivity, {})));
  try {
    draw();assert.equal(loads, 1);assert.ok(lastSource);f.rename();assert.equal(sourceChanges, 1);assert.equal(one(draw(), 'TopBarActivity').props.view.serverName, 'Renamed server');assert.equal(constructed, 1);assert.equal(loads, 1);
    f.source.update({identity: {...f.context.facts.identity, capabilities: {manageServer: false} as never}});assert.equal(lastSource, null);
  } finally {h.dispose();owner.dispose();f.source.dispose();}
});

test('Browse and Show committed adapters keep the original services and forward live identity into their command contexts', async () => {
  for (const kind of ['browse', 'show'] as const) {
    const f = fixture(), h = createRenderer();let received: ScreenContext | undefined, constructed = 0, disposed = 0;
    const context = {api: f.api, events: f.context.events, scope: {serverId: 'server', viewerId: 'viewer'}, artwork: {}, isCurrent: f.context.isCurrent,
      get identity() {return f.context.facts.identity;}, subscribeIdentity: (listener: () => void) => subscribeFacts(f.context, listener), preferences: {getSnapshot: () => preferences, subscribe: () => () => {}}, effects: {reportError() {}}} as unknown as ScreenContext;
    const file = kind === 'browse' ? '../src/screens/browse/useBrowseLease.ts' : '../src/screens/detail/use-show-route.ts';
    const module = await componentModule(new URL(file, import.meta.url), {react: h.react, '../detail/context': {forwardIdentity, screenLifetime}, './context': {forwardIdentity},
      './controller': {BrowseController: class {constructor() {constructed++;}dispose() {disposed++;}}},
      './show-bindings': {createShowBindings: (screen: ScreenContext) => {received = screen;constructed++;return {select: async () => {}, dispose() {disposed++;}};}},
      './episode-selection': {createEpisodeSelection: () => ({start() {}, dispose() {}})}});
    const captured = {context: f.context, binding: {}};
    const draw = () => h.render(() => kind === 'browse' ? module.useBrowseLease(context, '{}', '/library/library', () => true) : module.useShowRoute(context, captured, {libraryId: 'library', showId: 'show'}));
    try {
      draw();const lease = draw();if (kind === 'browse') received = lease.screen;assert.ok(received);let changes = 0;const off = received!.subscribeIdentity!(() => changes++);
      f.rename();assert.equal(changes, 1);assert.strictEqual(draw(), lease);assert.equal(received!.identity?.libraries[0]?.name, 'Renamed books');assert.equal(constructed, 1);assert.equal(disposed, 0);
      f.retire();assert.equal(received!.isCurrent(), false);off();
    } finally {h.dispose();assert.equal(disposed, 1);f.source.dispose();}
  }
});
