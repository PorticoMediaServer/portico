import test from 'node:test';
import assert from 'node:assert/strict';
import {createAttachmentDownloadPort} from '../src/attachment-download.ts';

const artifact = {kind: 'prepared', ref: 'version', sha256: 'a'.repeat(64), bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'film.mp4'};
const preparation = {id: 'prep', itemId: 'item', libraryId: 'library', profileId: 'profile', quality: 'original', origin: 'item', batchId: 'batch',
  title: {kind: 'movie', title: 'Film', onServer: true}, state: 'ready', reason: '', artifact,
  progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaMs: null}, actions: ['remove'], revision: 1,
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z', readyAt: '2026-01-01T00:00:00Z', expiresAt: '2030-01-01T00:00:00Z'};
function harness() {
  const calls: {path: string; body?: unknown; signal?: AbortSignal}[] = [], handed: string[] = [];
  let failGrant = true, current = true;
  const port = createAttachmentDownloadPort({mediaUrl: path => 'https://server.invalid' + path, request: async <T>(path: string, _method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    calls.push({path, body, signal});
    if (path === '/v1/items/item') return {id: 'item', libraryId: 'library', title: 'Film', kind: 'movie', available: true, durationMs: 1000, progressMs: 0} as T;
    if (path.endsWith('/download-options')) return {itemId: 'item', kind: 'movie', source: {container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', height: 1080, durationMs: 1000, bytes: 4096, available: true},
      options: [{quality: 'original', label: 'Original', kind: 'source', targetDisplayHeight: 1080, estimatedBytes: 4096, estimated: false, available: true, reason: '', requiresPreparation: false}],
      policy: {allowDownloads: true, reason: ''}, storage: {maxPreparedBytes: 0, committedBytes: 0, remainingBytes: null, retentionDays: 30}} as T;
    if (path === '/v1/downloads/preparations') return {batchId: 'batch', accepted: 1, items: [preparation], rejected: [], duplicate: false} as T;
    if (path.endsWith('/grant')) {
      if (failGrant) {failGrant = false; throw new Error('ambiguous');}
      return {preparationId: 'prep', itemId: 'item', token: 'opaque', url: '/v1/downloads/artifacts/opaque', issuedAt: '2026-01-01T00:00:00Z', expiresAt: '2099-01-01T00:00:00Z', renewAfterMs: 60000, renewed: false, artifact} as T;
    }
    throw new Error('unexpected request');
  }}, () => current, new AbortController().signal, (href, filename, check) => {check(); handed.push(href, filename);});
  return {port, calls, handed, retire: () => {current = false;}};
}
test('attachment retries keep the original preparation and grant intent, with no admission or policy fanout', async () => {
  const h = harness(), target = {itemId: 'item', libraryId: 'library'}, signal = new AbortController().signal;
  await assert.rejects(h.port.save(target, 'original', signal, () => true), /ambiguous/);
  await h.port.save(target, 'original', signal, () => true);
  assert.deepEqual(h.calls.map(c => c.path), ['/v1/items/item', '/v1/items/item/download-options', '/v1/downloads/preparations', '/v1/downloads/preparations/prep/grant', '/v1/downloads/preparations/prep/grant']);
  assert.deepEqual(h.calls[3]?.body, h.calls[4]?.body);
  assert.deepEqual(h.handed, ['https://server.invalid/v1/downloads/artifacts/opaque', 'film.mp4']);
  assert.ok(h.calls.every(call => call.signal instanceof AbortSignal));
});
test('a retired attachment owner cannot retry a captured intent or hand off its capability', async () => {
  const h = harness(), target = {itemId: 'item', libraryId: 'library'}, signal = new AbortController().signal;
  await assert.rejects(h.port.save(target, 'original', signal, () => true), /ambiguous/);
  h.retire();
  await assert.rejects(h.port.save(target, 'original', signal, () => true), {name: 'AbortError'});
  assert.equal(h.calls.length, 4); assert.deepEqual(h.handed, []);
});
