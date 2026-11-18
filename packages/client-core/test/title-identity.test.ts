import assert from 'node:assert/strict';
import test from 'node:test';
import {mergeTitles, removeLibrary} from '../src/title-identity.ts';
import {formValue, withFormValue} from '../src/server-admin/server-forms.ts';

test('merging sends what the owner confirmed; removing a library asks the rebuilt server and waits for the job that removes it', async () => {
  const calls: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => { calls.push([path, method, body]); return (path.endsWith('/merge') ? {itemId: 'b'} : path.startsWith('/v1/jobs/') ? {id: 'job_1', kind: 'delete_library', state: 'done'} : {id: 'job_1', kind: 'delete_library', state: 'waiting'}) as T; }};
  assert.equal(await mergeTitles(api, 'a', 'b'), 'b');
  await removeLibrary(api, 'films', 'Films', 7, 1);
  assert.deepEqual(calls, [['/v1/admin/items/b/merge', 'POST', {itemId: 'a'}], ['/v1/admin/libraries/films?confirm=Films', 'DELETE', undefined], ['/v1/jobs/job_1', 'GET', undefined]]);
  await assert.rejects(mergeTitles(api, 'a', 'a'));
});

test('a setting whose own name has a dot is one key, not a path', () => {
  const draft = {name: 'Portico', 'library.missingTitles': 'after_days', nested: {inner: 1}};
  assert.equal(formValue(draft, 'library.missingTitles'), 'after_days');
  assert.equal(formValue(draft, 'nested.inner'), 1);
  assert.deepEqual(withFormValue(draft, 'library.missingTitles', 'never'), {...draft, 'library.missingTitles': 'never'});
  assert.deepEqual(withFormValue(draft, 'nested.inner', 2), {...draft, nested: {inner: 2}});
});

test('File info commands name whole versions and titles on the rebuilt routes', async () => {
  const {splitTitleVersion} = await import('../src/title-identity.ts');
  const calls: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {calls.push([path, method, body]); return {itemId: 'n'} as T;}};
  assert.equal(await mergeTitles(api, 'a', 'b'), 'n');
  assert.equal(await splitTitleVersion(api, 'version/1'), 'n');
  assert.deepEqual(calls, [['/v1/admin/items/b/merge', 'POST', {itemId: 'a'}], ['/v1/admin/versions/version%2F1/split', 'POST', undefined]]);
});

test('version deletion uses the whole version route and has no fabricated impact preview', async () => {
  const {versionDeletionService} = await import('../src/administration.ts');
  const calls: unknown[] = [];
  const service = versionDeletionService(async <T>(path: string, method?: string, body?: unknown) => {
    calls.push([path, method, body]); return {removed: false, filesDeleted: 2, bytes: 2048, kept: [], alsoRemoved: []} as T;
  });
  await assert.rejects(service.preview('movie', 'version/1'), {code: 'version_delete_preview_unavailable'});
  assert.deepEqual(calls, []);
  const preview = {assetId: 'version/1', confirmation: 'A film', blocked: [], targets: [{itemId: 'movie', title: 'A film', allowMediaDeletion: true}]} as never;
  await assert.rejects(service.remove(preview, 'wrong title', 'operation'));
  const result = await service.remove(preview, 'A film', 'operation');
  assert.deepEqual(calls, [['/v1/admin/versions/version%2F1', 'DELETE', undefined]]);
  assert.equal(result.receipts[0].removed, true, 'the version was deleted even though other versions keep its title alive');
});
