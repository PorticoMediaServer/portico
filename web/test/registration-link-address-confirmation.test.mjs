import assert from 'node:assert/strict';
import test from 'node:test';
import {componentModule} from './helpers/component-harness.mjs';
import {CredentialSessionService} from '../../packages/client-core/src/credentials.ts';

// FX-199: the blocker's shape on this repo's own coordinator. A mailed signup
// link proves its address through the public capability first; adopting the
// original private registration is a separate, optional step that the real
// CredentialSessionService admits only for an opening that began signed out and
// stayed that way. Families appear here through the service's own published
// transitions (adopt/logout), never by editing hidden fixture storage.

class ApiError extends Error {
  constructor(status, code, message, retryable) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryable = retryable;
  }
}

const sessionFor = (id, familyId) => ({
  account: {id, username: id, displayName: id}, familyId, accessToken: 'fixture_access',
  expiresAt: new Date(Date.now() + 3600000).toISOString(),
  refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
});
const EXISTING = sessionFor('existing_account', 'existing_family');
const ADOPTED = sessionFor('adopted_account', 'adopted_family');
const COMPLETED = sessionFor('new_account', 'new_family');
const token = Buffer.from(JSON.stringify(['1', 'registration', 'registration_123', '482917'])).toString('base64url');
const unused = async () => assert.fail('unexpected coordinator work');
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return {promise, resolve}; };

async function harness({active = null, registration = {registrationId: 'registration_123'}, confirm, verify, restoring = false} = {}) {
  let record = {version: 1, active: null, revocations: []};
  let releaseLoad;
  const heldLoad = restoring ? new Promise(done => { releaseLoad = done; }) : undefined;
  const service = new CredentialSessionService(
    {authorityFromSession: value => ({familyId: value.familyId}), refresh: unused, revoke: async () => {}},
    {
      // A held load keeps the coordinator in its real "restoring" phase until the
      // test releases it; it never fabricates a signed-out snapshot.
      load: async () => { if (heldLoad) await heldLoad; return structuredClone(record); },
      save: async value => { record = structuredClone(value); },
    },
  );
  if (!restoring) await service.synchronize();
  if (active) await service.adopt(active);
  const calls = [];
  const client = {
    registration: async () => { calls.push({name: 'registration'}); return registration; },
    verifyRegistration: async (id, input) => { calls.push({name: 'verifyRegistration', id, input}); return (verify || (async () => COMPLETED))(id, input); },
    request: async (path, method, body) => { calls.push({name: 'public', path, method, body}); return (confirm || (() => ({kind: 'registration', status: 'confirmed'})))(); },
  };
  return {service, calls, account: {service, client}, token,
    adopted: () => service.getSnapshot().session,
    adopt: next => service.adopt(next),
    releaseLoad: () => releaseLoad?.(),
    logout: () => service.logout()};
}

async function load() {
  const fragments = await componentModule(new URL('../src/services/fragments.ts', import.meta.url), {});
  return componentModule(new URL('../src/services/link-effects.ts', import.meta.url), {
    react: {default: {}},
    '@core/index.ts': {ApiError},
    './fragments': fragments,
    './session': {accountIdentity: value => ({accountId: value.account.id, familyId: value.familyId}), currentAccountWork: unused},
    '../../../packages/contracts/src/hosted/routes.ts': {
      routePath: id => ({email_link_confirm: '/v1/email-links/confirm', account_security_email_confirm: '/v1/account/security/email/confirm', account_recovery_complete: '/v1/account/recovery/complete'})[id],
      routeBody: (_id, body) => body,
    },
  });
}

// Starts the proof and stops it inside the held public confirmation, so the
// service can publish a real family transition before that confirmation lands.
async function holdProof(verifyEmail, options, signal = new AbortController().signal) {
  const proof = deferred(), entered = deferred();
  const f = await harness({...options, confirm: () => { entered.resolve(); return proof.promise; }});
  const pending = verifyEmail(f.account, f.token, signal);
  await entered.promise;
  return {
    f, pending,
    land: async value => { proof.resolve(value); return pending; },
  };
}

test('an already signed-in family keeps its place and the confirmed-elsewhere result', async () => {
  const {verifyEmail} = await load();
  const f = await harness({active: EXISTING});
  assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'elsewhere');
  assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public']);
  assert.equal(f.calls[1].path, '/v1/email-links/confirm');
  assert.equal(f.calls[1].method, 'POST');
  assert.deepEqual(f.calls[1].body, {token: f.token});
  assert.equal(f.adopted().familyId, 'existing_family');
});

test('the original signed-out browser still confirms the address and completes signup', async () => {
  const {verifyEmail} = await load();
  const f = await harness({active: null});
  assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'confirmed');
  assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public', 'verifyRegistration']);
  assert.equal(f.calls[2].id, 'registration_123');          // the same captured registration
  assert.deepEqual(f.calls[2].input, {code: '482917'});
  assert.equal(f.adopted().familyId, 'new_family');
});

test('a family adopted while the proof is in flight keeps its place', async () => {
  const {verifyEmail} = await load();
  const held = await holdProof(verifyEmail, {active: null});
  const f = held.f;
  await f.adopt(ADOPTED);                                   // a real published transition
  assert.equal(await held.land({kind: 'registration', status: 'confirmed'}), 'elsewhere');
  assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public']);
  assert.equal(f.adopted().familyId, 'adopted_family');
});

