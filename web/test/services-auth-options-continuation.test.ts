import assert from 'node:assert/strict';
import {test} from 'node:test';
import type {IdentityOptions} from '@core/hosted-security.ts';
import {ServicesAuthFlow} from '../src/services/auth-flow.ts';
import type {ServicesAccount} from '../src/services/session.ts';

// FX133-C: the original continuation across a failed capability read, authored
// UNRUN. Real ServicesAuthFlow; the port counts private reads so "options only"
// and "never adopt a successor" are measurable. Provider shapes follow the
// existing services-auth-flow fixtures.

const options: IdentityOptions = {password: true, recovery: true, registration: true,
  providers: [{id: 'apple', enabled: true, native: false, nativeClientIds: []}, {id: 'google', enabled: true, native: false, nativeClientIds: []}],
  verifiedContactRequired: true, accountSecurity: true};
const registration = (status: 'contact_pending' | 'verified') => ({kind: 'registration' as const, registrationId: 'ret_' + 'A'.repeat(43),
  status, revision: '1', maskedAddress: 'a***@example.test', expiresAt: '2030-01-01T00:00:00Z', contactVerified: status === 'verified'});
const onboarding = (provider: 'apple' | 'google' = 'apple') => ({transactionId: 'txn_original', provider, mode: 'login' as const,
  status: 'onboarding' as const, expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true, needsUsername: true,
  onboardingToken: 'onboarding_token_original', suggestedName: 'Person', verifiedEmail: 'actual@privaterelay.appleid.com', mfaRequired: false});
const verifiedProvider = () => ({transactionId: 'txn_verified', provider: 'google' as const, mode: 'login' as const, status: 'verified' as const,
  expiresAt: new Date(Date.now() + 60000).toISOString(), contactVerified: true, needsUsername: false, mfaRequired: false});

function port() {
  const reads = {options: 0, registration: 0, transaction: 0, login: 0, complete: 0};
  let fail = true, registrationValue: unknown = null, transactionValue: unknown = null, signedIn = 0;
  const completed: string[] = [];
  const proofs: string[] = [];
  const accepted = {account: {id: 'accepted-account', username: 'person', displayName: 'Person'}, familyId: 'accepted-family',
    accessToken: 'test-access', expiresAt: '2030-01-01T00:00:00Z'};
  const value = {
    client: {
      options: async () => {reads.options++; if (fail) throw new Error('service unavailable'); return options;},
      registration: async () => {reads.registration++; return registrationValue;},
      transaction: async () => {reads.transaction++; return transactionValue;},
      login: async () => {reads.login++; return {};},
      complete: async (id: string, input: {onboardingToken?: string}) => {reads.complete++; completed.push(id); proofs.push(input.onboardingToken ?? ''); return accepted;},
      mfa: async () => ({}), request: async () => ({}),
    },
    service: {authenticateIfSignedOut: async (work: (signal: AbortSignal) => Promise<unknown>) => work(new AbortController().signal)},
  } as unknown as ServicesAccount;
  return {value, reads, completed, proofs, restore: () => {fail = false;},
    setRegistration: (v: unknown) => {registrationValue = v;}, setTransaction: (v: unknown) => {transactionValue = v;},
    signedIn: () => signedIn, done: () => {signedIn++;}};
}

for (const status of ['contact_pending', 'verified'] as const) test('a failed first signin keeps the original '+status+' registration and ignores a successor', async () => {
  const p = port(); p.setRegistration(registration(status));
  const flow = new ServicesAuthFlow(p.value, p.done, 'signin');
  await flow.initialize();
  assert.equal(flow.getSnapshot().optionsFailed, true);
  // A successor becomes available only after the failure.
  p.setRegistration({...registration('verified'), registrationId: 'ret_' + 'B'.repeat(43)});
  p.restore();
  await flow.retryInitialization();
  assert.deepEqual({options: p.reads.options, registration: p.reads.registration, transaction: p.reads.transaction}, {options: 2, registration: 1, transaction: 1});
  assert.equal(flow.getSnapshot().mode, 'verify');
  assert.equal(flow.getSnapshot().registration?.registrationId, 'ret_' + 'A'.repeat(43));
  flow.dispose();
});

