import assert from 'node:assert/strict';
import {test} from 'node:test';
import {takeLinkFragment, verificationLink} from '../src/services/fragments.ts';
import {currentAccountWork} from '../src/services/session.ts';
import {resetPassword, verifyEmail} from '../src/services/link-effects.ts';
import {CredentialSessionService, type CredentialRecord} from '../../packages/client-core/src/credentials.ts';
import type {ServicesAccount} from '../src/services/session.ts';

test('link secrets are scrubbed synchronously, including duplicate, unexpected and malformed fragments', () => {
  for (const hash of ['#token=abc_123', '#token=a&token=b', '#token=a&return=https://bad.example', '#token=%00', '#unknown=a']) {
    const calls: unknown[] = [];
    const result = takeLinkFragment({hash, pathname: '/reset', search: ''}, {state: {keep: true}, replaceState: (...args) => { calls.push(args); }});
    assert.deepEqual(calls, [[{keep: true}, '', '/reset']]);
    assert.deepEqual(result, hash === '#token=abc_123' ? {token: 'abc_123'} : {});
  }
  assert.deepEqual(takeLinkFragment({hash: '#token=abc', pathname: '/reset', search: '?token=secret'}, {state: null, replaceState() {}}), {});
});

const token = (kind = 'registration', code = '482917') => btoa(JSON.stringify(['1', kind, 'registration_123', code])).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
test('verification tuple accepts the two current kinds and refuses retired provider mail and unbounded input', () => {
  assert.deepEqual(verificationLink(token()), {kind: 'registration', resource: 'registration_123', code: '482917'});
  assert.equal(verificationLink(token('oidc_contact')), undefined);
  assert.equal(verificationLink(token('registration', '12345')), undefined);
  assert.equal(verificationLink('a'.repeat(2049)), undefined);
});

type PortSession = {
  account: {id: string; username: string; displayName: string};
  familyId: string;
  accessToken: string;
  expiresAt: string;
  refreshExpiresAt: string;
};

const deferred = <T = unknown>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {resolve = done;});
  return {promise, resolve};
};

/** The canonical CredentialSessionService itself, on plain controlled storage and a
 * plain API boundary. Nothing here re-implements a coordinator transition: the
 * opening is only the service's own published snapshot, families move only through
 * adopt/logoutIfCurrent, and the witnesses record the real API effects (retired
 * authorities and renewal requests) instead of a fabricated phase. */
async function accountPort({signedIn = true, registration = null, confirm}: {
  signedIn?: boolean;
  registration?: {registrationId: string} | null;
  confirm?: () => Promise<unknown>;
} = {}) {
  const family = (familyId: string): PortSession => ({
    account: {id: 'account_a', username: 'account', displayName: 'Account'},
    familyId, accessToken: `access_${familyId}`,
    expiresAt: new Date(Date.now() + 3600000).toISOString(),
    refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
  });
  const requests: unknown[][] = [];
  const registrationCalls: unknown[][] = [];
  const verifyCalls: unknown[][] = [];
  const renewals: unknown[] = [];
  const retired: {familyId: string}[] = [];
  let stored: CredentialRecord<{familyId: string}> | undefined;
  const service = new CredentialSessionService<{familyId: string}>({
    authorityFromSession: value => ({familyId: value.familyId}),
    // Only a real renewal may ask; a mailed link that did would fail here too.
    refresh: async authority => {renewals.push(authority); throw new Error('a mailed link must not renew credentials');},
    revoke: async authority => {retired.push(authority);},
  }, {
    load: async () => stored === undefined ? undefined : structuredClone(stored),
    save: async value => {stored = structuredClone(value);},
  });
  await service.synchronize();
  if (signedIn) await service.adopt(family('family_a'));
  const account = {
    client: {
      request: async (...args: unknown[]) => {
        requests.push(args);
        if (confirm) return confirm();
        const body = args[2] as {token?: unknown} | undefined;
        // The public confirm capability answers the shape its mailed token proves.
        const kind = typeof body?.token === 'string' ? verificationLink(body.token)?.kind : undefined;
        return kind === 'email_change'
          ? {kind: 'email_change', status: 'email_changed', accountId: 'account_a'}
          : {kind: 'registration', status: 'confirmed'};
      },
      registration: async (...args: unknown[]) => {registrationCalls.push(args); return registration;},
      // A completed signup adopts the family the completion itself issued, so a
      // wrongful adoption on a signed-in browser could not hide behind family_a.
      verifyRegistration: async (...args: unknown[]) => {verifyCalls.push(args); return family('family_new');},
    },
    service,
  } as unknown as ServicesAccount;
  return {account, service, requests, registrationCalls, verifyCalls, renewals, retired,
    session: family, change: () => service.adopt(family('family_b'))};
}

