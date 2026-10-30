import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';

test('a required second factor asks for two-step setup, never a sign-out or a blind retry', () => {
  const result = presentError({code: 'factor_setup_required', status: 403, message: 'factor setup required'}, 'sign-in', {operation: 'action'});
  assert.equal(result.category, 'permission');
  assert.equal(result.messageId, 'error.factorSetupRequired');
  assert.equal(result.body, 'Turn on two-step verification for this account, then try again.');
  assert.equal(result.action, 'none');
});
