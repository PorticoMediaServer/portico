import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = () => {let resolve!: (value?: unknown) => void; const promise = new Promise(resolveValue => {resolve = resolveValue;}); return {promise, resolve};};
const scope = {accountId: 'account', familyId: 'family'}, hostedUrl = 'https://accounts.example.invalid';
const initialSecurity = {...scope, contactAddress: 'person@example.invalid', hasPassword: false, mfaEnabled: false, privateEmail: false,
  recoveryCodesRemaining: 5, providers: [{id: 'google-link', provider: 'google', revision: '4'}, {id: 'apple-link', provider: 'apple', revision: '7'}]};
class ApiError extends Error {readonly status: number; readonly code: string; constructor(status: number, message: string, code = 'request_failed') {super(message); this.status = status; this.code = code;}}
const control = () => ({held: undefined as Promise<unknown> | undefined, errors: [] as unknown[], entered: deferred()});

async function fixture(security = initialSecurity) {
  let active = true, sequence = 0, session = {account: {id: 'account', username: 'person'}, familyId: 'family', accessToken: 'fixture-token'};
  const calls: any[][] = [], credentials = {getSnapshot: () => ({session})};
  const reconfirm = control(), proof = control(), action = control();
  const options = {providers: [{id: 'google', enabled: true, native: false, nativeClientIds: [] as string[]},
    {id: 'apple', enabled: true, native: true, nativeClientIds: ['bundle.id']}]};
  let result: any = {...scope, status: 'complete'};
  const bridge = {
    newBinding: async () => 'a'.repeat(43), newRequestId: async () => `operation-${String(++sequence).padStart(16, '0')}`,
    openBrowser: async (...args: unknown[]) => {calls.push(['browser', ...args]); return 'fixture-return-link';},
    signInApple: async (...args: unknown[]) => {calls.push(['apple', ...args]); return {idToken: 'fixture-apple-token'};},
    canOpenBrowser: true, canSignInApple: true, bundleId: 'bundle.id',
  };
  const client = {
    options: async (...args: unknown[]) => {calls.push(['options', ...args]); return options;},
    passwordProof: async (...args: unknown[]) => {calls.push(['password-proof', ...args]); throw new Error('provider-only settings must never request a password proof');},
    action: async (...args: unknown[]) => {
      calls.push(['action', ...args]); action.entered.resolve(); if (action.held) await action.held;
      if (action.errors.length) throw action.errors.shift(); return result;
    },
    confirm: async (...args: unknown[]) => {calls.push(['confirm', ...args]); return result;},
  };
  const {NativeAccountSecurity} = await screenModule(new URL('../src-next/platform/account-security.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {NativeModules: {PorticoAccountAuth: bridge}},
    '../../../../packages/client-core/src': {ApiError, HttpLocalApi: class {}},
    '../../../../packages/client-core/src/account-notifications': {AccountNotificationsClient: class {}},
    '../../../../packages/client-core/src/hosted-security': {securityPurposeNeedsProof: (purpose: string) => ['password_set_or_change', 'email_change', 'mfa_enroll', 'mfa_disable', 'recovery_codes'].includes(purpose)},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    '../../../../packages/client-core/src/provider-security-proof': {
      reconfirmWithProvider: async (...args: any[]) => {
        calls.push(['reconfirm', ...args]); reconfirm.entered.resolve(); if (reconfirm.held) await reconfirm.held;
        if (reconfirm.errors.length) throw reconfirm.errors.shift();
        return Object.freeze({transactionId: 'fixture-transaction', provider: args[1].provider,
          intent: Object.freeze({...args[1].intent}), completionCode: 'fixture-completion'});
      },
      providerRecentProof: async (...args: any[]) => {
        calls.push(['provider-proof', ...args]); proof.entered.resolve(); if (proof.held) await proof.held;
        if (proof.errors.length) throw proof.errors.shift(); return {proof: 'fixture-provider-proof'};
      },
    },
    './account-work': {accountExpectation: (value: typeof session) => ({accountId: value.account.id, familyId: value.familyId, accessToken: value.accessToken}),
      nativeAccountRead: () => {throw new Error('a credential mutation must not use the retryable read wrapper');},
      nativeAccountWork: async (_: unknown, work: (value: typeof session) => Promise<unknown>, opening: any) => {
        calls.push(['command']); assert.deepEqual(opening.expected, {...scope, accessToken: 'fixture-token'});
        if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired'); const value = await work(session);
        if (!opening.isCurrent() || opening.signal.aborted) throw new Error('retired'); return value;
      }},
    './account-authority': {hostedUrl}, './server-connections': {nativeNetworkFetch: () => {throw new Error('unexpected transport');}},
  });
  const owner = {accountCredentials: () => credentials,
    accountClient: (binding: string, current: () => boolean) => {calls.push(['client', binding, current]); return client;},
    signOutPorticoAccount: async (value: unknown) => {calls.push(['sign-out', value]);}};
  const service = new NativeAccountSecurity(owner, scope, security, () => active);
  return {service, client, calls, bridge, options, reconfirm, proof, action, events: (name: string) => calls.filter(value => value[0] === name),
    result: (value: any) => {result = value;}, retire: () => {active = false;}, replace: () => {session = {...session, familyId: 'other-family'};}};
}