test('account work rejects a changed family even when the account id is unchanged', async () => {
  const port = await accountPort();
  await assert.rejects(currentAccountWork(port.account, async () => { await port.change(); return 'stale'; }), {code: 'account_changed'});
  await assert.rejects(currentAccountWork(port.account, async () => 'unused', {accountId: 'account_a', familyId: 'family_a'}), {code: 'account_changed'});
  assert.equal(port.service.getSnapshot().session?.familyId, 'family_b');
});

test('password reset uses the unauthenticated recovery endpoint and never adopts a session', async () => {
  const port = await accountPort();
  await resetPassword(port.account, 'token', 'a long new password', ' 482917 ', new AbortController().signal);
  assert.equal(port.requests.length, 1);
  assert.deepEqual(port.requests[0].slice(0, 3), ['/v1/account/recovery/complete', 'POST', {token: 'token', newPassword: 'a long new password', factor: '482917'}]);
  assert.equal(port.service.getSnapshot().session?.familyId, 'family_a');
  assert.deepEqual(port.verifyCalls, []);
  assert.deepEqual(port.renewals, []);
});

test('a link on another device confirms the address without creating a session', async () => {
  const port = await accountPort();
  assert.equal(await verifyEmail(port.account, token(), new AbortController().signal), 'elsewhere');
  assert.equal(port.service.getSnapshot().session?.familyId, 'family_a');
  assert.equal(port.requests[0][0], '/v1/email-links/confirm');
  assert.deepEqual(port.verifyCalls, []);
  assert.deepEqual(port.retired, []);
  assert.deepEqual(port.renewals, []);
});

test('only a matching registration can finish its browser session', async () => {
  // The signup completion is admitted only for an opening that began signed out.
  const port = await accountPort({signedIn: false, registration: {registrationId: 'registration_123'}});
  assert.equal(port.service.getSnapshot().phase, 'signedOut');
  assert.equal(await verifyEmail(port.account, token(), new AbortController().signal), 'confirmed');
  assert.equal(port.service.getSnapshot().phase, 'ready');
  assert.equal(port.service.getSnapshot().session?.familyId, 'family_new');
  assert.equal(port.registrationCalls.length, 1);
  assert.deepEqual(port.verifyCalls.map(call => call.slice(0, 2)), [['registration_123', {code: '482917'}]]);
  assert.deepEqual(port.requests.map(call => call.slice(0, 2)), [['/v1/email-links/confirm', 'POST']]);
  assert.deepEqual(port.renewals, []);
});

test('an email change retires only the reviewed family through the canonical coordinator', async () => {
  const port = await accountPort();
  assert.equal(await verifyEmail(port.account, token('email_change'), new AbortController().signal), 'changed');
  // A mailed email change uses the same public capability as any other link ACK.
  assert.deepEqual(port.requests.map(call => call.slice(0, 2)), [['/v1/email-links/confirm', 'POST']]);
  assert.deepEqual(port.retired, [{familyId: 'family_a'}]);
  assert.equal(port.service.getSnapshot().phase, 'signedOut');
  assert.equal(port.service.getSnapshot().session, undefined);
  assert.deepEqual(port.renewals, []);
});

test('an email confirmation cannot sign out a family that replaced the initiating account', async () => {
  const entered = deferred(), answer = deferred();
  const port = await accountPort({confirm: () => {entered.resolve(undefined); return answer.promise;}});
  const pending = verifyEmail(port.account, token('email_change'), new AbortController().signal);
  await entered.promise;
  await port.service.adopt(port.session('family_b'));
  // The deliberate switch retires the superseded original family …
  assert.deepEqual(port.retired, [{familyId: 'family_a'}]);
  answer.resolve({kind: 'email_change', status: 'email_changed', accountId: 'account_a'});
  // … and the held original ACK adds no retirement, so the successor keeps its place.
  assert.equal(await pending, 'changed');
  assert.deepEqual(port.retired, [{familyId: 'family_a'}]);
  assert.equal(port.service.getSnapshot().session?.familyId, 'family_b');
  assert.deepEqual(port.renewals, []);
});
