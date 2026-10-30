import assert from 'node:assert/strict';
import test, {type TestContext} from 'node:test';
import {NativeModules} from './react-native-stub.mjs';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src/index.ts';
import {installationIdentity, setInstallationIdentity} from '../../../packages/client-core/src/installation.ts';
import {deviceClassHeader} from '../../../packages/client-core/src/delivery.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';
import {nativeNetworkFetch} from '../src-next/platform/session.ts';
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
  const envelope = (domain: 'preferences' | 'inbox', data: unknown) => ({scope: {serverId: state.servers[domain], viewerFence: state.fences[domain]}, data});
  const response = (request: Request): Reply => {
    const path = request.url.pathname;
    if (path === routePath('get_preferences')) {
      // This is the server's required query contract. A delivery header cannot supply it.
      if (request.url.searchParams.get('deviceClass') !== deviceClass) return {status: 400, value: {error: {code: 'invalid_console_request', message: 'Invalid device class.'}}};
      return {value: envelope('preferences', preferenceData(state.deviceClass, state.speed))};
    }
    if (path === routePath('get_notifications_inbox')) {
      const audience = request.url.searchParams.get('audience');
      assert.equal(audience, role === 'owner' ? 'account-admin' : 'profile');
      return {value: envelope('inbox', {revision: 7, audience, audiences: audience === 'account-admin' ? ['profile', 'account-admin'] : ['profile'], state: 'unread',
        counts: counts(2), items: [], nextCursor: '', observedAt: '2026-10-22T12:00:00Z', retentionDays: 180})};
    }
    if (path === routePath('get_notifications_unread_count')) {
      const audience = request.url.searchParams.get('audience');
      assert.ok(audience === 'profile' || audience === 'account-admin');
      return {value: envelope('inbox', {revision: 7, audience, counts: counts(audience === 'profile' ? 2 : 3), observedAt: '2026-10-22T12:00:00Z'})};
    }
    if (path === routePath('get_guide_sources')) return {value: {protocolVersion: '1.0', serverId: state.servers.guide, viewerFence: state.fences.guide, revision: 'guide-1',
      sources: [{id: 'live-source', name: 'Actual authorized source', generation: 'g1', publishedAt: '2026-10-22T12:00:00Z', availableStart: '', availableEnd: '',
        provenance: 'live-source', refreshState: 'healthy', position: 0, recordAvailable: true, guideDays: 7, channelCount: 2, favoriteCount: 1,
        groups: ['News'], groupCounts: [{name: 'News', count: 2}], unavailableReasons: {}}]}};
    if (path === routePath('get_home')) return {value: {serverId: state.servers.home, viewerFence: state.fences.home, rows: [homeRow(state.fences.home)], spotlight: [],
      layout: {revision: 1, rowOrder: ['recent'], hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: '2026-10-22T12:00:00Z'}};
    if (path === routePath('get_home_row', {id: 'recent'})) return {value: {...homeRow(state.fences.home), entries: [], start: 1, hasMore: false, nextCursor: ''}};
    if (path === routePath('get_direct_account')) return {value: {authority: 'local', serverId: 'server', canManage: true,
      capabilities: {manageAccount: true, managePeople: state.role === 'owner' || state.role === 'admin',
        manageServer: state.role === 'owner' || state.role === 'admin', manageSignIn: state.role === 'owner'},
      account: {id: 'account', username: 'owner', primaryProfileId: 'profile', role: state.role, revision: 1, disabled: state.disabled, allowedLibraries: ['movies'],
        ...(state.linkedAccountId ? {hostedAccountId: state.linkedAccountId} : {})},
      profiles: [{id: 'profile', name: 'Actual profile', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}]}};
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

for (const deviceClass of ['mobile', 'television'] as const) test(`native ${deviceClass} sends the required preferences query on first load and refresh and parses scoped data unchanged`, async t => {
  const f = fixture(t, deviceClass);
  const [first] = await Promise.all([f.services.read(), f.services.home.load()]);
  assert.equal(first.preferences.deviceClass, deviceClass);
  assert.equal(first.preferences.effective['playback.defaultSpeed'], 1.85);
  assert.equal(first.preferences.registry.fields.find(value => value.key === 'playback.defaultSpeed')?.default, 1);
  assert.equal(nativePlaybackSettings(first.preferences.effective, {playerEngine: 'native'}).videoRate, 1.85);
  assert.equal(nativePlaybackSettings(first.preferences.effective, null).audiobookRate, 1.65);
  assert.equal(first.unread, 5, 'actual inbox audiences include the owner account-admin badge');
  assert.equal(first.hasLiveTV, true);
  assert.equal(f.services.home.getSnapshot().document?.viewerFence, f.state.fences.home);
  assert.equal(f.state.ended, 0, 'valid Home/preferences/inbox/guide fences are deliberately unequal');
  f.state.speed = 1.35;
  const next = await f.services.read();
  assert.equal(next.preferences.effective['playback.defaultSpeed'], 1.35);
  const requests = f.state.calls.filter(value => value.url.pathname === routePath('get_preferences'));
  assert.equal(requests.length, 2);
  for (const request of requests) {
    assert.equal(request.url.searchParams.get('deviceClass'), deviceClass);
    assert.equal(request.headers.get(deviceClassHeader), deviceClass);
    assert.equal(request.headers.get('Authorization'), 'Bearer in_memory_access');
    assert.equal(request.headers.get('X-Portico-Installation-Id'), null, 'ordinary reads use the bearer; the installation claim accompanies credential exchanges');
    assert.equal(request.method, 'GET'); assert.equal(request.body, null);
  }
  assert.equal(installationIdentity()?.installationId, 'native_bootstrap_0123456789abcdef01234567', 'reading preferences preserves the actual registered claim');
  assert.ok(f.state.calls.filter(value => value.url.pathname === routePath('get_guide_sources')).every(value => value.url.searchParams.get('timezone') === Intl.DateTimeFormat().resolvedOptions().timeZone));
});

test('Home and required preferences can finish while auxiliary badges and guide are still waiting', async t => {
  const f = fixture(t), delayed = deferred<void>(), entered = deferred<void>();
  f.state.reply = async request => {
    if (new Set<string>([routePath('get_notifications_inbox'), routePath('get_guide_sources')]).has(request.url.pathname)) {entered.resolve(); await delayed.promise;}
    return f.response(request);
  };
  let auxiliaryReady = false;
  const auxiliary = Promise.all([f.services.readUnread(), f.services.readGuideAvailability()]).then(value => {auxiliaryReady = true; return value;});
  await entered.promise;
  const [preferences] = await Promise.all([f.services.readPreferences('mobile'), f.services.home.load()]);
  assert.equal(preferences.deviceClass, 'mobile'); assert.equal(f.services.home.getSnapshot().phase, 'ready');
  assert.equal(auxiliaryReady, false, 'the ready Home does not wait for inbox or guide');
  delayed.resolve(); assert.deepEqual(await auxiliary, [5, true]);
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
    if (request.url.pathname === routePath('get_notifications_inbox')) {await delayed.promise; return {status: 403, value: {error: {code: 'forbidden'}}};}
    return f.response(request);
  };
  const unread = f.services.readUnread(); delayed.resolve(); await assert.rejects(unread);
  assert.equal(f.state.ended, 1); assert.equal(f.services.home.getSnapshot().document, undefined);
});

test('an unscoped preference snapshot and the wrong returned device class are refused', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === routePath('get_preferences') ? {value: preferenceData('mobile', 1.85)} : f.response(request);
  await assert.rejects(f.services.read()); assert.equal(f.services.getSnapshot(), undefined);
  f.state.reply = async request => f.response(request); f.state.deviceClass = 'television';
  await assert.rejects(f.services.read(), {code: 'invalid_response'});
  assert.equal(f.state.ended, 0); assert.equal(f.services.getSnapshot(), undefined);
});

