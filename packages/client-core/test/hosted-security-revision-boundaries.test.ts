import assert from 'node:assert/strict';
import test from 'node:test';
import {parseIdentityOptions, parseSecurityState} from '../src/hosted-security.ts';

// ACC-LEAF-29: the real provider-revision and closed reauthenticate-marker
// boundaries of the Hosted response parsers, authored UNRUN. Response data only;
// this never proves provider acceptance or grants anything.

const scope = {accountId: 'acct_A', familyId: 'family_A'};
const state = (over: Record<string, unknown> = {}) => ({
  ...scope, contactAddress: 'a@example.test', privateEmail: false, hasPassword: true,
  mfaEnabled: false, recoveryCodesRemaining: 0,
  providers: [{id: 'identity_1', provider: 'google', revision: '7'}], ...over,
});
test('a real provider revision survives, a missing one stays absent', () => {
  assert.equal(parseSecurityState(state(), scope).providers[0].revision, '7');
  const older = parseSecurityState(state({providers: [{id: 'identity_1', provider: 'google'}]}), scope).providers[0];
  assert.equal('revision' in older, false);
  assert.equal(parseSecurityState(state({providers: [{id: 'identity_1', provider: 'google', revision: '9223372036854775807'}]}), scope).providers[0].revision, '9223372036854775807');
});

test('zero, negative, noncanonical, overflowing or non-string revisions refuse', () => {
  for (const revision of ['0', '-1', '01', '9'.repeat(19), 7, true, null]) {
    assert.throws(() => parseSecurityState(state({providers: [{id: 'identity_1', provider: 'google', revision}]}), scope), String(revision));
  }
});

test('a wrong account or family, or a bad provider, refuses', () => {
  for (const bad of [{...scope, accountId: 'acct_B'}, {...scope, familyId: 'family_B'}]) {
    assert.throws(() => parseSecurityState(state(), bad));
  }
  assert.throws(() => parseSecurityState(state({providers: [{id: 'identity_1', provider: 'github', revision: '1'}]}), scope));
});

test('the credential-provider capability is preserved exactly and only as a known string', () => {
  const options = (over: Record<string, unknown> = {}) => ({password: true, verifiedContactRequired: false, providers: [], ...over});
  assert.equal(parseIdentityOptions(options({credentialProviderReauthentication: 'account-settings-v1'})).credentialProviderReauthentication, 'account-settings-v1');
  for (const value of [undefined, 'account-settings-v2', 1, true, null]) {
    const parsed = parseIdentityOptions(options(value === undefined ? {} : {credentialProviderReauthentication: value}));
    assert.equal('credentialProviderReauthentication' in parsed, false, String(value));
  }
});
