import assert from 'node:assert/strict';
import test from 'node:test';
import {accountSecurityUIURL} from '../src/platform/account-security-policy.ts';

// ACC-LEAF-31: the Account Settings UI destination boundary, authored UNRUN.
// This is UI context only; it neither proves nor grants account authority.

const media = 'https://media.example.test';
const accounts = 'https://accounts.example.test';

test('the exact server Account/profile query and the exact Hosted security tab succeed', () => {
  assert.equal(accountSecurityUIURL('server-account', media, media + '/settings?scope=account&section=profile'), true);
  assert.equal(accountSecurityUIURL('server-account', media, '/settings?section=profile&scope=account'), true);
  assert.equal(accountSecurityUIURL('hosted-account', accounts, accounts + '/account?tab=security'), true);
});

test('duplicate, mixed, Server Settings, wrong section, unknown keys, token or return parameters refuse', () => {
  for (const href of [
    media + '/settings?scope=account&section=profile&scope=account',
    media + '/settings?scope=account&section=profile&scope=server',
    media + '/settings?section=profile&section=profile&scope=account',
    media + '/settings?scope=account&section=server',
    media + '/settings?scope=server&section=server',
    media + '/settings?scope=account',
    media + '/settings?scope=account&section=profile&tab=security',
    media + '/settings?scope=account&section=profile&token=abc',
    media + '/settings?scope=account&section=profile&proof=abc',
    media + '/settings?scope=account&section=profile&returnUrl=https%3A%2F%2Fevil.example',
    media + '/settings?scope=account&section=profile#x',
  ]) assert.equal(accountSecurityUIURL('server-account', media, href), false, href);
  assert.equal(accountSecurityUIURL('server-account', media, media + '/settings?scope=account&section=profile&tab=security'), false);
  assert.equal(accountSecurityUIURL('hosted-account', accounts, accounts + '/account?tab=security&tab=security'), false);
  assert.equal(accountSecurityUIURL('server-account', media, media + '/settings?scope=account&section=profile&section=profile'), false);
  assert.equal(accountSecurityUIURL('server-account', media, media + '/settings?scope=account&section=profile&tab=security'), false);
  assert.equal(accountSecurityUIURL('hosted-account', accounts, accounts + '/settings?scope=account&section=profile'), false);
  assert.equal(accountSecurityUIURL('hosted-account', accounts, accounts + '/account?tab=devices'), false);
  assert.equal(accountSecurityUIURL('hosted-account', accounts, accounts + '/settings?scope=account&section=profile'), false);
});

test('another origin, HTTP, userinfo, a path mismatch or a bad origin refuse', () => {
  assert.equal(accountSecurityUIURL('server-account', media, 'https://evil.example/settings?scope=account&section=profile'), false);
  assert.equal(accountSecurityUIURL('server-account', media, 'http://media.example.test/settings?scope=account&section=profile'), false);
  assert.equal(accountSecurityUIURL('server-account', media, 'https://user@media.example.test/settings?scope=account&section=profile'), false);
  assert.equal(accountSecurityUIURL('server-account', media, media + '/account?tab=security'), false);
  assert.equal(accountSecurityUIURL('server-account', media + '/path', media + '/settings?scope=account&section=profile'), false);
  assert.equal(accountSecurityUIURL('hosted-account', 'http://accounts.example.test', 'http://accounts.example.test/account?tab=security'), false);
});

test('a relative same-path query resolves to the same original destination', () => {
  assert.equal(accountSecurityUIURL('server-account', media, '/settings?scope=account&section=profile'), true);
  assert.equal(accountSecurityUIURL('hosted-account', accounts, '/account?tab=security'), true);
});
