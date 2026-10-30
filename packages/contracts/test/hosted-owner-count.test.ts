import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/hosted/runtime.ts';
import {decodeServerClaimLookupResponse, decodeServerClaimPendingResponse} from '../src/hosted/decoders.ts';

const pending = {purpose: 'switch', code: 'claim_fixture', serverId: 'server_fixture', name: 'Server', address: 'https://server.test',
  requestedAt: '2030-01-02T03:04:05Z', expiresAt: '2030-01-02T03:05:05Z', status: 'pending', ownerTransactionId: 'transaction_fixture'};

test('mirrored count is bounded and lookup-only without changing registration projection', () => {
  assert.equal('otherPeopleCount' in decodeServerClaimLookupResponse(pending), false);
  for (const count of [0, 10000]) assert.equal(decodeServerClaimLookupResponse({...pending, otherPeopleCount: count}).otherPeopleCount, count);
  for (const count of [null, -1, 10001, 0.5, '0']) assert.throws(() => decodeServerClaimLookupResponse({...pending, otherPeopleCount: count}));
  assert.equal('otherPeopleCount' in decodeServerClaimPendingResponse({...pending, otherPeopleCount: 0}), false);
});

test('mirrored optional count stays on the complete owner request branch', () => {
  const proof = {payload: 'fixture', signature: 'fixture'};
  assert.doesNotThrow(() => decode('ServerClaimPendingRequest', {...proof, ownerStart: proof, ownerSwitchCount: proof}));
  for (const body of [{...proof, ownerSwitchCount: proof}, {...proof, ownerStart: proof, ownerSwitchCount: null},
    {...proof, ownerStart: proof, ownerSwitchCount: {payload: 'fixture'}}]) assert.throws(() => decode('ServerClaimPendingRequest', body));
});