test('Google-only password change uses the opening client, provider intent and proof, then signs out once', async t => {
  const f = await fixture(); t.after(() => f.service.dispose());
  f.result({...scope, status: 'complete', signOut: true});
  f.service.open('password'); assert.deepEqual(f.calls, []);
  await f.service.confirmWithProvider('google-link', {newPassword: 'fixture-new'});
  for (const name of ['client', 'options', 'reconfirm', 'provider-proof', 'action']) assert.equal(f.events(name).length, 1, `${name} must run once`);
  const [reconfirm] = f.events('reconfirm'), [proof] = f.events('provider-proof'), [action] = f.events('action');
  assert.equal(reconfirm[1], f.client);
  assert.deepEqual(reconfirm[2], {provider: 'google', intent: {purpose: 'password_set_or_change', target: '', operationId: 'operation-0000000000000001'}, nativeApple: false, accountOrigin: hostedUrl, expectedIdentity: {id: 'google-link', revision: '4'}});
  assert.equal(reconfirm[3], 'fixture-token'); assert.equal(reconfirm[4].signInApple, undefined);
  assert.equal(f.events('options')[0][1], reconfirm[5]); assert.equal(reconfirm[5].aborted, false);
  assert.equal(proof[1], f.client); assert.deepEqual(proof[2], {transactionId: 'fixture-transaction', provider: 'google', intent: reconfirm[2].intent, completionCode: 'fixture-completion'});
  assert.equal(proof[3], ''); assert.deepEqual(proof[4], scope); assert.equal(proof[5], 'fixture-token'); assert.equal(proof[6], reconfirm[5]);
  assert.deepEqual(action.slice(1), [proof[2].intent, 'fixture-provider-proof', 'fixture-new', scope, 'fixture-token', reconfirm[5]]);
  assert.equal(action[1], proof[2].intent); assert.deepEqual(f.events('sign-out'), [['sign-out', scope]]); assert.equal(f.events('password-proof').length, 0);
  assert.deepEqual(f.calls.filter(value => ['options', 'reconfirm', 'provider-proof', 'action', 'sign-out'].includes(value[0])).map(value => value[0]),
    ['options', 'reconfirm', 'provider-proof', 'action', 'sign-out']);
  const firstId = await reconfirm[4].newRequestId(), secondId = await reconfirm[4].newRequestId();
  assert.ok(firstId.length >= 16); assert.notEqual(firstId, secondId); assert.notEqual(firstId, action[1].operationId);
  assert.equal(await reconfirm[4].openBrowser('https://provider.example.invalid', 'transaction'), 'fixture-return-link');
  assert.deepEqual(f.events('browser'), [['browser', 'https://provider.example.invalid', 'transaction']]);
});

test('two-step waits at factor and submits the entered code before the action', async t => {
  const f = await fixture({...initialSecurity, mfaEnabled: true}); t.after(() => f.service.dispose());
  f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'fixture-new'});
  assert.deepEqual(f.service.getSnapshot(), {kind: 'password', step: 'factor', busy: false, locked: true});
  assert.equal(f.events('provider-proof').length, 0); assert.equal(f.events('action').length, 0);
  await f.service.submit({code: '123456'});
  assert.equal(f.events('provider-proof')[0][3], '123456'); assert.equal(f.events('action')[0][2], 'fixture-provider-proof');
  assert.equal(f.events('action')[0][3], 'fixture-new'); assert.equal(f.service.getSnapshot().step, 'done'); assert.equal(f.events('password-proof').length, 0);
});

