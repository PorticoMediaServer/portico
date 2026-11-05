import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsStorageSource} from '../src/screens/settings/server/storage-source.ts';
import {LiveSourceController} from '../src/screens/live/owner/source-controller.ts';

function deferred<T>() {let resolve!: (value: T) => void;let reject!: (error: unknown) => void;const promise = new Promise<T>((yes, no) => {resolve = yes;reject = no;});return {promise, resolve, reject};}

test('storage waits for the original pair before parsing or reporting either successful document', async () => {
  const report = deferred<unknown>(), usage = deferred<unknown>();let reads = 0, reports = 0, settled = false;
  const malformed = {get result() {reads++;throw new Error('parsed');}};
  const source = new SettingsStorageSource({request: <T>(path: string) => (path.endsWith('storage-usage') ? report.promise : usage.promise) as Promise<T>}, 'server', () => true, () => {reports++;});
  const pending = source.load().then(() => {settled = true;});await Promise.resolve();
  report.resolve(malformed);await Promise.resolve();await Promise.resolve();
  assert.equal(settled, false);assert.equal(reads, 0);assert.equal(reports, 0);
  source.dispose();usage.resolve(malformed);await pending;
  assert.equal(reads, 0);assert.equal(reports, 0);
});

test('retired live-source read guards before touching a malformed successful response', async () => {
  const answer = deferred<unknown>();let reads = 0, current = true, reports = 0;
  const source = new LiveSourceController({request: <T>() => answer.promise as Promise<T>}, 'server', () => current, () => {reports++;});
  const pending = source.load();current = false;
  answer.resolve({get protocolVersion() {reads++;throw new Error('parsed');}, get serverId() {reads++;throw new Error('parsed');}});
  await pending;assert.equal(reads, 0);assert.equal(reports, 0);assert.equal(source.getSnapshot().document, undefined);source.dispose();
});
