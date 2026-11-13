import assert from 'node:assert/strict';
import test, {type TestContext} from 'node:test';
import {NativeModules} from './react-native-stub.mjs';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src/index.ts';
import {installationIdentity, setInstallationIdentity} from '../../../packages/client-core/src/installation.ts';
import {deviceClassHeader} from '../../../packages/client-core/src/delivery.ts';
import {accountAnswer, accountPath} from '../../../packages/client-core/test/next-wire.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {nativeNetworkFetch} from '../src-next/platform/server-connections.ts';
import {nativePlaybackSettings} from '../src-next/shell/playback-settings.ts';
import {NativeShellBootstrapServices, nativeShellDirectViewer, nativeShellPermissionDenied} from '../src-next/shell/bootstrap-services.ts';
import {createNativeShellAccountBinding} from '../src-next/shell/account-binding.ts';
import type {AppServerSession} from '../../../packages/client-core/src/app-sessions.ts';

const origin = 'https://native-shell.test';
const viewer: LocalSession['viewer'] = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'owner'};
type DeviceClass = 'mobile' | 'television';
type Request = {id: string; url: URL; method: string; headers: Headers; body: string | null};
type Reply = {value: unknown; status?: number};
const counts = (unread: number) => ({unread, read: 0, archived: 0, total: unread});
const abortError = (error: unknown) => error instanceof Error && error.name === 'AbortError';
test('permission classification accepts protocol codes without coercing arbitrary error values', () => {
  for (const code of ['unauthorized', 'forbidden', 'access_refused', 'identity_mismatch']) assert.equal(nativeShellPermissionDenied({code}), true);
  for (const status of [401, 403]) assert.equal(nativeShellPermissionDenied({status}), true);
  assert.equal(nativeShellPermissionDenied({code: {toString() {throw new Error('must not coerce error code');}}}), false);
  assert.equal(nativeShellPermissionDenied({code: 'network_error'}), false);
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(yes => {resolve = yes;});
  return {promise, resolve};
}
function preferenceData(deviceClass: DeviceClass, speed: number) {
  const effective = {'playback.defaultSpeed': speed, 'audiobooks.defaultSpeed': 1.65, 'playback.skipBackMs': 13_000,
    'playback.introSkip': 'ask', 'playback.recapSkip': 'auto', 'playback.creditsSkip': 'off',
    'playback.subtitleSize': 'large', 'playback.subtitleBackground': 'translucent',
    'playback.subtitleColor': 'white', 'playback.subtitlePosition': 'bottom', 'playback.subtitleFont': 'default',
    'playback.keepSubtitleStyling': true, 'playback.useSystemCaptionStyle': false};
  return {deviceClass, registryRevision: 'preferences-actual-test', registry: {revision: 'preferences-actual-test',
    fields: Object.entries(effective).map(([key, value]) => ({key, type: typeof value === 'number' ? 'number' : typeof value === 'boolean' ? 'boolean' : 'string',
      default: key === 'playback.defaultSpeed' ? 1 : value, group: key.split('.')[0], labelKey: 'preferences.' + key, scopes: ['profile-server', 'profile-device-class']}))},
    documents: ['profile-server', 'profile-device-class'].map(scope => ({scope, revision: 2, activeRevision: 2, digest: 'd'.repeat(64), values: scope === 'profile-device-class' ? effective : {}})),
    effective, effectiveSource: Object.fromEntries(Object.keys(effective).map(key => [key, 'profile-device-class'])), clampedFields: []};
}
function homeRow(fence: string) {
  return {id: 'recent', viewerFence: fence, title: 'Recent', kind: 'recent', artworkShape: 'poster', endpoint: routePath('get_home_row', {id: 'recent'}),
    libraryId: 'movies', privacySensitivity: 'catalog', policyState: 'available', priority: 100, required: false, hideable: true, reorderable: true,
    critical: false, cursorCapable: true, entries: [{id: 'movie', kind: 'movie', title: 'Private catalogue title', libraryId: 'movies', available: true,
      navigation: {view: 'item', entityId: 'movie'}, playback: {itemId: 'movie'}}], total: 2, start: 0, limit: 1, hasMore: true, nextCursor: 'page-two', revision: {catalog: 1, viewer: 1}};
}

/** Only the native transport is stood in. Requests traverse the actual HttpLocalApi,
 * native cancellation wrapper, PreferencesReader, InboxService, HomeService and guide parser. */
function fixture(t: TestContext, deviceClass: DeviceClass = 'mobile', role: 'owner' | 'admin' | 'member' = 'owner', linkedAccountId?: string) {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
  const names = ['networkRequest', 'playbackRequestId', 'cancelQueueRequest'] as const;
  const before = Object.fromEntries(names.map(name => [name, runtime[name]])), claim = installationIdentity();
  const api = new HttpLocalApi(origin, 'in_memory_access', nativeNetworkFetch);
  api.setDeviceClass(deviceClass);
  setInstallationIdentity({installationId: 'native_bootstrap_0123456789abcdef01234567', name: 'Native test device', app: 'Portico', platform: 'android', appVersion: '1.9.42'});
  const controller = new AbortController();
  const state = {
    current: true, ended: 0, speed: 1.85, deviceClass, role: String(role), disabled: false, linkedAccountId,
    fences: {home: 'home-domain', preferences: 'a'.repeat(64), inbox: 'b'.repeat(64), guide: 'guide-domain'},
    servers: {home: 'server', preferences: 'server', inbox: 'server', guide: 'server'},
    calls: [] as Request[], cancellations: [] as string[],
    reply: async (_request: Request): Promise<Reply> => {throw new Error('fixture not initialized');},
  };
  const envelope = (domain: 'inbox', data: unknown) => ({scope: {serverId: state.servers[domain], viewerFence: state.fences[domain]}, data});
  const response = (request: Request): Reply => {
    const path = request.url.pathname;
    if (path === routePath('get_preferences')) {
      // This is the server's required query contract. A delivery header cannot supply it.
      if (request.url.searchParams.get('deviceClass') !== deviceClass) return {status: 400, value: {error: {code: 'invalid_console_request', message: 'Invalid device class.'}}};
      return {value: preferenceData(state.deviceClass, state.speed)};
    }
    // The rebuilt server's inbox: one per profile, with its unread count, and no envelope.
    if (path === '/v1/notifications') return {value: {items: [], unreadCount: 2}};
    if (path === '/v1/live/channels') return {value: {items: [], sources: [{id: 'live-source', name: 'Actual authorized source', kind: 'live', channels: 2, favorites: 1, groups: [{name: 'News', count: 2}]}]}};
    if (path === routePath('get_home')) return {value: {serverId: state.servers.home, viewerFence: state.fences.home, rows: [homeRow(state.fences.home)], spotlight: [],
      layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T12:00:00Z'}};
    if (path === routePath('get_home_row', {id: 'recent'})) return {value: {...homeRow(state.fences.home), entries: [], start: 1, hasMore: false, nextCursor: ''}};
    if (accountPath(path)) return state.disabled ? {status: 401, value: {error: {code: 'unauthenticated'}}} : {value: accountAnswer(path, {account: {id: 'account', username: 'owner', role: state.role},
      profiles: [{id: 'profile', name: 'Actual profile', primary: true, art: 'mint'}]})};
    throw new Error('Unexpected request: ' + path);
  };
  state.reply = async request => response(request);
  let sequence = 0;
  runtime.playbackRequestId = () => 'bootstrap-' + ++sequence;
  runtime.cancelQueueRequest = (id: string) => {state.cancellations.push(id);};
  runtime.networkRequest = async (id: string, url: string, method: string, headers: Record<string, string>, body: string | null) => {
    const request = {id, url: new URL(url), method, headers: new Headers(headers), body}; state.calls.push(request);
    const reply = await state.reply(request);
    return {status: reply.status ?? 200, headers: {'Content-Type': 'application/json'}, body: JSON.stringify(reply.value)};
  };
  const pin = {serverId: 'server', publicKey: 'original-key', fingerprint: 'original-fingerprint'};
  const session: LocalSession = {viewer: {...viewer, role}, accessToken: 'in_memory_access', tokenGeneration: '1', sessionFamilyId: 'family',
    expiresAt: '2026-10-25T22:00:00Z', authorizationHorizon: '2026-10-26T22:00:00Z'};
  const selected: AppServerSession = {signIn: linkedAccountId ? 'portico' : 'local', connection: {version: '1', pin, session,
    logicalOrigin: origin, routes: {paired: []}, ...(linkedAccountId ? {hosted: {origin: 'https://accounts.test', accountId: linkedAccountId,
      key: {keyId: 'route-key', publicKey: 'route-public'}}} : {})}};
  const accountBinding = createNativeShellAccountBinding(selected, session, {current: () => state.current,
    session: () => session, pin: () => pin, readSelected: async () => selected});
  const services = new NativeShellBootstrapServices(api, {viewer: {...viewer, role}, deviceClass, signal: controller.signal, current: () => state.current,
    accountBinding, onAuthorityEnded: () => {state.ended++;}});
  t.after(() => {services.dispose(); for (const name of names) runtime[name] = before[name]; api.setDeviceClass(''); setInstallationIdentity(claim);});
  return {api, state, controller, services, response};
}

test('Home and required preferences can finish while auxiliary badges and guide are still waiting', async t => {
  const f = fixture(t), delayed = deferred<void>(), entered = deferred<void>();
  f.state.reply = async request => {
    if (new Set<string>(['/v1/notifications', '/v1/live/channels']).has(request.url.pathname)) {entered.resolve(); await delayed.promise;}
    return f.response(request);
  };
  let auxiliaryReady = false;
  const auxiliary = Promise.all([f.services.readUnread(), f.services.readGuideAvailability()]).then(value => {auxiliaryReady = true; return value;});
  await entered.promise;
  const [preferences] = await Promise.all([f.services.readPreferences('mobile'), f.services.home.load()]);
  assert.equal(preferences.deviceClass, 'mobile'); assert.equal(f.services.home.getSnapshot().phase, 'ready');
  assert.equal(auxiliaryReady, false, 'the ready Home does not wait for inbox or guide');
  delayed.resolve(); assert.deepEqual(await auxiliary, [2, true]);
});

test('a preference-only refresh never requests inbox, guide, libraries, account or Home', async t => {
  const f = fixture(t);
  const preferences = await f.services.readPreferences('mobile');
  assert.equal(preferences.deviceClass, 'mobile');
  assert.deepEqual(f.state.calls.map(request => request.url.pathname), [routePath('get_preferences')]);
});

test('a delayed auxiliary permission denial still retires an already-admitted Home', async t => {
  const f = fixture(t), delayed = deferred<void>();
  await Promise.all([f.services.readPreferences('mobile'), f.services.home.load()]);
  f.state.reply = async request => {
    if (request.url.pathname === '/v1/notifications') {await delayed.promise; return {status: 403, value: {error: {code: 'forbidden'}}};}
    return f.response(request);
  };
  const unread = f.services.readUnread(); delayed.resolve(); await assert.rejects(unread);
  assert.equal(f.state.ended, 1); assert.equal(f.services.home.getSnapshot().document, undefined);
});

test('settings answered for another kind of device are refused', async t => {
  const f = fixture(t);
  f.state.deviceClass = 'television';
  await assert.rejects(f.services.read());
  assert.equal(f.state.ended, 0); assert.equal(f.services.getSnapshot(), undefined);
});

// Rebuilt viewer channels and inbox have no server-id envelope; the captured request binds them.
test('a foreign server in the Home response is refused independently of bootstrap facts', async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  f.state.servers.home = 'other-server';
  await f.services.home.revalidate();
  assert.equal(f.state.ended, 1); assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.services.home.getSnapshot().document, undefined);
});

for (const [domain, path] of [['preferences', routePath('get_preferences')], ['inbox', '/v1/notifications'],
  ['guide', '/v1/live/channels'], ['home', routePath('get_home')]] as const) test(`a real HTTP permission denial in ${domain} cannot be swallowed by a core service`, async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  f.state.reply = async request => request.url.pathname === path ? {status: 403, value: {error: {code: 'forbidden', message: 'Denied.'}}} : f.response(request);
  if (domain === 'home') await f.services.home.revalidate(); else await assert.rejects(f.services.read());
  assert.equal(f.state.ended, 1); assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.services.home.getSnapshot().document, undefined);
});

