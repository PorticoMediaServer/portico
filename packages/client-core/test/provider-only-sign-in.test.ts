import test from 'node:test';
import assert from 'node:assert/strict';
import {providerOnlyLabel, providerOnlySignIn} from '../src/hosted-security.ts';

// Justin, 1 Nov: a Portico Account that signs in only with Google or Apple is never asked to set a
// password. Web and native read which providers from this one helper.
const link = (provider: 'google' | 'apple', id = provider) => ({id, provider});
const t = (key: string, values?: {provider: string}) => values ? `${key}(${values.provider})` : key;

test('only an account with no password and a linked provider is provider-only', () => {
  assert.equal(providerOnlySignIn({hasPassword: true, providers: [link('google')]}), undefined, 'a password account is unchanged');
  assert.equal(providerOnlySignIn({hasPassword: false, providers: []}), undefined, 'nothing to name');
  assert.deepEqual(providerOnlySignIn({hasPassword: false, providers: [link('google')]}), ['google']);
  assert.deepEqual(providerOnlySignIn({hasPassword: false, providers: [link('google'), link('apple'), link('google', 'second')]}), ['apple', 'google'], 'each once, Apple first');
});

test('the Password row names the provider, or both', () => {
  assert.equal(providerOnlyLabel(['google'], t), 'web.hostedSecurity.signsInWith(accounts.provider.google)');
  assert.equal(providerOnlyLabel(['apple'], t), 'web.hostedSecurity.signsInWith(accounts.provider.apple)');
  assert.equal(providerOnlyLabel(['apple', 'google'], t), 'web.hostedSecurity.signsInWithBoth');
});
