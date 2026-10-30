import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');

// Destructive Android recovery is exercised by PorticoRecordStoreTest using real sealed files,
// injected keystore/filesystem failures, and a fresh owner after each interrupted reset.

test('both runtime contracts prepare secure records before installation identity is used', () => {
  assert.match(source('android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt'), /fun prepareSecureRecords\(promise: Promise\)/);
  assert.match(source('Native/PorticoPlayer.m'), /RCT_REMAP_METHOD\(prepareSecureRecords/);
  const session = source('src-next/platform/session.ts');
  assert.ok(session.indexOf('await prepareSecureRecords()') < session.indexOf('Promise.all([profileInstallation()'));
  assert.match(session, /const recovered = await prepareSecureRecords\(\)/);
  assert.match(session, /return \{recovered,/);
});

test('an older native runtime still permits installation initialization', async () => {
  const {NativeModules} = await import('./react-native-stub.mjs');
  const bridge = await import('../src-next/platform/profile-trust.ts');
  const prepare = (bridge as any).prepareSecureRecords;
  assert.equal(typeof prepare, 'function');
  delete NativeModules.PorticoRuntime.prepareSecureRecords;
  assert.equal(await prepare(), false);
  NativeModules.PorticoRuntime.prepareSecureRecords = async () => true;
  assert.equal(await prepare(), true);
  NativeModules.PorticoRuntime.prepareSecureRecords = async () => { throw new Error('Storage unavailable'); };
  await assert.rejects(prepare(), /Storage unavailable/);
  delete NativeModules.PorticoRuntime.prepareSecureRecords;
});
