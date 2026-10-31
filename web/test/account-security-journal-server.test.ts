import assert from 'node:assert/strict';
import test from 'node:test';
import {parseSecurityJournal} from '../src/platform/account-security-journal.ts';
import {clone, hostedFixture, iso} from './support/account-security-journal-fixture.ts';

// A server-account Security return is bound to the server sign-in, not its token: the family's
// horizon bounds the record, the server token's own expiry (a refresh replaces it) does not.
function serverRecord(serverTokenUntil: string, horizon: string) {
  const f = hostedFixture();
  const original = {...clone(f.original), kind: 'server-account' as const, server: {revision: 1, selectionId: 's'.repeat(32),
    pin: {serverId: 'srv_fixture', publicKey: Buffer.alloc(32, 1).toString('base64url'), fingerprint: Buffer.alloc(32, 2).toString('base64url')},
    session: {accessToken: 'b'.repeat(43), expiresAt: serverTokenUntil, authorizationHorizon: horizon, sessionFamilyId: 'server_family', tokenGeneration: '1',
      viewer: {accountId: 'owner', profileId: 'profile', serverId: 'srv_fixture', authority: 'local', role: 'owner'}}}};
  return {original};
}

test('a server Security return may outlive the server token, never the family horizon', () => {
  // The return lasts two minutes; the server token ends in one, the family in an hour.
  assert.equal(parseSecurityJournal(serverRecord(iso(60000), iso(3600000))).original.kind, 'server-account');
  assert.throws(() => parseSecurityJournal(serverRecord(iso(60000), iso(60000))), 'a family horizon before the return ends it');
});
