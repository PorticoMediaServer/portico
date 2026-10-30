import assert from 'node:assert/strict';
import {test} from 'node:test';
import {ServicesBootstrap} from '../src/services/bootstrap.ts';
import {hostedServicesPage} from '../src/services/mode.ts';
import type {ServicesAccount} from '../src/services/session.ts';
import type {ServicesLinkStore} from '../src/services/intake.ts';

test('web.getportico.tv starts the Services entry only to capture a link (H14)', async () => {
  const page = (pathname: string, search = '', hash = '') => ({pathname, search, hash});
  for (const [path, search, hash] of [['/claim', '', '#code=x'], ['/join', '?code=abc', ''], ['/join', '', '#abc'], ['/reset', '?token=x', ''], ['/reset-password', '', '#t'], ['/verify-email', '?token=x', ''], ['/device', '?code=ABCD', ''],
    ['/account', '?securityIntent=x', ''], ['/account', '?identityTransaction=x', ''], ['/app/account-return', '', ''], ['/', '', '#signIn=abc'], ['/', '', '#join'], ['/', '?mode=forgot', ''], ['/', '?identityError=google', ''], ['/servers', '', '#signIn=abc']] as const)
    assert.equal(await hostedServicesPage(page(path, search, hash)), true, path + search + hash);
  for (const [path, search] of [['/', ''], ['/account', ''], ['/account', '?tab=security'], ['/device', ''], ['/join', ''], ['/sign-in', ''], ['/sign-in', '?x=1'], ['/welcome', ''], ['/servers', ''], ['/settings', ''], ['/item/x', ''], ['/profiles', '']] as const)
    assert.equal(await hostedServicesPage(page(path, search)), false, path + search);
});

function ports(signedIn = false) {
  const calls: string[] = [];
  const account = {service: {
    synchronize: async () => { calls.push('cookie-synchronize'); },
    getSnapshot: () => ({session: signedIn ? {account: {id: 'current'}, familyId: 'family'} : undefined}),
    accessSession: async () => { calls.push('cookie-renew'); },
  }} as unknown as Pick<ServicesAccount, 'service'>;
  const store: ServicesLinkStore = {read: async () => { calls.push('protected-read'); return undefined; }, change: async () => { calls.push('protected-write'); }};
  return {account, store, calls};
}

test('signed-out startup restores only the protected link and canonical cookie coordinator', async () => {
  const p = ports(), boot = new ServicesBootstrap(p.account, () => p.store);
  await boot.start();
  assert.deepEqual(p.calls, ['protected-read', 'cookie-synchronize']);
  assert.equal(boot.getSnapshot().phase, 'ready'); boot.dispose();
});

test('a captured link is saved before cookie work and existing sessions renew through the singleton', async () => {
  const p = ports(true), link = {version: 1, kind: 'device', value: '7KQM-4TXP', createdAt: Date.now()} as const;
  const boot = new ServicesBootstrap(p.account, () => p.store, {kind: 'device', link});
  await boot.start();
  assert.deepEqual(p.calls, ['protected-write', 'cookie-synchronize', 'cookie-renew']);
  assert.deepEqual(boot.getSnapshot().link, link); boot.dispose();
});

test('a protected-store failure cannot fall back to plaintext or start account effects', async () => {
  const p = ports(), failure = new Error('storage unavailable');
  const boot = new ServicesBootstrap(p.account, () => { throw failure; });
  await boot.start();
  assert.equal(boot.getSnapshot().phase, 'error'); assert.equal(boot.getSnapshot().error, failure);
  assert.deepEqual(p.calls, []); boot.dispose();
});

test('disposed startup cannot renew or publish a late restored continuation', async () => {
  const p = ports(true); let resolve!: () => void;
  p.store.read = () => new Promise(done => { resolve = () => done(undefined); });
  const boot = new ServicesBootstrap(p.account, () => p.store), flight = boot.start();
  const before = boot.getSnapshot(); boot.dispose(); resolve(); await flight;
  assert.equal(boot.getSnapshot(), before); assert.deepEqual(p.calls, []);
});
