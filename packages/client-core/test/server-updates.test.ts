import test from 'node:test';
import assert from 'node:assert/strict';
import {readServerUpdates, updatePillShown} from '../src/server-admin/server-updates.ts';

test('the Update pill reads the rebuilt server\'s answer and is on only when a newer build is out', async () => {
  const paths: string[] = [];
  const api = {request: async <T,>(path: string, method = 'GET'): Promise<T> => {paths.push(`${method} ${path}`); return {version: '3.0.0', newer: true, install: 'docker'} as T;}};
  assert.equal(updatePillShown(await readServerUpdates(api)), true);
  assert.deepEqual(paths, ['GET /v1/admin/updates']);
  assert.equal(updatePillShown({version: '3.0.0', newer: false, install: 'docker'}), false);
  // The old server's report, or anything else this app cannot read, leaves the pill off rather than failing.
  for (const other of [{channel: 'stable', updateAvailable: true}, null, 'newer', {newer: 'yes'}]) assert.equal(updatePillShown(other), false);
});
