import assert from 'node:assert/strict';
import test from 'node:test';
import {routePath} from '../../packages/contracts/src/routes.ts';
import {accountSecurityRequest} from '../src/platform/account-security-policy.ts';

// ACC-LEAF-32: the read-only transport snapshot, authored UNRUN. It pins the
// captured URL, method and headers, and nothing here authenticates anyone.

const origin = 'https://media.example.test';
const proofPath = routePath('prove_network_identity');

test('a mutable URL and header set changed after dispatch cannot retarget the capture', () => {
  const url = new URL(origin + '/settings?scope=account&section=profile');
  const headers = new Headers({Authorization: 'Bearer original'});
  const {message, request} = accountSecurityRequest(url, {method: 'POST', headers}, origin, false);
  url.pathname = '/admin';
  url.searchParams.set('scope', 'server');
  headers.set('Authorization', 'Bearer swapped');
  assert.equal(new URL(String(message)).pathname, '/settings');
  assert.equal(new URL(String(message)).searchParams.get('scope'), 'account');
  assert.equal(new Headers(request.headers).get('Authorization'), 'Bearer original');
  assert.equal(request.redirect, 'error');
  assert.equal(request.method, 'POST');
});

test('a Request input is cloned', () => {
  const request = new Request(origin + '/settings?scope=account&section=profile', {method: 'POST', headers: {Authorization: 'Bearer original'}});
  const captured = accountSecurityRequest(request, {redirect: 'follow'}, origin, false);
  assert.equal(captured.request.redirect, 'error');
  assert.equal(new Headers(captured.request.headers).get('Authorization'), 'Bearer original');
  assert.equal(captured.message instanceof Request, true);
  assert.notEqual(captured.message, request);
});

test('readonly permits GET and the query-free proof POST only', () => {
  assert.doesNotThrow(() => accountSecurityRequest(origin + '/me', {method: 'GET'}, origin, true));
  assert.doesNotThrow(() => accountSecurityRequest(origin + proofPath, {method: 'POST'}, origin, true));
  // Readonly permits every same-origin GET; only the proof POST is narrow.
  assert.doesNotThrow(() => accountSecurityRequest(origin + proofPath + '?nonce=1', {method: 'GET'}, origin, true));
  for (const [method, path] of [['POST', '/settings?scope=account&section=profile'], ['PUT', '/me'], ['PATCH', '/me'], ['DELETE', '/me'],
    ['POST', proofPath + '?nonce=1']] as const) {
    assert.throws(() => accountSecurityRequest(origin + path, {method}, origin, true), method + ' ' + path);
  }
});

test('a wrong origin, userinfo, hash, HTTP or a bad origin refuses', () => {
  for (const href of ['https://evil.example/me', 'https://user@media.example.test/me', origin + '/me#x', 'http://media.example.test/me']) {
    assert.throws(() => accountSecurityRequest(href, {method: 'GET'}, origin, true), href);
  }
  assert.throws(() => accountSecurityRequest(origin + '/me', {method: 'GET'}, 'http://media.example.test', true));
});

test('normal Security command mode allows a registered-origin POST and captures headers', () => {
  const headers = new Headers({Authorization: 'Bearer session'});
  const {request} = accountSecurityRequest(origin + '/v1/direct/password', {method: 'POST', headers}, origin, false);
  assert.equal(request.method, 'POST');
  assert.equal(new Headers(request.headers).get('Authorization'), 'Bearer session');
  assert.equal(request.redirect, 'error');
});
