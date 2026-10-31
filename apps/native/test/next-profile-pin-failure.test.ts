import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import test from 'node:test';
import {ApiError} from '../../../packages/client-core/src';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {profilePinFailureText} from '../src-next/screens/accounts/profile-pin-failure';

// Android walk, 31 Oct: a wrong profile PIN read "Your server sent something Portico couldn't
// read", because the shared presenter takes any `*_invalid` code for an unreadable reply.

test('a wrong profile PIN says the PIN is not right', () => {
  const wrong = new ApiError(403, 'profile_pin_invalid', 'The request could not be completed.');
  assert.equal(presentError(wrong, 'profiles').category, 'invalid-response', 'why the screens need this');
  assert.equal(profilePinFailureText(wrong), defaultI18n.t('accounts.profiles.pinFailed'));
});

test('a locked PIN says how long to wait when the server said', () => {
  const locked = Object.assign(new ApiError(403, 'profile_pin_locked', 'Locked.'), {retryAfterSeconds: 29.2});
  assert.equal(profilePinFailureText(locked), defaultI18n.t('accounts.profiles.pinLocked', {seconds: 30}));
  assert.equal(profilePinFailureText(new ApiError(403, 'profile_pin_locked', 'Locked.')), defaultI18n.t('accounts.profiles.pinFailed'));
});

test('anything else keeps the shared presentation', () => {
  for (const other of [new ApiError(503, 'server_busy', 'Busy.'), new Error('x'), undefined, null, 'profile_pin_invalid'])
    assert.equal(profilePinFailureText(other), undefined);
});

test('both PIN screens ask for the PIN text before the shared presentation', () => {
  const read = (name: string) => readFileSync(new URL(`../src-next/screens/accounts/${name}`, import.meta.url), 'utf8');
  assert.match(read('LifecycleProfileGate.tsx'), /profilePinFailureText\(failed\) \?\? failure\.body/);
  assert.equal(read('AccountRoutes.tsx').match(/profilePinFailureText\(error\) \?\? presentError\(error, 'profiles'\)\.body/g)?.length, 3);
});