for (const domain of ['preferences', 'inbox', 'guide', 'home'] as const) test(`a changed ${domain} fence retires the genuine authority and hides every private body`, async t => {
  const f = fixture(t);
  await Promise.all([f.services.read(), f.services.home.load()]);
  assert.equal(f.services.home.getSnapshot().document?.rows[0]?.entries[0]?.title, 'Private catalogue title');
  f.state.fences[domain] = 'e'.repeat(64);
  if (domain === 'home') await f.services.home.revalidate(); else await assert.rejects(f.services.read());
  assert.equal(f.state.ended, 1);
  assert.equal(f.services.getSnapshot(), undefined);
  assert.equal(f.services.home.getSnapshot().document, undefined);
  await assert.rejects(f.services.read(), abortError);
  f.services.dispose(); assert.equal(f.state.ended, 1);
});

for (const domain of ['preferences', 'inbox', 'guide', 'home'] as const) test(`a foreign server in the ${domain} response is refused independently of Home`, async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  f.state.servers[domain] = 'other-server';
  if (domain === 'home') await f.services.home.revalidate(); else await assert.rejects(f.services.read());
  assert.equal(f.state.ended, 1); assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.services.home.getSnapshot().document, undefined);
});

for (const [domain, path] of [['preferences', routePath('get_preferences')], ['inbox', routePath('get_notifications_unread_count')],
  ['guide', routePath('get_guide_sources')], ['home', routePath('get_home')]] as const) test(`a real HTTP permission denial in ${domain} cannot be swallowed by a core service`, async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  f.state.reply = async request => request.url.pathname === path ? {status: 403, value: {error: {code: 'forbidden', message: 'Denied.'}}} : f.response(request);
  if (domain === 'home') await f.services.home.revalidate(); else await assert.rejects(f.services.read());
  assert.equal(f.state.ended, 1); assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.services.home.getSnapshot().document, undefined);
});

