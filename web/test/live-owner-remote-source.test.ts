import test from 'node:test';
import assert from 'node:assert/strict';
import {RemoteSourceController} from '../src/screens/live/owner/remote-source-controller.ts';
const id = 'a'.repeat(48);
import {remotePreview as preview, sourceReceipt} from './live-owner-fixtures.ts';
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
test('local-network roots are sent only after explicit confirmation and cleared for another endpoint', async () => {
  const bodies: unknown[] = [];
  const controller = new RemoteSourceController({async request<T>(_path: string, _method?: string, body?: unknown) {
    bodies.push(body);if (bodies.length === 1) throw {code: 'source_lan_confirmation_required', confirmRoots: ['http://192.168.1.50:80']};return preview as T;
  }}, 'server', () => true, () => {}, () => id);
  controller.edit({...initial, kind: 'hdhomerun', locator: '192.168.1.50'});await controller.check();
  assert.deepEqual((bodies[0] as {confirmedLanRoots: unknown}).confirmedLanRoots, []);
  await controller.check(true);assert.deepEqual((bodies[1] as {confirmedLanRoots: unknown}).confirmedLanRoots, ['http://192.168.1.50']);
  controller.edit({locator: '192.168.1.51'});assert.deepEqual(controller.getSnapshot().draft.confirmedLanRoots, []);controller.dispose();
});
test('save retries keep the preview operation id and never send credentials', async () => {
  const writes: unknown[] = [];let serial = 0;
  const controller = new RemoteSourceController({async request<T>(_path: string, method?: string, body?: unknown) {
    if (_path.endsWith('/save')) {writes.push(body);if (writes.length === 1) throw {status: 503};return sourceReceipt('1'.padStart(48, '0')) as T;}return preview as T;
  }}, 'server', () => true, () => {}, () => String(++serial).padStart(48, '0'));
  controller.edit({...initial, username: 'private-user', password: 'private-pass'});await controller.check();
  assert.equal(await controller.save(), false);assert.equal(await controller.save(), true);assert.deepEqual(writes[0], writes[1]);
  assert.deepEqual(Object.keys(writes[0] as object).sort(), ['previewId', 'requestId']);controller.dispose();
});
test('retired identity cannot save a checked preview', async () => {
  let current = true, calls = 0;
  const controller = new RemoteSourceController({async request<T>() {calls++;return preview as T;}}, 'server', () => current, () => {}, () => id);
  controller.edit(initial);await controller.check();current = false;
  assert.equal(await controller.save(), false);assert.equal(calls, 1);controller.dispose();
});
