import test from 'node:test';
import assert from 'node:assert/strict';
import {RemoteSourceController} from '../src/screens/live/owner/remote-source-controller.ts';
const id = 'a'.repeat(48);
import {rebuiltPreview as preview, rebuiltSource} from './live-owner-fixtures.ts';
const initial = {name: 'Stored source', locator: 'https://provider.example/playlist'};
test('editing every saved field retires the checked preview', async () => {
  const controller = new RemoteSourceController({async request<T>() {return preview as T;}}, 'server', () => true, () => {}, () => id);
  controller.edit(initial);await controller.check();assert.ok(controller.getSnapshot().preview);
  controller.edit({viewerAccess: 'owner-only'});assert.equal(controller.getSnapshot().preview, undefined);assert.equal(await controller.save(), false);controller.dispose();
});
test('a late preview cannot validate an edited draft', async () => {
  let finish!: (value: unknown) => void;
  const controller = new RemoteSourceController({request<T>() {return new Promise(done => {finish = done;}) as Promise<T>;}}, 'server', () => true, () => {}, () => id);
  controller.edit(initial);const check = controller.check();controller.edit({name: 'Changed'});finish(preview);await check;
  assert.equal(controller.getSnapshot().preview, undefined);assert.equal(controller.getSnapshot().draft.name, 'Changed');controller.dispose();
});
test('preview and create use actual DTOs; an uncertain retry keeps body and id, including sealed-provider input', async () => {
  const writes: any[] = [];let serial = 0;
  const controller = new RemoteSourceController({async request<T>(path, method, body) {assert.equal(path, '/v1/admin/live/sources/preview');assert.equal(method, 'POST');assert.equal((body as any).config.serverUrl, initial.locator);return preview as T;},
    async requestRaw(path, method, options) {writes.push({path,method,...options});if(writes.length===1)throw {status:503};return {status:201,headers:{},body:rebuiltSource};}
  }, 'server', () => true, () => {}, () => String(++serial).padStart(48, '0'));
  controller.edit({...initial,kind:'xtream',username:'private-user',password:'private-pass'});await controller.check();
  assert.equal(await controller.save(),false);controller.edit({name:'Cannot overtake the pending create'});
  assert.equal(controller.getSnapshot().draft.name, initial.name);assert.equal(await controller.save(),true);
  assert.equal(writes[0].path,'/v1/admin/live/sources');assert.equal(writes[0].method,'POST');
  assert.deepEqual(writes[0].body,writes[1].body);assert.deepEqual(writes[0].headers,writes[1].headers);
  assert.deepEqual(writes[0].body.signIn,{username:'private-user',password:'private-pass'});
  assert.equal(controller.getSnapshot().draft.password,'');controller.dispose();
});
test('guide-only URL uses the guide source; a tuner uses discovered capacity', async () => {
  const bodies: any[] = [];
  const controller = new RemoteSourceController({async request<T>(_path,_method,body) {bodies.push(body);return preview as T;},async requestRaw(_path,_method,options) {bodies.push(options?.body);return {status:201,headers:{},body:rebuiltSource};}}, 'server', () => true, () => {});
  controller.edit({...initial,kind:'guide'});await controller.check();assert.deepEqual(bodies[0].config,{guideUrl:initial.locator,refreshHours:6});
  controller.edit({kind:'hdhomerun',locator:'192.168.1.50'});await controller.check();await controller.save();assert.equal(bodies[2].tunerCount,2);controller.dispose();
});
test('retired identity cannot save a checked preview', async () => {
  let current = true, calls = 0;
  const controller = new RemoteSourceController({async request<T>() {calls++;return preview as T;}}, 'server', () => current, () => {}, () => id);
  controller.edit(initial);await controller.check();current = false;
  assert.equal(await controller.save(), false);assert.equal(calls, 1);controller.dispose();
});
