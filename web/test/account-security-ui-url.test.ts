import assert from 'node:assert/strict';
import test from 'node:test';
import {accountSecurityUIURL} from '../src/platform/account-security-policy.ts';

// ACC-LEAF-31: the Account Settings UI destination boundary, authored UNRUN.
// This is UI context only; it neither proves nor grants account authority.

const accounts = 'https://accounts.example.test';

test('the exact Hosted security tab succeeds', () => {
  assert.equal(accountSecurityUIURL(accounts, accounts + '/account?tab=security'), true);
});

test('duplicate, unknown or return parameters, a hash or another tab refuse', () => {
  for (const href of [
    accounts + '/account?tab=security&tab=security',
    accounts + '/account?tab=security&token=abc',
    accounts + '/account?tab=security&proof=abc',
    accounts + '/account?tab=security&returnUrl=https%3A%2F%2Fevil.example',
    accounts + '/account?tab=devices',
    accounts + '/settings?scope=account&section=profile',
  ]) assert.equal(accountSecurityUIURL(accounts, href), false, href);
  assert.equal(accountSecurityUIURL(accounts, accounts + '/account?tab=security#x'), false);
});

test('another origin, HTTP, userinfo or a bad origin refuse', () => {
  assert.equal(accountSecurityUIURL(accounts, 'https://evil.example/account?tab=security'), false);
  assert.equal(accountSecurityUIURL(accounts, 'http://accounts.example.test/account?tab=security'), false);
  assert.equal(accountSecurityUIURL(accounts, 'https://user@accounts.example.test/account?tab=security'), false);
  assert.equal(accountSecurityUIURL('http://accounts.example.test', 'http://accounts.example.test/account?tab=security'), false);
  assert.equal(accountSecurityUIURL(accounts + '/path', accounts + '/account?tab=security'), false);
});

test('a relative same-path query resolves to the same original destination', () => {
  assert.equal(accountSecurityUIURL(accounts, '/account?tab=security'), true);
});
