import test from 'node:test';
import assert from 'node:assert/strict';
import {TVConsentApprover, type TVConsentAuthority} from '../src/tv-consent.ts';
const localPreview = () => ({device: {name: 'Living room', platform: 'tv', appVersion: '1'}, expiresAt: new Date(Date.now() + 60000).toISOString()});
const preview = () => ({requestId: 'request', userCode: '2345-ABCD', deviceName: 'Living room', platform: 'tv', appVersion: '1', expiresAt: new Date(Date.now() + 60000).toISOString()});
test('one generated review/decision path per kind, on captured account service only', async () => {
  for (const kind of ['local', 'portico'] as const) {
    const authority = {kind, origin: 'https://' + kind + '.test', accountId: 'member', sessionFamilyId: 'family', ...(kind === 'local' ? {serverId: 'server'} : {})};
    const calls: unknown[][] = [];
    const approver = new TVConsentApprover({baseUrl: authority.origin, request: async <T>(...args: unknown[]) => {calls.push(args); return (calls.length === 1 ? kind === 'local' ? localPreview() : preview() : undefined) as T;}}, authority, () => authority);
    const reviewed = await approver.review('2345abcd'); await approver.decide(reviewed, 'approve');
    assert.equal(calls[0][0], kind === 'local' ? '/v1/auth/codes/2345-ABCD' : '/v1/device-authorizations/review');
    assert.equal(calls[0][1], kind === 'local' ? 'GET' : 'POST');
    assert.equal(calls[1][0], kind === 'local' ? '/v1/auth/codes/2345-ABCD/approve' : '/v1/device-authorizations/decision');
    assert.deepEqual(calls[1][2], kind === 'local' ? {deny: false} : {requestId: 'request', userCode: '2345-ABCD', decision: 'approve'});
    await assert.rejects(approver.decide(reviewed, 'approve'), {code: 'consent_retired'}); approver.dispose();
  }
});
test('account/family/server retirement, forged preview, cancellation and expired review never approve', async () => {
  const authority: TVConsentAuthority = {kind: 'local', origin: 'http://server.test', serverId: 'server', accountId: 'account', sessionFamilyId: 'family'};
  let current: TVConsentAuthority | undefined = authority, decisions = 0;
  const approver = new TVConsentApprover({baseUrl: authority.origin, request: async <T>(path: string) => {if (path.endsWith('decision')) decisions++; return localPreview() as T;}}, authority, () => current);
  const reviewed = await approver.review('2345ABCD');
  await assert.rejects(approver.decide({...reviewed}, 'approve'), {code: 'consent_retired'});
  current = {...authority, accountId: 'other'}; await assert.rejects(approver.decide(reviewed, 'approve'), {code: 'consent_retired'});
  current = authority; approver.cancel(); await assert.rejects(approver.decide(reviewed, 'approve'), {code: 'consent_retired'});
  assert.equal(decisions, 0); approver.dispose();
});
test('offline and cancelled late review cannot be adopted even when transport ignores abort', async () => {
  const authority: TVConsentAuthority = {kind: 'local', origin: 'http://server.test', serverId: 'server', accountId: 'account', sessionFamilyId: 'family'};
  let answer!: (v: unknown) => void;
  const approver = new TVConsentApprover({baseUrl: authority.origin, request: <T>() => new Promise<T>(resolve => {answer = v => resolve(v as T);})}, authority, () => authority, 10);
  const request = approver.review('2345ABCD'); approver.cancel(); answer(localPreview());
  await assert.rejects(request, {code: 'consent_cancelled'});
  await assert.rejects(approver.review('2345ABCD'), {code: 'consent_timeout'}); answer(localPreview()); approver.dispose();
});
