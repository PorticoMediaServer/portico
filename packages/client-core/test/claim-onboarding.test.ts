import test from 'node:test';
import assert from 'node:assert/strict';
import {parseClaimStatus, type ClaimStatus} from '../src/claim-onboarding.ts';

const pin = {serverId: 'server-1', publicKey: 'pubkey', fingerprint: 'fp'};
function status(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    state: 'waiting', identity: {serverId: 'server-1', localGeneration: '7'},
    installationAcknowledged: true, approvalRequired: false, actions: ['prepare', 'cancel'], ...over,
  };
}

test('a well-formed status passes through untouched', () => {
  const s = parseClaimStatus(status(), pin);
  assert.equal(s.state, 'waiting');
  assert.equal(s.identity.localGeneration, '7');
  const withOp = parseClaimStatus(status({
    operation: {operationId: 'op-1', localGeneration: '7', revision: '3'},
    actions: ['continue', 'cancel'],
  }), pin);
  assert.equal((withOp as ClaimStatus).operation?.operationId, 'op-1');
});

test('identity, generation counter and action vocabulary are fenced', () => {
  assert.throws(() => parseClaimStatus(status({identity: {serverId: 'other', localGeneration: '7'}}), pin));
  assert.throws(() => parseClaimStatus(status({identity: {serverId: 'server-1', localGeneration: '-1'}}), pin));
  assert.throws(() => parseClaimStatus(status({identity: {serverId: 'server-1', localGeneration: '99999999999999999999'}}), pin));
  assert.throws(() => parseClaimStatus(status({actions: ['launch']}), pin));
  assert.throws(() => parseClaimStatus(status({actions: 'prepare'}), pin));
  assert.throws(() => parseClaimStatus(null, pin));
  assert.throws(() => parseClaimStatus({state: 'waiting'}, pin));
});

test('operation and approval requests must belong to this server generation', () => {
  assert.throws(() => parseClaimStatus(status({operation: {operationId: 'x'.repeat(129), localGeneration: '7', revision: '1'}}), pin));
  const op = {operationId: 'op-1', localGeneration: '7', revision: '3'};
  const approval = {operationId: 'op-1', serverId: 'server-1', publicKey: 'pubkey', localGeneration: '7', name: 'TV', expectedRevision: '2'};
  const ok = parseClaimStatus(status({operation: op, approvalRequired: true, actions: ['approve', 'cancel'], approvalRequest: approval}), pin);
  assert.equal(ok.approvalRequest?.name, 'TV');
  assert.throws(() => parseClaimStatus(status({operation: op, approvalRequired: true, actions: ['approve'], approvalRequest: {...approval, publicKey: 'other'}}), pin));
  assert.throws(() => parseClaimStatus(status({operation: op, approvalRequired: true, actions: ['approve'], approvalRequest: {...approval, operationId: 'op-2'}}), pin));
  assert.throws(() => parseClaimStatus(status({operation: op, approvalRequired: true, actions: ['approve'], approvalRequest: {...approval, localGeneration: '8'}}), pin));
});
