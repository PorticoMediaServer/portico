/**
 * The native record store still tells a record that can never be read (the store says its bytes
 * are not a record) apart from one it cannot read right now (locked, busy). A damaged record is
 * reported to every owner of the store, and the platform runtimes keep the same code.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {NativeModules} from './react-native-stub.mjs';
import {NativeQueueStore} from '../src-next/platform/queue-storage-legacy.ts';

/** The native record store: text per key, or a rejection with the native module's code. */
function device(initial: Record<string, string | {code: string}> = {}) {
  const records = new Map(Object.entries(initial));
  const saves: [string, string][] = [];
  Object.assign(NativeModules.PorticoRuntime, {
    async loadQueueRecord(key: string) {
      const found = records.get(key);
      if (found && typeof found === 'object') throw Object.assign(new Error('Queue recovery could not be read.'), found);
      return found ?? null;
    },
    async saveQueueRecord(key: string, json: string) { saves.push([key, json]); records.set(key, json); return true; },
  });
  return {records, saves};
}
const code = (e: unknown) => (e as {code?: unknown} | null)?.code;

test('other records in the same store keep their own failures', async () => {
  device({'setup:hosted': 'not json', 'queue.id': {code: 'secure_record_damaged'}});
  const store = new NativeQueueStore();
  await assert.rejects(store.change('setup:hosted', raw => ({record: raw, value: raw})), SyntaxError);
  await assert.rejects(store.change('queue.id', raw => ({record: raw, value: raw})), e => code(e) === 'secure_record_damaged');
});

test('both native stores answer a damaged record with the same code, apart from one they cannot read now', () => {
  const source = (path: string) => readFileSync(new URL('../' + path, import.meta.url), 'utf8');
  const android = source('android/app/src/main/java/tv/getportico/portico/native/PorticoRuntime.kt');
  const load = android.slice(android.indexOf('fun loadQueueRecord'), android.indexOf('fun saveQueueRecord'));
  assert.match(load, /store\.damaged\(PorticoRecordStore\.QUEUES, key\)\) promise\.reject\("secure_record_damaged"/);
  const records = source('android/app/src/main/java/tv/getportico/portico/native/PorticoRecordStore.kt');
  const damaged = records.slice(records.indexOf('fun damaged('), records.indexOf('/** Saves [bytes]'));
  assert.ok(damaged.length > 0);
  // Decided from the file alone: the Keystore, which can fail for a while, is never asked.
  assert.doesNotMatch(damaged, /key\(|Cipher|recoverLostKey|delete/);
  const apple = source('Native/PorticoPlayer.m');
  const appleLoad = apple.slice(apple.indexOf('RCT_REMAP_METHOD(loadQueueRecord'), apple.indexOf('RCT_REMAP_METHOD(saveQueueRecord'));
  assert.match(appleLoad, /status==errSecSuccess&&[^;]*reject\(@"secure_record_damaged"/);
});