test('initial guide denial hides Live TV without inventing a source; a later grant requires a new scope', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === routePath('get_guide_sources') ? {status: 403, value: {error: {code: 'forbidden'}}} : f.response(request);
  assert.equal((await f.services.read()).hasLiveTV, false); assert.equal(f.state.ended, 0);
  assert.equal((await f.services.read()).hasLiveTV, false); assert.equal(f.state.ended, 0);
  f.state.reply = async request => f.response(request);
  await assert.rejects(f.services.read()); assert.equal(f.state.ended, 1);
});

test('guide protocol and source summaries are parsed instead of trusted as a raw sources array', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === routePath('get_guide_sources') ? {value: {serverId: 'server', viewerFence: f.state.fences.guide, sources: [{}]}} : f.response(request);
  await assert.rejects(f.services.read(), {code: 'invalid_guide_directory'});
  assert.equal(f.services.getSnapshot(), undefined); assert.equal(f.state.ended, 0);
});

for (const path of [routePath('get_preferences'), routePath('get_notifications_unread_count'), routePath('get_guide_sources')]) test(`a transient ${path} refresh preserves the same authority and permits recovery`, async t => {
  const f = fixture(t); await Promise.all([f.services.read(), f.services.home.load()]);
  const before = f.services.getSnapshot(), homeBefore = f.services.home.getSnapshot().document;
  f.state.reply = async request => request.url.pathname === path ? {status: 503, value: {error: {code: 'temporarily_unavailable', retryable: true}}} : f.response(request);
  await assert.rejects(f.services.read());
  assert.strictEqual(f.services.getSnapshot(), before); assert.strictEqual(f.services.home.getSnapshot().document, homeBefore); assert.equal(f.state.ended, 0);
  f.state.reply = async request => f.response(request); f.state.speed = 1.25;
  assert.equal((await f.services.read()).preferences.effective['playback.defaultSpeed'], 1.25);
});

