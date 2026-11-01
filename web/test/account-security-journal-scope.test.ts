import assert from 'node:assert/strict';
import test from 'node:test';
import {AccountSecurityJournal} from '../src/platform/account-security-journal.ts';
import {clone, hostedFixture, iso} from './support/account-security-journal-fixture.ts';

// ACC-LEAF-27: the closed scope and finite-expiry boundaries of the journal
// parser, authored UNRUN. Every record is a canonical DTO clone; this is data
// validation, never signature or accepting-authority proof.

const journalOf = (record: unknown) => new AccountSecurityJournal({read: async () => record, change: async () => {}, assertAvailable: () => {}});

test('the canonical Hosted DTO parses with its actual provider revision', async () => {
  const f = hostedFixture();
  const parsed = await journalOf(clone(f.pending)).read();
  assert.equal(parsed && 'original' in parsed ? parsed.original.provider.revision : undefined, '1');
});

test('a missing, zero, negative, noncanonical, overflowing or mismatched revision refuses', async () => {
  const f = hostedFixture();
  const variants: Record<string, unknown>[] = [];
  const withoutRevision = clone(f.pending); delete (withoutRevision.original.provider as Record<string, unknown>).revision; variants.push(withoutRevision);
  for (const revision of ['0', '-1', '01', '9223372036854775808', '9'.repeat(20)]) {
    const value = clone(f.pending); value.original.provider.revision = revision; variants.push(value);
  }
  const mismatched = clone(f.pending); mismatched.original.provider.revision = '2'; variants.push(mismatched);
  for (const variant of variants) await assert.rejects(journalOf(variant).read(), JSON.stringify(variant.original.provider));
});

test('hasPassword, a foreign coordinator or a pending renewal refuses', async () => {
  const f = hostedFixture();
  const withPassword = clone(f.pending); withPassword.original.state.hasPassword = true;
  const foreignAccount = clone(f.pending);
  const accountCoordinator = JSON.parse(foreignAccount.original.coordinator);
  accountCoordinator.active.session.account.id = 'acct_other';
  foreignAccount.original.coordinator = JSON.stringify(accountCoordinator);
  const foreignSession = clone(f.pending);
  const sessionCoordinator = JSON.parse(foreignSession.original.coordinator);
  // Move the coordinator's session and authority family together, so the only
  // difference left is the family mismatch the journal is meant to catch.
  sessionCoordinator.active.session.familyId = 'family_other';
  sessionCoordinator.active.authority.familyId = 'family_other';
  foreignSession.original.coordinator = JSON.stringify(sessionCoordinator);
  const pendingRenewal = clone(f.pending);
  const renewal = JSON.parse(pendingRenewal.original.coordinator);
  renewal.active.pendingRequestId = '11111111-1111-4111-8111-111111111111';
  pendingRenewal.original.coordinator = JSON.stringify(renewal);
  const revoked = clone(f.pending);
  const revokedCoordinator = JSON.parse(revoked.original.coordinator);
  revokedCoordinator.revocations = [{familyId: f.session.familyId}];
  revoked.original.coordinator = JSON.stringify(revokedCoordinator);
  for (const variant of [withPassword, foreignAccount, foreignSession, pendingRenewal, revoked]) {
    await assert.rejects(journalOf(variant).read(), JSON.stringify(variant.original.state.providers));
  }
});

test('a non-credential purpose, a Server Settings extra field or a refresh credential refuses', async () => {
  const f = hostedFixture();
  const purpose = clone(f.pending); (purpose.original.intent as {purpose: string}).purpose = 'playback';
  const extra = clone(f.pending); (extra.original as Record<string, unknown>).serverSettings = 'on';
  const refresh = clone(f.pending); (refresh.original.session as Record<string, unknown>).refreshToken = 'r'.repeat(43);
  for (const variant of [purpose, extra, refresh]) await assert.rejects(journalOf(variant).read(), JSON.stringify(variant.original.intent));
});

test('an expiry beyond five minutes, before creation, or future creation refuses', async () => {
  const f = hostedFixture();
  const long = clone(f.pending); long.original.expiresAt = iso(400000);
  const backwards = clone(f.pending); backwards.original.expiresAt = iso(-1000); backwards.original.createdAt = iso(0);
  const future = clone(f.pending); future.original.createdAt = iso(120000); future.original.expiresAt = iso(260000);
  for (const variant of [long, backwards, future]) await assert.rejects(journalOf(variant).read(), JSON.stringify({createdAt: variant.original.createdAt, expiresAt: variant.original.expiresAt}));
});

test('an expired original refuses an ordinary read but is returned for exact cleanup', async () => {
  const f = hostedFixture();
  const expired = clone(f.pending);
  expired.original.createdAt = iso(-300000); expired.original.expiresAt = iso(-1000);
  await assert.rejects(journalOf(expired).read());
  const parsed = await journalOf(expired).read(true);
  assert.equal(parsed && 'original' in parsed ? parsed.original.openingId : undefined, f.original.openingId);
});
