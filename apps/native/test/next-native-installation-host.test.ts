import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const id = 'installation_fixture_01234567890123456789';
async function fixture() {
  let identity: any, sessionReads = 0, factsReads = 0;
  let readSession = async () => ({installation: id}), reread = async () => id;
  let facts = async () => ({deviceName: 'Fixture television', appVersion: '1.0'});
  const {react} = hooks();
  const module = await screenModule(new URL('../src-next/platform/installation-host.ts', import.meta.url), {
    react, 'react-native': {NativeModules: {PorticoCapabilities: {facts: () => {factsReads++; return facts();}}}},
    '../../../../packages/client-core/src/installation': {
      installationIdentity: () => identity, setInstallationIdentity: (value: any) => {identity = Object.freeze({...value});},
      validInstallationId: (value: unknown) => typeof value === 'string' && value.length >= 32 && value.length <= 128 && /^[A-Za-z0-9_-]+$/.test(value),
    },
    './session': {readNativeSession: () => {sessionReads++; return readSession();}, nativeSelectedConnectionEnvironment: () => ({storage: {read: async () => undefined}})},
    './profile-trust': {profileInstallation: () => reread()},
  });
  const begin = module.createNativeInstallationHost({platform: () => 'vega', description: (raw: any) => ({name: raw.deviceName, appVersion: raw.appVersion})});
  return {begin, identity: () => identity, counts: () => ({sessionReads, factsReads}), setRead: (read: typeof readSession) => {readSession = read;}, setFacts: (read: typeof facts) => {facts = read;}, setReread: (read: typeof reread) => {reread = read;}};
}

test('shared installation host coalesces preparation and disposal preserves the installed claim', async () => {
  const f = await fixture(), restore = f.begin();
  const first = restore.prepare(), second = restore.prepare();
  assert.equal(first, second);
  const result = await first;
  assert.deepEqual(result.identity, {installationId: id, name: 'Fixture television', appVersion: '1.0', platform: 'vega', app: 'Portico'});
  assert.equal(restore.current(), true); restore.dispose(); assert.equal(restore.current(), false);
  assert.equal(f.identity(), result.identity); assert.deepEqual(f.counts(), {sessionReads: 1, factsReads: 1});
});

test('a newer restore retires an older secure read before metadata or identity publication', async () => {
  const f = await fixture(); let resolve!: (saved: {installation: string}) => void;
  f.setRead(() => new Promise(done => {resolve = done;}));
  const original = f.begin(), rejected = assert.rejects(original.prepare(), {name: 'AbortError', message: 'native_installation_cancelled'});
  f.setRead(async () => ({installation: id}));
  const replacement = f.begin(), installed = await replacement.prepare();
  resolve({installation: id}); await rejected;
  assert.equal(original.current(), false); assert.equal(replacement.current(), true);
  assert.equal(f.identity(), installed.identity); assert.equal(f.counts().factsReads, 1);
});

test('secure identity reset or cancellation during metadata leaves no new installation claim', async () => {
  const reset = await fixture(); reset.setReread(async () => `${id}_reset`);
  await assert.rejects(reset.begin().prepare(), /installation_identity_mismatch/); assert.equal(reset.identity(), undefined);
  const cancelled = await fixture(), abort = new AbortController();
  cancelled.setFacts(async () => {abort.abort(); return {deviceName: 'Fixture television', appVersion: '1.0'};});
  await assert.rejects(cancelled.begin(abort.signal).prepare(), {name: 'AbortError', message: 'native_installation_cancelled'});
  assert.equal(cancelled.identity(), undefined);
});
