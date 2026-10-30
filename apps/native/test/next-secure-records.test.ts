/**
 * A device that can no longer read its saved records (the system lost the key that protects
 * them, and cannot say so for certain) removes nothing on its own. The app looks twice, then asks
 * the person; only their confirmation resets the device, and the app starts again afterwards.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {NativeModules} from './react-native-stub.mjs';
import {SECOND_LOOK_MS, SECURE_RECORDS_KEY_LOST, acknowledgeSecureRecordsRecovered, canResetSecureRecords, isSecureRecordsLost, prepareSecureRecords, resetSecureRecords, restartApp, secureRecordsRecoveredNotice} from '../src-next/platform/secure-records.ts';

const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');
const lost = () => Object.assign(new Error('lost'), {code: SECURE_RECORDS_KEY_LOST});
const runtime = NativeModules.PorticoRuntime as Record<string, unknown>;
function withRuntime(methods: Record<string, unknown>, run: () => Promise<void>) {
  const before: Record<string, unknown> = {};
  for (const name of ['prepareSecureRecords', 'resetSecureRecords', 'restart']) { before[name] = runtime[name]; delete runtime[name]; }
  Object.assign(runtime, methods);
  return run().finally(() => { for (const name of Object.keys(before)) { if (before[name] === undefined) delete runtime[name]; else runtime[name] = before[name]; } });
}

test('a key that answers on the second look is not reported lost', () => withRuntime({}, async () => {
  let calls = 0;
  const waits: number[] = [];
  runtime.prepareSecureRecords = async () => { if (++calls === 1) throw lost(); return false; };
  assert.equal(await prepareSecureRecords(async ms => { waits.push(ms); }), false);
  assert.equal(calls, 2);
  assert.deepEqual(waits, [SECOND_LOOK_MS]);
}));

test('a key that is still gone on the second look is reported lost', () => withRuntime({}, async () => {
  let calls = 0;
  runtime.prepareSecureRecords = async () => { calls++; throw lost(); };
  await assert.rejects(prepareSecureRecords(async () => {}), error => isSecureRecordsLost(error));
  assert.equal(calls, 2);
}));

test('any other storage failure is not looked at twice and is not a lost key', () => withRuntime({}, async () => {
  let calls = 0;
  runtime.prepareSecureRecords = async () => { calls++; throw Object.assign(new Error('busy'), {code: 'secure_storage'}); };
  await assert.rejects(prepareSecureRecords(async () => { assert.fail('no second look'); }), error => !isSecureRecordsLost(error));
  assert.equal(calls, 1);
}));

test('a store that reset itself says so', () => withRuntime({prepareSecureRecords: async () => true}, async () => {
  assert.equal(await prepareSecureRecords(), true);
  const notice = secureRecordsRecoveredNotice(); assert.ok(notice);
  runtime.prepareSecureRecords = async () => false;
  assert.equal(await prepareSecureRecords(), false);
  assert.equal(secureRecordsRecoveredNotice(), notice, 'a fresh root preparation cannot silently consume the pending notice');
  acknowledgeSecureRecordsRecovered({}); assert.equal(secureRecordsRecoveredNotice(), notice);
  acknowledgeSecureRecordsRecovered(notice); assert.equal(secureRecordsRecoveredNotice(), undefined);
}));

test('a runtime with no reset cannot be asked to reset, and nothing is claimed', () => withRuntime({}, async () => {
  assert.equal(canResetSecureRecords(), false);
  await assert.rejects(resetSecureRecords());
  assert.equal(await restartApp(), false);
}));

test('a confirmed reset reports whether anything was removed, and a failed one rejects', () => withRuntime({}, async () => {
  runtime.resetSecureRecords = async () => true;
  assert.equal(canResetSecureRecords(), true);
  assert.equal(await resetSecureRecords(), true);
  runtime.resetSecureRecords = async () => false;
  assert.equal(await resetSecureRecords(), false);
  runtime.resetSecureRecords = async () => { throw Object.assign(new Error('keystore'), {code: 'secure_storage'}); };
  await assert.rejects(resetSecureRecords());
  runtime.restart = async () => true;
  assert.equal(await restartApp(), true);
  runtime.restart = async () => { throw new Error('no host'); };
  assert.equal(await restartApp(), false);
}));

test('Android maps a lost key to its own error and exposes reset and restart', () => {
  // The record store's key-loss, confirmation and no-erase rules run as JVM
  // tests in android/app/src/test/.../PorticoRecordStoreTest.kt.
  const runtimeSource = source('android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt');
  assert.match(runtimeSource, /catch \(_: RecordKeyLostException\) \{ promise\.reject\("secure_records_key_lost"/);
  assert.match(runtimeSource, /fun resetSecureRecords\(promise: Promise\)/);
  assert.match(runtimeSource, /fun restart\(promise: Promise\)/);
});
