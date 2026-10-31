import assert from 'node:assert/strict';
import test from 'node:test';
import {securityCoordinatorHolds} from '../src/platform/account-security-journal.ts';
import {captureAccountSecurity} from '../src/platform/account-security.ts';
import {hostedFixture} from './support/account-security-journal-fixture.ts';

// A Portico Account Security return is bound to the sign-in, not to one stored session: another tab's routine
// refresh (which rewrites the browser's coordination record) keeps it; another sign-in, a revocation, a
// sign-out or a request in flight ends it. The return itself never refreshes; it starts with a whole window.

const iso = (offset: number) => new Date(Date.now() + offset).toISOString().replace(/\.\d{3}Z$/, 'Z');
function record(change: {expires?: number; family?: string; account?: string; revoked?: string[]; pending?: string; active?: boolean} = {}) {
  const family = change.family ?? 'family_fixture';
  const session = {account: {id: change.account ?? 'acct_fixture', username: 'fixture', displayName: 'Fixture'}, familyId: family, accessToken: '',
    expiresAt: iso(change.expires ?? 600000), refreshExpiresAt: iso(3600000)};
  return JSON.stringify({version: 1, revocations: (change.revoked ?? []).map(familyId => ({familyId})),
    ...(change.active === false ? {} : {active: {session, authority: {familyId: family}, context: {}, ...(change.pending ? {pendingRequestId: change.pending} : {})}})});
}

test('a refreshed coordination record keeps the Security return', () => {
  const captured = record();
  assert.equal(securityCoordinatorHolds(captured, captured), true);
  assert.equal(securityCoordinatorHolds(captured, record({expires: 900000})), true, 'the refresh rewrote the session expiry');
});

test('another family, account, a revocation, a sign-out or a request in flight ends it', () => {
  const captured = record();
  for (const [name, now] of Object.entries({
    family: record({family: 'family_other'}), account: record({account: 'acct_other'}), revoked: record({revoked: ['family_fixture']}),
    signedOut: record({active: false}), pending: record({pending: '11111111-1111-4111-8111-111111111111'}), missing: null, garbage: '{',
  })) assert.equal(securityCoordinatorHolds(captured, now), false, name);
});

test('capturing on a nearly expired token renews it first and gets the whole five minutes', async () => {
  const f = hostedFixture();
  const values = new Map<string, string>();
  (globalThis as unknown as {localStorage: unknown}).localStorage = {getItem: (k: string) => values.get(k) ?? null, setItem: (k: string, v: string) => {values.set(k, v);}};
  values.set('portico.account.v1:' + f.original.hostedOrigin, record());
  let forced = 0, expires = iso(120000);
  const session = () => ({...f.session, expiresAt: expires});
  const account = {api: {origin: f.original.hostedOrigin}, service: {
    synchronize: async () => {},
    getSnapshot: () => ({session: session()}),
    accessSession: async (force = false) => {if (force) {forced++; expires = iso(900000);} return session();},
  }};
  const journal = {assertAvailable() {}, create: async (pending: unknown) => pending};
  const before = Date.now();
  const pending = await captureAccountSecurity({account: account as never, identity: {accountId: f.session.account.id, familyId: f.session.familyId},
    state: f.state, purpose: 'password_set_or_change', target: '', providerId: f.state.providers[0].id, assertCurrent: () => {}}, journal as never);
  assert.equal(forced, 1, 'the ordinary refresh ran once, before capture');
  const window = Date.parse(pending.original.expiresAt) - Date.parse(pending.original.createdAt);
  assert.ok(window >= 299000 && window <= 300000, `the whole window: ${window} ms`);
  assert.ok(Date.parse(pending.original.createdAt) >= before - 1000);
});