test('initial guide denial hides Live TV without inventing a source; a later grant requires a new scope', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === '/v1/live/channels' ? {status: 403, value: {error: {code: 'forbidden'}}} : f.response(request);
  assert.equal((await f.services.read()).hasLiveTV, false); assert.equal(f.state.ended, 0);
  assert.equal((await f.services.read()).hasLiveTV, false); assert.equal(f.state.ended, 0);
  f.state.reply = async request => f.response(request);
  await assert.rejects(f.services.read()); assert.equal(f.state.ended, 1);
});

test('malformed source summaries cannot invent Live TV availability', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === '/v1/live/channels' ? {value: {serverId: 'server', viewerFence: f.state.fences.guide, sources: [{}]}} : f.response(request);
  assert.equal((await f.services.read()).hasLiveTV, false);
  assert.equal(f.state.ended, 0);
});

for (const path of [routePath('get_preferences'), '/v1/notifications', '/v1/live/channels']) test(`a transient ${path} refresh preserves the same authority and permits recovery`, async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  const before = f.services.getSnapshot(), homeBefore = f.services.home.getSnapshot().document;
  f.state.reply = async request => request.url.pathname === path ? {status: 503, value: {error: {code: 'temporarily_unavailable', retryable: true}}} : f.response(request);
  await assert.rejects(f.services.read());
  assert.strictEqual(f.services.getSnapshot(), before); assert.strictEqual(f.services.home.getSnapshot().document, homeBefore); assert.equal(f.state.ended, 0);
  f.state.reply = async request => f.response(request); f.state.speed = 1.25;
  assert.equal((await f.services.read()).preferences.effective['playback.defaultSpeed'], 1.25);
});

