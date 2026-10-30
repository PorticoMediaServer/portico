import assert from 'node:assert/strict';
import test from 'node:test';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

const deferred = () => {let resolve!: () => void; const promise = new Promise<void>(done => {resolve = done;}); return {promise, resolve};};

test('actual bootstrap device commits reach the existing capability publisher and survive a slower concurrent server refresh', async () => {
  const began = deferred(), release = deferred(); let holdLibraries = false;
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any) => {
    if (holdLibraries && request.path === '/v1/libraries') {began.resolve(); await release.promise;}
    return undefined;
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    const {nativeDeviceSettings} = await import('../src-next/platform/device-settings');
    const {NativeModules} = await import('react-native');
    await NativeModules.PorticoRuntime.saveQueueRecord('preferences:v1', JSON.stringify({playerEngine: 'automatic', forceStereo: false, cardScale: 1.25, unrelated: {kept: true}}));
    let captured: NativeShellBootstrapContext | undefined, ended = 0;
    runtime = await restoreNativeShellRuntime(context => {captured = context; return createNativeShellBindings(context);}, {onAuthorityEnded() {ended++;}});
    const context = captured; assert.ok(runtime); assert.ok(context);
    const source = nativeDeviceSettings(context); assert.equal(source.read('forceStereo'), false);
    holdLibraries = true; const refresh = runtime.refresh(); await began.promise;
    await source.write('forceStereo', true); await source.write('playerEngine', 'native'); await app.settle();
    assert.equal(source.read('forceStereo'), true);
    const published = app.requests.filter(request => request.path === '/v1/me/devices/current/capabilities').at(-1)?.body;
    assert.equal(published?.overrides.forceStereo, true); assert.equal(published?.engineSetting, 'native');
    release.resolve(); await refresh; await app.settle();
    assert.equal(source.read('forceStereo'), true, 'an earlier parallel record observation cannot overwrite the newer committed value');
    assert.equal(ended, 0, 'device commits never promote or retire the selected viewer');
    const record = JSON.parse(await NativeModules.PorticoRuntime.loadQueueRecord('preferences:v1'));
    assert.deepEqual(record.unrelated, {kept: true}); assert.equal(record.cardScale, 1.25);
    runtime.dispose(); assert.throws(() => source.read('forceStereo'), {name: 'AbortError'});
  } finally {
    try {
      release.resolve();
    } finally {
      try {
        runtime?.dispose();
      } finally {
        try {
          await app.unmount();
        } finally {
          app.disposeLoader();
        }
      }
    }
  }
});
