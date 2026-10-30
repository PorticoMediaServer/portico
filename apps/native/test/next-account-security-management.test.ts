import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = () => {let resolve!: (value?: unknown) => void; const promise = new Promise(resolveValue => {resolve = resolveValue;}); return {promise, resolve};};
const initialSecurity = {accountId: 'account', familyId: 'family', contactAddress: 'person@example.invalid', hasPassword: true, mfaEnabled: true, privateEmail: false, recoveryCodesRemaining: 5, providers: []};
class ApiError extends Error {readonly status: number; constructor(status: number, message: string) {super(message); this.status = status;}}

async function fixture(security = initialSecurity) {
  let active = true, session = {account: {id: 'account', username: 'person'}, familyId: 'family', accessToken: 'fixture-token'};
  const calls: unknown[][] = [], credentials = {getSnapshot: () => ({session})};
  let result: any = {accountId: 'account', familyId: 'family', status: 'complete'};
  let held: Promise<unknown> | undefined, rejection: unknown;
  const client = {
    passwordProof: async (...args: unknown[]) => {calls.push(['proof', ...args]); if (rejection) throw rejection; return {proof: 'fixture-proof'};},
    action: async (...args: unknown[]) => {calls.push(['action', ...args]); if (held) await held; return result;},
    confirm: async (...args: unknown[]) => {calls.push(['confirm', ...args]); return result;},
    deletionPreview: async (...args: unknown[]) => {calls.push(['preview', ...args]); return {deletionToken: 'fixture-deletion', expiresAt: new Date(Date.now() + 60000).toISOString(), ownedServers: [], affectedServers: [], canDelete: true};},
    acceptDeletion: async (...args: unknown[]) => {calls.push(['delete', ...args]); return {accountDeleted: true};},
  };
  const {NativeAccountSecurity} = await screenModule(new URL('../src-next/platform/account-security.ts', import.meta.url), {
    react: {default: {}},
    'react-native': {NativeModules: {PorticoAccountAuth: {newBinding: async () => 'a'.repeat(43), newRequestId: async () => 'operation-1234567890'}}},
    '../../../../packages/client-core/src': {ApiError, HttpLocalApi: class {}},
    '../../../../packages/client-core/src/account-notifications': {AccountNotificationsClient: class {}},
    '../../../../packages/client-core/src/hosted-security': {securityPurposeNeedsProof: (purpose: string) => ['password_set_or_change', 'email_change', 'mfa_enroll', 'mfa_disable', 'recovery_codes'].includes(purpose)},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    '../../../../packages/client-core/src/provider-security-proof': {reconfirmWithProvider: () => {throw new Error('a password account never confirms with a provider');}, providerRecentProof: () => {throw new Error('unexpected provider proof');}},
    './account-work': {accountExpectation: (value: typeof session) => ({accountId: value.account.id, familyId: value.familyId, accessToken: value.accessToken}),
      nativeAccountRead: () => {throw new Error('a credential mutation must not use the retryable read wrapper');},
      nativeAccountWork: async (_: unknown, work: (value: typeof session) => Promise<unknown>, opening: any) => {
        calls.push(['command']); assert.equal(opening.expected.accountId, 'account'); assert.equal(opening.expected.familyId, 'family');
        if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired'); const value = await work(session);
        if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired'); return value;
      }},
    './account-authority': {hostedUrl: 'https://accounts.example.invalid'}, './server-connections': {nativeNetworkFetch: () => {throw new Error('unexpected transport');}},
  });
  const owner = {accountCredentials: () => credentials, accountClient: () => client, signOutPorticoAccount: async (scope: unknown) => {calls.push(['sign-out', scope]);}};
  const service = new NativeAccountSecurity(owner, {accountId: 'account', familyId: 'family'}, security, () => active);
  return {service, calls, result: (value: any) => {result = value;}, hold: (value: Promise<unknown>) => {held = value;},
    reject: (value: unknown) => {rejection = value;},
    retire: () => {active = false;}, replace: () => {session = {...session, familyId: 'other-family'};}};
}

