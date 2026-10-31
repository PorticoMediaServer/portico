import test from 'node:test';
import assert from 'node:assert/strict';
import {setupAttemptBound, setupOwnerGrantFor} from '../src/setup-custody.ts';
import type {SetupAttempt, SetupRecord} from '../src/setup-store.ts';

const record: SetupRecord = {version: 2, serverId: 'srv', requestId: 'req', setupToken: 'tok', tokenAt: 1, name: 'Den', authMode: 'hosted'};
const pin = {serverId: 'srv', publicKey: 'pk', fingerprint: 'fp'};
const handoff = {expected: {operationId: 'op', localGeneration: '1', revision: '2'}, binding: {pin, origin: 'http://192.168.1.20:32400', hostedOrigin: 'https://hosted.example',
  operationId: 'op', localGeneration: '1', setupRequestId: 'req', setupRevision: '2', state: 's', codeChallenge: 'c', installationId: 'inst'},
  transactionId: 'tx', expiresAt: '2030-01-01T00:30:00Z', claim: {payload: 'p', signature: 's'}, start: {payload: 'p', signature: 's'}};
const attempt: SetupAttempt = {version: 2, serverId: 'srv', requestId: 'req', attemptId: 'a', origin: 'http://192.168.1.20:32400', hostedOrigin: 'https://hosted.example',
  installationId: 'inst', state: 's', verifier: 'v', codeChallenge: 'c', createdAt: 1, expiresAt: '2030-01-01T00:30:00Z', handoff};

test('a handoff counts only when bound to exactly this attempt and setup record', () => {
  assert.equal(setupAttemptBound(attempt, record), true);
  for (const [label, changed] of [
    ['no handoff', {...attempt, handoff: undefined}], ['another state', {...attempt, state: 'x'}], ['another challenge', {...attempt, codeChallenge: 'x'}],
    ['another installation', {...attempt, installationId: 'x'}], ['another page', {...attempt, origin: 'http://192.168.1.21:32400'}],
    ['a longer window than the handoff', {...attempt, expiresAt: '2030-01-01T00:31:00Z'}],
    ['another operation in the binding', {...attempt, handoff: {...handoff, binding: {...handoff.binding, operationId: 'other'}}}],
  ] as const) assert.equal(setupAttemptBound(changed as SetupAttempt, record), false, label);
  assert.equal(setupAttemptBound(attempt, {...record, requestId: 'other'}), false, 'another setup');
});

test('only an enabled Portico Account owner on the main profile, in a live session for this installation and server, is admitted', () => {
  const grant = {kind: 'signed-in', snapshot: {serverId: 'srv', account: {role: 'owner', disabled: false, hostedAccountId: 'acct', primaryProfileId: 'main'}},
    session: {viewer: {role: 'owner', serverId: 'srv', profileId: 'main'}, installationId: 'inst', serverIdentity: {publicKey: 'pk', fingerprint: 'fp'}, expiresAt: '2030-01-02T00:00:00Z'}} as never;
  const now = Date.parse('2030-01-01T00:00:00Z'), bound = attempt as SetupAttempt & {handoff: typeof handoff};
  assert.equal(setupOwnerGrantFor(grant, bound, now), true);
  const g = grant as {snapshot: {account: object}; session: object};
  for (const [label, changed] of [
    ['local owner', {...g, snapshot: {...g.snapshot, account: {...g.snapshot.account, hostedAccountId: undefined}}}],
    ['another installation', {...g, session: {...g.session, installationId: 'x'}}],
    ['another server key', {...g, session: {...g.session, serverIdentity: {publicKey: 'x', fingerprint: 'fp'}}}],
    ['expired', {...g, session: {...g.session, expiresAt: '2029-01-01T00:00:00Z'}}],
  ] as const) assert.equal(setupOwnerGrantFor(changed as never, bound, now), false, label);
});