test('Home row pages retain their own genuine fence without a repeated serverId', async t => {
  const f = fixture(t); await f.services.home.load();
  await f.services.home.more('recent'); assert.equal(f.state.ended, 0);
  assert.equal(f.services.home.getSnapshot().document?.rows[0]?.nextCursor, '');
  await f.services.home.load(); f.state.fences.home = 'changed-home-page';
  await f.services.home.more('recent'); assert.equal(f.state.ended, 1); assert.equal(f.services.home.getSnapshot().document, undefined);
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
    if (request.url.pathname === routePath('get_guide_sources')) {lateRequest = request; entered.resolve(); return delayed.promise;}
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

for (const replacement of [undefined, 'other-portico-account']) test(`linked account read refuses ${replacement ? 'a changed' : 'a missing'} Portico identity link`, async t => {
  const f = fixture(t, 'mobile', 'member', 'original-portico-account');
  assert.equal((await f.services.readAccount()).account.hostedAccountId, 'original-portico-account');
  f.state.linkedAccountId = replacement;
  await assert.rejects(f.services.readAccount(), {code: 'access_refused'});
  assert.equal(f.state.ended, 1);
  assert.equal(f.services.home.getSnapshot().document, undefined);
});

for (const role of ['owner', 'admin', 'member'] as const) test(`the actual ${role} role is retained without an owner fallback`, async t => {
  const f = fixture(t, 'mobile', role);
  assert.equal((await f.services.readAccount()).account.role, role);
  f.state.disabled = true;
  await assert.rejects(f.services.readAccount(), {code: 'access_refused'}); assert.equal(f.state.ended, 1);
});

test('a changed direct-account principal retires instead of retaining another account response', async t => {
  const f = fixture(t); await f.services.readAccount();
  f.state.reply = async request => request.url.pathname === routePath('get_direct_account')
    ? {value: {authority: 'local', serverId: 'server', account: {id: 'other-account'}}} : f.response(request);
  await assert.rejects(f.services.readAccount(), {code: 'access_refused'}); assert.equal(f.state.ended, 1);
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

test('a transient Home read retains the actual core document without ending its scope', async t => {
  const f = fixture(t); await f.services.home.load(); const before = f.services.home.getSnapshot();
  f.state.reply = async request => request.url.pathname === routePath('get_home') ? {status: 503, value: {error: {code: 'temporarily_unavailable'}}} : f.response(request);
  await assert.rejects(f.services.home.revalidate(), {status: 503});
  assert.strictEqual(f.services.home.getSnapshot(), before); assert.equal(f.state.ended, 0);
  f.state.reply = async request => f.response(request); await f.services.home.revalidate();
  assert.strictEqual(f.services.home.getSnapshot().document, before.document);
});

test('malformed inbox data cannot become a successful bootstrap badge', async t => {
  const f = fixture(t);
  f.state.reply = async request => request.url.pathname === routePath('get_notifications_inbox')
    ? {value: {scope: {serverId: 'server', viewerFence: f.state.fences.inbox}, data: {}}} : f.response(request);
  await assert.rejects(f.services.read()); assert.equal(f.services.getSnapshot(), undefined);
  f.state.reply = async request => f.response(request); const before = await f.services.read();
  f.state.reply = async request => request.url.pathname === routePath('get_notifications_unread_count')
    ? {value: {scope: {serverId: 'server', viewerFence: f.state.fences.inbox}, data: {counts: {unread: 'bad'}}}} : f.response(request);
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

for (const deviceClass of ['mobile', 'television'] as const) test(`native ${deviceClass} keeps its actual Home initial-window request through refresh`, async t => {
  const f = fixture(t, deviceClass);
  await f.services.home.load();
  await f.services.home.revalidate();
  const requests = f.state.calls.filter(value => value.url.pathname === routePath('get_home'));
  assert.equal(requests.length, 2);
  for (const request of requests) assert.equal(request.url.searchParams.get('limit'), deviceClass === 'television' ? '20' : null);
  assert.equal(f.state.ended, 0);
  assert.equal(f.services.home.getSnapshot().document?.viewerFence, f.state.fences.home);
});