test('security sends no request until submit and preserves the original credential intent across single-flight work', async () => {
  const f = await fixture(), pending = deferred();
  f.service.open('password'); assert.deepEqual(f.calls, []);
  f.hold(pending.promise);
  const first = f.service.submit({password: 'fixture-old', factor: 'fixture-factor', newPassword: 'fixture-new'});
  for (let i = 0; i < 10; i++) await Promise.resolve();
  await f.service.submit({password: 'different', newPassword: 'different'});
  assert.equal(f.calls.filter(value => value[0] === 'action').length, 1);
  const proof: any[] = f.calls.find(value => value[0] === 'proof')!;
  const action: any[] = f.calls.find(value => value[0] === 'action')!;
  assert.equal(proof[1], action[1]); assert.equal(action[1].purpose, 'password_set_or_change');
  assert.equal(proof[2], 'fixture-old'); assert.equal(proof[3], 'fixture-factor');
  assert.equal(action[2], 'fixture-proof'); assert.equal(action[3], 'fixture-new');
  pending.resolve(); await first; assert.equal(f.service.getSnapshot().step, 'done');
  const count = f.calls.length; await f.service.submit(); assert.equal(f.calls.length, count, 'a completed sheet cannot resubmit');
  f.service.dispose();
});

test('closed and replaced security openings cannot publish or sign out a successor', async () => {
  const f = await fixture(), pending = deferred();
  f.result({...initialSecurity, status: 'complete', signOut: true}); f.hold(pending.promise);
  f.service.open('password'); const first = f.service.submit({password: 'fixture-old', newPassword: 'fixture-new'});
  for (let i = 0; i < 10; i++) await Promise.resolve();
  f.service.close(); f.service.open('email'); pending.resolve(); await first;
  assert.equal(f.service.getSnapshot().kind, 'email'); assert.equal(f.service.getSnapshot().busy, false);
  assert.equal(f.calls.filter(value => value[0] === 'sign-out').length, 0);
  f.replace(); await f.service.submit({password: 'fixture-old', email: 'new@example.invalid'});
  assert.equal(f.calls.filter(value => value[0] === 'action').length, 1);
  f.service.dispose();
});

test('one-time Security results are cleared on close, retirement and disposal without notifying a successor', async () => {
  for (const finish of ['close', 'retire', 'dispose', 'replace'] as const) {
    const f = await fixture();
    f.result({...initialSecurity, status: 'complete', secret: 'fixture-secret', recoveryCodes: ['fixture-recovery']});
    f.service.open('codes'); await f.service.submit({password: 'fixture-old', factor: 'fixture-factor'});
    assert.deepEqual(f.service.getSnapshot().result.recoveryCodes, ['fixture-recovery']);
    let notifications = 0; f.service.subscribe(() => notifications++);
    if (finish === 'close') f.service.close();
    else if (finish === 'replace') f.replace();
    else {f.retire(); if (finish === 'dispose') f.service.dispose();}
    const cleared = f.service.getSnapshot();
    assert.deepEqual(cleared, {step: 'form', busy: false});
    assert.equal(f.service.getSnapshot(), cleared, 'the empty retired snapshot is stable');
    assert.equal(notifications, finish === 'close' ? 1 : 0, 'retirement never publishes onto a different opening');
    f.service.dispose();
  }
});

test('provider-only credential changes do not substitute an empty password proof', async () => {
  const f = await fixture({...initialSecurity, hasPassword: false});
  f.service.open('password'); await f.service.submit({newPassword: 'fixture-new'});
  assert.equal(f.calls.filter(value => value[0] === 'proof' || value[0] === 'action').length, 0);
  assert.match(f.service.getSnapshot().failure, /provider_proof_unavailable/);
  f.service.dispose();
});

test('a definitive password refusal leaves fields editable for a corrected original-account attempt', async () => {
  const f = await fixture(); f.reject(new ApiError(401, 'password refused'));
  f.service.open('password'); await f.service.submit({password: 'incorrect', newPassword: 'fixture-new'});
  assert.equal(f.service.getSnapshot().locked, false);
  f.reject(undefined); await f.service.submit({password: 'corrected', newPassword: 'fixture-new'});
  const proofs = f.calls.filter(value => value[0] === 'proof');
  assert.equal(proofs[0][2], 'incorrect'); assert.equal(proofs[1][2], 'corrected');
  assert.equal(f.service.getSnapshot().step, 'done');
  f.service.dispose();
});

test('account deletion uses preview and exact typed account confirmation, without a password proof', async () => {
  const f = await fixture(); f.service.open('delete'); await f.service.submit();
  assert.equal(f.service.getSnapshot().step, 'deletion');
  await f.service.submit({confirmation: 'someone-else@example.invalid'});
  assert.equal(f.calls.filter(value => value[0] === 'delete').length, 0);
  await f.service.submit({confirmation: 'person@example.invalid'});
  assert.equal(f.calls.filter(value => value[0] === 'delete').length, 1);
  assert.equal(f.calls.filter(value => value[0] === 'proof').length, 0);
  assert.deepEqual(f.calls.find(value => value[0] === 'sign-out'), ['sign-out', {accountId: 'account', familyId: 'family'}]);
  f.service.dispose();
});