test('a sign-out during the proof cannot turn an active opening into a signed-out one', async () => {
  const {verifyEmail} = await load();
  const held = await holdProof(verifyEmail, {active: EXISTING});
  const f = held.f;
  await f.logout();                                          // back to signed out, but the opening began signed in
  assert.equal(await held.land({kind: 'registration', status: 'confirmed'}), 'elsewhere');
  assert.equal(f.calls.some(call => call.name === 'verifyRegistration'), false);
  assert.equal(f.adopted(), undefined);
});

test('signed-out, adopted, then signed-out again stays permanently retired', async () => {
  const {verifyEmail} = await load();
  const held = await holdProof(verifyEmail, {active: null});
  const f = held.f;
  await f.adopt(ADOPTED);
  await f.logout();                                          // the ABA that used to look signed out
  assert.equal(await held.land({kind: 'registration', status: 'confirmed'}), 'elsewhere');
  assert.equal(f.calls.some(call => call.name === 'verifyRegistration'), false);
  assert.equal(f.adopted(), undefined);
});

test('a replaced family during the proof keeps the successor', async () => {
  const {verifyEmail} = await load();
  const held = await holdProof(verifyEmail, {active: null});
  const f = held.f;
  await f.adopt(EXISTING);
  await f.adopt(ADOPTED);                                     // another tab replaces the family
  assert.equal(await held.land({kind: 'registration', status: 'confirmed'}), 'elsewhere');
  assert.equal(f.adopted().familyId, 'adopted_family');
  assert.equal(f.calls.some(call => call.name === 'verifyRegistration'), false);
});

test('an unknown or still-restoring opening never completes signup', async () => {
  const {verifyEmail} = await load();
  const f = await harness({active: null, restoring: true});   // phase "restoring", no confirmed sign-out
  assert.equal(f.service.getSnapshot().phase, 'restoring');
  assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'elsewhere');
  assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public']);
  assert.equal(f.adopted(), undefined);
  // Control: the same link completes once the same browser has a confirmed
  // signed-out opening, so the refusal above came from the captured phase.
  f.releaseLoad();
  await f.service.synchronize();
  assert.equal(f.service.getSnapshot().phase, 'signedOut');
  f.calls.length = 0;
  assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'confirmed');
  assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public', 'verifyRegistration']);
  assert.equal(f.adopted().familyId, 'new_family');
});

test('an abort during the held proof stops it before any completion', async () => {
  const {verifyEmail} = await load();
  const controller = new AbortController(), reason = new Error('cancelled');
  const held = await holdProof(verifyEmail, {active: null}, controller.signal);
  controller.abort(reason);
  await assert.rejects(held.land({kind: 'registration', status: 'confirmed'}), error => error === reason);
  assert.equal(held.f.calls.some(call => call.name === 'verifyRegistration'), false);
  assert.equal(held.f.adopted(), undefined);
});

test('the address proof still lands for a different browser or a successor registration', async () => {
  const {verifyEmail} = await load();
  for (const registration of [null, {registrationId: 'successor_registration'}]) {
    const f = await harness({active: null, registration});
    assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'elsewhere');
    assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public']);
    assert.equal(f.adopted(), undefined);                          // no foreign binding is adopted
  }
});

test('a replayed link confirms idempotently and cannot replace the family it first adopted', async () => {
  const {verifyEmail} = await load();
  let confirmed = 0;
  const f = await harness({active: null, confirm: () => ({kind: 'registration', status: confirmed++ ? 'already_confirmed' : 'confirmed'})});
  assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'confirmed');
  assert.equal(f.adopted().familyId, 'new_family');
  assert.equal(await verifyEmail(f.account, f.token, new AbortController().signal), 'elsewhere');
  assert.deepEqual(f.calls.map(call => call.name), ['registration', 'public', 'verifyRegistration', 'registration', 'public']);
  assert.equal(f.adopted().familyId, 'new_family');
});

test('an unverified proof never reaches completion or an address-confirmed result', async () => {
  const {verifyEmail} = await load();
  const expired = await harness({active: null, confirm: () => { throw new ApiError(400, 'invalid_link', 'invalid_link', false); }});
  await assert.rejects(verifyEmail(expired.account, expired.token, new AbortController().signal), {code: 'invalid_link'});
  assert.deepEqual(expired.calls.map(call => call.name), ['registration', 'public']);
  assert.equal(expired.adopted(), undefined);

  const wrongKind = await harness({active: null, confirm: () => ({kind: 'email_change', status: 'confirmed'})});
  await assert.rejects(verifyEmail(wrongKind.account, wrongKind.token, new AbortController().signal), {code: 'invalid_response'});
  assert.deepEqual(wrongKind.calls.map(call => call.name), ['registration', 'public']);
  assert.equal(wrongKind.adopted(), undefined);

  const refused = await harness({active: null, verify: async () => { throw new ApiError(409, 'username_taken', 'That username is taken.', false); }});
  await assert.rejects(verifyEmail(refused.account, refused.token, new AbortController().signal), {code: 'username_taken'});
  assert.deepEqual(refused.calls.map(call => call.name), ['registration', 'public', 'verifyRegistration']);
  assert.equal(refused.adopted(), undefined);                     // the confirmed address does not imply a session
});
