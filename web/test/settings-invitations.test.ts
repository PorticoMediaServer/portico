import test from 'node:test';
import assert from 'node:assert/strict';
import type {ServerInvitation} from '@core/direct-account.ts';
import {parseJoinLink} from '@core/join-link.ts';
import {SettingsInvitations, invitationQuery} from '../src/screens/settings/invitations.ts';
import type {TableQuery} from '../src/kit/settings/table-model.ts';

const serverId = 'srv_' + 's'.repeat(43), origin = 'https://server.example';
const row: ServerInvitation = {id: 'invitation', revision: 7, role: 'member', state: 'pending', createdAt: '2026-10-23T12:00:00Z', expiresAt: '2099-01-01T00:00:00Z', allowedLibraries: ['library'], notes: 'Private management note', access: {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''}};
const query: TableQuery = {search: 'real query', filters: {status: 'waiting'}, sort: {by: 'sent', direction: 'desc'}};
const live = () => ({signal: new AbortController().signal, isCurrent: () => true});
function fixture(reply: (path: string, method: string, body: unknown) => unknown | Promise<unknown>) {
  let current = true;
  const calls: {path: string; method: string; body: unknown; signal?: AbortSignal}[] = [];
  const source = new SettingsInvitations({request: async <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => {
    calls.push({path, method, body, signal});return await reply(path, method, body) as T;
  }}, () => current, origin, serverId);
  return {source, calls, retire: () => {current = false;}};
}
test('invitation queries use genuine producer status/sort and preserve complete counts and opaque cursor', async () => {
  for (const [status, expected] of [['all', undefined], ['waiting', 'pending'], ['used', 'accepted'], ['expired', 'expired']]) {
    assert.equal(invitationQuery({...query, filters: {status: status!}}).state, expected);
  }
  const page = {items: [row], limit: 40, totalCount: 137, matchedCount: 37, nextCursor: 'producer-next'};
  const f = fixture(() => page);
  try {
    assert.deepEqual(f.calls, []);const value = await f.source.read(query, 'opaque-current', live());
    assert.equal(value.totalCount, 137);assert.equal(value.matchedCount, 37);assert.equal(value.nextCursor, 'producer-next');assert.equal(value.items[0].notes, row.notes);
    const sent = new URL(f.calls[0].path, origin);
    assert.equal(sent.pathname, '/v1/admin/access/invitations');assert.equal(sent.searchParams.get('cursor'), 'opaque-current');
    assert.equal(sent.searchParams.get('q'), 'real query');assert.equal(sent.searchParams.get('state'), 'pending');
    assert.equal(sent.searchParams.get('sort'), 'sent');assert.equal(sent.searchParams.get('direction'), 'desc');
    assert.throws(() => invitationQuery({...query, sort: {by: 'role', direction: 'asc'}}));assert.equal(f.calls.length, 1);
  } finally {f.source.dispose();}
});
test('retired query/row captures and disposed authority reject late results even if transport ignores abort', async () => {
  for (const end of ['query', 'signal', 'authority', 'dispose']) {
    let resolve!: (value: unknown) => void, selected = true;
    const f = fixture(() => new Promise(value => {resolve = value;})), controller = new AbortController();
    const pending = f.source.read(query, '', {signal: controller.signal, isCurrent: () => selected});
    if (end === 'query') selected = false;else if (end === 'signal') controller.abort();else if (end === 'authority') f.retire();else f.source.dispose();
    resolve({items: [row], limit: 40, totalCount: 137, matchedCount: 37, nextCursor: ''});
    await assert.rejects(pending, {name: 'AbortError'});
    if (end === 'signal' || end === 'dispose') assert.equal(f.calls[0].signal?.aborted, true);
    f.source.dispose();
  }
});
test('share links are same-server, current-row one-use results with no private notes or recipient fabrication', async () => {
  const code = 'c'.repeat(43);
  const f = fixture(() => ({code, expiresAt: '2099-01-01T00:00:00Z', serverId}));
  try {
    const link = await f.source.link(row, live());assert.deepEqual(parseJoinLink(link), {code, server: origin, id: serverId});
    assert.doesNotMatch(link, /Private|management|note|email/);
    assert.deepEqual(f.calls[0].body, {expectedRevision: 7});
    await assert.rejects(f.source.link(row, {signal: new AbortController().signal, isCurrent: () => false}), {name: 'AbortError'});assert.equal(f.calls.length, 1);
    await assert.rejects(f.source.link({...row, state: 'expired'}, live()));assert.equal(f.calls.length, 1);
  } finally {f.source.dispose();}
  for (const reply of [{code, expiresAt: '2000-01-01T00:00:00Z', serverId}, {code, expiresAt: '2099-01-01T00:00:00Z', serverId: 'srv_' + 'a'.repeat(42) + 'A'}]) {
    const other = fixture(() => reply);try {await assert.rejects(other.source.link(row, live()));} finally {other.source.dispose();}
  }
});
test('explicit short codes use the caller operation, stay one request, and preserve a replay absence', async () => {
  const expiry = '2099-01-01T00:00:00Z';
  const replay = fixture(() => ({serverId, expiresAt: expiry}));
  try {
    const value = await replay.source.shortCode(row, 'first-explicit-code', live());
    assert.deepEqual(value, {serverId, expiresAt: expiry});
    assert.equal(Object.hasOwn(value, 'shortCode'), false);
    assert.equal(replay.calls.length, 1);
    assert.equal(new URL(replay.calls[0].path, origin).pathname, '/v1/admin/access/invitations/invitation/short-code');
    assert.deepEqual(replay.calls[0].body, {expectedRevision: 7, operationId: 'first-explicit-code'});
    // The absence is not an invitation to rotate: no second request happened.
    assert.equal(replay.calls.length, 1);
  } finally {replay.source.dispose();}
  const issued = fixture(() => ({serverId, expiresAt: expiry, shortCode: '008j4ct4ank7f24s'}));
  try {
    const value = await issued.source.shortCode(row, 'second-explicit-code', live());
    assert.equal(value.shortCode, '008J-4CT4-ANK7-F24S');
    assert.equal(issued.calls.length, 1);
    // A distinct explicit New code is a distinct operation, never a replay.
    await issued.source.shortCode(row, 'third-explicit-code', live());
    assert.deepEqual(issued.calls.map(call => (call.body as {operationId: string}).operationId), ['second-explicit-code', 'third-explicit-code']);
  } finally {issued.source.dispose();}
});

