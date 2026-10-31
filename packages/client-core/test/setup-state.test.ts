import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSetupState, readSetupState} from '../src/setup-state.ts';

test('the coarse status alone, as anyone reads it', () => {
  assert.deepEqual(parseSetupState({status: 'ready'}), {status: 'ready'});
  assert.deepEqual(parseSetupState({status: 'in_progress', retryAfterSeconds: 1700}), {status: 'in_progress', retryAfterSeconds: 1700});
  assert.deepEqual(parseSetupState({status: 'complete'}), {status: 'complete'});
});

test('the caller\'s own setup, with its window and operation', () => {
  const state = parseSetupState({status: 'in_progress', setup: {variant: 'awaiting_approval', requestId: '44444444-4444-4444-8444-444444444444', revision: '1',
    expiresAt: '2026-10-31T02:40:47Z', operation: {operationId: 'op_setup', localGeneration: '0', revision: '2'}}});
  assert.deepEqual(state.setup, {phase: 'awaiting_approval', requestId: '44444444-4444-4444-8444-444444444444', revision: '1', expiresAt: '2026-10-31T02:40:47Z',
    operation: {operationId: 'op_setup', localGeneration: '0', revision: '2'}, cancelling: false, renewable: false});
  assert.equal(Object.isFrozen(state), true);
  const lapsed = parseSetupState({status: 'in_progress', setup: {variant: 'installed_lapsed', requestId: '44444444-4444-4444-8444-444444444444', revision: '1', renewable: true}});
  assert.equal(typeof lapsed.setup === 'object' && lapsed.setup.renewable, true);
});

test('delivery facts while the server applies an approval', () => {
  const state = parseSetupState({status: 'in_progress', setup: {variant: 'exhausted', requestId: '44444444-4444-4444-8444-444444444444', revision: '1',
    delivery: {attempts: 3, slow: true, since: '2026-10-31T01:00:00Z'}}});
  assert.deepEqual(typeof state.setup === 'object' && state.setup.delivery, {attempts: 3, slow: true, since: '2026-10-31T01:00:00Z'});
});

test('a phase this app doesn\'t know is unknown, never a guess', () => {
  const state = parseSetupState({status: 'in_progress', setup: {variant: 'claimed_by_token', requestId: 'x', revision: '1'}});
  assert.equal(state.setup, 'unknown');
});

test('a status this app doesn\'t know is unknown, never a throw', () => {
  assert.deepEqual(parseSetupState({status: 'paused', retryAfterSeconds: 5}), {status: 'unknown', retryAfterSeconds: 5});
});

test('a document that doesn\'t match the contract is refused', () => {
  assert.throws(() => parseSetupState({}));
  assert.throws(() => parseSetupState({status: 'in_progress', setup: {variant: 'started'}}));
});

test('reads GET /v1/setup/state through the caller\'s API', async () => {
  let asked = '';
  const state = await readSetupState({request: async <T>(path: string, method?: string) => {asked = `${method} ${path}`; return {status: 'ready'} as T;}});
  assert.equal(asked, 'GET /v1/setup/state');
  assert.equal(state.status, 'ready');
});

test('a setup started again after its Portico Account declined says so', () => {
  const state = parseSetupState({status: 'in_progress', setup: {variant: 'started', requestId: '44444444-4444-4444-8444-444444444444', revision: '1', outcome: 'denied'}});
  assert.equal(typeof state.setup === 'object' && state.setup.outcome, 'denied');
  const plain = parseSetupState({status: 'in_progress', setup: {variant: 'started', requestId: '44444444-4444-4444-8444-444444444444', revision: '1'}});
  assert.equal(typeof plain.setup === 'object' && plain.setup.outcome, undefined);
});