test('a wrong factor retries the same provider confirmation and original intent', async t => {
  const f = await fixture({...initialSecurity, mfaEnabled: true}); t.after(() => f.service.dispose());
  f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'first-password'});
  const original = f.events('reconfirm')[0][2].intent;
  f.proof.errors.push(new ApiError(400, 'factor refused', 'challenge_invalid')); await f.service.submit({code: 'wrong'});
  assert.equal(f.service.getSnapshot().step, 'factor'); assert.equal(f.service.getSnapshot().failure, 'factor refused');
  assert.equal(f.service.getSnapshot().locked, true); assert.equal(f.events('action').length, 0);
  await f.service.submit({code: '123456', newPassword: 'ignored-password'});
  assert.equal(f.events('reconfirm').length, 1);
  assert.equal(f.events('provider-proof')[0][2], f.events('provider-proof')[1][2]);
  assert.equal(f.events('provider-proof')[1][2].completionCode, 'fixture-completion');
  assert.equal(f.events('provider-proof').length, 2); assert.equal(f.events('action').length, 1);
  assert.deepEqual(f.events('action')[0][1], original);
  assert.equal(f.events('action')[0][3], 'first-password'); assert.equal(f.service.getSnapshot().step, 'done');
});

test('a stale provider completion still restarts from the form', async t => {
  const f = await fixture({...initialSecurity, mfaEnabled: true}); t.after(() => f.service.dispose());
  f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'first-password'});
  f.proof.errors.push(new ApiError(401, 'session retired', 'credentials_invalid'));
  await f.service.submit({code: '123456'});
  assert.equal(f.service.getSnapshot().step, 'form'); assert.equal(f.service.getSnapshot().locked, false);
  await f.service.confirmWithProvider('google-link', {newPassword: 'second-password'});
  assert.equal(f.events('reconfirm').length, 2);
  assert.notEqual(f.events('reconfirm')[0][2].intent.operationId, f.events('reconfirm')[1][2].intent.operationId);
});

test('a selected provider without a revision cannot fall back to another identity', async t => {
  const f = await fixture({...initialSecurity, providers: [{id: 'google-link', provider: 'google', revision: ''}]}); t.after(() => f.service.dispose());
  f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'fixture-new'});
  assert.match(f.service.getSnapshot().failure, /provider_unavailable/);
  assert.equal(f.events('reconfirm').length, 0); assert.equal(f.events('provider-proof').length, 0);
});

for (const mfaEnabled of [false, true]) {
  test(`lost proof answer keeps the transaction and callback code with two-step ${mfaEnabled}`, async t => {
    const f = await fixture({...initialSecurity, mfaEnabled}); t.after(() => f.service.dispose());
    f.proof.errors.push(new Error('proof answer lost'));
    f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'original-password'});
    if (mfaEnabled) await f.service.submit({code: '123456'});
    assert.equal(f.service.getSnapshot().failure, 'proof answer lost');
    if (mfaEnabled) await f.service.submit({code: '123456'});
    else await f.service.confirmWithProvider('google-link');
    assert.equal(f.events('reconfirm').length, 1);
    assert.equal(f.events('provider-proof').length, 2);
    assert.equal(f.events('provider-proof')[0][2], f.events('provider-proof')[1][2]);
    assert.equal(f.events('provider-proof')[1][2].completionCode, 'fixture-completion');
    assert.equal(f.service.getSnapshot().step, 'done');
    assert.equal(f.events('action')[0][3], 'original-password');
  });
}

for (const mfaEnabled of [false, true]) {
  test(`lost action answer reuses the same proof and intent with two-step ${mfaEnabled ? 'on' : 'off'}`, async t => {
    const f = await fixture({...initialSecurity, mfaEnabled}); t.after(() => f.service.dispose());
    f.action.errors.push(new Error('action answer lost'));
    f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'original-password'});
    if (mfaEnabled) await f.service.submit({code: '123456'});
    assert.equal(f.service.getSnapshot().failure, 'action answer lost'); assert.equal(f.service.getSnapshot().step, mfaEnabled ? 'factor' : 'form');
    const first = f.events('action')[0]; assert.ok(first);
    if (mfaEnabled) await f.service.submit({code: '654321', newPassword: 'different-password'});
    else await f.service.confirmWithProvider('google-link', {newPassword: 'different-password'});
    assert.equal(f.events('action').length, 2); const second = f.events('action')[1];
    assert.equal(second[1], first[1], 'a lost action answer must retain the exact intent');
    assert.equal(second[2], first[2], 'a lost action answer must reuse its one-use proof');
    assert.deepEqual(second.slice(1), first.slice(1)); assert.equal(second[3], 'original-password');
    for (const name of ['reconfirm', 'provider-proof', 'options']) assert.equal(f.events(name).length, 1, `${name} must not repeat after a lost action answer`);
    assert.equal(f.service.getSnapshot().step, 'done'); assert.equal(f.service.getSnapshot().failure, undefined); assert.equal(f.events('password-proof').length, 0);
  });
}