// Client data step 5: who the viewer is and what they may see is the library directory's privacy
// fence (checked in bootstrap.ts). Home's own fence moves when a library is added or removed; it
// never retires the shell, at the manifest or on a row page.
test('a changed Home fence is followed by Home and never retires the shell', async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  await f.services.home.more('recent'); assert.equal(f.state.ended, 0);
  f.state.fences.home = 'e'.repeat(64);
  await f.services.home.revalidate(); assert.equal(f.state.ended, 0);
  await f.services.home.more('recent'); assert.equal(f.state.ended, 0);
  assert.ok(f.services.getSnapshot());
});

for (const finish of ['dispose', 'abort', 'supersede'] as const) test(`${finish} cancels native owned reads and rejects late private responses`, async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  const entered = deferred<void>(), delayed = deferred<Reply>(); let lateRequest: Request | undefined;
  f.state.reply = async request => {
    if (request.url.pathname === routePath('get_preferences')) {lateRequest = request; entered.resolve(); return delayed.promise;}
    return f.response(request);
  };
  const pending = f.services.read(); await entered.promise;
  if (finish === 'dispose') f.services.dispose(); else if (finish === 'abort') f.controller.abort(); else {f.state.current = false; assert.equal(f.services.getSnapshot(), undefined);}
  assert.ok(lateRequest); assert.ok(f.state.cancellations.includes(lateRequest.id));
  delayed.resolve(f.response(lateRequest)); await assert.rejects(pending);
  assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.services.home.getSnapshot().document, undefined); assert.equal(f.state.ended, 0);
});

