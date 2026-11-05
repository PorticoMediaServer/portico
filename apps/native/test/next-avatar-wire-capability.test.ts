import assert from 'node:assert/strict';
import test from 'node:test';
import {HttpLocalApi} from '../../../packages/client-core/src/index.ts';
import {profileAvatarUpload} from '../src-next/platform/profile-avatars.ts';
import {nativeNetworkFetch} from '../src-next/platform/server-connections.ts';
import {NativeModules} from './react-native-stub.mjs';

test('a legacy provider keeps its original avatar envelope on the same authenticated native port', async t => {
  const runtime = NativeModules.PorticoRuntime, previous = runtime.avatarWireBase64;
  delete runtime.avatarWireBase64;
  t.after(() => {runtime.avatarWireBase64 = previous;});
  const calls: unknown[][] = [];
  t.mock.method(runtime, 'networkRequest', async (...args: unknown[]) => {
    calls.push(args);return {status: 200, headers: {}, body: '{"saved":true}'};
  });
  const api = new HttpLocalApi('https://original.test', 'original-token', nativeNetworkFetch);
  assert.deepEqual(await profileAvatarUpload(api)('/v1/direct/profiles/profile/avatar', new Uint8Array([0, 128, 255]), 'image/png'), {saved: true});
  assert.equal(calls.length, 1);
  assert.equal(calls[0]![1], 'https://original.test/v1/direct/profiles/profile/avatar');
  assert.equal((calls[0]![3] as Record<string, string>).authorization, 'Bearer original-token');
  assert.equal(calls[0]![4], '{"nativeAvatarBase64":"AID/"}');
});
