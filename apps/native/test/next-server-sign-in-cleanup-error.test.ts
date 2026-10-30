import test from 'node:test';
import assert from 'node:assert/strict';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

// R-19: when cleanup of an issued session itself fails, the caller still sees the original error.
let fixture: any;
test('a failed sign-out during cleanup never masks the error that triggered it', async () => {
  const app = await standardHomeApp({setup: true, setupResponse: async (request: any, f: any) => {
    fixture = f;
    if (request.path === '/v1/system') return {status: 200, body: {id: f.pin.serverId, name: 'Synthetic local server', signIn: 'local',
      version: '1', move: null, setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}};
    if (request.path === '/v1/direct') return {status: 400, body: {error: {code: 'original_failure'}}};
    if (request.path === '/v1/sessions/current') return {status: 503, body: {error: {code: 'cleanup_failure'}}};
    return undefined;
  }});
  const {NativeServerSignIn} = await import('../src-next/platform/server-sign-in.ts');
  const signIn = new NativeServerSignIn();
  try {
    await signIn.inspect('https://standard-home.test'); await signIn.confirmLocal(await signIn.inspectLocalIdentity());
    const session = {...fixture.session, accessToken: 'tv_access', sessionFamilyId: 'tv_family', viewer: {...fixture.viewer, role: 'member'}};
    await assert.rejects(signIn.acceptLocalTVSession(session), (error: {code?: string}) => error.code === 'original_failure');
  } finally {signIn.dispose(); await app.settle();}
});
