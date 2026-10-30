import assert from 'node:assert/strict';
import test, {type TestContext} from 'node:test';
import {readFileSync} from 'node:fs';
import {NativeModules, Platform} from './react-native-stub.mjs';
import {beginNativeInstallation} from '../src-next/platform/installation.ts';
import {nativeSelectedConnectionEnvironment} from '../src-next/platform/session.ts';
import {HttpLocalApi, type LocalSession} from '../../../packages/client-core/src/index.ts';
import {installationIdentity, setInstallationIdentity} from '../../../packages/client-core/src/installation.ts';
import {refreshStoredSession, type RememberedServer} from '../../../packages/client-core/src/server-connections.ts';
import {selectedViewerRecord} from '../../../packages/client-core/src/viewer-selection.ts';
import {routePath} from '../../../packages/contracts/src/routes.ts';

const origin = 'https://native-installation.test';
const id = 'native_installation_0123456789abcdef0123456789';
const otherId = 'other_installation_0123456789abcdef0123456789';
const metadata = {deviceName: 'Living room', model: 'AppleTV14,1', appVersion: '1.9.42'};
const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
const modules = NativeModules as Record<string, unknown>;
const platform = Platform as {OS: string; isTV: boolean; isPad?: boolean; constants?: {Manufacturer?: string}};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(yes => {resolve = yes;});
  return {promise, resolve};
}

