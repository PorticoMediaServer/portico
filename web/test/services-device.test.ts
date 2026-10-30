import assert from 'node:assert/strict';
import {test} from 'node:test';
import {decideHostedDevice, reviewHostedDevice} from '../src/services/device-approval.ts';
import type {ServicesAccount} from '../src/services/session.ts';

function accountPort() {
  let session = {account: {id: 'account_a', username: 'account', displayName: 'Account'}, familyId: 'family_a', accessToken: 'test-access'};
  const account = {api: {origin: 'https://services-device-test.example'}, service: {
    synchronize: async () => {}, accessSession: async () => session, getSnapshot: () => ({session}),
  }} as unknown as ServicesAccount;
  return {account, changeFamily: () => { session = {...session, familyId: 'family_b'}; }};
}
const preview = () => ({requestId: 'request_a', userCode: '7KQM-4TXP', deviceName: 'Test device', platform: 'web', appVersion: 'test', expiresAt: new Date(Date.now() + 60000).toISOString()});

test('Hosted device review and decision use only the selected account authority and actual bound family', async t => {
  const original = globalThis.fetch, calls: {url: string; body: unknown; authorization: string | null}[] = [];
  globalThis.fetch = async (url, options) => {
    calls.push({url: String(url), body: JSON.parse(String(options?.body)), authorization: new Headers(options?.headers).get('Authorization')});
    return String(url).endsWith('/review') ? Response.json(preview()) : new Response(null, {status: 204});
  };
  t.after(() => { globalThis.fetch = original; });
  const p = accountPort(), signal = new AbortController().signal;
  const review = await reviewHostedDevice(p.account, '7KQM4TXP', signal);
  assert.deepEqual(review.identity, {accountId: 'account_a', familyId: 'family_a'});
  await decideHostedDevice(p.account, review, 'approve', signal);
  assert.deepEqual(calls.map(x => [new URL(x.url).origin, new URL(x.url).pathname, x.authorization]), [
    ['https://services-device-test.example', '/v1/device-authorizations/review', 'Bearer test-access'],
    ['https://services-device-test.example', '/v1/device-authorizations/decision', 'Bearer test-access'],
  ]);
  assert.deepEqual(calls[1].body, {requestId: 'request_a', userCode: '7KQM-4TXP', decision: 'approve'});
  p.changeFamily();
  await assert.rejects(decideHostedDevice(p.account, review, 'deny', signal), {code: 'account_changed'});
  assert.equal(calls.length, 2, 'a review from another family must never send a decision');
});

test('a device response cannot publish a preview after the current account family changes', async t => {
  const original = globalThis.fetch, p = accountPort();
  globalThis.fetch = async () => { p.changeFamily(); return Response.json(preview()); };
  t.after(() => { globalThis.fetch = original; });
  await assert.rejects(reviewHostedDevice(p.account, '7KQM-4TXP', new AbortController().signal), {code: 'account_changed'});
});
