// A Portico Account signs in to a server on the new server, against stand-ins for the server and
// for Portico Services: the server's challenge, Services' note for it, the note back to the server.
import test from 'node:test';
import assert from 'node:assert/strict';
import {setInstallationIdentity} from '../src/installation.ts';
import {porticoServerSignIn} from '../src/portico-server-sign-in.ts';
import {parseRememberedServer} from '../src/remembered-server.ts';
import type {ProfileAPI} from '../src/profile-management.ts';

const INSTALLATION = 'installation_for_portico_sign_in_00001';
const later = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString();
const session = () => ({accessToken: 'access_account', expiresAt: later(15), sessionId: 'ses_1', tokenGeneration: '1', sessionExpiresAt: later(60 * 24 * 90),
  deviceId: 'dev_1', installationId: INSTALLATION, refreshToken: 'refresh_token_0000000001', viewer: {accountId: 'acct', serverId: 'srv', role: 'account'}});
const signedIn = {session: session(), account: {id: 'acct', username: 'ada', role: 'member'}, profiles: [{id: 'main', name: 'Ada', color: 'blue', isPrimary: true, pinRequired: true}]};
type Call = {path: string; method: string; body: any};
function server(answers: Record<string, (call: Call) => unknown>) {
  const calls: Call[] = [];
  const api: ProfileAPI = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    const call = {path, method, body: body as any}; calls.push(call);
    const answer = answers[method + ' ' + path];
    if (!answer) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
    return answer(call) as T;
  }};
  return {api, calls};
}
test.beforeEach(() => setInstallationIdentity({installationId: INSTALLATION, name: 'Portico on Android', platform: 'android', app: 'portico', appVersion: '1'}));
test.after(() => setInstallationIdentity(undefined));

test('the server\'s challenge goes to Portico Services, and Services\' note goes back to the server with this device', async () => {
  const note = {payload: 'note', signature: 'sig', keyId: 'k'};
  const s = server({
    'POST /v1/auth/portico/challenge': () => ({challenge: 'challenge-from-srv-000000', serverId: 'srv', expiresAt: later(5)}),
    'POST /v1/auth/portico/exchange': call => call.body.challenge === 'challenge-from-srv-000000' && call.body.identity === note ? signedIn : assert.fail('the note was not sent as given'),
    'POST /v1/hosted/notices': () => ({ack: {}}),
  });
  const asked: string[] = [];
  const result = await porticoServerSignIn(s.api, 'srv', async challenge => {asked.push(challenge); return {identity: note, note: {news: 1}};});
  assert.deepEqual(asked, ['challenge-from-srv-000000'], 'Services is asked once, for the server\'s own challenge');
  assert.equal(result.kind, 'signed-in');
  assert.deepEqual(s.calls[1].body.device, {installationId: INSTALLATION, name: 'Portico on Android', platform: 'android', app: 'portico', appVersion: '1'});
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(s.calls.map(call => call.path), ['/v1/auth/portico/challenge', '/v1/auth/portico/exchange', '/v1/hosted/notices'], 'a server that is behind is given its news');
});

test('a server answering as another server is not given a note, and a refusal to carry the news does not fail the sign-in', async () => {
  const other = server({'POST /v1/auth/portico/challenge': () => ({challenge: 'challenge-from-elsewhere0', serverId: 'srv_other', expiresAt: later(5)})});
  let asked = 0;
  await assert.rejects(porticoServerSignIn(other.api, 'srv', async () => {asked++; return {identity: {}};}), {code: 'server_mismatch'});
  assert.equal(asked, 0, 'no note is asked for another server\'s challenge');

  const behind = server({
    'POST /v1/auth/portico/challenge': () => ({challenge: 'challenge-from-srv-000000', serverId: 'srv', expiresAt: later(5)}),
    'POST /v1/auth/portico/exchange': () => signedIn,
    'POST /v1/hosted/notices': () => { throw Object.assign(new Error('refused'), {status: 400, code: 'bad_note'}); },
  });
  assert.equal((await porticoServerSignIn(behind.api, 'srv', async () => ({identity: {}, note: {}}))).kind, 'signed-in');
});

test('a server opened from the new signed server list is remembered under its Portico Account with no route key', () => {
  const saved = (hosted: unknown) => ({version: '1', logicalOrigin: 'https://a1.ns.portico.direct:32400', session: session(),
    pin: {serverId: 'srv', publicKey: 'key', fingerprint: 'print'}, routes: {paired: ['https://a1.ns.portico.direct:32400']}, hosted});
  const remembered = parseRememberedServer(saved({origin: 'https://web.getportico.tv', accountId: 'acct'}));
  assert.deepEqual(remembered?.hosted, {origin: 'https://web.getportico.tv', accountId: 'acct'});
  assert.throws(() => parseRememberedServer(saved({origin: 'https://web.getportico.tv', accountId: 'acct', key: {keyId: 1}})), {code: 'invalid_saved_connection'});
});
