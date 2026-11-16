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
  assert.deepEqual(await profileAvatarUpload(api)('/v1/profiles/profile/avatar', new Uint8Array([0, 128, 255]), 'image/png'), {saved: true});
  assert.equal(calls.length, 1);
  assert.equal(calls[0]![1], 'https://original.test/v1/profiles/profile/avatar');
  assert.equal((calls[0]![3] as Record<string, string>).authorization, 'Bearer original-token');
  assert.equal(calls[0]![4], '{"nativeAvatarBase64":"AID/"}');
});

test('a native channel logo reaches the rebuilt PUT route as picture bytes, without the old multipart request', async t => {
  const {uploadNativeOwnerChannelLogo}=await import('../../../packages/client-core/src/server-admin/owner-library-channels.ts');
  const runtime=NativeModules.PorticoRuntime,previous=runtime.avatarWireBase64;runtime.avatarWireBase64=true;
  t.after(()=>{runtime.avatarWireBase64=previous;});
  const calls:unknown[][]=[];
  t.mock.method(runtime,'networkRequest',async(...args:unknown[])=>{calls.push(args);return {status:200,headers:{},body:'{"id":"channel","name":"Movies","revision":"2"}'};});
  const api=new HttpLocalApi('https://original.test','original-token',nativeNetworkFetch);
  const answer=await uploadNativeOwnerChannelLogo(api,'channel','AID/');assert.equal(answer.revision,'2');
  assert.equal(calls.length,1);assert.equal(calls[0][1],'https://original.test/v1/admin/library-channels/channel/logo');assert.equal(calls[0][2],'PUT');
  assert.equal((calls[0][3] as Record<string,string>)['content-type'],'image/jpeg');assert.equal(calls[0][4],'AID/');
});
