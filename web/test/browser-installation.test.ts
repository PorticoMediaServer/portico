import '../src/platform/polyfills.ts';
import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi, type LocalSession} from '@core/index.ts';
import {credentialLock, installationIdentity, setCredentialLock, setInstallationIdentity, validInstallationId} from '@core/installation.ts';
import {parseLocalSession} from '@core/local-session.ts';
import {refreshStoredSession, type RememberedServer} from '@core/server-connections.ts';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {initializeBrowserInstallation} from '../src/platform/installation.ts';
import {installInsecureContextFallbacks} from '../src/platform/polyfills.ts';

const installationKey = 'portico.profile-installation.v1';
const persistedId = '00000000-0000-4000-8000-000000000001';
const userAgent = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36';
const origin = 'https://server.example';
type StoragePort = Pick<Storage, 'getItem' | 'setItem'>;

function storage(initial: string | null = null) {
  let value = initial;
  const writes: string[] = [];
  return {
    getItem: (key: string) => { assert.equal(key, installationKey); return value; },
    setItem: (key: string, next: string) => { assert.equal(key, installationKey); writes.push(next); value = next; },
    peek: () => value, writes,
  };
}
function replace(t: TestContext, key: string, value: unknown) {
  const before = Object.getOwnPropertyDescriptor(globalThis, key);
  Object.defineProperty(globalThis, key, {configurable: true, value});
  t.after(() => { if (before) Object.defineProperty(globalThis, key, before); else delete (globalThis as Record<string, unknown>)[key]; });
}
function browser(t: TestContext, saved: StoragePort, agent = userAgent) {
  const previousIdentity = installationIdentity(), previousLock = credentialLock();
  setInstallationIdentity(undefined); setCredentialLock(undefined);
  t.after(() => { setInstallationIdentity(previousIdentity); setCredentialLock(previousLock); });
  const navigator = {userAgent: agent} as Navigator;
  // Use the application's accepted lock implementation, rather than a second test lock model.
  installInsecureContextFallbacks({crypto: globalThis.crypto, navigator});
  const request = navigator.locks.request.bind(navigator.locks), lockNames: string[] = [];
  t.mock.method(navigator.locks, 'request', (name: string, work: () => Promise<unknown>) => {
    lockNames.push(name); return request(name, work);
  });
  replace(t, 'navigator', navigator); replace(t, 'localStorage', saved);
  return lockNames;
}
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return {promise, resolve};
}
function remembered() {
  const now = Date.now();
  const session = parseLocalSession({accessToken: 'synthetic-access-before', refreshToken: 'synthetic-refresh-before', installationId: persistedId,
    expiresAt: new Date(now - 60000).toISOString(), authorizationHorizon: new Date(now + 86400000).toISOString(),
    sessionFamilyId: 'family', tokenGeneration: '1', deviceId: 'device',
    viewer: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', role: 'owner'}}, {}, {stored: true});
  let value: RememberedServer = {version: '1', selectionId: 'S'.repeat(32), logicalOrigin: origin, session,
    pin: {serverId: 'server', publicKey: 'k'.repeat(43), fingerprint: 'f'.repeat(43)}, routes: {paired: [origin]}};
  let changes = 0;
  return {
    read: async () => structuredClone(value),
    change: async (update: (record: RememberedServer | undefined) => RememberedServer | undefined) => {
      const next = update(structuredClone(value)); assert.ok(next, 'startup must retain the saved credential'); value = structuredClone(next); changes++;
    },
    peek: () => structuredClone(value), changes: () => changes,
  };
}

test('first startup persists one canonical installation and reload reuses the identical claim', t => {
  const saved = storage(); browser(t, saved);
  const first = initializeBrowserInstallation('2026.10.22');
  assert.ok(first); assert.ok(validInstallationId(first.installationId));
  assert.equal(saved.peek(), first.installationId); assert.equal(saved.writes.length, 1);
  assert.deepEqual(installationIdentity(), first);
  // A reload has new core memory, while the browser's actual durable record survives.
  setInstallationIdentity(undefined); setCredentialLock(undefined);
  const reloaded = initializeBrowserInstallation('2026.10.23');
  assert.equal(reloaded?.installationId, first.installationId); assert.equal(saved.writes.length, 1);
  assert.equal(installationIdentity()?.appVersion, '2026.10.23');
});

test('an existing installation needs no storage write and sends real build and device metadata only on credential exchanges', async t => {
  const saved = storage(persistedId);
  saved.setItem = () => { assert.fail('an existing installation must not be rewritten'); };
  browser(t, saved);
  initializeBrowserInstallation('2026.10.22');
  const calls: Headers[] = [];
  const api = new HttpLocalApi(origin, '', async (_input, init) => {
    calls.push(new Headers(init?.headers)); return new Response('{}', {headers: {'Content-Type': 'application/json'}});
  });
  await api.request(routePath('post_direct_sign_in'), 'POST', {username: 'synthetic', password: 'synthetic'});
  assert.equal(calls[0].get('X-Portico-Installation-Id'), persistedId);
  assert.equal(calls[0].get('X-Portico-Device-Name'), 'Chrome on Linux');
  assert.equal(calls[0].get('X-Portico-Device-Platform'), 'web');
  assert.equal(calls[0].get('X-Portico-App'), 'Portico Web');
  assert.equal(calls[0].get('X-Portico-App-Version'), '2026.10.22');
  await api.request(routePath('get_system'));
  assert.equal(calls[1].has('X-Portico-Installation-Id'), false);
  assert.equal(calls[1].has('X-Portico-Device-Name'), false);
  assert.deepEqual(saved.writes, []);
});

test('device names describe the actual browser and operating system without importing an old component', async t => {
  for (const [agent, name] of [
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Version/18.0 Safari/604.1', 'Safari on iPhone'],
    ['Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) Version/18.0 Safari/604.1', 'Safari on iPad'],
    ['Mozilla/5.0 (Windows NT 10.0) Chrome/140.0 Safari/537.36 Edg/140.0', 'Edge on Windows'],
    ['Mozilla/5.0 (Linux; Android 15) Chrome/140.0 Safari/537.36 OPR/100.0', 'Opera on Android'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 15_0) Firefox/140.0', 'Firefox on Mac'],
    ['Mozilla/5.0 (X11; CrOS x86_64) Chrome/140.0 Safari/537.36', 'Chrome on Chromebook'],
    ['', 'Web browser'],
  ]) await t.test(name, child => {
    browser(child, storage(persistedId), agent);
    assert.equal(initializeBrowserInstallation('2026.10.22')?.name, name);
  });
});

test('malformed persisted IDs are left intact and never replaced or registered', async t => {
  for (const invalid of ['', 'short', 'x'.repeat(129), '/'.repeat(32), ' '.repeat(32)]) await t.test(JSON.stringify(invalid), child => {
    const saved = storage(invalid); browser(child, saved);
    assert.equal(initializeBrowserInstallation('2026.10.22'), undefined);
    assert.equal(installationIdentity(), undefined); assert.equal(saved.peek(), invalid); assert.deepEqual(saved.writes, []);
  });
});

test('an installation changed between startup reads is not silently rebound', t => {
  let reads = 0;
  browser(t, {
    getItem: () => ++reads === 1 ? persistedId : '00000000-0000-4000-8000-000000000002',
    setItem: () => assert.fail('startup must not repair a changed identity'),
  });
  assert.equal(initializeBrowserInstallation('2026.10.22'), undefined); assert.equal(installationIdentity(), undefined);
});

const refusedStorage: Record<string, () => StoragePort> = {
  'reads are refused': () => ({getItem: () => { throw new DOMException('Refused', 'SecurityError'); }, setItem: () => assert.fail('no replacement write')}),
  'first write is refused': () => ({getItem: () => null, setItem: () => { throw new DOMException('Full', 'QuotaExceededError'); }}),
  'writes are silently dropped': () => ({getItem: () => null, setItem: () => {}}),
  'readback is refused': () => {
    let reads = 0;
    return {getItem: () => { if (++reads >= 3) throw new DOMException('Refused', 'SecurityError'); return null; }, setItem: () => {}};
  },
};
test('unavailable or nonpersistent local storage never produces an ephemeral claim', async t => {
  for (const [name, create] of Object.entries(refusedStorage)) await t.test(name, child => {
    browser(child, create());
    assert.equal(initializeBrowserInstallation('2026.10.22'), undefined); assert.equal(installationIdentity(), undefined);
  });
  await t.test('the localStorage property itself is refused', child => {
    browser(child, storage());
    Object.defineProperty(globalThis, 'localStorage', {configurable: true, get() { throw new DOMException('Refused', 'SecurityError'); }});
    assert.equal(initializeBrowserInstallation('2026.10.22'), undefined); assert.equal(installationIdentity(), undefined);
  });
});

test('real stored-session renewal uses the startup claim and browser lock, preserving one rotation for concurrent callers', {timeout: 5000}, async t => {
  const lockNames = browser(t, storage(persistedId)); initializeBrowserInstallation('2026.10.22');
  const saved = remembered(), entered = deferred(), release = deferred();
  t.after(release.resolve);
  const before = saved.peek().session;
  const next: LocalSession = {...before, accessToken: 'synthetic-access-after', refreshToken: 'synthetic-refresh-after', tokenGeneration: '2', expiresAt: new Date(Date.now() + 3600000).toISOString()};
  let requests = 0;
  const api = new HttpLocalApi(origin, before.accessToken, async (input, init) => {
    requests++; assert.equal(String(input), origin + routePath('post_auth_refresh'));
    const body = JSON.parse(String(init?.body));
    assert.equal(body.installationId, persistedId); assert.equal(body.refreshToken, before.refreshToken);
    assert.equal(body.requestId, saved.peek().pendingRefresh, 'the real refresh service persists its retry identity before sending');
    assert.equal(new Headers(init?.headers).get('X-Portico-Installation-Id'), persistedId);
    entered.resolve(); await release.promise;
    return new Response(JSON.stringify(next), {headers: {'Content-Type': 'application/json'}});
  });
  const options = {env: {storage: saved}, api, family: {sessionFamilyId: before.sessionFamilyId, serverId: before.viewer.serverId}};
  const first = refreshStoredSession(options);
  await Promise.race([entered.promise, first]);
  const second = refreshStoredSession(options);
  assert.deepEqual(lockNames, ['portico.session-refresh:server', 'portico.session-refresh:server']);
  assert.equal(requests, 1);
  release.resolve();
  const results = await Promise.all([first, second]);
  assert.ok(results.every(result => result.accessToken === next.accessToken)); assert.equal(requests, 1);
  assert.equal(saved.peek().session.refreshToken, next.refreshToken); assert.equal(saved.peek().session.installationId, persistedId);
  assert.equal(saved.peek().pendingRefresh, undefined);
});

test('invalid or refused installation storage leaves the real credential unchanged and refuses refresh before transport', {timeout: 5000}, async t => {
  for (const [name, create] of Object.entries({...refusedStorage, 'invalid stored identity': () => storage('broken')})) await t.test(name, async child => {
    const lockNames = browser(child, create()); initializeBrowserInstallation('2026.10.22');
    const saved = remembered(), before = saved.peek(); let requests = 0;
    const api = new HttpLocalApi(origin, '', async () => { requests++; assert.fail('a missing installation claim cannot renew a bound credential'); });
    await assert.rejects(refreshStoredSession({env: {storage: saved}, api, family: {sessionFamilyId: 'family', serverId: 'server'}}),
      (error: unknown) => (error as {code?: string}).code === 'refresh_unavailable' && (error as {signOut?: boolean}).signOut === false);
    assert.equal(requests, 0); assert.equal(saved.changes(), 0); assert.deepEqual(saved.peek(), before);
    assert.deepEqual(lockNames, ['portico.session-refresh:server']);
  });
});

test('entry keeps the polyfills first and binds installation before constructing protected restoration', () => {
  const main = readFileSync(new URL('../src/app-main.tsx', import.meta.url), 'utf8');
  assert.match(main, /^import '\.\/platform\/polyfills';/);
  const initialization = main.indexOf("initializeAppInstallation(import.meta.env.VITE_APP_VERSION ?? '', signal)");
  assert.ok(initialization > main.indexOf('function start()'));
  const applicationImport = main.indexOf("await Promise.all([import('./App')");
  const bootstrap = main.indexOf('new normal.Bootstrap(');
  assert.ok(applicationImport > initialization, 'protected application modules load only after installation');
  assert.ok(bootstrap > applicationImport, 'the loaded Bootstrap is constructed after installation and imports');
  assert.ok(bootstrap < main.indexOf('createRoot('), 'restoration is bound before mounting the root');
  assert.match(main, /if \(!\(window as Window & \{__porticoUnsupportedBrowser\?: boolean\}\)\.__porticoUnsupportedBrowser\) void start\(\);/);
  const arrival = main.indexOf('captureWebAccountArrival(location, history)');
  assert.ok(arrival >= 0 && arrival < initialization, 'the actual arrival registry captures the original link before installation');
});
