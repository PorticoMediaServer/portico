import test from 'node:test';
import assert from 'node:assert/strict';
import {ProfileRecoveryController} from '../src/profile-recovery.ts';
import type {DirectSnapshot} from '../src/profile-management.ts';
import type {HttpLocalApi} from '../src/index.ts';

const snapshot = {canManage: false, profiles: [{id: 'prf_kid', primary: false, pinRequired: true}]} as unknown as DirectSnapshot;
const refusal = (code: string) => Object.assign(new Error(code), {status: 403, code});
function flow(answers: ((body: Record<string, unknown>) => unknown)[]) {
  const sent: {path: string; method?: string; body: Record<string, unknown>}[] = [];
  const api = {request: async (path: string, method?: string, body?: unknown) => {sent.push({path, method, body: body as Record<string, unknown>}); const answer = answers.shift()!(body as Record<string, unknown>); if (answer instanceof Error) throw answer; return answer;}} as unknown as HttpLocalApi;
  return {sent, controller: new ProfileRecoveryController({api, snapshot, profileId: 'prf_kid', selectedFamilyId: 'family', kind: 'local', authorityCurrent: () => true})};
}

test('Forgot PIN sends the account password with the new PIN from this sign-in, and asks for the two-step code only when the server does', async () => {
  const {sent, controller} = flow([() => refusal('two_factor_invalid'), () => ({id: 'prf_kid'})]);
  assert.equal(controller.getSnapshot().phase, 'password');
  await controller.password('account password');
  assert.equal(controller.getSnapshot().phase, 'pin');
  await controller.reset('2468');
  assert.equal(controller.getSnapshot().phase, 'challenge');
  assert.equal(controller.getSnapshot().error, undefined, 'the request for a code is not shown as a failure');
  await controller.code('123456');
  assert.equal(controller.getSnapshot().phase, 'done');
  assert.deepEqual(sent.map(call => [call.method, call.path, call.body]), [
    ['PUT', '/v1/profiles/prf_kid/pin', {pin: '2468', currentPassword: 'account password'}],
    ['PUT', '/v1/profiles/prf_kid/pin', {pin: '2468', currentPassword: 'account password', code: '123456'}],
  ]);
});

test('a wrong password goes back to the password with the server\'s words', async () => {
  const {controller} = flow([() => refusal('current_password_incorrect')]);
  await controller.password('wrong');
  await controller.reset('2468');
  assert.equal(controller.getSnapshot().phase, 'password');
  assert.equal((controller.getSnapshot().error as {code?: string}).code, 'current_password_incorrect');
});

function porticoFlow() {
  const sent: {path: string; body?: any}[] = [], service: any[] = [];
  let current = true, marked = true;
  const account = {serverId: 'srv_den', account: {id: 'local_ada', hostedAccountId: 'acct_ada'}, canManage: false,
    profiles: [{id: 'prf_kid', primary: false, pinRequired: true}]} as unknown as DirectSnapshot;
  const api = {request: async (path: string, _method?: string, body?: unknown) => {
    sent.push({path, body});
    if (path === '/v1/auth/portico/challenge') return {serverId: 'srv_den', challenge: 'fresh-server-challenge'};
    assert.equal(path, '/v1/profiles/prf_kid/pin'); return {};
  }} as unknown as HttpLocalApi;
  const source = {accountId: 'acct_ada', isCurrent: () => current,
    identity: async (serverId: string, challenge: string, proof: unknown) => {
      service.push({serverId, challenge, proof});
      return {identity: {payload: btoa(JSON.stringify({accountId: 'acct_ada', serverId, challenge, passwordChecked: marked}))}};
    }};
  const controller = new ProfileRecoveryController({api, snapshot: account, profileId: 'prf_kid', selectedFamilyId: 'selected-family',
    kind: 'portico', authorityCurrent: () => true, porticoPIN: source});
  return {controller, sent, service, changeAccount: () => {current = false;}, unmarked: () => {marked = false;}};
}