test('browser cancellation shows failure and retries the original provider intent', async t => {
  const f = await fixture(); t.after(() => f.service.dispose()); f.reconfirm.errors.push(new Error('browser cancelled'));
  f.service.open('password'); await f.service.confirmWithProvider('google-link', {newPassword: 'original-password'});
  assert.equal(f.service.getSnapshot().failure, 'browser cancelled'); assert.equal(f.service.getSnapshot().step, 'form');
  assert.equal(f.events('provider-proof').length, 0); assert.equal(f.events('action').length, 0);
  const original = f.events('reconfirm')[0][2].intent;
  await f.service.confirmWithProvider('google-link', {newPassword: 'different-password'});
  assert.equal(f.events('reconfirm').length, 2); assert.equal(f.events('reconfirm')[1][2].intent, original);
  assert.equal(f.events('reconfirm')[1][2].intent.operationId, original.operationId);
  assert.equal(f.events('action')[0][3], 'original-password'); assert.equal(f.service.getSnapshot().step, 'done');
});

test('email change trims the target and accepts the email challenge continuation', async t => {
  const f = await fixture(); t.after(() => f.service.dispose()); f.result({...scope, status: 'pending', challengeId: 'email-challenge'});
  f.service.open('email'); await f.service.confirmWithProvider('google-link', {email: '  new@example.invalid  '});
  const intent = f.events('reconfirm')[0][2].intent;
  assert.equal(intent.purpose, 'email_change'); assert.equal(intent.target, 'new@example.invalid');
  assert.equal(f.events('action')[0][3], ''); assert.equal(f.service.getSnapshot().step, 'email');
  f.result({...scope, status: 'complete'}); await f.service.submit({code: 'email-code'});
  const [confirm] = f.events('confirm'); assert.equal(confirm[1], 'email'); assert.equal(confirm[2], 'email-challenge'); assert.equal(confirm[3], 'email-code');
  assert.notEqual(confirm[4], intent.operationId); assert.deepEqual(confirm[5], scope); assert.equal(confirm[6], 'fixture-token');
  assert.equal(f.service.getSnapshot().step, 'done'); assert.equal(f.events('reconfirm').length, 1);
});

for (const canSignInApple of [true, false]) {
  test(`Apple uses ${canSignInApple ? 'the native port for the supported bundle' : 'the browser when native sign-in is unavailable'}`, async t => {
    const f = await fixture(); t.after(() => f.service.dispose()); f.bridge.canSignInApple = canSignInApple;
    if (canSignInApple) f.bridge.canOpenBrowser = false;
    f.service.open('password'); await f.service.confirmWithProvider('apple-link', {newPassword: 'fixture-new'});
    const [reconfirm] = f.events('reconfirm'); assert.ok(reconfirm);
    assert.equal(reconfirm[2].provider, 'apple'); assert.equal(reconfirm[2].nativeApple, canSignInApple); assert.equal(reconfirm[2].accountOrigin, hostedUrl);
    if (canSignInApple) {
      assert.deepEqual(await reconfirm[4].signInApple('fixture-nonce'), {idToken: 'fixture-apple-token'});
      assert.deepEqual(f.events('apple'), [['apple', 'fixture-nonce']]);
    } else {assert.equal(reconfirm[4].signInApple, undefined); assert.equal(typeof reconfirm[4].openBrowser, 'function');}
    assert.equal(f.service.getSnapshot().step, 'done');
  });
}

test('Apple falls back to the browser when native options do not support this app', async t => {
  for (const unsupported of ['bundle', 'native-disabled']) {
    const f = await fixture(); t.after(() => f.service.dispose());
    if (unsupported === 'bundle') f.bridge.bundleId = 'other.bundle'; else f.options.providers[1].native = false;
    f.service.open('password'); await f.service.confirmWithProvider('apple-link', {newPassword: 'fixture-new'});
    assert.equal(f.events('reconfirm')[0][2].nativeApple, false); assert.equal(f.events('reconfirm')[0][4].signInApple, undefined);
    assert.equal(f.service.getSnapshot().step, 'done');
  }
});

