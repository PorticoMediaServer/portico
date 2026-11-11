import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeLocalInvitation} from '../src-next/platform/local-invitation.ts';
import {ApiError} from '../../../packages/client-core/src/index.ts';

const origin = 'https://inviting.example', code = 'K7QP-2MXD-9HRT-4WNC';
const preview = {serverName: 'Home', role: 'member', allLibraries: true, libraries: [], expiresAt: '2099-01-01T00:00:00Z'};
function fixture(answer: (path: string) => unknown = () => preview) {
  const paths: string[] = [], joins: unknown[] = [], signIns: unknown[] = [];
  const recipient = new NativeLocalInvitation<string>({origin, serverId: 'srv_' + 's'.repeat(43), current: () => true,
    api: {request: async <T,>(path: string) => {paths.push(path); const value = answer(path); if (value instanceof Error) throw value; return value as T;}} as never,
    signIn: async (username, password) => {signIns.push([username, password]); return 'signed-in-again';},
    accept: async (capability, username, password, name) => {joins.push([capability, username, password, name]); return 'joined';}});
  return {recipient, paths, joins, signIns};
}

test('a typed code reads the invitation on the rebuilt server, and joining keeps the sign-in it answers with', async () => {
  const f = fixture(), read = await f.recipient.preview(code);
  assert.deepEqual(f.paths, ['/v1/auth/invitations/' + code]);
  assert.equal(read.serverName, 'Home');
  assert.equal(await f.recipient.join(read, 'dana', 'Long enough Password 1', 'Dana'), 'joined');
  assert.deepEqual(f.joins, [[{shortCode: 'K7QP2MXD9HRT4WNC'}, 'dana', 'Long enough Password 1', 'Dana']]);
  assert.equal(await f.recipient.join(read, 'dana', 'Long enough Password 1', 'Dana'), 'signed-in-again', 'joined once; again on this page is a sign-in');
  assert.deepEqual(f.signIns, [['dana', 'Long enough Password 1']]);
});

test('a refused join can be tried again as a join; a typed code the server does not know stays on the form, a link\'s reads as expired', async () => {
  const f = fixture(), read = await f.recipient.preview(code);
  const taken = new ApiError(409, 'username_taken', 'That username is already in use.');
  let refuse = true;
  (f.recipient as unknown as {ports: {accept: unknown}}).ports.accept = async () => {if (refuse) throw taken; return 'joined';};
  await assert.rejects(f.recipient.join(read, 'dana', 'Long enough Password 1', ''), error => error === taken);
  assert.equal(f.recipient.failure(taken), 'username');
  refuse = false; assert.equal(await f.recipient.join(read, 'dana2', 'Long enough Password 1', ''), 'joined');
  const notFound = () => new ApiError(404, 'invitation_not_found', 'This invitation is no longer valid.');
  // A typed code the server does not know keeps the person on the code form to check what they typed.
  const typed = fixture(notFound), typedError = await typed.recipient.preview(code).catch(value => value);
  assert.equal(typed.recipient.failure(typedError), 'mistyped');
  // A code opened from a link that no longer works is the "can't be used" page.
  const linked = fixture(notFound), linkError = await linked.recipient.preview('portico://join#code=' + 'c'.repeat(43) + '&server=' + encodeURIComponent(origin)).catch(value => value);
  assert.equal(linked.recipient.failure(linkError), 'expired');
});
