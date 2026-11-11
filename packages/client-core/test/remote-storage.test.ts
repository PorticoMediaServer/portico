import test from 'node:test';
import assert from 'node:assert/strict';
import {addRemoteFolderToLibrary, listRemoteSources, remoteSourceCreate} from '../src/remote-storage.ts';

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

test('every page of sources is read', async () => {
  const {sent, api} = server(({path}) => path.includes('cursor=c2') ? {items: [{id: 'rs_2'}]} : {items: [{id: 'rs_1'}], nextCursor: 'c2'});
  assert.deepEqual((await listRemoteSources(api)).map(one => one.id), ['rs_1', 'rs_2']);
  assert.deepEqual(sent.map(one => one.path), ['/v1/admin/remote-sources', '/v1/admin/remote-sources?cursor=c2']);
});

test('Continue setup puts a source folder in a library, keeping its other folders on remote sources, and adds it once', async () => {
  const library = {id: 'lib_1', name: 'Films', kind: 'movies', folders: [{path: '/media/films', available: true, watched: true, scanEveryMinutes: 0},
    {path: 'Old', source: 'rs_1', available: true, watched: false, scanEveryMinutes: 0}], options: {}, scanning: false, revision: 'r1'};
  const {sent, api} = server(({method}) => method === 'PATCH' ? library : library);
  await addRemoteFolderToLibrary(api, 'lib_1', {source: 'rs_2', path: 'Films'});
  assert.deepEqual(sent.at(-1), {path: '/v1/admin/libraries/lib_1', method: 'PATCH', body: {remoteFolders: [{source: 'rs_1', path: 'Old'}, {source: 'rs_2', path: 'Films'}]}});
  sent.length = 0;
  await addRemoteFolderToLibrary(api, 'lib_1', {source: 'rs_1', path: 'Old'});
  assert.deepEqual(sent.map(one => one.method), ['GET']);
});
