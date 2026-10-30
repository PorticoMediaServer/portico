import test from 'node:test';
import assert from 'node:assert/strict';
import {canOpenShortInvitation} from '../src/screens/accounts/short-invitation-join.ts';

const rendered = {phase: 'password', server: {id: 'srv_current', signIn: 'local'}, origin: 'https://server.example', busy: false};
const form = {busy: false};
test('a Join click may leave only its own current local-password state', () => {
  assert.equal(canOpenShortInvitation({...rendered}, rendered, form, true, false, false), true);
  assert.equal(canOpenShortInvitation({...rendered, phase: 'challenge'}, rendered, form, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered, busy: true}, rendered, form, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered, origin: 'https://other.example'}, rendered, form, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered, server: {id: 'srv_other', signIn: 'local'}}, rendered, form, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered, server: {id: 'srv_current', signIn: 'portico'}}, rendered, form, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered}, {...rendered, phase: 'challenge'}, form, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered}, rendered, {busy: true}, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered}, rendered, {busy: false, factor: {}}, true, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered}, rendered, form, false, false, false), false);
  assert.equal(canOpenShortInvitation({...rendered}, rendered, form, true, true, false), false);
  assert.equal(canOpenShortInvitation({...rendered}, rendered, form, true, false, true), false);
});
