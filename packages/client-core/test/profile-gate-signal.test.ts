import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError, HttpLocalApi, type ProfileGateRefusal} from '../src/index.ts';

// Android walk, 31 Oct: after a PIN change elsewhere, a screen's own read (Saved) answered
// profile_pin_reentry and showed "This couldn't load". The session's holder now hears every such
// refusal, whichever screen made the request, with the token the request went out with.

const refusal = (status: number, code: string) => new Response(JSON.stringify({error: {code, message: 'Refused.'}}), {status, headers: {'Content-Type': 'application/json'}});
const reply = (status: number, code: string) => async () => refusal(status, code);

test('a request refused with profile_pin_reentry tells the session holder and still fails for its caller', async () => {
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'token-a', reply(403, 'profile_pin_reentry'));
  const heard: ProfileGateRefusal[] = [];
  const off = api.subscribeProfileGate(value => heard.push(value));
  const failed = await api.request('/v1/me/saved').catch(error => error);
  assert.ok(failed instanceof ApiError);
  assert.equal(failed.code, 'profile_pin_reentry');
  assert.equal(heard.length, 1);
  assert.equal(heard[0]!.error, failed);
  assert.equal(heard[0]!.accessToken, 'token-a');
  off();
  await api.request('/v1/me/saved').catch(() => {});
  assert.equal(heard.length, 1, 'not heard after unsubscribing');
});

test('a reply sent under token A that arrives after the switch to B names A, so B is not gated by it', async () => {
  let answer!: (response: Response) => void;
  const api = new HttpLocalApi('http://127.0.0.1:19447', 'token-a', () => new Promise<Response>(resolve => {answer = resolve;}));
  const heard: ProfileGateRefusal[] = [];
  api.subscribeProfileGate(value => heard.push(value));
  const pending = api.request('/v1/me/saved').catch(error => error);
  await new Promise(resolve => setImmediate(resolve));
  api.setAccessToken('token-b');
  answer(refusal(403, 'profile_pin_reentry'));
  assert.equal((await pending).code, 'profile_pin_reentry');
  assert.equal(heard.length, 1);
  assert.equal(heard[0]!.accessToken, 'token-a', 'the token the request went out with');
  const holderOfB = heard.filter(value => value.accessToken === 'token-b');
  assert.deepEqual(holderOfB, [], 'a holder that compares tokens does not hear it as B\'s gate');
});

test('other refusals are not a PIN gate', async () => {
  for (const [status, code] of [[403, 'forbidden'], [401, 'unauthorized'], [403, 'profile_pin_invalid'], [403, 'profile_pin_manage'], [503, 'server_busy']] as const) {
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
