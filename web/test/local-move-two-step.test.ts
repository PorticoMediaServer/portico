import test from 'node:test';
import assert from 'node:assert/strict';
import {discardTwoStepSetup, hasTwoStepSetup, requestTwoStepSetup, takeTwoStepSetup} from '../src/platform/two-step-continuation.ts';
import {LocalMoveCustody} from '../src/platform/local-move.ts';

// OWN-009: "Two-step verification — Optional. Set up after you switch." Only the
// choice travels, bound to the server and the new local account; no password.

test('a two-step request opens once, only for the server and account it was made for', () => {
  discardTwoStepSetup();
  requestTwoStepSetup({serverId: 'srv_a', accountId: 'owner'});
  assert.equal(hasTwoStepSetup('srv_a', 'owner'), true);
  assert.equal(takeTwoStepSetup('srv_b', 'owner'), false, 'another server');
  assert.equal(takeTwoStepSetup('srv_a', 'someone-else'), false, 'another account');
  assert.equal(takeTwoStepSetup('srv_a', 'owner'), true);
  assert.equal(takeTwoStepSetup('srv_a', 'owner'), false, 'taken once');
  requestTwoStepSetup({serverId: 'srv_a', accountId: 'owner'});
  discardTwoStepSetup();
  assert.equal(hasTwoStepSetup('srv_a', 'owner'), false, 'a refused move discards it');
});

test('the move custody names its server and account without exposing its grant', () => {
  const grant = {kind: 'signed-in', session: {accessToken: 'secret-access', refreshToken: 'secret-refresh'}, snapshot: {serverId: 'srv_a', account: {id: 'owner'}}};
  const custody = new LocalMoveCustody({grant, issuer: {origin: 'https://server.example', pin: {}}, serverId: 'srv_a', selectionRevision: 1} as never, async () => {});
  assert.deepEqual(custody.target, {serverId: 'srv_a', accountId: 'owner'});
  assert.doesNotMatch(JSON.stringify(custody.target), /secret/);
  // Reading the target never claims the one-shot grant.
  assert.ok(custody.claim());
});
