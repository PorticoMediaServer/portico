import test from 'node:test';
import assert from 'node:assert/strict';
import {dataFolderConfirmText, fixDataFolderPermissions, parseDataFolderPermissions, readDataFolderPermissions, DataFolderPermissionsError} from '../src/admin/data-folder.ts';

// The data folder's permissions on the rebuilt server, as the Dashboard and Settings > Troubleshooting share them.
const readable = {path: '/srv/portico', owner: 'portico', account: 'portico', readableByOthers: true, fixable: false, reason: 'not_kept_here', message: 'This drive does not keep who may open a folder.', checked: true};

test('the folder is read and fixed through the owner routes, and kept as the server says it', async () => {
  const calls: unknown[] = [];
  const api = {request: async <T>(path: string, method?: string) => {calls.push([path, method]); return (method === 'POST' ? {...readable, readableByOthers: false, fixable: false, reason: undefined, message: undefined} : readable) as T;}};
  assert.deepEqual(await readDataFolderPermissions(api), readable);
  assert.equal((await fixDataFolderPermissions(api)).readableByOthers, false);
  assert.deepEqual(calls, [['/v1/admin/data-folder/permissions', 'GET'], ['/v1/admin/data-folder/permissions/fix', 'POST']]);
});

test('a malformed answer is refused, and the confirm says in plain words what will change', () => {
  for (const bad of [null, {...readable, readableByOthers: 'yes'}, {...readable, path: 7}]) assert.throws(() => parseDataFolderPermissions(bad), DataFolderPermissionsError);
  assert.equal(dataFolderConfirmText('portico', '/srv/portico'), 'Only portico will be able to open /srv/portico. Nothing inside it changes.');
});
