import test from 'node:test';
import assert from 'node:assert/strict';
import {checkServerUpdates, latestVersionLine, readServerUpdates, updatePillShown} from '../src/server-admin/server-updates.ts';

test('the Update pill reads the rebuilt server\'s answer and is on only when a newer build is out', async () => {
  const paths: string[] = [];
  const api = {request: async <T,>(path: string, method = 'GET'): Promise<T> => {paths.push(`${method} ${path}`); return {version: '3.0.0', newer: true, install: 'docker'} as T;}};
  assert.equal(updatePillShown(await readServerUpdates(api)), true);
  assert.deepEqual(paths, ['GET /v1/admin/updates']);
  assert.equal(updatePillShown({version: '3.0.0', newer: false, install: 'docker'}), false);
  // The old server's report, or anything else this app cannot read, leaves the pill off rather than failing.
  for (const other of [{channel: 'stable', updateAvailable: true}, null, 'newer', {newer: 'yes'}]) assert.equal(updatePillShown(other), false);
});

test('the Latest version line says the server\'s own words for a failed check, the newer build when one is out, else up to date', async () => {
  const t = (id: string, values?: Record<string, string>) => values ? `${id}:${values.version}` : id;
  assert.equal(latestVersionLine({version: '3.0.0', newer: false, install: 'docker', error: 'The release feed did not answer.'}, t), 'The release feed did not answer.');
  assert.equal(latestVersionLine({version: '3.0.0', newer: true, install: 'docker', newest: {version: '3.1.0'}}, t), 'next.dashboard.updateAvailable:3.1.0');
  assert.equal(latestVersionLine({version: '3.0.0', newer: true, install: 'docker'}, t), 'web.maintenance.updateAvailable');
  assert.equal(latestVersionLine({version: '3.0.0', newer: false, install: 'docker'}, t), '');
  assert.equal(latestVersionLine({version: '3.0.0', newer: false, install: 'docker', checkedAt: '2026-11-13T15:00:00Z'}, t), 'web.maintenance.upToDate');
  // Check now asks the server once, on its own route.
  const sent: string[] = [];
  const api = {request: async <T,>(path: string, method = 'GET'): Promise<T> => {sent.push(`${method} ${path}`); return {version: '3.0.0', newer: false, install: 'docker'} as T;}};
  await checkServerUpdates(api);
  assert.deepEqual(sent, ['POST /v1/admin/updates/check']);
});
