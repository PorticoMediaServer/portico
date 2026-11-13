import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';

test('deleting a Portico Account that still owns a server says what to do first; an ended sign-in asks to sign in again', () => {
  const owns = presentError({code: 'owns_server', status: 409}, 'portico-account');
  assert.equal(owns.messageId, 'error.ownsServer');
  assert.match(owns.body, /Transfer it to someone else, switch it to local accounts, or start it over/);
  for (const code of ['sign_in_again', 'signed_out']) assert.equal(presentError({code, status: 401}, 'portico-account').category, 'authentication', code);
});
