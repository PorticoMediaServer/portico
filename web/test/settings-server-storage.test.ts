import test from 'node:test';
import assert from 'node:assert/strict';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {SettingsStorageSource, storageReport} from '../src/screens/settings/server/storage-source.ts';

const usage = (overrides: Record<string, unknown> = {}) => ({protocolVersion: '1', serverId: 'server', result: {stateDirectory: '/data', totalBytes: 300, totalBytesText: '300 B', observedAt: '2026-10-24T12:00:00Z', truncated: false, artworkEncoding: [],
  categories: [{id: 'artwork', name: 'Artwork', description: 'Posters the server fetched.', directories: ['artwork'], paths: ['/data/artwork'], retentionKey: '', bytes: 100, bytesText: '100 B',
    fileCount: 2, present: true, oldestDays: 3, cleanable: false, regenerable: true}], ...overrides}});
const layout = (overrides: Record<string, unknown> = {}) => ({dataDirectory: '/data', transcodeDirectory: '/data/transcode', transcodeBytes: 40, folders: [{name: 'Database', bytes: 100}], notices: [], ...overrides});

test('the generated decoder supplies the shared total, the one folder and each present category', () => {
  const report = storageReport(usage(), 'server');
  assert.equal(report.stateDirectory, '/data'); assert.equal(report.totalBytes, 300); assert.equal(report.totalBytesText, '300 B');
  assert.equal(report.categories[0].name, 'Artwork'); assert.equal(report.categories[0].regenerable, true); assert.equal(report.truncated, false);
  assert.ok(Object.isFrozen(report) && Object.isFrozen(report.categories) && Object.isFrozen(report.categories[0]));
});

test('another server, a missing field or an impossible size never becomes a report', () => {
  assert.throws(() => storageReport(usage(), 'other'));
  assert.throws(() => storageReport({protocolVersion: '1', serverId: 'server'}, 'server'));
  assert.throws(() => storageReport(usage({totalBytes: '300'}), 'server'));
  assert.throws(() => storageReport(usage({categories: [{id: 'artwork'}]}), 'server'));
});

test('one shared sweep serves both rows, and a ready report is not read again', async () => {
  const reads = new Map<string, number>();
  const source = new SettingsStorageSource({request: async path => {
    reads.set(path, (reads.get(path) ?? 0) + 1);
    return path === routePath('get_admin_storage_usage') ? usage() : layout();
  }}, 'server', () => true);
  await Promise.all([source.load(), source.load()]);
  const snapshot = source.getSnapshot();
  assert.equal(snapshot.phase, 'ready');
  // Both drawn rows come from one sweep, and each document is read exactly once.
  assert.equal(snapshot.report?.stateDirectory, '/data');
  assert.equal(snapshot.usage?.dataDirectory, '/data');
  assert.deepEqual([...reads], [[routePath('get_admin_storage_usage'), 1], [routePath('get_data_storage_usage'), 1]]);
  await source.load();
  assert.deepEqual([...reads], [[routePath('get_admin_storage_usage'), 1], [routePath('get_data_storage_usage'), 1]]);
  source.dispose();
});

test('a failed read stays a failure with a real retry, and a retired scope never reads', async () => {
  const reads = new Map<string, number>();
  let fail = true;
  const request = async (path: string) => {
    reads.set(path, (reads.get(path) ?? 0) + 1);
    if (fail) throw new Error('down');
    return path === routePath('get_admin_storage_usage') ? usage() : layout();
  };
  const source = new SettingsStorageSource({request}, 'server', () => true);
  await source.load();
  assert.deepEqual(source.getSnapshot(), {phase: 'error', error: true});
  fail = false;
  await source.retry();
  assert.equal(source.getSnapshot().phase, 'ready');
  assert.equal(source.getSnapshot().usage?.dataDirectory, '/data');
  assert.deepEqual([...reads.values()], [2, 2]);
  source.dispose();
  await source.load();
  assert.deepEqual([...reads.values()], [2, 2]);
  let current = true;
  const fenced = new SettingsStorageSource({request}, 'server', () => current);
  current = false;
  await fenced.load();
  assert.deepEqual([...reads.values()], [2, 2]);
  assert.deepEqual(fenced.getSnapshot(), {phase: 'idle'});
});
