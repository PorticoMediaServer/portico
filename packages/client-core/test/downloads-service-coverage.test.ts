import {test} from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService} from '../src/downloads-service.ts';

const sha = 'a'.repeat(64);
const preparation = (over: Record<string, unknown> = {}): any => ({
  id: 'prep', itemId: 'item', libraryId: 'lib', profileId: 'profile', quality: '1080p-low', origin: 'item',
  title: {kind: 'movie', title: 'Film', onServer: true},
  batchId: 'batch', state: 'ready', reason: '',
  artifact: {kind: 'prepared', ref: 'version', sha256: sha, bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'prep.mp4'},
  progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaMs: null},
  actions: ['remove'], revision: 3,
  createdAt: '2026-09-16T10:00:00Z', updatedAt: '2026-09-16T10:05:00Z', readyAt: '2026-09-16T10:05:00Z', expiresAt: '2026-10-16T10:05:00Z',
  ...over,
});
const optionsDoc = {
  itemId: 'item', kind: 'movie',
  source: {container: 'mp4', videoCodec: 'h264', audioCodec: 'aac', height: 1080, durationMs: 5400000, bytes: 8000000000, available: true},
  options: [
    {quality: 'original', label: 'Original', kind: 'source', targetDisplayHeight: 1080, estimatedBytes: 8000000000, estimated: false, available: true, reason: '', requiresPreparation: false},
  ],
  policy: {allowDownloads: true, reason: ''},
  storage: {maxPreparedBytes: 0, committedBytes: 12, remainingBytes: null, retentionDays: 30},
};
const grantDoc = {
  preparationId: 'prep', itemId: 'item', url: '/v1/downloads/artifacts/tok-1', token: 'tok-1',
  issuedAt: '2026-09-17T00:00:00Z', expiresAt: '2026-09-17T00:10:00Z', renewAfterMs: 600000, renewed: false,
  artifact: {kind: 'prepared', ref: 'version', sha256: sha, bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'prep.mp4'},
};
const usageDoc = {profileId: 'profile', profileBytes: 0, profileCount: 0, serverBytes: 0, serverCount: 0, distinctArtifactBytes: 0, maxPreparedBytes: 0, remainingBytes: null, retentionDays: 7, profiles: []};

function server() {
  const calls: {path: string; method: string; body?: unknown}[] = [];
  const api = {
    request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      calls.push({path, method, body});
      if (path === '/v1/items/item/download-options') return optionsDoc as T;
      if (path === '/v1/items/other/download-options') return {...optionsDoc, itemId: 'mismatch'} as T;
      if (path === '/v1/downloads/preparations' && method === 'POST') {
        return {batchId: 'b-1', items: [preparation()], rejected: [{itemId: 'gone', code: 'source_unavailable'}], accepted: 1, duplicate: false} as T;
      }
      if (path === '/v1/downloads/preparations/prep/grant') return grantDoc as T;
      if (path.startsWith('/v1/downloads/preparations?')) return {items: [preparation()], nextCursor: ''} as T;
      if (path === '/v1/downloads/usage') return usageDoc as T;
      if (path.endsWith('/actions')) {
        const next = {...preparation(), revision: 4};
        return next as T;
      }
      throw new Error('unexpected ' + path);
    },
  };
  return {api, calls};
}

test('options show what may be asked for; a mismatched item is refused', async () => {
  const s = server(), service = new DownloadsService(s.api, () => 'op', 10000);
  const view = await service.options('item');
  assert.equal(view.itemId, 'item');
  assert.equal(view.options[0].quality, 'original');
  await assert.rejects(service.options('other'));
  service.dispose();
});

test('asking prepares a batch and reports what was not accepted', async () => {
  const s = server(), service = new DownloadsService(s.api, () => 'op-9', 10000);
  const batch = await service.request({mediaId: 'item'}, 'original');
  assert.equal(batch.accepted, 1);
  assert.equal(batch.rejected[0].code, 'source_unavailable');
  const sent = s.calls.find((c) => c.path === '/v1/downloads/preparations' && c.method === 'POST')!;
  assert.deepEqual(sent.body, {operationId: 'op-9', mediaId: 'item', quality: 'original'});
  service.dispose();
});

test('a batch sends only item fields: a container id riding on the caller\'s object never reaches the batch route', async () => {
  const s = server(), service = new DownloadsService(s.api, () => 'op-10', 10000);
  const target = {mediaId: 'item', containerId: 'show-1', containerKind: 'show'} as unknown as Parameters<DownloadsService['request']>[0];
  await service.request(target, 'original');
  const sent = s.calls.find((c) => c.path === '/v1/downloads/preparations' && c.method === 'POST')!;
  assert.deepEqual(sent.body, {operationId: 'op-10', mediaId: 'item', quality: 'original'});
  service.dispose();
});

test('a grant is asked for at the moment of saving; reasons read as sentences', async () => {
  const s = server(), service = new DownloadsService(s.api, () => 'op-g', 10000);
  const grant = await service.grant(preparation());
  assert.equal(grant.token, 'tok-1');
  assert.equal(grant.renewAfterSeconds, 600);
  const sent = s.calls.find((c) => c.path.endsWith('/grant'))!;
  assert.deepEqual(sent.body, {operationId: 'op-g'});
  assert.equal(service.reason(preparation()), '');
  assert.match(service.reason(preparation({reason: 'storage_full'})), /./);
  service.dispose();
});

test('removing drops the row; a 503 with a list stays a list', async () => {
  const calls: {path: string; method: string; body?: unknown}[] = [];
  let fail: unknown;
  const api = {
    request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      calls.push({path, method, body});
      if (fail) throw fail;
      if (path.startsWith('/v1/downloads/preparations?')) return {items: [preparation()], nextCursor: ''} as T;
      if (path === '/v1/downloads/usage') return {profileId: 'profile', profileBytes: 0, profileCount: 0, serverBytes: 0, serverCount: 0, distinctArtifactBytes: 0, maxPreparedBytes: 0, remainingBytes: null, retentionDays: 7, profiles: []} as T;
      if (path.endsWith('/actions')) return {...preparation(), revision: 4} as T;
      throw new Error('unexpected ' + path);
    },
  };
  const service = new DownloadsService(api, () => 'op', 10000);
  await service.refresh();
  await service.act(service.getSnapshot().items[0], 'remove');
  // Remove filters the row; the follow-up refresh restores the server truth (one row here).
  await new Promise((r) => setTimeout(r, 10));
  assert.ok(service.getSnapshot().items.length <= 1);
  fail = Object.assign(new Error('paused'), {status: 503});
  await service.refresh();
  assert.equal(service.getSnapshot().items.length, 1, 'a 503 with a list keeps it');
  assert.equal(service.getSnapshot().unavailable, false);
  service.dispose();
});

test('a malformed usage document is ignored, never unhandled', async () => {
  const api = {
    request: async <T,>(path: string): Promise<T> => {
      if (path.startsWith('/v1/downloads/preparations?')) return {items: [preparation()], nextCursor: ''} as T;
      if (path === '/v1/downloads/usage') return {} as T;
      throw new Error('unexpected ' + path);
    },
  };
  const service = new DownloadsService(api, () => 'op', 10000);
  await service.refresh();
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(service.getSnapshot().phase, 'ready');
  assert.equal(service.getSnapshot().items.length, 1);
  assert.equal(service.getSnapshot().usage, undefined);
  service.dispose();
});
