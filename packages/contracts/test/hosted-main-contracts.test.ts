import test from 'node:test';
import assert from 'node:assert/strict';
import {
  decodeIdentityCurrentResponse,
  decodeIdentityTransactionContactVerifyResponse,
  decodeIdentityTransactionGetResponse,
  decodeIdentityTransactionNativeAppleResponse,
} from '../src/hosted/index.ts';
import {decode} from '../src/hosted/runtime.ts';

test('all four Hosted identity responses preserve the genuine optional verified email', () => {
  const current = {transactionId: 'transaction', status: 'verified', provider: 'email', contactVerified: true, expiresAt: '2026-10-23T23:00:00Z', needsUsername: false, mode: 'sign-in'};
  for (const read of [decodeIdentityCurrentResponse, decodeIdentityTransactionContactVerifyResponse, decodeIdentityTransactionGetResponse, decodeIdentityTransactionNativeAppleResponse]) {
    assert.equal(read({...current, verifiedEmail: 'reader@example.test'}).verifiedEmail, 'reader@example.test');
    assert.equal(Object.hasOwn(read(current), 'verifiedEmail'), false);
    assert.throws(() => read({...current, verifiedEmail: true}));
  }
});

test('Hosted pending requests distinguish normal and owner signed proofs without stripping unknown authority', () => {
  const signed = {payload: 'encoded-claim', signature: 'encoded-signature'};
  assert.deepEqual({...decode('ServerClaimPendingRequest', signed)}, signed);
  const owner = {...signed, ownerStart: {...signed}};
  assert.deepEqual(JSON.parse(JSON.stringify(decode('ServerClaimPendingRequest', owner))), owner);
  for (const value of [{...signed, unknown: true}, {...owner, unknown: true}, {...owner, ownerStart: {...signed, unknown: true}}, {...owner, ownerStart: {payload: 'claim'}}]) {
    assert.throws(() => decode('ServerClaimPendingRequest', value));
  }
});

test('Hosted exchange branches reject hybrid code/state and non-switch owner purposes', () => {
  const common = {transactionId: 'transaction', verifier: 'v'.repeat(43), registrationId: 'registration', installationId: '11111111-1111-4111-8111-111111111111', serverChallenge: 's'.repeat(43), purpose: 'switch'};
  assert.doesNotThrow(() => decode('ServerReturnExchangeRequest', {...common, code: 'c'.repeat(43)}));
  assert.doesNotThrow(() => decode('ServerReturnExchangeRequest', {...common, state: 's'.repeat(43)}));
  for (const value of [{...common, code: 'c'.repeat(43), state: 's'.repeat(43)}, {...common, state: 's'.repeat(43), purpose: 'sign-in'}, {...common, state: 's'.repeat(43), unknown: true}, common]) {
    assert.throws(() => decode('ServerReturnExchangeRequest', value));
  }
});
