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
