import test from 'node:test';
import assert from 'node:assert/strict';
import {IntegrationSwitchesSource} from '../src/server-admin/integration-switches.ts';

test('the Integrations switches read what the server keeps, save one at a time, and keep the old value when a save fails', async () => {
  let kept = {assistants: false, compatibleRoutes: false}, fail = false;
  const sent: unknown[] = [];
  const api = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    assert.equal(path, '/v1/admin/settings/integrations');
    if (method === 'PUT') {sent.push(body); if (fail) throw Object.assign(new Error('refused'), {status: 403, code: 'not_permitted'}); kept = {...kept, ...(body as object)};}
    return kept as T;
  }};
  const source = new IntegrationSwitchesSource(api, () => true);
  await source.load();
  assert.deepEqual(source.getSnapshot().value, {assistants: false, compatibleRoutes: false});
  const saving = source.set('compatibleRoutes', true);
  assert.equal(source.getSnapshot().saving, 'compatibleRoutes');
  assert.equal(await source.set('assistants', true), false, 'one change at a time');
  assert.equal(await saving, true);
  assert.deepEqual(sent, [{compatibleRoutes: true}], 'a save names only the switch that changed');
  assert.deepEqual(source.getSnapshot().value, {assistants: false, compatibleRoutes: true});
  fail = true;
  assert.equal(await source.set('compatibleRoutes', false), false);
  assert.deepEqual([source.getSnapshot().value?.compatibleRoutes, (source.getSnapshot().error as {code?: string}).code], [true, 'not_permitted']);
  source.dispose();
});