test('a failed refresh aborts sibling native reads, ignores their late denial and does not poison the next refresh', async t => {
  const f = fixture(t); await f.services.read();
  const entered = deferred<void>(), delayed = deferred<Reply>(); let lateRequest: Request | undefined;
  f.state.reply = async request => {
    if (request.url.pathname === '/v1/live/channels') {lateRequest = request; entered.resolve(); return delayed.promise;}
    if (request.url.pathname === routePath('get_preferences')) {await entered.promise; return {status: 503, value: {error: {code: 'temporarily_unavailable'}}};}
    return f.response(request);
  };
  await assert.rejects(f.services.read()); assert.ok(lateRequest); assert.ok(f.state.cancellations.includes(lateRequest.id));
  f.state.reply = async request => f.response(request);
  const next = f.services.read();
  delayed.resolve({status: 403, value: {error: {code: 'forbidden'}}});
  assert.equal((await next).hasLiveTV, true); assert.equal(f.state.ended, 0);
});

test('a refresh cancellation never publishes late preferences and leaves the accepted scope available', async t => {
  const f = fixture(t); const before = await f.services.read();
  const entered = deferred<void>(), delayed = deferred<Reply>(); let lateRequest: Request | undefined;
  f.state.reply = async request => {
    if (request.url.pathname === routePath('get_preferences')) {lateRequest = request; entered.resolve(); return delayed.promise;}
    return f.response(request);
  };
  const round = new AbortController(), pending = f.services.read(round.signal); await entered.promise; round.abort();
  assert.ok(lateRequest); assert.ok(f.state.cancellations.includes(lateRequest.id));
  f.state.speed = 2.25; delayed.resolve(f.response(lateRequest)); await assert.rejects(pending);
  assert.strictEqual(f.services.getSnapshot(), before); assert.equal(f.state.ended, 0);
  f.state.reply = async request => f.response(request); assert.equal((await f.services.read()).preferences.effective['playback.defaultSpeed'], 2.25);
});

test('direct account parsing preserves actual role and profiles, and refuses a changed membership', async t => {
  const f = fixture(t), account = await f.services.readAccount();
  assert.equal(account.account.role, viewer.role); assert.equal(account.profiles[0]?.name, 'Actual profile'); assert.equal(account.profiles[0]?.art, 'mint');
  f.state.role = 'member'; await assert.rejects(f.services.readAccount(), {code: 'access_refused'}); assert.equal(f.state.ended, 1);
});