function fixture(t: TestContext) {
  const previousClaim = installationIdentity(), platformBefore = {...platform}, capabilitiesBefore = modules.PorticoCapabilities;
  const methods = ['prepareSecureRecords', 'profileInstallation', 'loadSelectedViewer', 'saveSelectedViewer', 'loadQueueRecord', 'saveQueueRecord', 'networkRequest'];
  const before = Object.fromEntries(methods.map(name => [name, runtime[name]]));
  const now = Date.now();
  const expired: LocalSession = {accessToken: 'expired_native_access', refreshToken: 'native_refresh_0123456789abcdef', installationId: id,
    expiresAt: new Date(now - 60_000).toISOString(), sessionFamilyId: 'native_family', tokenGeneration: '1', authorizationHorizon: new Date(now + 86_400_000).toISOString(),
    viewer: {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', role: 'owner'}};
  const fresh: LocalSession = {...expired, accessToken: 'renewed_native_access', refreshToken: 'rotated_refresh_0123456789abcdef', tokenGeneration: '2', expiresAt: new Date(now + 900_000).toISOString()};
  const remembered: RememberedServer = {version: '1', logicalOrigin: origin, session: expired,
    pin: {serverId: 'server', publicKey: 'k'.repeat(43), fingerprint: 'f'.repeat(43)}, routes: {paired: [origin]}};
  const state = {
    id: id as unknown,
    selected: JSON.stringify(selectedViewerRecord(origin, expired, true)),
    connection: JSON.stringify(remembered), recordRevision: 1,
    events: [] as string[], writes: [] as string[],
    requests: [] as {url: string; method: string; headers: Record<string, string>; body: string | null}[],
    prepare: async () => false,
    facts: async (): Promise<unknown> => metadata,
    readId: async (): Promise<unknown> => state.id,
  };
  Object.assign(platform, {OS: 'ios', isTV: true, isPad: false, constants: {}});
  setInstallationIdentity(undefined);
  runtime.prepareSecureRecords = async () => {state.events.push('prepare'); return state.prepare();};
  runtime.profileInstallation = async () => {state.events.push('installation-read'); return state.readId();};
  runtime.loadSelectedViewer = async () => {throw new Error('legacy viewer read is forbidden in canonical restoration');};
  runtime.saveSelectedViewer = async () => {state.writes.push('viewer');};
  runtime.loadQueueRecord = async (key: string) => {assert.equal(key, 'accounts.sessions.v1'); state.events.push('connection-read'); return JSON.stringify({version: 1, revision: state.recordRevision, selectedServerId: 'server', servers: [{signIn: 'local', connection: JSON.parse(state.connection)}]});};
  runtime.saveQueueRecord = async (key: string, value: string) => {assert.equal(key, 'accounts.sessions.v1'); const saved = JSON.parse(value); state.writes.push('connection'); state.recordRevision = saved.revision; state.connection = JSON.stringify(saved.servers[0].connection);};
  modules.PorticoCapabilities = {facts: async () => {state.events.push('facts'); return state.facts();}};
  runtime.networkRequest = async (_request: string, url: string, method: string, headers: Record<string, string>, body: string | null) => {
    state.events.push('request'); state.requests.push({url, method, headers, body});
    return {status: 200, headers: {'Content-Type': 'application/json'}, body: JSON.stringify(fresh)};
  };
  const scopes: ReturnType<typeof beginNativeInstallation>[] = [];
  const begin = (signal?: AbortSignal) => {const scope = beginNativeInstallation(signal); scopes.push(scope); return scope;};
  t.after(() => {
    for (const scope of scopes) scope.dispose();
    for (const name of methods) {if (before[name] === undefined) delete runtime[name]; else runtime[name] = before[name];}
    if (capabilitiesBefore === undefined) delete modules.PorticoCapabilities; else modules.PorticoCapabilities = capabilitiesBefore;
    for (const key of ['OS', 'isTV', 'isPad', 'constants'] as const) delete (platform as Record<string, unknown>)[key];
    Object.assign(platform, platformBefore); setInstallationIdentity(previousClaim);
  });
  const env = nativeSelectedConnectionEnvironment(), api = new HttpLocalApi(origin, expired.accessToken, env.fetcher);
  const refresh = () => refreshStoredSession({env, api, family: {serverId: expired.viewer.serverId, sessionFamilyId: expired.sessionFamilyId}});
  return {state, begin, refresh, expired, fresh, remembered};
}

const abortError = (error: unknown) => (error as {name?: unknown})?.name === 'AbortError';
const mismatchError = (error: unknown) => (error as {code?: unknown})?.code === 'identity_mismatch';

test('startup prepares secure records before metadata, then real core renewal sends the persisted claim in body and headers', async t => {
  const f = fixture(t), prepared = deferred<boolean>(), preparing = deferred<void>();
  f.state.prepare = () => {preparing.resolve(); return prepared.promise;};
  const scope = f.begin(), pending = scope.prepare();
  assert.strictEqual(scope.prepare(), pending);
  await preparing.promise;
  assert.deepEqual(f.state.events, ['prepare']);
  assert.equal(installationIdentity(), undefined);
  prepared.resolve(false);
  const ready = await pending;
  assert.equal(ready.saved.installation, id);
  assert.equal(ready.identity.installationId, id);
  assert.equal(ready.identity.appVersion, metadata.appVersion);
  assert.equal(ready.identity.name, metadata.deviceName);
  assert.equal(ready.identity.platform, 'tvos');
  assert.equal(f.state.events.filter(event => event === 'installation-read').length, 2);
  assert.ok(f.state.events.indexOf('connection-read') < f.state.events.indexOf('facts'));
  assert.deepEqual(f.state.writes, []);
  const renewed = await f.refresh();
  assert.equal(renewed.accessToken, f.fresh.accessToken);
  assert.equal(f.state.requests.length, 1);
  const request = f.state.requests[0]; assert.ok(request);
  assert.equal(request.url, origin + routePath('post_auth_refresh'));
  assert.equal(request.method, 'POST');
  const body = JSON.parse(request.body!);
  assert.equal(body.installationId, id);
  assert.equal(body.refreshToken, f.expired.refreshToken);
  assert.match(body.requestId, /^[A-Za-z0-9_-]{1,128}$/);
  const headers = new Headers(request.headers);
  assert.equal(headers.get('X-Portico-Installation-Id'), id);
  assert.equal(headers.get('X-Portico-Device-Name'), metadata.deviceName);
  assert.equal(headers.get('X-Portico-Device-Platform'), 'tvos');
  assert.equal(headers.get('X-Portico-App'), 'Portico');
  assert.equal(headers.get('X-Portico-App-Version'), metadata.appVersion);
  assert.equal(JSON.parse(f.state.connection).session.refreshToken, f.fresh.refreshToken);
  assert.ok(f.state.events.lastIndexOf('installation-read') < f.state.events.indexOf('request'));
});

test('an expired stored family without the prerequisite cannot send a renewal request', async t => {
  const f = fixture(t), original = f.state.connection;
  await assert.rejects(f.refresh(), error => (error as {code?: unknown})?.code === 'refresh_unavailable');
  assert.deepEqual(f.state.requests, []);
  assert.deepEqual(f.state.writes, []);
  assert.equal(f.state.connection, original);
});

test('repeated restore reuses the persisted ID and disposal preserves the current shared claim', async t => {
  const f = fixture(t), original = f.state.connection;
  const first = f.begin(); await first.prepare();
  const previous = installationIdentity();
  const second = f.begin();
  assert.strictEqual(installationIdentity(), previous);
  const result = await second.prepare(), installed = installationIdentity();
  assert.equal(result.identity.installationId, id);
  assert.equal(first.current(), false);
  first.dispose(); first.dispose();
  assert.strictEqual(installationIdentity(), installed);
  assert.equal(second.current(), true);
  second.dispose(); assert.strictEqual(installationIdentity(), installed);
  assert.equal(f.state.connection, original);
  assert.deepEqual(f.state.writes, []);
});

test('native classification preserves Apple, iPad, Android TV and Amazon Fire TV identities', async t => {
  const f = fixture(t);
  for (const [os, television, pad, manufacturer, expected] of [
    ['ios', false, false, '', 'ios'], ['ios', false, true, '', 'ipados'], ['ios', true, false, '', 'tvos'],
    ['android', false, false, 'Amazon', 'android'], ['android', true, false, 'Google', 'androidtv'], ['android', true, false, 'AmAzOn', 'firetv'],
  ] as const) {
    Object.assign(platform, {OS: os, isTV: television, isPad: pad, constants: {Manufacturer: manufacturer}});
    const scope = f.begin(); assert.equal((await scope.prepare()).identity.platform, expected); scope.dispose();
  }
  Object.assign(platform, {OS: 'windows'});
  await assert.rejects(f.begin().prepare(), /invalid_installation_metadata/);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('generic Apple names use actual model facts and named devices retain their native names', async t => {
  const f = fixture(t);
  f.state.facts = async () => ({...metadata, deviceName: 'Apple TV'});
  assert.equal((await f.begin().prepare()).identity.name, 'Apple TV 4K');
  f.state.facts = async () => ({...metadata, deviceName: '', model: 'SHIELD Android TV'});
  assert.equal((await f.begin().prepare()).identity.name, 'SHIELD Android TV');
  f.state.facts = async () => ({...metadata, deviceName: 'Télévision'});
  assert.equal((await f.begin().prepare()).identity.name, 'Television');
});

test('delayed native metadata cannot install after auth abort or explicit disposal', async t => {
  const f = fixture(t);
  for (const retire of ['abort', 'dispose'] as const) {
    const facts = deferred<unknown>(), entered = deferred<void>(), abort = new AbortController();
    f.state.facts = () => {entered.resolve(); return facts.promise;};
    const scope = f.begin(abort.signal), pending = scope.prepare();
    await entered.promise;
    if (retire === 'abort') abort.abort(); else scope.dispose();
    facts.resolve(metadata);
    await assert.rejects(pending, abortError);
    assert.equal(installationIdentity(), undefined);
  }
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('a newer restore wins while retired native facts are outstanding, without clearing its claim', async t => {
  const f = fixture(t), facts = deferred<unknown>(), entered = deferred<void>();
  f.state.facts = () => {entered.resolve(); return facts.promise;};
  const old = f.begin(), pending = old.prepare(); await entered.promise;
  f.state.facts = async () => metadata;
  const latest = f.begin(); await latest.prepare();
  const installed = installationIdentity();
  facts.resolve({...metadata, deviceName: 'Retired metadata'});
  await assert.rejects(pending, abortError); old.dispose();
  assert.strictEqual(installationIdentity(), installed);
  assert.equal(latest.current(), true);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('aborting before preparation or superseding a delayed secure read never starts metadata or a request', async t => {
  const f = fixture(t), abort = new AbortController(); abort.abort();
  await assert.rejects(f.begin(abort.signal).prepare(), abortError);
  assert.deepEqual(f.state.events, []);
  const prepared = deferred<boolean>(), entered = deferred<void>();
  f.state.prepare = () => {entered.resolve(); return prepared.promise;};
  const old = f.begin(), pending = old.prepare(); await entered.promise;
  f.state.prepare = async () => false;
  const latest = f.begin(); await latest.prepare();
  const installed = installationIdentity(); prepared.resolve(false);
  await assert.rejects(pending, abortError);
  assert.equal(f.state.events.filter(event => event === 'facts').length, 1);
  assert.strictEqual(installationIdentity(), installed);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('a reset during delayed facts is detected by persisted readback even with no previous canonical claim', async t => {
  const f = fixture(t), facts = deferred<unknown>(), entered = deferred<void>();
  f.state.facts = () => {entered.resolve(); return facts.promise;};
  const pending = f.begin().prepare(); await entered.promise;
  f.state.id = otherId; setInstallationIdentity(undefined); facts.resolve(metadata);
  await assert.rejects(pending, mismatchError);
  assert.equal(installationIdentity(), undefined);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('clearing an existing canonical claim during metadata prevents late registration', async t => {
  const f = fixture(t); await f.begin().prepare();
  const facts = deferred<unknown>(), entered = deferred<void>();
  f.state.facts = () => {entered.resolve(); return facts.promise;};
  const pending = f.begin().prepare(); await entered.promise;
  setInstallationIdentity(undefined); facts.resolve(metadata);
  await assert.rejects(pending, abortError);
  assert.equal(installationIdentity(), undefined);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('unreadable, refused or invalid installation metadata preserves saved credentials and makes no request', async t => {
  const f = fixture(t), selected = f.state.selected, connection = f.state.connection;
  for (const value of [undefined, null, '', 'short', 'bad!'.repeat(10), 'a'.repeat(129)]) {
    f.state.id = value;
    await assert.rejects(f.begin().prepare(), /invalid_installation_metadata/);
  }
  f.state.id = id;
  f.state.readId = async () => {throw new Error('keychain refused');};
  await assert.rejects(f.begin().prepare(), /invalid_installation_metadata/);
  f.state.readId = async () => id;
  f.state.prepare = async () => {throw new Error('secure preparation refused');};
  await assert.rejects(f.begin().prepare(), /secure preparation refused/);
  assert.equal(installationIdentity(), undefined);
  assert.equal(f.state.selected, selected); assert.equal(f.state.connection, connection);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('missing, malformed, empty or refused native facts never use a guessed build version or overwrite a valid claim', async t => {
  const f = fixture(t); await f.begin().prepare();
  const installed = installationIdentity(), connection = f.state.connection;
  for (const value of [null, [], {}, {...metadata, appVersion: ''}, {...metadata, appVersion: 42}, {...metadata, appVersion: '1.0\n'},
    {...metadata, appVersion: 'v'.repeat(65)}, {...metadata, deviceName: 12}, {...metadata, deviceName: '\nInjected'}, {...metadata, deviceName: '', model: ''}]) {
    f.state.facts = async () => value;
    await assert.rejects(f.begin().prepare(), /invalid_installation_metadata/);
    assert.strictEqual(installationIdentity(), installed);
  }
  f.state.facts = async () => {throw new Error('native facts refused');};
  await assert.rejects(f.begin().prepare(), /native facts refused/);
  delete modules.PorticoCapabilities;
  await assert.rejects(f.begin().prepare());
  assert.strictEqual(installationIdentity(), installed);
  assert.equal(f.state.connection, connection);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('a final Keychain read refusal cannot register a claim or rewrite the credential', async t => {
  const f = fixture(t); let reads = 0;
  f.state.readId = async () => {if (++reads === 1) return id; throw new Error('keychain readback refused');};
  await assert.rejects(f.begin().prepare(), /keychain readback refused/);
  assert.equal(installationIdentity(), undefined);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('abort during final persisted-ID readback cannot publish a late identity', async t => {
  const f = fixture(t), readback = deferred<unknown>(), entered = deferred<void>(), abort = new AbortController(); let reads = 0;
  f.state.readId = async () => {if (++reads === 1) return id; entered.resolve(); return readback.promise;};
  const pending = f.begin(abort.signal).prepare(); await entered.promise;
  abort.abort(); readback.resolve(id);
  await assert.rejects(pending, abortError);
  assert.equal(installationIdentity(), undefined);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('a different existing canonical installation is preserved rather than rebound', async t => {
  const f = fixture(t);
  setInstallationIdentity({installationId: otherId, name: 'Existing device', appVersion: '1.9.41', platform: 'tvos', app: 'Portico'});
  const installed = installationIdentity();
  await assert.rejects(f.begin().prepare(), mismatchError);
  assert.strictEqual(installationIdentity(), installed);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('the historical selected viewer cannot override a canonical family, whose installation binding remains mandatory', async t => {
  const f = fixture(t), connection = f.state.connection;
  f.state.selected = JSON.stringify(selectedViewerRecord(origin, {...f.expired, installationId: otherId}, true));
  const first = f.begin(); await first.prepare(); first.dispose();
  assert.equal(installationIdentity()?.installationId, id);
  assert.equal(f.state.events.includes('viewer-read'), false);
  f.state.connection = JSON.stringify({...f.remembered, session: {...f.expired, installationId: otherId}});
  await assert.rejects(f.begin().prepare(), mismatchError);
  assert.equal(installationIdentity()?.installationId, id);
  assert.equal(JSON.parse(f.state.connection).session.installationId, otherId);
  assert.notEqual(f.state.connection, connection);
  assert.deepEqual(f.state.requests, []); assert.deepEqual(f.state.writes, []);
});

test('bootstrap awaits the scoped prerequisite before hosted or local restoration and uses its native version', () => {
  const source = readFileSync(new URL('../src-next/shell/bootstrap.ts', import.meta.url), 'utf8');
  // A root may already own the installation; bootstrap reuses that generation instead of
  // beginning a competing one, and still awaits its prepare before any restoration.
  const start = source.indexOf('const installation = options.installationOwner ?? beginNativeInstallation(options.signal)');
  const prepare = source.indexOf('await installation.prepare()');
  assert.ok(start >= 0 && prepare > start);
  assert.equal(source.match(/\?\? beginNativeInstallation\(/g)?.length, 1);
  for (const operation of ['await nativeAppSessions.selected()', 'await restoreNativeConnection(']) {
    assert.ok(source.indexOf(operation) > prepare, operation);
  }
  assert.doesNotMatch(source, /await credentials\.synchronize\(\)/);
  // Only the installation this bootstrap began is disposed; a borrowed one belongs to its root.
  assert.match(source, /const ownsInstallation = !options\.installationOwner;/);
  assert.match(source, /alive = false; if \(ownsInstallation\) installation\.dispose\(\);/);
  assert.match(source, /appVersion: installed\.appVersion/);
  assert.doesNotMatch(source, /PorticoCapabilities\.facts|package\.json/);
});
