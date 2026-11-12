import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeMemberAccountMove} from '../src-next/platform/member-account-move';
import {setInstallationIdentity} from '../../../packages/client-core/src/installation';
import {parseServerSignIn} from '../../../packages/client-core/src/sign-in';
import {systemAnswer} from '../../../packages/client-core/test/next-wire';

test('the phone accepts the owner’s short move code and moves to local sign-in without Services', async () => {
  const installationId = 'i'.repeat(43), serverId = 'srv_' + 'A'.repeat(43), code = 'K7QP-2MXD-9HRT';
  setInstallationIdentity({installationId, name: 'Phone', platform: 'android', app: 'portico', appVersion: '1'});
  const calls: Array<{path: string; body: any}> = [];
  const move = new NativeMemberAccountMove({
    server: parseServerSignIn({...systemAnswer({id: serverId, name: 'Den', signIn: 'local'}), move: 'to_local'}, true),
    pin: {serverId, publicKey: 'A'.repeat(43), fingerprint: 'A'.repeat(43)}, crypto: {} as never,
    origin: 'https://den.example', installationId, signal: new AbortController().signal,
    isCurrent: () => true, assertCurrent: async () => {}, operationAccountFamilyId: () => undefined,
    captureHosted: async () => {throw new Error('a local move never contacts Services');},
    retireLateGrant: async () => {}, release: () => {},
    api: {request: async <T>(path: string, _method?: string, body?: unknown) => {
      calls.push({path, body});
      if (path === '/v1/auth/move/check') return {username: 'ada', next: 'choose_password'} as T;
      assert.equal(path, '/v1/auth/move/complete');
      return {session: {accessToken: 'new-access', refreshToken: 'new-refresh-credential', sessionFamilyId: 'new-family', tokenGeneration: '1', installationId,
        deviceId: 'device', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
        viewer: {authority: 'local', serverId, accountId: 'member', role: 'account'}},
        account: {id: 'member', username: 'ada', role: 'member'}, profiles: [{id: 'main', name: 'Ada', color: 'blue', isPrimary: true, pinRequired: true}]} as T;
    }},
    adopt: async result => {assert.equal(result.kind, 'signed-in'); return {result: 'admitted', isCurrent: () => true};},
  }, 'code');
  try {
    assert.equal(move.readCodeInput(code), code);
    assert.equal(move.readCodeInput('k7qp2mxd9hrt'), 'k7qp2mxd9hrt');
    assert.equal(move.readCodeInput('K7QP-2MXD'), undefined);
    await move.previewCode(code);
    assert.equal(move.getSnapshot().phase, 'local'); assert.equal(move.getSnapshot().username, 'ada');
    await move.redeemLocal('ada', 'ordinary new password');
    assert.deepEqual(calls.map(call => call.path), ['/v1/auth/move/check', '/v1/auth/move/complete']);
    assert.equal(calls[1].body.device.installationId, installationId);
    assert.equal(move.takeAcceptedResult(), 'admitted');
    assert.doesNotMatch(JSON.stringify(move.getSnapshot()), /K7QP/);
  } finally {move.dispose(); setInstallationIdentity(undefined);}
});


test('the phone checks the old password on its server, then requires a named confirmation before the note and move', async () => {
  const installationId = 'i'.repeat(43), serverId = 'srv_' + 'A'.repeat(43);
  setInstallationIdentity({installationId, name: 'Phone', platform: 'android', app: 'portico', appVersion: '1'});
  const calls: Array<{path: string; body?: any}> = [], accountId = 'acct_portico';
  let currentAccount = true;
  const move = new NativeMemberAccountMove({
    server: parseServerSignIn({...systemAnswer({id: serverId, name: 'Den', signIn: 'portico'}), move: 'to_portico'}, true),
    pin: {serverId, publicKey: 'A'.repeat(43), fingerprint: 'A'.repeat(43)}, crypto: {} as never,
    origin: 'https://den.example', installationId, signal: new AbortController().signal,
    isCurrent: () => true, assertCurrent: async () => {}, operationAccountFamilyId: () => undefined,
    captureHosted: async () => ({origin: 'https://web.getportico.tv', accountId, username: 'portico_ada', familyId: 'held-family',
      isCurrent: () => currentAccount, assertCurrent: async () => {}, identity: async challenge => {
        calls.push({path: 'Services:identity', body: {challenge}});
        return {identity: {payload: btoa(JSON.stringify({accountId, serverId, challenge})).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')}};
      }}),
    retireLateGrant: async () => {}, release: () => {},
    api: {request: async <T>(path: string, _method?: string, body?: unknown) => {
      calls.push({path, body});
      if (path === '/v1/auth/move/check') return {username: 'local_ada', next: 'portico_account', ticket: 'ticketfromtheserverabcdefghijklmnop'} as T;
      if (path === '/v1/auth/portico/challenge') return {challenge: 'fresh-server-challenge', serverId} as T;
      assert.equal(path, '/v1/auth/move/complete');
      return {session: {accessToken: 'new-access', refreshToken: 'new-refresh-credential', sessionFamilyId: 'new-family', tokenGeneration: '1', installationId,
        deviceId: 'device', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
        viewer: {authority: 'local', serverId, accountId: 'member', role: 'account'}},
        account: {id: 'member', username: 'local_ada', role: 'member'}, profiles: [{id: 'main', name: 'Ada', color: 'blue', isPrimary: true, pinRequired: true}]} as T;
    }},
    adopt: async result => {assert.equal(result.kind, 'signed-in'); return {result: 'admitted', isCurrent: () => currentAccount};},
  }, 'password');
  try {
    await move.proveOldPassword('local_ada', 'old password');
    assert.deepEqual(calls.map(call => call.path), ['/v1/auth/move/check']);
    await move.redeemPortico();
    assert.equal(calls.length, 1, 'signing in alone cannot press the confirmation');
    await move.preparePortico();
    assert.equal(move.getSnapshot().phase, 'confirm-portico');
    assert.equal(move.getSnapshot().username, 'local_ada');
    assert.deepEqual(move.getSnapshot().porticoAccount, {id: accountId, username: 'portico_ada'});
    assert.equal(calls.length, 1, 'preparing the confirmation gets no note');
    move.cancel(); await move.redeemPortico();
    assert.equal(calls.length, 1, 'Cancel spends nothing');

    await move.proveOldPassword('local_ada', 'old password'); await move.preparePortico();
    currentAccount = false; await move.redeemPortico();
    assert.equal(calls.some(call => call.path === 'Services:identity'), false, 'a changed account cannot spend the confirmation');
    currentAccount = true;
    await move.proveOldPassword('local_ada', 'old password'); await move.preparePortico();
    move.changePorticoAccount(); await move.redeemPortico();
    assert.equal(calls.some(call => call.path === 'Services:identity'), false, 'Not you? needs a new confirmation');
    await move.preparePortico(); await move.redeemPortico();
    assert.deepEqual(calls.slice(-3).map(call => call.path), ['/v1/auth/portico/challenge', 'Services:identity', '/v1/auth/move/complete']);
    const body = calls.at(-1)!.body;
    assert.equal(body.ticket, 'ticketfromtheserverabcdefghijklmnop');
    assert.equal(body.password, undefined); assert.equal(body.username, undefined);
    assert.equal(move.takeAcceptedResult(), 'admitted');
    assert.doesNotMatch(JSON.stringify(move.getSnapshot()), /old password|ticketfromtheserver/);
  } finally {move.dispose(); setInstallationIdentity(undefined);}
});
