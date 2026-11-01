import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateReference } from './sync-api.mjs';
const operation = (id, audience, extra = {}) => ({ operationId: id, tags: [audience], 'x-audience': audience, 'x-scope': 'read', 'x-access': 'device', responses: {}, ...extra });
const spec = { info: {}, paths: {
  '/v1/new-tool-route': { get: operation('new', 'Integrations') },
  '/v1/new-player-route': { post: operation('player', 'Clients') },
  '/Items': { get: operation('alias', 'Integrations', { 'x-protocol': 'integration-compatibility' }) }
} };
test('new routes enter the full reference; only native tagged operations enter Integrations', () => {
  const result = generateReference(spec, { serverCommit: 'pin' });
  assert.equal(result.operations.length, 3);
  assert.deepEqual(result.operations.filter((o) => o.audience === 'Integrations' && o.protocol === 'native').map((o) => o.operationId), ['new']);
  assert.equal(result.operations.find((o) => o.operationId === 'alias').group, 'compatibility');
  assert.equal(result.operations.find((o) => o.operationId === 'player').scope, 'read');
  assert.deepEqual(generateReference(spec, { serverCommit: 'pin' }), result);
});
test('missing, contradictory and duplicate classifications fail instead of silently hiding routes', () => {
  for (const extra of [{ tags: [] }, { 'x-audience': 'Clients' }, { tags: ['Integrations', 'Clients'] }, { 'x-protocol': 'unknown' }]) {
    assert.throws(() => generateReference({ paths: { '/v1/new': { get: operation('new', 'Integrations', extra) } } }, {}));
  }
  assert.throws(() => generateReference({ paths: { '/a': { get: operation('same', 'Clients') }, '/b': { get: operation('same', 'Clients') } } }, {}));
});
