/** "Forgot password" for the owner of a server that uses Portico Accounts: the reset file's code is sent to the rebuilt server. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {signInAnswer, systemAnswer} from '../../../packages/client-core/test/next-wire.ts';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('the code from the reset file is sent to POST /v1/auth/owner-reset/session, and the owner sign-in it answers with is kept', async () => {
  const sent: {method: string; path: string; body?: any}[] = [];
  const app = await standardHomeApp({setup: true, accountRole: 'owner', setupResponse: async (request: any, f: any) => {
    sent.push({method: request.method, path: request.path, body: typeof request.body === 'string' ? JSON.parse(request.body) : request.body});
    if (request.path === '/v1/system') return {status: 200, body: systemAnswer({id: f.pin.serverId, name: 'Portico server', version: '1', signIn: 'portico'})};
    if (request.path === '/v1/auth/owner-reset') return {status: 200, body: {fileName: 'reset-code.txt', retryAfterMs: 0}};
    if (request.path === '/v1/auth/owner-reset/session') return {status: 200, body: signInAnswer({
      account: {id: f.viewer.accountId, username: 'owner', role: 'owner'}, profiles: [{id: f.viewer.profileId, name: 'Owner', primary: true}],
      session: {...f.session, sessionFamilyId: 'reset_family', viewer: {...f.viewer, role: 'owner'}}})};
    return undefined;
  }});
  const {NativeServerSignIn} = await import('../src-next/platform/server-sign-in.ts');
  const signIn = new NativeServerSignIn();
  try {
    await signIn.inspect('https://standard-home.test');
    await signIn.prepareOwnerReset();
    assert.deepEqual(await signIn.requestOwnerReset(), {fileName: 'reset-code.txt', retryAfterSeconds: 0});
    await signIn.verifyOwnerReset(' K7QP-2MXD-9HRT ');
    const reset = sent.filter(value => value.path.startsWith('/v1/auth/owner-reset'));
    assert.deepEqual(reset.map(value => value.method + ' ' + value.path), ['POST /v1/auth/owner-reset', 'POST /v1/auth/owner-reset/session']);
    assert.equal(reset[1]!.body.code, 'K7QP-2MXD-9HRT');
    assert.equal(typeof reset[1]!.body.device?.installationId, 'string', 'the sign-in names this installation');
    assert.ok(!sent.some(value => value.path === '/v1/auth/owner-reset/verify'), 'the old route is never asked');
  } finally {signIn.dispose(); await app.settle();}
});
