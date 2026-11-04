import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSettingsConfiguration} from '../src/settings-configuration.ts';
import {isSignInPolicyRow, patchSignInPolicy, signInPolicyConflict, signInPolicyRows, signInPolicyValues, signInPolicyWith} from '../src/sign-in-policy.ts';

const field = (id: string, key: string, current: unknown) => ({id, key, current, effective: current, default: current, source: 'default', configOnly: false, applicationMode: 'hot'});
const nine = [
  field('signin.stayLength', 'signin.stay_signed_in_days', 90), field('signin.pinRemember', 'signin.pin_remember_days', 30),
  field('signin.passwordAttempts', 'signin.password_attempts', 'standard'), field('signin.pinAttempts', 'signin.pin_attempts', 'standard'),
  field('signin.passwordLength', 'signin.password_min_length', 8), field('signin.resetCodeLife', 'signin.member_reset_code_minutes', 60),
  field('signin.requireTwoStep', 'signin.require_two_step', 'nobody'), field('signin.rememberAccounts', 'signin.remember_accounts', true),
  field('signin.endedKeep', 'signin.ended_sessions_days', 90),
];
const document = {revision: 3, registryRevision: 'r', path: '/srv/portico.toml', signInPolicyRevision: '7', fields: nine};

test('the configuration keeps the sign-in policy revision (#713)', () => {
  const parsed = parseSettingsConfiguration({scope: {}, data: document});
  assert.equal(parsed.signInPolicyRevision, '7');
  assert.throws(() => parseSettingsConfiguration({...document, signInPolicyRevision: 7}));
  assert.throws(() => parseSettingsConfiguration({...document, signInPolicyRevision: ''}));
  // Required, like any other field the contract requires.
  assert.throws(() => parseSettingsConfiguration({...document, signInPolicyRevision: undefined}));
});

test('the nine rows read as the typed policy, and a change sends the complete object', async () => {
  assert.deepEqual(Object.keys(signInPolicyRows).sort(), ['signin.endedKeep', 'signin.passwordAttempts', 'signin.passwordLength', 'signin.pinAttempts', 'signin.pinRemember', 'signin.rememberAccounts', 'signin.requireTwoStep', 'signin.resetCodeLife', 'signin.stayLength']);
  assert.equal(isSignInPolicyRow('signin.stayLength'), true); assert.equal(isSignInPolicyRow('signin.dataFolder'), false);
  const values = signInPolicyValues(parseSettingsConfiguration(document))!;
  assert.deepEqual(values, {staySignedInDays: 90, pinRememberDays: 30, passwordAttempts: 'standard', pinAttempts: 'standard', passwordMinLength: 8,
    memberResetCodeMinutes: 60, requireTwoStep: 'nobody', rememberAccounts: true, endedSessionsDays: 90});
  // Select controls hand numbers over as strings; the policy takes numbers.
  assert.deepEqual(signInPolicyWith(values, 'signin.stayLength', '365'), {...values, staySignedInDays: 365});
  assert.deepEqual(signInPolicyWith(values, 'signin.pinRemember', '0'), {...values, pinRememberDays: 0});
  assert.deepEqual(signInPolicyWith(values, 'signin.requireTwoStep', 'everyone'), {...values, requireTwoStep: 'everyone'});
  assert.deepEqual(signInPolicyWith(values, 'signin.rememberAccounts', false), {...values, rememberAccounts: false});
  assert.throws(() => signInPolicyWith(values, 'signin.stayLength', 'soon'));
  // Any missing or malformed row means no write.
  assert.equal(signInPolicyValues(parseSettingsConfiguration({...document, fields: nine.slice(1)})), undefined);
  assert.equal(signInPolicyValues(parseSettingsConfiguration({...document, fields: [{...nine[0]!, current: 'ninety'}, ...nine.slice(1)]})), undefined);
  const calls: unknown[][] = [];
  await patchSignInPolicy({request: async (...args: unknown[]) => {calls.push(args); return {};}}, '7', {...values, staySignedInDays: 30});
  assert.deepEqual(calls[0]!.slice(0, 3), ['/v1/admin/sign-in/policy', 'PATCH', {expectedRevision: '7', values: {...values, staySignedInDays: 30}}]);
});

test('a revision mismatch is a conflict to re-read, not an error', () => {
  assert.equal(signInPolicyConflict({status: 412, code: 'revision_mismatch'}), true);
  assert.equal(signInPolicyConflict({code: 'revision_mismatch'}), true);
  // The code decides, not the status: another 409 or 412 is not this conflict.
  assert.equal(signInPolicyConflict({status: 412}), false);
  assert.equal(signInPolicyConflict({status: 409, code: 'conflict'}), false);
  assert.equal(signInPolicyConflict({status: 403, code: 'not_permitted'}), false);
  assert.equal(signInPolicyConflict({code: 'factor_setup_required'}), false);
});
