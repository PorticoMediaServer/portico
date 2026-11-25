import test from 'node:test';
import assert from 'node:assert/strict';
import {createAttachmentDownloadPort} from '../src/attachment-download.ts';

const item = {id: 'item', libraryId: 'library', title: 'Film', kind: 'movie', images: {}, available: true, durationMs: 1000, progressMs: 0};
const download = {id: 'download', target: {id: item.id, kind: item.kind, title: item.title}, quality: 'original'};
function harness(failCreate = false) {
  const calls: {path: string; body?: unknown; signal?: AbortSignal}[] = [], handed: string[] = [];
  let failGrant = true, current = true;
  const port = createAttachmentDownloadPort({mediaUrl: path => 'https://server.invalid' + path, request: async <T>(path: string, _method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, body, signal});
    if (path === '/v1/items/item') return item as T;
    if (path.endsWith('/download-options')) return {itemId: 'item', kind: 'movie', titles: 1, default: 'ask',
      items: [{quality: 'original', label: 'Original', available: true, sizeBytes: 4096, estimated: false, prepared: false}]} as T;
    if (path === '/v1/downloads') {
      if (failCreate) {failCreate = false; throw new Error('lost create response');}
      return download as T;
    }
    if (path === '/v1/downloads/download') return {...download, items: [{itemId: 'item', state: 'ready'}]} as T;
    if (path === '/v1/downloads/download/items/item') {
      if (failGrant) {failGrant = false; throw new Error('ambiguous');}
      return {item, quality: download.quality, files: [{url: '/v1/media/opaque/file/1', sizeBytes: 4096, container: 'mp4'}], subtitles: []} as T;
    }
    throw new Error('unexpected request');
  }}, () => current, new AbortController().signal, (href, filename, check) => {check(); handed.push(href, filename);});
  return {port, calls, handed, retire: () => {current = false;}};
}
test('Save retries use one download intent and hand off the current attachment capability', async () => {
  const h = harness(true), target = {itemId: 'item', libraryId: 'library'}, signal = new AbortController().signal;
  await assert.rejects(h.port.save(target, 'original', signal, () => true), /lost create response/);
  await assert.rejects(h.port.save(target, 'original', signal, () => true), /ambiguous/);
  await h.port.save(target, 'original', signal, () => true);
  assert.deepEqual(h.calls.map(c => c.path), ['/v1/items/item', '/v1/items/item/download-options', '/v1/downloads', '/v1/downloads',
    '/v1/downloads/download', '/v1/downloads/download/items/item', '/v1/downloads/download', '/v1/downloads/download/items/item']);
  assert.deepEqual(h.calls[2]?.body, h.calls[3]?.body);
  assert.deepEqual(h.handed, ['https://server.invalid/v1/media/opaque/file/1?save=1', 'Film.mp4']);
  assert.ok(h.calls.every(call => call.signal instanceof AbortSignal));
});
test('a retired attachment owner cannot retry a captured intent or hand off its capability', async () => {
  const h = harness(), target = {itemId: 'item', libraryId: 'library'}, signal = new AbortController().signal;
  await assert.rejects(h.port.save(target, 'original', signal, () => true), /ambiguous/);
  h.retire();
  await assert.rejects(h.port.save(target, 'original', signal, () => true), {name: 'AbortError'});
  assert.equal(h.calls.length, 5); assert.deepEqual(h.handed, []);
});
test('a terminal download failure clears perTarget tracking so retry creates a fresh download', async () => {
  let attempt = 0;
  const handed: string[] = [];
  const bodies: unknown[] = [];
  const port = createAttachmentDownloadPort({mediaUrl: path => 'https://server.invalid' + path, request: async <T>(path: string, _method?: string, body?: unknown): Promise<T> => {
    if (path === '/v1/items/item') return item as T;
    if (path.endsWith('/download-options')) return {itemId: 'item', kind: 'movie', titles: 1, default: 'ask',
      items: [{quality: 'original', label: 'Original', available: true, sizeBytes: 4096, estimated: false, prepared: false}]} as T;
    if (path === '/v1/downloads') {
      attempt++;
      bodies.push(body);
      return {...download, id: `download-${attempt}`} as T;
    }
    if (path === '/v1/downloads/download-1') return {...download, id: 'download-1', items: [{itemId: 'item', state: 'failed'}]} as T;
    if (path === '/v1/downloads/download-2') return {...download, id: 'download-2', items: [{itemId: 'item', state: 'ready'}]} as T;
    if (path === '/v1/downloads/download-2/items/item') return {item, quality: download.quality, files: [{url: '/v1/media/opaque/file/2', sizeBytes: 4096, container: 'mp4'}], subtitles: []} as T;
    throw new Error('unexpected request: ' + path);
  }}, () => true, new AbortController().signal, (href, filename, check) => {check(); handed.push(href, filename);});
  const target = {itemId: 'item', libraryId: 'library'}, signal = new AbortController().signal;
  await assert.rejects(port.save(target, 'original', signal, () => true), /download-not-ready/);
  await port.save(target, 'original', signal, () => true);
  assert.equal(attempt, 2, 'retry after terminal failure submits a fresh download');
  assert.notDeepEqual(bodies[0], bodies[1], 'fresh retry uses a new operationId');
  assert.deepEqual(handed, ['https://server.invalid/v1/media/opaque/file/2?save=1', 'Film.mp4']);
});
