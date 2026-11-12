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
