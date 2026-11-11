import test from 'node:test';
import assert from 'node:assert/strict';
import {askAndSave, BrowserDownloads, browserSaveAddresses} from '../src/browser-downloads.ts';
import {createAttachmentDownloadPort} from '../src/attachment-download.ts';

type Sent = {path: string; method: string; body: unknown};
const server = (answer: (sent: Sent) => unknown) => {
  const sent: Sent[] = [];
  return {sent, api: {mediaUrl: (path: string) => 'https://server.test' + path, request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    const one = {path, method, body};
    sent.push(one);
    const value = answer(one);
    if (value instanceof Error) throw value;
    return value as T;
  }}};
};
const counts = (titles: number, ready: number, waiting = 0) => ({titles, ready, waiting, failed: 0});
const film = {id: 'dl_film', deviceId: 'dev_1', target: {kind: 'movie', id: 'itm_film', title: 'Film'}, quality: 'original', selection: 'all', paused: false,
  counts: counts(1, 1), createdAt: '2026-11-11T10:00:00Z'};
const show = {id: 'dl_show', deviceId: 'dev_2', target: {kind: 'season', id: 'itm_s1', title: 'Season 1'}, quality: '720p-low', selection: 'all', paused: false,
  counts: counts(2, 1, 1), createdAt: '2026-11-11T09:00:00Z'};
const titles: Record<string, unknown[]> = {
  dl_film: [{itemId: 'itm_film', kind: 'movie', title: 'Film', state: 'ready'}],
  dl_show: [{itemId: 'itm_e1', kind: 'episode', title: 'One', state: 'ready', sizeBytes: 10}, {itemId: 'itm_e2', kind: 'episode', title: 'Two', state: 'preparing', doneBytes: 5, totalBytes: 10}],
};

test('the Downloads page lists every title of the profile\'s downloads, acts on a request of one title from its row and on a season from its own, and reads again only what moved', async () => {
  const {sent, api} = server(({path}) => path === '/v1/downloads?scope=profile' ? {items: [film, show]} : path === '/v1/downloads/usage' ? {copiesBytes: 10}
    : path.startsWith('/v1/downloads/dl_') ? {...(path.endsWith('film') ? film : show), items: titles[path.slice(14)]} : new Error('unexpected ' + path));
  const list = new BrowserDownloads(api, 60_000);
  await list.refresh();
  const snapshot = list.getSnapshot();
  assert.deepEqual(snapshot.rows.map(row => [row.key, row.title.state, row.actions]), [
    ['dl_film/itm_film', 'ready', ['remove']], ['dl_show/itm_e1', 'ready', []], ['dl_show/itm_e2', 'preparing', []]]);
  assert.deepEqual(snapshot.requests.map(one => [one.download.id, one.actions]), [['dl_show', ['pause', 'remove']]]);
  assert.equal(snapshot.copiesBytes, 10);

  // The film settled; the season is still being made, so only it is read again.
  sent.length = 0;
  await list.refresh();
  assert.deepEqual(sent.map(one => one.path).filter(path => path.startsWith('/v1/downloads/dl_')), ['/v1/downloads/dl_show']);
  list.dispose();
});

test('a person with downloads turned off sees that, and a failed read keeps what is shown', async () => {
  let refusal: Error | undefined = Object.assign(new Error('off'), {status: 403, code: 'downloads_not_allowed'});
  const {api} = server(({path}) => refusal ?? (path === '/v1/downloads?scope=profile' ? {items: [film]} : path === '/v1/downloads/usage' ? {copiesBytes: 0} : {...film, items: titles.dl_film}));
  const list = new BrowserDownloads(api, 60_000);
  await list.refresh();
  assert.equal(list.getSnapshot().unavailable, true);
  refusal = undefined;
  await list.refresh();
  assert.equal(list.getSnapshot().rows.length, 1);
  refusal = new TypeError('offline');
  await list.refresh();
  assert.equal(list.getSnapshot().phase, 'ready');
  assert.equal(list.getSnapshot().rows.length, 1);
  list.dispose();
});

test('Save file hands the browser one address for each file of the title, each asking to be saved', async () => {
  const {api} = server(() => ({files: [{url: '/v1/media/g1/file/1', sizeBytes: 1}, {url: '/v1/media/g1/file/2', sizeBytes: 1}]}));
  assert.deepEqual(await browserSaveAddresses(api, 'dl_film', 'itm_film'), ['https://server.test/v1/media/g1/file/1?save=1', 'https://server.test/v1/media/g1/file/2?save=1']);
});

test('from a title\'s page a smaller copy is asked for, waited for and saved, and one the server let go is made again', async () => {
  const states = ['expired', 'waiting', 'ready'];
  const {sent, api} = server(({path, method}) => {
    if (method === 'POST') return {...film, quality: '720p-low'};
    if (method === 'PATCH') return film;
    if (path === '/v1/downloads/dl_film') return {...film, items: [{itemId: 'itm_film', kind: 'movie', title: 'Film', state: states.shift()}]};
    return {files: [{url: '/v1/media/g1/file/1', sizeBytes: 1}]};
  });
  assert.deepEqual(await askAndSave(api, 'itm_film', '720p-low', undefined, 60_000, 1), ['https://server.test/v1/media/g1/file/1?save=1']);
  assert.deepEqual(sent.filter(one => one.method !== 'GET').map(one => [one.method, one.path, one.body]),
    [['POST', '/v1/downloads', {itemId: 'itm_film', quality: '720p-low'}], ['PATCH', '/v1/downloads/dl_film', {retry: true}]]);
});

test('the title\'s download sheet says downloads are off instead of failing, and a second tap is the same save', async () => {
  let off = true;
  const {sent, api} = server(({path, method}) => path.endsWith('/download-options')
    ? off ? Object.assign(new Error('off'), {code: 'downloads_off'}) : {itemId: 'itm_film', kind: 'movie', titles: 1, items: [], default: 'original'}
    : method === 'POST' ? film : path === '/v1/downloads/dl_film' ? {...film, items: titles.dl_film} : {files: [{url: '/v1/media/g1/file/1', sizeBytes: 1}]});
  const handed: string[] = [];
  const port = createAttachmentDownloadPort(api, () => true, new AbortController().signal, href => { handed.push(href); });
  const target = {libraryId: 'lib_1', itemId: 'itm_film'}, signal = new AbortController().signal;
  assert.equal((await port.options(target, signal, () => true)).allowed, false);
  off = false;
  assert.equal((await port.options(target, signal, () => true)).allowed, true);
  await Promise.all([port.save(target, 'original', signal, () => true), port.save(target, 'original', signal, () => true)]);
  assert.equal(sent.filter(one => one.method === 'POST').length, 1);
  assert.deepEqual(handed, ['https://server.test/v1/media/g1/file/1?save=1']);
});
