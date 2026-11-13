import test from 'node:test';
import assert from 'node:assert/strict';
import {UploadSourceController} from '../src/screens/live/owner/upload-source-controller.ts';
import {rebuiltPreview as preview, rebuiltSource} from './live-owner-fixtures.ts';
test('file replacement retires preview and save retries retain the draft request id', async () => {
  const writes: unknown[] = [];
  const controller = new UploadSourceController({async request<T>() {return preview as T;},async requestRaw(path,method,options) {assert.equal(path,'/v1/admin/live/sources');assert.equal(method,'POST');writes.push(options);if (writes.length===1)throw {status:503};return {status:201,headers:{},body:rebuiltSource};}}, 'server', () => true, () => {});
  controller.edit({name: 'Stored playlist'});await controller.read('playlist', async () => '#EXTM3U');await controller.check();
  assert.ok(controller.getSnapshot().preview);await controller.read('guide', async () => '<tv/>');assert.equal(controller.getSnapshot().preview, undefined);
  await controller.check();assert.equal(await controller.save(), false);assert.equal(await controller.save(), true);assert.deepEqual((writes[0] as any).body, (writes[1] as any).body);assert.deepEqual((writes[0] as any).headers, (writes[1] as any).headers);controller.dispose();
});
test('closing while a file is being read never publishes that file or starts a preview', async () => {
  let finish!: (value: string) => void;let calls = 0;
  const controller = new UploadSourceController({async request<T>() {calls++;return preview as T;}}, 'server', () => true, () => {});
  controller.edit({name: 'Stored playlist'});const pending = controller.read('playlist', () => new Promise(done => {finish = done;}));
  controller.dispose();finish('#EXTM3U');await pending;await controller.check();assert.equal(calls, 0);assert.equal(controller.getSnapshot().draft.playlist, '');
});

test('a guide file can be previewed and added without a playlist',async()=>{
 let body:any;const controller=new UploadSourceController({async request<T>(_path,_method,value){body=value;return preview as T;},async requestRaw(_path,_method,options){assert.deepEqual(options?.body,body);return {status:201,headers:{},body:rebuiltSource};}},'server',()=>true,()=>{});
 controller.edit({name:'Guide'});await controller.read('guide',async()=>'<tv/>');await controller.check();assert.equal(body.kind,'guide');assert.equal(body.guide,'<tv/>');assert.equal(await controller.save(),true);controller.dispose();
});
