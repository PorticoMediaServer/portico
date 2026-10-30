import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeModules} from './react-native-stub.mjs';
import {NativeQueueStore} from '../src-next/platform/queue-storage.ts';
import {nativeNetworkFetch} from '../src-next/platform/server-connections.ts';
import {readNativeSession} from '../src-next/platform/session.ts';
import {glassRecipe, phoneTabBarRecipe} from '../src-next/platform/glass.ts';

test('copied durable store serializes writers, reads without writes, and retains ambiguous failures', async () => {
  let text = '0'; let saves = 0;
  Object.assign(NativeModules.PorticoRuntime, {loadQueueRecord: async () => text, saveQueueRecord: async (_: string, value: string) => {text = value; saves++;}});
  const store = new NativeQueueStore();
  await Promise.all(Array.from({length: 8}, () => store.change('next-counter', raw => ({record: Number(raw) + 1, value: undefined}))));
  assert.equal(text, '8'); assert.equal(saves, 8);
  assert.equal(await store.change('next-counter', raw => ({record: raw, value: raw})), 8); assert.equal(saves, 8);
  NativeModules.PorticoRuntime.loadQueueRecord = async () => {throw new Error('Locked');};
  await assert.rejects(store.change('next-counter', () => ({record: null, value: undefined})), /Locked/);
  assert.equal(saves, 8);
});

test('copied native transport carries headers and cancellation without raw fetch', async () => {
  const calls: unknown[][] = []; const cancelled: string[] = [];
  Object.assign(NativeModules.PorticoRuntime, {playbackRequestId: () => 'native-next', cancelQueueRequest: (id: string) => cancelled.push(id), networkRequest: async (...args: unknown[]) => {calls.push(args); return {status: 204, body: '', headers: {}};}});
  const response = await nativeNetworkFetch('https://server.test/resource', {headers: {Authorization: 'Bearer viewer'}});
  assert.equal(response.status, 204); assert.equal(response.body, null);
  assert.deepEqual(calls[0], ['native-next', 'https://server.test/resource', 'GET', {authorization: 'Bearer viewer'}, null]);
  const controller = new AbortController(); let finish!: (value: unknown) => void;
  NativeModules.PorticoRuntime.networkRequest = () => new Promise(resolve => {finish = resolve;});
  const pending = nativeNetworkFetch('https://server.test/resource', {signal: controller.signal});
  controller.abort(); finish({status: 200, body: '{}', headers: {}});
  await assert.rejects(pending, (e: {code: string}) => e.code === 'cancelled');
  assert.deepEqual(cancelled, ['native-next']);
});

test('session preparation happens before secure viewer and installation reads', async () => {
  const calls: string[] = [];
  Object.assign(NativeModules.PorticoRuntime, {prepareSecureRecords: async () => {calls.push('prepare'); return true;}, profileInstallation: async () => {calls.push('installation'); return 'installation';}, loadSelectedViewer: async () => {throw new Error('historical viewer must not be read');}, loadQueueRecord: async (key: string) => {assert.equal(key, 'accounts.sessions.v1'); calls.push('sessions'); return null;}});
  const result = await readNativeSession();
  assert.equal(calls[0], 'prepare'); assert.ok(calls.includes('installation')); assert.ok(calls.includes('sessions')); assert.equal(calls.includes('viewer'), false); assert.deepEqual(result, {recovered: true, installation: 'installation', viewer: null});
  NativeModules.PorticoRuntime.prepareSecureRecords = async () => {throw new Error('Locked');}; calls.length = 0;
  await assert.rejects(readNativeSession(), /Locked/); assert.deepEqual(calls, []);
});

test('Q43 uses native blur on phones and Apple TV and fill on Android TV and Fire TV', () => {
  assert.equal(glassRecipe('ios', false).nativeBlur, true);
  assert.equal(glassRecipe('android', false).nativeBlur, true);
  assert.equal(glassRecipe('ios', true).nativeBlur, true);
  assert.equal(glassRecipe('android', true).nativeBlur, false);
  assert.equal(glassRecipe('android', true).backgroundAlpha, .14);
  assert.equal(phoneTabBarRecipe().height, 84); assert.equal(phoneTabBarRecipe().blur, 24);
  assert.equal(phoneTabBarRecipe().backgroundColor, '#10212d'); assert.equal(phoneTabBarRecipe().alpha, .82);
});


test('Menu targets only the currently focused enabled control and never synthesizes Select', async () => {
  const {registerFocusedMenu, dispatchMenuKey} = await import('../src-next/platform/menu-key.ts');
  const calls: string[] = [];
  const retireA = registerFocusedMenu({}, () => calls.push('A'));
  const retireB = registerFocusedMenu({}, () => calls.push('B'));
  retireA();
  assert.equal(dispatchMenuKey({eventType: 'select', eventKeyAction: 1}, 'android', true), false);
  assert.equal(dispatchMenuKey({eventType: 'menu', eventKeyAction: 0}, 'android', true), false);
  assert.equal(dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'ios', true), false);
  assert.equal(dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'android', false), false);
  assert.equal(dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'android', true), true);
  assert.deepEqual(calls, ['B']); retireB();
  assert.equal(dispatchMenuKey({eventType: 'menu', eventKeyAction: 1}, 'android', true), false);
});