test('password accounts ignore provider confirmation without preparing a client or making requests', async t => {
  const f = await fixture({...initialSecurity, hasPassword: true}); t.after(() => f.service.dispose());
  f.service.open('password'); const before = f.service.getSnapshot(); await f.service.confirmWithProvider('google-link', {newPassword: 'fixture-new'});
  assert.deepEqual(f.calls, []); assert.equal(f.service.getSnapshot(), before);
});

for (const refusal of ['unknown-link', 'disabled-provider', 'no-transport', 'export', 'delete'] as const) {
  test(`provider confirmation refuses ${refusal} before any provider trip or proof`, async t => {
    const f = await fixture(); t.after(() => f.service.dispose());
    if (refusal === 'disabled-provider') f.options.providers[0].enabled = false;
    if (refusal === 'no-transport') {f.bridge.canOpenBrowser = false; f.bridge.canSignInApple = false;}
    f.service.open(refusal === 'export' || refusal === 'delete' ? refusal : 'password');
    await f.service.confirmWithProvider(refusal === 'unknown-link' ? 'unknown' : 'google-link', {newPassword: 'fixture-new'});
    assert.match(f.service.getSnapshot().failure, /provider_unavailable/); assert.equal(f.service.getSnapshot().busy, false);
    for (const name of ['reconfirm', 'provider-proof', 'password-proof', 'action', 'browser', 'apple']) assert.equal(f.events(name).length, 0, `${refusal} must not call ${name}`);
  });
}

for (const finish of ['close', 'reopen', 'retire', 'replace', 'dispose'] as const) {
  test(`a pending provider return cannot act or publish factor after ${finish}`, async t => {
    const f = await fixture({...initialSecurity, mfaEnabled: true}), pending = deferred(); t.after(() => f.service.dispose());
    f.reconfirm.held = pending.promise; f.service.open('password'); const first = f.service.confirmWithProvider('google-link', {newPassword: 'fixture-new'});
    await f.reconfirm.entered.promise; const signal = f.events('reconfirm')[0][5];
    if (finish === 'close') {f.service.close(); f.service.open('email');}
    else if (finish === 'reopen') f.service.open('email');
    else if (finish === 'retire') f.retire(); else if (finish === 'replace') f.replace(); else f.service.dispose();
    const before = f.service.getSnapshot(); let notifications = 0; f.service.subscribe(() => notifications++); assert.equal(signal.aborted, true);
    pending.resolve(); await first;
    assert.equal(f.service.getSnapshot(), before, 'a late provider return must preserve the successor or closed snapshot');
    assert.equal(notifications, 0, 'a late provider return must not notify a successor'); assert.notEqual(f.service.getSnapshot().step, 'factor');
    if (finish === 'close' || finish === 'reopen') assert.equal(f.service.getSnapshot().kind, 'email'); else assert.deepEqual(f.service.getSnapshot(), {step: 'form', busy: false});
    assert.equal(f.events('provider-proof').length, 0); assert.equal(f.events('action').length, 0); assert.equal(f.events('sign-out').length, 0);
  });
}

test('provider-only submit from the form refuses instead of using an empty password proof', async t => {
  const f = await fixture(); t.after(() => f.service.dispose()); f.service.open('password'); await f.service.submit({newPassword: 'fixture-new'});
  assert.match(f.service.getSnapshot().failure, /provider_proof_unavailable/); assert.equal(f.service.getSnapshot().step, 'form');
  for (const name of ['password-proof', 'provider-proof', 'reconfirm', 'action']) assert.equal(f.events(name).length, 0);
});

for (const [kind, purpose, result, step] of [
  ['mfa-on', 'mfa_enroll', {...scope, status: 'pending', challengeId: 'mfa-challenge', secret: 'fixture-secret'}, 'mfa'],
  ['mfa-off', 'mfa_disable', {...scope, status: 'complete'}, 'done'],
  ['codes', 'recovery_codes', {...scope, status: 'complete', recoveryCodes: ['fixture-recovery']}, 'codes'],
] as const) {
  test(`provider-confirmed ${kind} preserves its intent and accepts the ${step} result`, async t => {
    const f = await fixture(); t.after(() => f.service.dispose()); f.result(result); f.service.open(kind); await f.service.confirmWithProvider('google-link');
    assert.equal(f.events('reconfirm')[0][2].intent.purpose, purpose); assert.equal(f.events('reconfirm')[0][2].intent.target, '');
    assert.equal(f.events('action')[0][3], ''); assert.equal(f.service.getSnapshot().step, step);
    assert.deepEqual(f.service.getSnapshot().result, result); assert.equal(f.events('password-proof').length, 0);
  });
}
