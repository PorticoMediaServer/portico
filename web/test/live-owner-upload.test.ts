import test from 'node:test';
import assert from 'node:assert/strict';
import {UploadSourceController} from '../src/screens/live/owner/upload-source-controller.ts';
import {uploadPreview as preview, sourceReceipt} from './live-owner-fixtures.ts';
test('file replacement retires preview and save retries retain the draft request id', async () => {
  const writes: unknown[] = [];
  const controller = new UploadSourceController({async request<T>(path: string, _method?: string, body?: unknown) {
    if (!path.endsWith('/preview')) {writes.push(body);if (writes.length === 1) throw {status: 503};return sourceReceipt((body as {id: string}).id) as T;}return preview as T;
  }}, 'server', () => true, () => {});
  controller.edit({name: 'Stored playlist'});await controller.read('playlist', async () => '#EXTM3U');await controller.check();
  assert.ok(controller.getSnapshot().preview);await controller.read('guide', async () => '<tv/>');assert.equal(controller.getSnapshot().preview, undefined);
  await controller.check();assert.equal(await controller.save(), false);assert.equal(await controller.save(), true);assert.deepEqual(writes[0], writes[1]);controller.dispose();
});
test('closing while a file is being read never publishes that file or starts a preview', async () => {
  let finish!: (value: string) => void;let calls = 0;
  const controller = new UploadSourceController({async request<T>() {calls++;return preview as T;}}, 'server', () => true, () => {});
  controller.edit({name: 'Stored playlist'});const pending = controller.read('playlist', () => new Promise(done => {finish = done;}));
  controller.dispose();finish('#EXTM3U');await pending;await controller.check();assert.equal(calls, 0);assert.equal(controller.getSnapshot().draft.playlist, '');
});
