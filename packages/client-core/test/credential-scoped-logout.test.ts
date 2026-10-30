import test from 'node:test';
import assert from 'node:assert/strict';
import {CredentialSessionService, type CredentialRecord, type HostedSession} from '../src/index.ts';

const session = (name: string): HostedSession => ({
  accessToken: `access-${name}`, refreshToken: `refresh-${name}`, familyId: `family-${name}`,
  expiresAt: new Date(Date.now() + 3600000).toISOString(),
  refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
  account: {id: name, username: name, displayName: name},
});
const scopeA = {accountId: 'A', familyId: 'family-A'};
function fixture() {
  let record: CredentialRecord | undefined;
  const revoked: string[] = [];
  let failSave = false;
  let failRevoke = false;
  const service = new CredentialSessionService({
    authorityFromSession: value => value.refreshToken!,
    refresh: async () => {throw new Error('Unexpected refresh');},
    revoke: async token => {revoked.push(token); if (failRevoke) throw new Error('Offline');},
  }, {
    load: async () => structuredClone(record),
    save: async value => {if (failSave) throw new Error('Secure storage unavailable'); record = structuredClone(value);},
  }, async () => 'request-id');
  return {service, revoked, record: () => record, failSave: () => {failSave = true;}, failRevoke: () => {failRevoke = true;}};
}

test('scoped cleanup preserves adoption started by the signed-out subscriber', async () => {
  const f = fixture(); await f.service.adopt(session('A'));
  let replacement: Promise<void> | undefined;
  let triggered = false;
  const unsubscribe = f.service.subscribe(() => {
    if (!triggered && f.service.getSnapshot().phase === 'signedOut') {
      triggered = true;
      replacement = f.service.adopt(session('B'));
    }
  });
  assert.equal(await f.service.logoutIfCurrent(scopeA), true);
  await replacement; unsubscribe();
  assert.equal(f.service.getSnapshot().session?.account.id, 'B');
  assert.equal(f.record()?.active?.session.familyId, 'family-B');
  assert.ok(f.revoked.includes('refresh-A'));
  assert.ok(!f.revoked.includes('refresh-B'));
});

test('scoped cleanup does not cancel an adoption queued immediately after it', async () => {
  const f = fixture(); await f.service.adopt(session('A'));
  const cleanup = f.service.logoutIfCurrent(scopeA);
  const replacement = f.service.adopt(session('B'));
  await Promise.all([cleanup, replacement]);
  assert.equal(f.service.getSnapshot().session?.familyId, 'family-B');
  assert.equal(f.record()?.active?.session.account.id, 'B');
  assert.ok(!f.revoked.includes('refresh-B'));
});

test('late cleanup for a previous family is a read-only no-op', async () => {
  const f = fixture(); await f.service.adopt(session('B'));
  const before = structuredClone(f.record());
  assert.equal(await f.service.logoutIfCurrent(scopeA), false);
  assert.deepEqual(f.record(), before);
  assert.deepEqual(f.revoked, []);
  assert.equal(f.service.getSnapshot().session?.account.id, 'B');
});

test('scoped cleanup persists old-family revocation while offline without clearing replacement', async () => {
  const f = fixture(); await f.service.adopt(session('A')); f.failRevoke();
  const cleanup = f.service.logoutIfCurrent(scopeA);
  const replacement = f.service.adopt(session('B'));
  await Promise.all([cleanup, replacement]);
  assert.equal(f.record()?.active?.session.familyId, 'family-B');
  assert.deepEqual(f.record()?.revocations, ['refresh-A']);
  assert.ok(!f.revoked.includes('refresh-B'));
});

test('scoped cleanup reports secure-storage failure without restoring the ended session', async () => {
  const f = fixture(); await f.service.adopt(session('A')); f.failSave();
  await assert.rejects(f.service.logoutIfCurrent(scopeA), /Secure storage unavailable/);
  assert.equal(f.service.getSnapshot().session, undefined);
  assert.equal(f.service.getSnapshot().phase, 'signedOut');
  assert.equal(f.service.getSnapshot().pendingRevocations, 1);
  assert.deepEqual(f.revoked, []);
});
