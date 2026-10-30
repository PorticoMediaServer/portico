import test from 'node:test';
import assert from 'node:assert/strict';
import {invitationCardFacts} from '../src/invitation-card.ts';

test('share-card facts contain only server, access and expiry facts', () => {
  const invitation = {expiresAt: '2026-11-01T00:00:00Z', allowedLibraries: ['films'], notes: 'Private note', porticoUsername: 'private-name', email: 'private@example.test'};
  assert.deepEqual(invitationCardFacts('Actual server', invitation, [{id: 'films', name: 'Movies'}]), {
    serverName: 'Actual server', expiresAt: invitation.expiresAt, libraryNames: ['Movies'], libraryCount: 1, allLibraries: false,
  });
});

test('unavailable library names do not turn a restricted invitation into All libraries', () => {
  assert.deepEqual(invitationCardFacts('Server', {expiresAt: '2026-11-01T00:00:00Z', allowedLibraries: ['missing']}, []), {
    serverName: 'Server', expiresAt: '2026-11-01T00:00:00Z', libraryNames: [], libraryCount: 1, allLibraries: false,
  });
  assert.equal(invitationCardFacts('Server', {expiresAt: '2026-11-01T00:00:00Z', allowedLibraries: []}, []).allLibraries, true);
});