for (const role of ['owner', 'admin', 'member'] as const) test(`the actual ${role} role is retained without an owner fallback`, async t => {
  const f = fixture(t, 'mobile', role);
  assert.equal((await f.services.readAccount()).account.role, role);
  f.state.disabled = true;
  await assert.rejects(f.services.readAccount(), {code: 'access_refused'}); assert.equal(f.state.ended, 1);
});

for (const role of ['admin', 'member'] as const) test(`the ${role} badge is the profile's own inbox, read once: server notices come to it`, async t => {
  const f = fixture(t, 'mobile', role);
  const [first] = await Promise.all([f.services.read(), f.services.home.load()]);
  assert.equal(first.unread, 2);
  const reads = f.state.calls.filter(value => value.url.pathname === '/v1/notifications').map(value => value.url.searchParams.get('view'));
  assert.ok(reads.length >= 1 && reads.every(view => view === 'unread'), 'only the profile\'s unread view is read');
});

test('an account answer for another account is refused, never adopted', async t => {
  const f = fixture(t); await f.services.readAccount();
  f.state.reply = async request => request.url.pathname === '/v1/me'
    ? {value: accountAnswer('/v1/me', {account: {id: 'other-account', username: 'other', role: 'owner'}, profiles: [{id: 'profile', name: 'Actual profile', primary: true}]})} : f.response(request);
  await assert.rejects(f.services.readAccount(), {code: 'invalid_response'});
});

test('an already-aborted restore cannot publish private service facts or start HTTP reads', async t => {
  const f = fixture(t); f.controller.abort();
  await assert.rejects(f.services.read(), abortError); assert.deepEqual(f.state.calls, []);
});

test('an authenticated service HTTP 401 retires the current scope', async t => {
  const f = fixture(t); await f.services.read();
  f.state.reply = async request => request.url.pathname === routePath('get_preferences') ? {status: 401, value: {error: {code: 'unauthorized'}}} : f.response(request);
  await assert.rejects(f.services.read()); assert.equal(f.state.ended, 1); assert.equal(f.services.getSnapshot(), undefined);
});

test('hosted and unselected account authorities are never parsed or defaulted as a local viewer', () => {
  assert.doesNotThrow(() => nativeShellDirectViewer(viewer));
  assert.throws(() => nativeShellDirectViewer({...viewer, authority: 'hosted'}), {code: 'native_shell_hosted_account_binding_unavailable'});
  assert.throws(() => nativeShellDirectViewer({...viewer, authority: 'unknown'}), {code: 'native_shell_hosted_account_binding_unavailable'});
  assert.throws(() => nativeShellDirectViewer({...viewer, role: 'account'}), {code: 'access_refused'});
});

test('malformed inbox data cannot become a successful bootstrap badge', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === '/v1/notifications' ? {value: {}} : f.response(request);
  await assert.rejects(f.services.read()); assert.equal(f.services.getSnapshot(), undefined);
  f.state.reply = async request => f.response(request); const before = await f.services.read();
  f.state.reply = async request => request.url.pathname === '/v1/notifications' ? {value: {items: [], unreadCount: 'bad'}} : f.response(request);
  await assert.rejects(f.services.read()); assert.strictEqual(f.services.getSnapshot(), before); assert.equal(f.state.ended, 0);
});

test('disposal aborts a core Home revalidation and its retained public snapshot stays empty and stable', async t => {
  const f = fixture(t); await f.services.home.load();
  const entered = deferred<void>(), delayed = deferred<Reply>(); let lateRequest: Request | undefined;
  f.state.reply = async request => {lateRequest = request; entered.resolve(); return delayed.promise;};
  const pending = f.services.home.revalidate(); await entered.promise; f.services.dispose();
  assert.ok(lateRequest); assert.ok(f.state.cancellations.includes(lateRequest.id));
  const closed = f.services.home.getSnapshot(); assert.equal(closed.document, undefined);
  delayed.resolve(f.response(lateRequest)); await pending;
  assert.strictEqual(f.services.home.getSnapshot(), closed); assert.equal(f.state.ended, 0);
});

test('a late response observes an authority handover even before another consumer checks the scope', async t => {
  const f = fixture(t), entered = deferred<void>(), delayed = deferred<Reply>(); let lateRequest: Request | undefined;
  f.state.reply = async request => {
    if (request.url.pathname === routePath('get_preferences')) {lateRequest = request; entered.resolve(); return delayed.promise;}
    return f.response(request);
  };
  const pending = f.services.read(); await entered.promise; f.state.current = false;
  assert.ok(lateRequest); delayed.resolve(f.response(lateRequest)); await assert.rejects(pending);
  assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.services.home.getSnapshot().document, undefined); assert.equal(f.state.ended, 0);
});
