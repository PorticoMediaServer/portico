import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';

test('a Google or Apple sheet closed without an answer says the sign-in did not finish, not "something went wrong"', () => {
  for (const context of ['sign-in', 'portico-account', 'profiles'] as const) {
    const result = presentError({code: 'provider_cancelled', message: 'Apple sign-in was cancelled or could not finish.'}, context);
    assert.equal(result.messageId, 'error.providerUnfinished', context);
    assert.equal(result.body, 'That sign-in didn’t finish. Try again.', context);
  }
});