test('Portico Forgot PIN sends the password and two-step only to Services, and only its marked note to the server', async () => {
  const f = porticoFlow();
  await f.controller.porticoPassword('typed just now', '123456');
  assert.deepEqual(f.service, [{serverId: 'srv_den', challenge: 'fresh-server-challenge', proof: {currentPassword: 'typed just now', twoStepCode: '123456'}}]);
  assert.equal(f.controller.getSnapshot().phase, 'pin');
  assert.doesNotMatch(JSON.stringify(f.controller.getSnapshot()), /typed just now|payload/);
  await f.controller.reset('2468');
  assert.equal(f.controller.getSnapshot().phase, 'done');
  assert.deepEqual(f.sent.map(call => call.path), ['/v1/auth/portico/challenge', '/v1/profiles/prf_kid/pin']);
  assert.deepEqual(Object.keys(f.sent[1].body).sort(), ['challenge', 'identity', 'pin']);
  assert.equal(f.sent[1].body.pin, '2468');
  assert.doesNotMatch(JSON.stringify(f.sent), /typed just now|123456|refresh|select/);
  f.controller.dispose();
});

test('an unmarked note, a changed account or closing recovery cannot reset a Portico PIN', async () => {
  const unmarked = porticoFlow(); unmarked.unmarked();
  await unmarked.controller.porticoPassword('typed just now');
  assert.equal(unmarked.controller.getSnapshot().phase, 'portico');
  await unmarked.controller.reset('2468');
  assert.equal(unmarked.sent.length, 1);
  unmarked.controller.dispose();

  const changed = porticoFlow(); await changed.controller.porticoPassword('typed just now');
  changed.changeAccount(); await changed.controller.reset('2468');
  assert.equal(changed.sent.length, 1); assert.equal(changed.controller.getSnapshot().phase, 'portico'); changed.controller.dispose();

  const closed = porticoFlow(); await closed.controller.porticoPassword('typed just now');
  closed.controller.dispose(); await closed.controller.reset('2468');
  assert.equal(closed.sent.length, 1);
});


test('Services passwordless refusal reaches the recovery screen as account_password_required', async () => {
  const {ServicesError} = await import('../src/portico-account.ts');
  const {porticoApiError} = await import('../src/portico-services-native.ts');
  const error = new ServicesError(409, 'account_password_required');
  const native = porticoApiError(error);
  assert.equal((native as {code: string}).code, 'account_password_required');
  const f = porticoFlow();
  const source = {accountId: 'acct_ada', isCurrent: () => true, identity: async () => {throw native;}};
  await f.controller.porticoPassword('anything typed', '', source);
  assert.equal(f.controller.getSnapshot().phase, 'portico');
  assert.equal((f.controller.getSnapshot().error as {code: string}).code, 'account_password_required');
  assert.equal(f.sent.length, 1, 'no PIN goes to the server');
  f.controller.dispose();
});


test('provider PIN recovery requires its own checked mark and forwards only the note and new PIN', async () => {
  for (const marked of [false, true]) {
    const f = porticoFlow();
    const source = {accountId: 'acct_ada', isCurrent: () => true, identity: async () => {throw new Error('password route must not run');},
      providerIdentity: async (serverId: string, challenge: string, provider: string, code: unknown) => {
        assert.equal(provider, 'google'); assert.equal(code, '123456');
        return {identity: {payload: btoa(JSON.stringify({accountId: 'acct_ada', serverId, challenge, providerChecked: marked, passwordChecked: !marked}))}};
      }};
    await f.controller.porticoProvider('google', '123456', source);
    assert.equal(f.controller.getSnapshot().phase, marked ? 'pin' : 'portico');
    await f.controller.reset('2468');
    assert.equal(f.sent.length, marked ? 2 : 1);
    if (marked) {
      const body = f.sent[1].body;
      assert.deepEqual(Object.keys(body).sort(), ['challenge', 'identity', 'pin']);
      assert.equal(body.pin, '2468'); assert.equal(f.controller.getSnapshot().phase, 'done');
    }
    f.controller.dispose();
  }
});
