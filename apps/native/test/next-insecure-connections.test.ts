import assert from 'node:assert/strict';
import test from 'node:test';
import {NativeModules, Platform} from './react-native-stub.mjs';
import {NativeQueueStore} from '../src-next/platform/queue-storage';
import {bindNativeDeviceSettings} from '../src-next/platform/device-settings';
import {loadConnectionPreference, nativeConnectionPolicy} from '../src-next/platform/connection-preference';
import {localPlainHttpHost} from '../src-next/platform/apple-plain-http';

const key = 'preferences:v1';
test('Insecure connections: On my network by default, Never or On my network on Apple, Always elsewhere; the race reads the committed value', async () => {
  const runtime = NativeModules.PorticoRuntime as Record<string, unknown>, old = {read: runtime.loadQueueRecord, write: runtime.saveQueueRecord};
  let raw: string | null = JSON.stringify({playerEngine: 'automatic'});
  runtime.loadQueueRecord = async (recordKey: string) => {assert.equal(recordKey, key); return raw;};
  runtime.saveQueueRecord = async (_key: string, next: string) => {raw = next;};
  const platform = Platform as {OS: string};
  try {
    // Before the saved value has loaded the race is strict.
    assert.equal(nativeConnectionPolicy().insecure, 'never');
    await loadConnectionPreference();
    assert.deepEqual(nativeConnectionPolicy(), {insecure: 'local', platform: 'apple'});
    const storage = new NativeQueueStore();
    let observed = await storage.change(key, value => ({record: value, value}));
    const owner = {current: () => true, subscribePreferences: () => () => {}};
    const source = bindNativeDeviceSettings(owner, {storage, readRecord: () => observed, acceptRecord: value => {observed = value;}});
    assert.equal(source.read('allowInsecure'), 'local');
    await assert.rejects(source.write('allowInsecure', 'always'));
    await source.write('allowInsecure', 'never');
    assert.equal(source.read('allowInsecure'), 'never');
    assert.equal(nativeConnectionPolicy().insecure, 'never');
    assert.equal(JSON.parse(raw!).allowInsecure, 'never');
    platform.OS = 'android';
    await source.write('allowInsecure', 'always');
    assert.deepEqual(nativeConnectionPolicy(), {insecure: 'always', platform: 'native'});
  } finally {platform.OS = 'ios'; runtime.loadQueueRecord = old.read; runtime.saveQueueRecord = old.write;}
});

test('the Apple plain-HTTP floor is the shared rule: home and overlay literals and ATS names, never a public address or name', () => {
  for (const host of ['192.168.1.20', '10.0.0.2', '100.101.102.103', 'fd7a:115c:a1e0::5', '[fe80::1]', 'nas', 'nas.local', 'nas.home.arpa', 'nas.lan', 'localhost'])
    assert.equal(localPlainHttpHost(host), true, host);
  for (const host of ['8.8.8.8', '2001:db8::1', 'media.example.com', 'nas.tailnet.ts.net', '66-94-102-39.ptc-aaaaaaaaaaaaaaaaaaaa.portico.direct', ''])
    assert.equal(localPlainHttpHost(host), false, host);
});
