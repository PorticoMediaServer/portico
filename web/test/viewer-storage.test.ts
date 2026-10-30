import test from 'node:test';
import assert from 'node:assert/strict';
import {createBrowserViewerStorage} from '../src/platform/viewer-selection-storage.ts';
import {selectedViewerRecord, type SelectedViewerRecord} from '@core/viewer-selection.ts';
const until = new Date(Date.now() + 3600000).toISOString();
const record = (family: string) => selectedViewerRecord('https://server.example', {accessToken: 'synthetic-' + family, expiresAt: until, authorizationHorizon: until, sessionFamilyId: family, tokenGeneration: '1', viewer: {accountId: 'a', profileId: 'p', serverId: 's', authority: 'local', role: 'member'}});
function fixture() {
  let value: SelectedViewerRecord | undefined;
  const vault = {read: async () => structuredClone(value), change: async (update: (v: SelectedViewerRecord | undefined) => SelectedViewerRecord | undefined) => { value = update(structuredClone(value)); }};
  return {storage: createBrowserViewerStorage(vault), vault};
}
test('selected viewer survives a read and readback, with no localStorage access', async () => {
  const f = fixture(); assert.equal(await f.storage.read(), null);
  await f.storage.write(record('one')); assert.deepEqual(await f.storage.read(), record('one'));
  await f.storage.write(null); assert.equal(await f.storage.read(), null);
});
test('a stale tab never clears another family, and damaged records are removed', async () => {
  const f = fixture(); await f.storage.write(record('new'));
  await f.storage.clearIfFamily!('old'); assert.deepEqual(await f.storage.read(), record('new'));
  await f.storage.clearIfFamily!('new'); assert.equal(await f.storage.read(), null);
  await f.vault.change(() => ({broken: true}) as never); assert.equal(await f.storage.read(), null);
  assert.equal(await f.vault.read(), undefined);
});
test('failed durable readback rejects the write', async () => {
  const storage = createBrowserViewerStorage({read: async () => undefined, change: async () => {}});
  await assert.rejects(storage.write(record('one')), /read back/);
});
