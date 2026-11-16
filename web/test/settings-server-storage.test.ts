import test from 'node:test';
import assert from 'node:assert/strict';
import {storageUsageLines} from '../../packages/client-core/src/server-admin/server-storage.ts';
import {SettingsStorageSource} from '../src/screens/settings/server/storage-source.ts';

const storage = {dataFolder: '/srv/portico', sharedDrive: false, diskBytes: 1000, freeBytes: 400, level: 'ok', measuredAt: '2026-11-13T00:00:00Z',
  folders: [{name: 'database', bytes: 50}, {name: 'artwork', bytes: 30}, {name: 'conversions', bytes: 20}, {name: 'logs', bytes: 0}, {name: 'other-files', bytes: 5}],
  conversions: [{folder: '/srv/portico/transcodes', bytes: 20, external: false}, {folder: '/mnt/fast/convert', bytes: 70, external: true}],
  recordings: [{folder: '/mnt/dvr', bytes: 0, external: true, problem: 'The folder cannot be read.'}]};

test('what uses space lists the data folder\'s parts, then places outside it, never counting one twice (#3208)', () => {
  const lines = storageUsageLines(storage as never);
  assert.deepEqual(lines.map(line => line.id), ['part:database', 'part:artwork', 'part:conversions', 'part:other-files', 'conversions:/mnt/fast/convert', 'recordings:/mnt/dvr']);
  const outside = lines.find(line => line.id === 'conversions:/mnt/fast/convert')!;
  assert.equal(outside.bytes, 70); assert.equal(outside.external, true);
  // A place the server could not measure has no size, and says why.
  const dvr = lines.find(line => line.kind === 'recordings')!;
  assert.equal(dvr.bytes, undefined); assert.equal(dvr.problem, 'The folder cannot be read.');
});

// The server may still send a prepared-downloads time (before Builder R's removal) or not at all (after): both read the
// same, and only the preset is ever sent (owner, 16 Nov: a download copy is kept only until the device has the file).
test('one load serves every Storage row, and a keeping change sends only the preset and takes the server\'s answer', async () => {
  const paths: string[] = [], sent: unknown[] = [];
  let keeping: Record<string, unknown> = {preset: 'balanced', preparedDownloadsDays: 30};
  const api = {request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    paths.push(`${method ?? 'GET'} ${path}`);
    if (path === '/v1/admin/storage') return storage as T;
    if (method === 'PUT') {sent.push(body); keeping = {preset: (body as {preset: string}).preset};}
    return keeping as T;
  }};
  const source = new SettingsStorageSource(api, () => true);
  await Promise.all([source.load(), source.load()]);
  assert.deepEqual(paths, ['GET /v1/admin/storage', 'GET /v1/admin/storage/keeping']);
  assert.deepEqual(source.getSnapshot().keeping, {preset: 'balanced'});
  await source.saveKeeping({preset: 'long'});
  assert.deepEqual(sent, [{preset: 'long'}]);
  assert.deepEqual(source.getSnapshot().keeping, {preset: 'long'});
  source.dispose();
});