test('an explicit move away and back retires the automatic restore', async () => {
  const p = port(); p.setRegistration(registration('contact_pending'));
  const flow = new ServicesAuthFlow(p.value, p.done, 'signin');
  await flow.initialize();
  flow.go('create'); flow.go('signin');
  p.restore(); p.setRegistration(registration('verified'));
  await flow.retryInitialization();
  assert.equal(p.reads.registration, 1);
  assert.equal(flow.getSnapshot().mode, 'signin');
  assert.equal(flow.getSnapshot().registration, undefined);
  flow.dispose();
});

test('an onboarding provider token is held across the failure, restored without adoption, and finishes only explicitly', async () => {
  const p = port(); p.setRegistration(null); p.setTransaction(onboarding());
  const flow = new ServicesAuthFlow(p.value, p.done, 'signin');
  await flow.initialize();
  p.setTransaction({...onboarding('google'), transactionId: 'txn_successor'});
  p.restore();
  await flow.retryInitialization();
  assert.deepEqual({registration: p.reads.registration, transaction: p.reads.transaction}, {registration: 1, transaction: 1});
  assert.equal(flow.getSnapshot().mode, 'provider');
  assert.equal(p.reads.complete, 0);
  assert.equal(p.signedIn(), 0);
  await flow.finishProvider('person', 'Person');
  assert.deepEqual(p.completed, ['txn_original']);
  assert.deepEqual(p.proofs, ['onboarding_token_original']);
  assert.equal(flow.getSnapshot().mode, 'ready');
  assert.equal(p.signedIn(), 0);                 // ready is not sign-in consent
  flow.dispose();
});

test('a verified provider auto-completion runs only after capabilities return, with the original transaction', async () => {
  const p = port(); p.setRegistration(null); p.setTransaction(verifiedProvider());
  const flow = new ServicesAuthFlow(p.value, p.done, 'signin');
  await flow.initialize();
  assert.equal(p.reads.complete, 0);             // failed options gate the original completion
  p.setTransaction({...verifiedProvider(), transactionId: 'txn_successor'});
  p.restore();
  await flow.retryInitialization();
  assert.deepEqual(p.completed, ['txn_verified']);
  assert.equal(p.reads.transaction, 1);          // no new browser read
  assert.equal(p.signedIn(), 1);                // existing verified-return continuation
  flow.dispose();
});

test('create and forgot never read the private continuation', async () => {
  for (const mode of ['create', 'forgot'] as const) {
    const p = port(), flow = new ServicesAuthFlow(p.value, p.done, mode);
    await flow.initialize();
    p.restore();
    await flow.retryInitialization();
    assert.deepEqual({registration: p.reads.registration, transaction: p.reads.transaction}, {registration: 0, transaction: 0}, mode);
    assert.equal(flow.getSnapshot().options?.password, true);
    flow.dispose();
  }
});

test('an ordinary signin retries capabilities only; explicit sign-in alone calls completion', async () => {
  const p = port(); p.setRegistration(null); p.setTransaction(null);
  const flow = new ServicesAuthFlow(p.value, p.done, 'signin');
  await flow.initialize();
  p.restore();
  await flow.retryInitialization();
  assert.deepEqual({login: p.reads.login, complete: p.reads.complete, signedIn: p.signedIn()}, {login: 0, complete: 0, signedIn: 0});
  assert.equal(flow.getSnapshot().mode, 'signin');
  await flow.signIn('person', 'Password-1!', '');
  assert.equal(p.reads.login, 1);
  assert.equal(p.signedIn(), 1);
  flow.dispose();
});
