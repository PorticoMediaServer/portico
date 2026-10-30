import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';

const nonce = Buffer.alloc(32, 1).toString('base64url');
const installationId = '11111111-1111-4111-8111-111111111111';

test('the generated owner-start action matches the strict three-field request decoder', () => {
  const request = {expected: {operationId: 'operation', localGeneration: '1', revision: '1'}, ownerStart: {state: nonce, codeChallenge: nonce, installationId}};
  assert.equal((decode('PostNetworkingClaimWebApprovalRequest', request) as typeof request).ownerStart.installationId, installationId);
  for (const value of [
    {...request, ownerStart: null}, {...request, unknown: true},
    {...request, expected: {...request.expected, unknown: true}},
    {...request, ownerStart: {...request.ownerStart, unknown: true}},
    {...request, ownerStart: {...request.ownerStart, state: 'a'.repeat(43)}},
    {...request, ownerStart: {...request.ownerStart, installationId: 'short'}},
  ]) assert.throws(() => decode('PostNetworkingClaimWebApprovalRequest', value));
});

test('the generated extended return request retains separate normal-code and owner-state branches', () => {
  const common = {transactionId: 'transaction', verifier: nonce, registrationId: 'registration', installationId, serverChallenge: nonce};
  const normal = {...common, purpose: 'sign-in', code: nonce};
  const owner = {...common, purpose: 'switch', state: nonce};
  assert.doesNotThrow(() => decode('PostAuthPorticoReturnExchangeRequest', normal));
  assert.doesNotThrow(() => decode('PostAuthPorticoReturnExchangeRequest', owner));
  for (const value of [{...normal, state: nonce}, {...owner, code: nonce}, {...owner, purpose: 'sign-in'}, {...normal, requestId: null}, {...owner, unknown: true}]) {
    assert.throws(() => decode('PostAuthPorticoReturnExchangeRequest', value));
  }
});
