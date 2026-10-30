import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {installationIdentity, installationHeaders, setInstallationIdentity, credentialLock, setCredentialLock, type InstallationIdentity} from '@core/installation.ts';
import {installPlatformInstallationProvider, initializeAppInstallation} from '../src/platform/installation-provider.ts';
import {installInsecureContextFallbacks} from '../src/platform/polyfills.ts';

const native: InstallationIdentity = {installationId: '00000000-0000-4000-8000-000000000009', name: 'Actual computer', platform: 'macos', app: 'Portico Desktop', appVersion: '0.0.9'};
function replace(t: TestContext, key: string, value: unknown) {
  const before = Object.getOwnPropertyDescriptor(globalThis, key);
  Object.defineProperty(globalThis, key, {configurable: true, value});
  t.after(() => {if (before) Object.defineProperty(globalThis, key, before);else delete (globalThis as Record<string, unknown>)[key];});
}
function environment(t: TestContext) {
  const previous = installationIdentity(), lock = credentialLock();setInstallationIdentity(undefined);
  t.after(() => {setInstallationIdentity(previous);setCredentialLock(lock);});
  const navigator = {userAgent: 'Safari/604.1'} as Navigator;
  installInsecureContextFallbacks({crypto: globalThis.crypto, navigator});replace(t, 'navigator', navigator);
  replace(t, 'localStorage', {getItem: () => assert.fail('native bootstrap must not read browser installation storage'), setItem: () => assert.fail('native bootstrap must not mint a browser installation')});
}
test('a preloaded native identity survives startup without a Web/Safari metadata override', async t => {
  environment(t);t.after(installPlatformInstallationProvider(native));
  assert.equal(installationIdentity(), undefined, 'registration itself is inert');
  assert.deepEqual(await initializeAppInstallation('different-web-version'), native);
  assert.deepEqual(installationIdentity(), native);assert.equal(installationHeaders()['X-Portico-Device-Name'], native.name);
  assert.equal(installationHeaders()['X-Portico-Device-Platform'], 'macos');assert.equal(installationHeaders()['X-Portico-App-Version'], native.appVersion);
  assert.equal(await credentialLock()('platform-test', async () => 'actual lock work'), 'actual lock work');
});
test('async native loading finishes before installation can be used by restoration', async t => {
  environment(t);let resolve!: (identity: InstallationIdentity) => void, calls = 0;
  t.after(installPlatformInstallationProvider(() => {calls++;return new Promise(done => {resolve = done;});}));
  const loading = initializeAppInstallation('web-version');assert.equal(calls, 1);assert.equal(installationIdentity(), undefined);
  assert.deepEqual(installationHeaders(), {});resolve(native);assert.deepEqual(await loading, native);
});
test('native absence, invalid identity and bridge rejection never fall back to browser identity', async t => {
  for (const value of [undefined, {...native, installationId: 'invalid'}, {...native, platform: undefined}]) await t.test('unavailable or invalid provider output', async child => {
    environment(child);child.after(installPlatformInstallationProvider(() => value as InstallationIdentity));
    await assert.rejects(initializeAppInstallation('web-version'));assert.equal(installationIdentity(), undefined);
  });
  await t.test('bridge rejection', async child => {
    environment(child);child.after(installPlatformInstallationProvider(async () => {throw new Error('fixture-native-unavailable:' + native.installationId);}));
    await assert.rejects(initializeAppInstallation('web-version'), (error: Error) => error.message === 'platform_installation_unavailable' && !String(error.stack).includes(native.installationId));
    assert.equal(installationIdentity(), undefined);
  });
});
test('an obsolete native provider cannot overwrite a newer provider after an await', async t => {
  environment(t);let resolve!: (identity: InstallationIdentity) => void;
  const removeFirst = installPlatformInstallationProvider(() => new Promise(done => {resolve = done;}));t.after(removeFirst);
  const old = initializeAppInstallation('web-version'), newer = {...native, name: 'Current computer', appVersion: '0.0.10'};
  t.after(installPlatformInstallationProvider(newer));removeFirst();
  assert.deepEqual(await initializeAppInstallation('web-version'), newer);
  resolve(native);await assert.rejects(old, /platform_installation_changed/);assert.deepEqual(installationIdentity(), newer);
});
test('with no native provider the existing durable browser claim remains the default', async t => {
  environment(t);let reads = 0;
  replace(t, 'localStorage', {getItem: () => {reads++;return native.installationId;}, setItem: assert.fail});
  const identity = await initializeAppInstallation('web-build');
  assert.ok(reads > 0);assert.equal(identity?.installationId, native.installationId);assert.equal(identity?.platform, 'web');assert.equal(identity?.app, 'Portico Web');assert.equal(identity?.appVersion, 'web-build');
});
test('closing startup fences a late native provider before identity or lock publication', async t => {
  environment(t);const controller = new AbortController(), beforeLock = credentialLock();let resolve!: (identity: InstallationIdentity) => void;
  t.after(installPlatformInstallationProvider(() => new Promise(done => {resolve = done;})));
  const loading = initializeAppInstallation('web-version', controller.signal);
  controller.abort();resolve(native);
  await assert.rejects(loading, {name: 'AbortError'});
  assert.equal(installationIdentity(), undefined);assert.equal(credentialLock(), beforeLock);
});
test('already closed startup never invokes an installed native provider', async t => {
  environment(t);t.after(installPlatformInstallationProvider(() => {assert.fail('disposed startup must not start a bridge read');}));
  await assert.rejects(initializeAppInstallation('web-version', AbortSignal.abort()), {name: 'AbortError'});
  assert.equal(installationIdentity(), undefined);
});
