import test from 'node:test';
import assert from 'node:assert/strict';
import {addCloudTokenSource, addRcloneRemoteFromFields, addRemoteFolderToLibrary, listRemoteSources, missingField, remoteRefusal, remoteSourceCreate} from '../src/remote-storage.ts';

type Sent = {path: string; method: string; body: unknown};
const server = (answer: (sent: Sent) => unknown) => {
  const sent: Sent[] = [];
  return {sent, api: {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => { const one = {path, method, body}; sent.push(one); return answer(one) as T; }}};
};

test('the wizard sends only what each kind needs, with the default preset, and names a missing field instead of sending', () => {
  assert.deepEqual(remoteSourceCreate({kind: 'webdav', name: ' NAS ', address: 'https://nas.local/dav', user: 'me', password: 'pw'}),
    {name: 'NAS', preset: 'read-once', kind: 'webdav', webdav: {address: 'https://nas.local/dav', user: 'me', password: 'pw'}});
  assert.deepEqual(remoteSourceCreate({kind: 'webdav', name: 'NAS', address: 'nas.local', user: '', password: ''}), {missing: 'address'});
  // One remote pasted as rclone config show prints it (rule 8).
  assert.deepEqual(remoteSourceCreate({kind: 'rclone', name: 'Box', config: '[box]\ntype = box\ntoken = {}', path: 'Films'}, 'list-only'),
    {name: 'Box', preset: 'list-only', kind: 'rclone', rclone: {config: '[box]\ntype = box\ntoken = {}', path: 'Films'}});
  assert.deepEqual(remoteSourceCreate({kind: 'rclone', name: 'Box', config: 'token = {}', path: ''}), {missing: 'config'});
  assert.deepEqual(remoteSourceCreate({kind: 'drive', name: 'Drive', path: '', signIn: 'si_1', redirectedUrl: ''}), {missing: 'redirectedUrl'});
});

test('a cloud token is sent as an rclone option and a refused sign-in keeps the server’s wording', async () => {
  const token = '{"access_token":"stand-in","refresh_token":"stand-in-refresh"}';
  const fields = {name: 'OneDrive', provider: 'onedrive', token, path: 'Films', preset: 'list-only'} as const;
  const {api, sent} = server(() => ({id: 'rs_cloud'}));
  assert.equal((await addCloudTokenSource(api, fields)).id, 'rs_cloud');
  assert.deepEqual(sent, [{path: '/v1/admin/remote-sources', method: 'POST', body: {
    kind: 'rclone', name: 'OneDrive', preset: 'list-only', rclone: {type: 'onedrive', options: {token}, path: 'Films'},
  }}]);

  const message = 'The source refused its sign-in: check the user name and password, key or token.';
  const refused = server(() => {throw Object.assign(new Error(message), {code: 'source_sign_in'});});
  await assert.rejects(addCloudTokenSource(refused.api, fields), error => {
    assert.deepEqual(remoteRefusal(error), {sentence: message});
    return true;
  });
});

test('a new rclone remote sends the backend and its options and maps a refused rclone field', async () => {
  const fields = {name: 'Archive', type: 'sftp', options: {host: 'storage.example', user: 'owner', pass: 'stand-in'}, path: '/srv/media', preset: 'on-request'} as const;
  const {api, sent} = server(() => ({id: 'rs_sftp'}));
  assert.equal((await addRcloneRemoteFromFields(api, fields)).id, 'rs_sftp');
  assert.deepEqual(sent, [{path: '/v1/admin/remote-sources', method: 'POST', body: {
    kind: 'rclone', name: 'Archive', preset: 'on-request',
    rclone: {type: 'sftp', options: {host: 'storage.example', user: 'owner', pass: 'stand-in'}, path: '/srv/media'},
  }}]);

  const refused = server(() => {throw Object.assign(new Error('Check the request and try again.'), {code: 'invalid_request', path: 'rclone'});});
  await assert.rejects(addRcloneRemoteFromFields(refused.api, fields), error => {
    assert.deepEqual(remoteRefusal(error), {field: 'config'});
    return true;
  });
});

test('every page of sources is read', async () => {
  const {sent, api} = server(({path}) => path.includes('cursor=c2') ? {items: [{id: 'rs_2'}]} : {items: [{id: 'rs_1'}], nextCursor: 'c2'});
  assert.deepEqual((await listRemoteSources(api)).map(one => one.id), ['rs_1', 'rs_2']);
  assert.deepEqual(sent.map(one => one.path), ['/v1/admin/remote-sources', '/v1/admin/remote-sources?cursor=c2']);
});

test('Continue setup puts a source folder in a library, keeping its other folders on remote sources, and adds it once', async () => {
  const library = {id: 'lib_1', name: 'Films', kind: 'movies', folders: [{path: '/media/films', available: true, watched: true, scanEveryMinutes: 0},
    {path: 'Old', source: 'rs_1', available: true, watched: false, scanEveryMinutes: 0}], options: {}, scanning: false, revision: 'r1'};
  // A server with folders on remote sources answers the library with the folder in it.
  const {sent, api} = server(({method, body}) => method === 'PATCH'
    ? {...library, folders: [library.folders[0], ...(body as {remoteFolders: {source: string; path: string}[]}).remoteFolders.map(one => ({...one, available: true, watched: false, scanEveryMinutes: 0}))]} : library);
  await addRemoteFolderToLibrary(api, 'lib_1', {source: 'rs_2', path: 'Films'});
  assert.deepEqual(sent.at(-1), {path: '/v1/admin/libraries/lib_1', method: 'PATCH', body: {remoteFolders: [{source: 'rs_1', path: 'Old'}, {source: 'rs_2', path: 'Films'}]}});
  sent.length = 0;
  await addRemoteFolderToLibrary(api, 'lib_1', {source: 'rs_1', path: 'Old'});
  assert.deepEqual(sent.map(one => one.method), ['GET']);
});

// A server without the remoteFolders field reads the PATCH as an edit of nothing and answers the
// library as it was. Continue setup must not report that as done (review of #2757).
test('Continue setup does not report a folder as added when the answer does not have it', async () => {
  const library = {id: 'lib_1', name: 'Films', kind: 'movies', folders: [{path: '/media/films', available: true, watched: true, scanEveryMinutes: 0}], options: {}, scanning: false, revision: 'r1'};
  const api = {request: async <T>(): Promise<T> => library as T};
  await assert.rejects(addRemoteFolderToLibrary(api, 'lib_1', {source: 'rs_2', path: 'Films'}), {code: 'not_on_server'});
});

test('a refused request says what the server wrote for this area, or names the refused field, never a stranger sentence', () => {
  // Seen in the browser try: an address nothing answers on, and rclone missing on the server.
  assert.deepEqual(remoteRefusal({code: 'source_unreachable', message: 'Portico could not reach that address: connection refused'}),
    {sentence: 'Portico could not reach that address: connection refused'});
  assert.deepEqual(remoteRefusal({code: 'source_in_use', message: "Remove this source's folders from their libraries first."}),
    {sentence: "Remove this source's folders from their libraries first."});
  assert.deepEqual(remoteRefusal({code: 'invalid_request', message: 'Check the request and try again.', path: 'rclone.config'}), {field: 'config'});
  assert.deepEqual(remoteRefusal({code: 'invalid_request', message: 'Check the request and try again.', path: 'webdav.address'}), {field: 'address'});
  assert.deepEqual(remoteRefusal(missingField('config')), {field: 'config'});
  assert.deepEqual(remoteRefusal({code: 'internal', message: '<pre>stack</pre>'}), {other: 'internal'});
});
