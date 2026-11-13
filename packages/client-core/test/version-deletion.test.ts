// Delete this version… (03-scanning rules 64-66): the server's preview of this exact version, then the delete.
import test from 'node:test';
import assert from 'node:assert/strict';
import {deleteVersionFiles, previewVersionDeletion} from '../src/administration.ts';

const preview = {title: 'Roses Above the Quiet City', titles: 0, files: 1, bytes: 105083, listEntries: 2, otherPeople: 3, alsoRemoves: []};
function server(answers: Record<string, unknown>) {
  const calls: string[] = [];
  return {calls, api: {async request<T>(path: string, method = 'GET'): Promise<T> {calls.push(method + ' ' + path); return answers[method + ' ' + path] as T;}}};
}

test('the preview names what deleting the version takes: files, size, titles that go, list entries and other people\'s places', async () => {
  const s = server({'GET /v1/admin/versions/ver_2/deletion': {...preview, titles: 2, alsoRemoves: [{id: 'itm_9', title: 'Episode two'}]}});
  const deletion = await previewVersionDeletion(s.api, 'ver_2');
  assert.deepEqual(s.calls, ['GET /v1/admin/versions/ver_2/deletion']);
  assert.deepEqual(deletion, {versionId: 'ver_2', title: 'Roses Above the Quiet City', titles: 2, files: 1, bytes: 105083, listEntries: 2, otherPeople: 3, alsoRemoves: [{id: 'itm_9', title: 'Episode two'}]});
  await assert.rejects(previewVersionDeletion(server({'GET /v1/admin/versions/ver_2/deletion': {...preview, files: -1}}).api, 'ver_2'), 'an unreadable preview is refused, never drawn');
});

test('the delete names the previewed version, and a kept file says the title was kept', async () => {
  const deletion = await previewVersionDeletion(server({'GET /v1/admin/versions/ver_2/deletion': preview}).api, 'ver_2');
  const done = server({'DELETE /v1/admin/versions/ver_2': {removed: true, filesDeleted: 1, bytes: 105083, kept: [], alsoRemoved: []}});
  assert.deepEqual([(await deleteVersionFiles(done.api, deletion)).filesRetained, done.calls], [false, ['DELETE /v1/admin/versions/ver_2']]);
  const kept = server({'DELETE /v1/admin/versions/ver_2': {removed: false, filesDeleted: 0, bytes: 0, kept: [{path: 'x'}], alsoRemoved: []}});
  assert.equal((await deleteVersionFiles(kept.api, deletion)).filesRetained, true);
});
