import assert from 'node:assert/strict';
import test from 'node:test';
import type {LocalSession} from '../../../packages/client-core/src/index.ts';
import type {AppServerSession} from '../../../packages/client-core/src/app-sessions.ts';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management.ts';
import {createNativeShellAccountBinding} from '../src-next/shell/account-binding.ts';

function fixture(portico = true) {
  const pin = {serverId: 'server', publicKey: 'original-key', fingerprint: 'original-fingerprint'};
  const session: LocalSession = {accessToken: 'test-access', expiresAt: '2026-10-25T22:00:00Z',
    sessionFamilyId: 'original-family', tokenGeneration: '3', authorizationHorizon: '2026-10-26T22:00:00Z',
    deviceId: 'device', installationId: 'installation',
    viewer: {authority: 'local', serverId: 'server', accountId: 'local-member', profileId: 'profile', role: 'member'}};
  const selected: AppServerSession = {signIn: portico ? 'portico' : 'local', connection: {
    version: '1', selectionId: 'original-selection', logicalOrigin: 'https://server.test', pin, session, routes: {paired: []},
    ...(portico ? {hosted: {origin: 'https://accounts.test', accountId: 'portico-account', key: {keyId: 'route-key', publicKey: 'route-public'}}} : {}),
  }};
  const state = {current: true, session, pin, selected: selected as AppServerSession | undefined,
    read: async (): Promise<AppServerSession | undefined> => state.selected};
  const binding = createNativeShellAccountBinding(selected, session, {current: () => state.current,
    session: () => state.session, pin: () => state.pin, readSelected: () => state.read()});
  const snapshot: DirectSnapshot = {authority: 'local', serverId: 'server', canManage: false,
    capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: 'local-member', username: 'actual-member', primaryProfileId: 'profile', role: 'member', revision: 1,
      disabled: false, allowedLibraries: ['movies'], ...(portico ? {hostedAccountId: 'portico-account'} : {})},
    profiles: [{id: 'profile', name: 'Actual profile', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false}]};
  return {binding, state, selected, session, snapshot};
}

test('Portico-linked local snapshots use the real local role and linkage without a Hosted credential client', async () => {
  const f = fixture();
  await f.binding.assertCurrent();
  assert.doesNotThrow(() => f.binding.assertAccount(f.snapshot));
  for (const hostedAccountId of [undefined, 'other-account']) {
    assert.throws(() => f.binding.assertAccount({...f.snapshot, account: {...f.snapshot.account, hostedAccountId}}), {code: 'identity_mismatch'});
  }
  assert.throws(() => f.binding.assertAccount({...f.snapshot, account: {...f.snapshot.account, role: 'owner'}}), {code: 'identity_mismatch'});
  assert.throws(() => f.binding.assertAccount({...f.snapshot, account: {...f.snapshot.account, disabled: true}}), {code: 'identity_mismatch'});
});

test('ordinary local sessions have no Portico credential or linkage dependency', async () => {
  const f = fixture(false);
  await f.binding.assertCurrent();
  assert.equal(f.binding.hostedAccountId, undefined);
  assert.doesNotThrow(() => f.binding.assertAccount(f.snapshot));
});

test('original pin, family and authorization epoch cannot be borrowed from a replacement selection', async () => {
  for (const change of [
    (f: ReturnType<typeof fixture>) => {f.state.pin = {...f.state.pin, publicKey: 'other-key'};},
    (f: ReturnType<typeof fixture>) => {f.state.session = {...f.session, sessionFamilyId: 'other-family'};},
    (f: ReturnType<typeof fixture>) => {f.state.session = {...f.session, authorizationHorizon: '2026-10-27T22:00:00Z'};},
    (f: ReturnType<typeof fixture>) => {f.state.session = {...f.session, viewer: {...f.session.viewer, profileId: 'other-profile'}};},
  ]) {
    const f = fixture(); change(f);
    await assert.rejects(f.binding.assertCurrent(), {name: 'AbortError'});
    assert.throws(() => f.binding.assertAccount(f.snapshot), {name: 'AbortError'});
  }
});

test('late protected reads retain the original linkage and opening', async () => {
  const f = fixture();
  let resolve!: (value: AppServerSession | undefined) => void;
  f.state.read = () => new Promise(yes => {resolve = yes;});
  const pending = f.binding.assertCurrent();
  resolve({...f.selected, connection: {...f.selected.connection, hosted: {...f.selected.connection.hosted!, accountId: 'other-account'}}});
  await assert.rejects(pending, {code: 'identity_mismatch'});
  const next = f.binding.assertCurrent();
  f.state.current = false; resolve(f.selected);
  await assert.rejects(next, {name: 'AbortError'});
});

test('same-epoch token rotation is allowed, a token-generation rollback is refused', async () => {
  const f = fixture();
  f.state.session = {...f.session, tokenGeneration: '4'};
  f.state.selected = {...f.selected, connection: {...f.selected.connection, session: f.state.session}};
  await f.binding.assertCurrent();
  f.state.selected = {...f.selected, connection: {...f.selected.connection, session: {...f.session, tokenGeneration: '2'}}};
  await assert.rejects(f.binding.assertCurrent(), {code: 'identity_mismatch'});
});

// E-tv-41: switching from the owner's main profile to Guest verifies the account snapshot read
// under the new Guest session. The server reports that snapshot's account role as the session's
// role (member), so comparing it to the owner binding's role refused a valid switch. The cross-
// profile check is identity only; the issued session's own role is checked against the snapshot.
test('E-tv-41: a snapshot read under another profile of the same account passes the identity check', async () => {
  const f = fixture();
  const owner = {...f.session, viewer: {...f.session.viewer, role: 'owner' as const}};
  const binding = createNativeShellAccountBinding(f.selected, owner, {current: () => true,
    session: () => owner, pin: () => f.state.pin, readSelected: async () => f.selected});
  const underGuest = {...f.snapshot, account: {...f.snapshot.account, role: 'member' as const}};
  assert.doesNotThrow(() => binding.assertSameAccount(underGuest));
  for (const change of [{id: 'other-account'}, {disabled: true}, {hostedAccountId: 'other-portico'}])
    assert.throws(() => binding.assertSameAccount({...underGuest, account: {...underGuest.account, ...change}}), {code: 'identity_mismatch'});
  assert.throws(() => binding.assertSameAccount({...underGuest, serverId: 'other-server'}), {code: 'identity_mismatch'});
});
