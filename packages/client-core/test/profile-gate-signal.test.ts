import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi} from '../src/index.ts';

// Android walk, 31 Oct: after a PIN change elsewhere, a screen's own read (Saved) answered
// profile_pin_reentry and showed "This couldn't load". The session's holder now hears every such
// refusal, whichever screen made the request.

const reply = (status: number, code: string) => async () => new Response(JSON.stringify({error: {code, message: 'Refused.'}}), {status, headers: {'Content-Type': 'application/json'}});

test('a request refused with profile_pin_reentry tells the session holder and still fails for its caller', async () => {
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'token', reply(403, 'profile_pin_reentry'));
  const heard: ApiError[] = [];
  const off = api.subscribeProfileGate(error => heard.push(error));
  const failed = await api.request('/v1/me/saved').catch(error => error);
  assert.ok(failed instanceof ApiError);
  assert.equal(failed.code, 'profile_pin_reentry');
  assert.deepEqual(heard, [failed]);
  off();
  await api.request('/v1/me/saved').catch(() => {});
  assert.equal(heard.length, 1, 'not heard after unsubscribing');
});

test('other refusals are not a PIN gate', async () => {
  for (const [status, code] of [[403, 'forbidden'], [401, 'unauthorized'], [403, 'profile_pin_invalid'], [503, 'server_busy']] as const) {
    const api = new HttpLocalApi('http://127.0.0.1:19447', 'token', reply(status, code));
    let heard = 0;
    api.subscribeProfileGate(() => {heard++;});
    await api.request('/v1/me/saved').catch(() => {});
    assert.equal(heard, 0, code);
  }
});

test('a listener that throws changes nothing for the caller or the other listeners', async () => {
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'token', reply(403, 'profile_pin_reentry'));
  let later = 0;
  api.subscribeProfileGate(() => {throw new Error('listener broke');});
  api.subscribeProfileGate(() => {later++;});
  const failed = await api.request('/v1/me/saved').catch(error => error);
  assert.equal(failed.code, 'profile_pin_reentry');
  assert.equal(later, 1);
});
