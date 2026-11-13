import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineModule} from './helpers/offline-module.mjs';

test('offline playback and the returning online shell reuse each engine view registration', async () => {
  const registered = new Set<string>();
  const {nativePlaybackView} = await offlineModule(new URL('../src-next/platform/playback/native-surface.ts', import.meta.url), {
    'react-native': {requireNativeComponent: (name: string) => {
      assert.equal(registered.has(name), false, 'React Native refuses duplicate host view registrations');
      registered.add(name); return {name};
    }},
  });
  const offline = nativePlaybackView('PorticoPlayer');
  assert.equal(nativePlaybackView('PorticoPlayer'), offline);
  const mpv = nativePlaybackView('PorticoMpvPlayer');
  assert.equal(nativePlaybackView('PorticoMpvPlayer'), mpv);
  assert.notEqual(offline, mpv); assert.equal(registered.size, 2);
});
