import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {provesIdentity} from '../src/installation.ts';

// A Quick Connect sign-in is a family issued to another device record: binding it to this device's
// record answers 401. That is an answer about the binding, not a refused sign-in, so it must not
// start a renewal (which, for a TV, ended the sign-in seconds after it was approved).
test('a 401 from binding a device record never starts a renewal', async () => {
  assert.equal(provesIdentity('/v1/devices/dev_1/sessions/bind', 'POST'), true);
  assert.equal(provesIdentity('/v1/devices/dev_1', 'PATCH'), false);
  let renewals = 0;
  const api = new HttpLocalApi('http://127.0.0.1:1', 'token_a', async () => new Response(JSON.stringify({error: {code: 'unauthorized'}}), {status: 401, headers: {'Content-Type': 'application/json'}}));
  api.setAuthRecovery({token: async current => current, recover: async () => { renewals++; return 'token_b'; }});
  await assert.rejects(api.request('/v1/devices/dev_1/sessions/bind', 'POST', {installationId: 'i', sessionFamilyId: 'f'}), (e: {status?: number}) => e.status === 401);
  assert.equal(renewals, 0);
  await assert.rejects(api.request('/v1/devices/dev_1', 'PATCH', {name: 'x'}));
  assert.equal(renewals, 1, 'other 401s still renew once');
});