test('short-code refusal, foreign identity and expiry fail without a retry or a foreign command', async () => {
  const refusal = Object.assign(new Error('conflict'), {status: 409}), failed = fixture(() => {throw refusal;});
  try {
    await assert.rejects(failed.source.shortCode(row, 'same-explicit-code', live()), refusal);
    assert.equal(failed.calls.length, 1);
  } finally {failed.source.dispose();}
  const foreign = fixture(() => ({serverId: 'srv_' + 'a'.repeat(42) + 'A', expiresAt: '2099-01-01T00:00:00Z', shortCode: '008J4CT4ANK7F24S'}));
  try {
    await assert.rejects(foreign.source.shortCode(row, 'foreign-code', live()), /invitation_server_changed/);
    assert.equal(foreign.calls.length, 1);
  } finally {foreign.source.dispose();}
  const expired = fixture(() => ({serverId, expiresAt: '2000-01-01T00:00:00Z', shortCode: '008J4CT4ANK7F24S'}));
  try {
    await assert.rejects(expired.source.shortCode(row, 'expired-code', live()), {code: 'invitation_expired'});
    assert.equal(expired.calls.length, 1);
  } finally {expired.source.dispose();}
  const empty = fixture(() => ({serverId, expiresAt: '2099-01-01T00:00:00Z'}));
  try {
    await assert.rejects(empty.source.shortCode(row, '', live()), /invitation_operation_required/);
    assert.equal(empty.calls.length, 0);
  } finally {empty.source.dispose();}
});

test('revocation and edit preserve the actual captured revision and caller operation key without automatic retry', async () => {
  const f = fixture((_path, _method, body) => ({...row, revision: 8, state: 'revoked', notes: (body as {notes?: string}).notes ?? row.notes}));
  try {
    const result = await f.source.revoke(row, 'captured-operation', live());assert.equal(result.state, 'revoked');
    assert.deepEqual(f.calls[0].body, {expectedRevision: 7, operationId: 'captured-operation'});
    await assert.rejects(f.source.edit(row, {expectedRevision: 6, operationId: 'edit-key', role: 'member', allowedLibraries: [], expiresAt: row.expiresAt}, live()));assert.equal(f.calls.length, 1);
    await f.source.edit(row, {expectedRevision: 7, operationId: 'edit-key', role: 'member', allowedLibraries: [], expiresAt: row.expiresAt, notes: 'Updated note'}, live());
    assert.equal((f.calls[1].body as {operationId: string}).operationId, 'edit-key');assert.equal((f.calls[1].body as {expectedRevision: number}).expectedRevision, 7);
  } finally {f.source.dispose();}
  const conflict = Object.assign(new Error('conflict'), {status: 409}), failed = fixture(() => {throw conflict;});
  try {await assert.rejects(failed.source.revoke(row, 'same-key', live()), conflict);assert.equal(failed.calls.length, 1);} finally {failed.source.dispose();}
});
test('creation returns its real one-time code without a list cache, and owner refusal closes all later requests', async () => {
  const f = fixture(() => ({...row, code: 'c'.repeat(43)}));
  try {
    const value = await f.source.create({role: 'member', allowedLibraries: ['library'], expiresInHours: 24, operationId: 'create-key', notes: row.notes}, live());
    assert.equal(value.code, 'c'.repeat(43));assert.equal(value.notes, row.notes);assert.equal((f.calls[0].body as {operationId: string}).operationId, 'create-key');
  } finally {f.source.dispose();}
  const refusal = Object.assign(new Error('owner permission ended'), {status: 403}), denied = fixture(() => {throw refusal;});
  try {
    await assert.rejects(denied.source.read(query, '', live()), refusal);
    await assert.rejects(denied.source.link(row, live()), {name: 'AbortError'});assert.equal(denied.calls.length, 1);
  } finally {denied.source.dispose();}
});
test('explicit foreign server identities fail closed without dropping rows or issuing a foreign row command', async () => {
  const foreign = {...row, serverId: 'srv_' + 'a'.repeat(42) + 'A'};
  const page = fixture(() => ({items: [foreign], limit: 40, totalCount: 137, matchedCount: 37, nextCursor: 'next'}));
  try {await assert.rejects(page.source.read(query, '', live()), /invitation_server_changed/);assert.equal(page.calls.length, 1);} finally {page.source.dispose();}
  const action = fixture(() => ({code: 'c'.repeat(43), expiresAt: row.expiresAt, serverId}));
  try {
    await assert.rejects(action.source.link(foreign, live()), /invitation_server_changed/);
    await assert.rejects(action.source.revoke(foreign, 'actual-operation', live()), /invitation_server_changed/);
    assert.deepEqual(action.calls, []);
  } finally {action.source.dispose();}
  const created = fixture(() => ({...foreign, code: 'c'.repeat(43)}));
  try {await assert.rejects(created.source.create({role: 'member', allowedLibraries: [], expiresInHours: 24, operationId: 'create-key'}, live()), /invitation_server_changed/);}
  finally {created.source.dispose();}
});
