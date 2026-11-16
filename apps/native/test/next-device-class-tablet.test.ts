import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineModule} from './helpers/offline-module.mjs';

// A tablet keeps its own reading settings, apart from a phone (10-reading rule 36): it must tell the
// server it is a tablet, or a phone's text size and margins land on its much larger page.
async function deviceClass(platform: Record<string, unknown>, screen = {width: 390, height: 844}) {
  const module = await offlineModule(new URL('../src-next/platform/device-platform.ts', import.meta.url), {
    'react-native': {Platform: {constants: {}, isTV: false, ...platform}, Dimensions: {get: () => screen}, NativeModules: {}},
  });
  return module.nativeDeviceClass;
}

test('an iPad or a large Android device says tablet; a phone says mobile; a TV says television', async () => {
  assert.equal(await deviceClass({OS: 'ios', isPad: true}), 'tablet');
  assert.equal(await deviceClass({OS: 'ios'}), 'mobile');
  assert.equal(await deviceClass({OS: 'android'}, {width: 800, height: 1280}), 'tablet');
  assert.equal(await deviceClass({OS: 'android'}), 'mobile');
  assert.equal(await deviceClass({OS: 'android', isTV: true}, {width: 1920, height: 1080}), 'television');
});
