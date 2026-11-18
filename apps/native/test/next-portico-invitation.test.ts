/** Joining a server that uses Portico Accounts with an invitation: the preview reads the rebuilt server's own route. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativePorticoInvitation, NativeInvitationUnavailable, readPorticoInvitationPreview} from '../src-next/platform/portico-invitation.ts';
import {ApiError} from '../../../packages/client-core/src/index.ts';

const origin = 'https://inviting.example', serverId = 'srv_' + 's'.repeat(43), code = 'K7QP-2MXD-9HRT-4WNC';
const preview = {serverName: 'Home', role: 'member', allLibraries: false, libraries: ['Films'], expiresAt: '2099-01-01T00:00:00Z'};
const link = 'https://web.getportico.tv/join#' + new URLSearchParams({code, server: origin, id: serverId});
const notFound = new ApiError(404, 'invitation_not_found', 'This invitation is not usable.');
function api(answer: () => unknown = () => preview) {
  const sent: {path: string; method?: string; body?: unknown}[] = [];
  return {sent, api: {request: async <T,>(path: string, method?: string, body?: unknown) => {sent.push({path, method, body}); const value = answer(); if (value instanceof Error) throw value; return value as T;}}};
}
const target = {origin, id: serverId, fingerprint: 'fp'};

test('the signed-out preview of a typed code or a link reads GET /v1/auth/invitations/{code}', async () => {
  for (const raw of [code, link]) {
    const f = api(), read = await readPorticoInvitationPreview(f.api, raw, target, new AbortController().signal, () => true);
    assert.deepEqual(f.sent, [{path: '/v1/auth/invitations/' + code, method: 'GET', body: undefined}]);
    assert.equal(read.preview.serverName, 'Home');
    assert.equal(read.preview.signInMethod, 'portico');
    assert.deepEqual(read.preview.libraries, [{name: 'Films'}]);
  }
});

test('a typed code the server no longer has is unavailable', async () => {
  const f = api(() => notFound);
  await assert.rejects(readPorticoInvitationPreview(f.api, code, target, new AbortController().signal, () => true), NativeInvitationUnavailable);
});

test('the signed-in preview reads the same route, and its failures read as before', async () => {
  const f = api(), joins: unknown[] = [];
  const invitation = new NativePorticoInvitation<string>({api: f.api as never, origin, serverId, fingerprint: 'fp', username: 'dana',
    scope: {accountId: 'acc', familyId: 'fam'}, current: () => true, accept: async capability => {joins.push(capability); return 'joined';}});
  const read = await invitation.preview(link);
  assert.deepEqual(f.sent.map(value => value.method + ' ' + value.path), ['GET /v1/auth/invitations/' + code]);
  assert.equal(await invitation.join(read), 'joined');
  assert.deepEqual(joins, [{code}]);
  assert.equal(invitation.failure(notFound), 'expired', 'a link whose invitation was used or ended');
  const typed = new NativePorticoInvitation<string>({api: api(() => notFound).api as never, origin, serverId, fingerprint: 'fp', username: 'dana',
    scope: {accountId: 'acc', familyId: 'fam'}, current: () => true, accept: async () => 'joined'});
  const error = await typed.preview(code).catch(value => value);
  assert.equal(typed.failure(error), 'unavailable', 'a typed code the server does not have');
});
