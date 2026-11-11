import test from 'node:test';
import assert from 'node:assert/strict';
import type {ServerInvitation} from '@core/direct-account.ts';
import {parseJoinLink} from '@core/join-link.ts';
import {SettingsInvitations, invitationQuery} from '../src/screens/settings/invitations.ts';
import type {TableQuery} from '../src/kit/settings/table-model.ts';

const serverId = 'srv_' + 's'.repeat(43), origin = 'https://server.example';
const code = 'K7QP-2MXD-9HRT-4WNC';
/** The rebuilt server's invitation, and the same invitation as the People screens read it. */
const wire = {id: 'invitation', code, role: 'member', state: 'open', allLibraries: false, libraries: ['library'], note: 'Private management note', createdAt: '2026-10-23T12:00:00Z', expiresAt: '2099-01-01T00:00:00Z'};
const row: ServerInvitation = {id: 'invitation', revision: 1, role: 'member', state: 'pending', createdAt: wire.createdAt, expiresAt: wire.expiresAt, allowedLibraries: ['library'], notes: wire.note, access: {maxStreamsMode: 'inherit', maxStreams: 0, allowRemote: true, accessTokens: true, maxContentRating: ''}};
const list = (...items: unknown[]) => ({items: items.length ? items : [wire]});
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
test('invitation queries keep their status and sort, and the whole list arrives as one page', async () => {
  for (const [status, expected] of [['all', undefined], ['waiting', 'pending'], ['used', 'accepted'], ['expired', 'expired']]) {
    assert.equal(invitationQuery({...query, filters: {status: status!}}).state, expected);
  }
  const f = fixture(() => list(wire, {...wire, id: 'used', state: 'accepted', note: undefined}));
  try {
    assert.deepEqual(f.calls, []);
    const value = await f.source.read({...query, search: 'private'}, '', live());
    assert.deepEqual(value.items, [row]); assert.equal(value.totalCount, 2); assert.equal(value.matchedCount, 1); assert.equal(value.nextCursor, '');
    assert.equal(f.calls[0].path, '/v1/admin/invitations');
    assert.throws(() => invitationQuery({...query, sort: {by: 'role', direction: 'asc'}})); assert.equal(f.calls.length, 1);
  } finally {f.source.dispose();}
});
test('retired query/row captures and disposed authority reject late results even if transport ignores abort', async () => {
  for (const end of ['query', 'signal', 'authority', 'dispose']) {
    let resolve!: (value: unknown) => void, selected = true;
    const f = fixture(() => new Promise(value => {resolve = value;})), controller = new AbortController();
    const pending = f.source.read(query, '', {signal: controller.signal, isCurrent: () => selected});
    if (end === 'query') selected = false;else if (end === 'signal') controller.abort();else if (end === 'authority') f.retire();else f.source.dispose();
    resolve(list());
    await assert.rejects(pending, {name: 'AbortError'});
    if (end === 'signal' || end === 'dispose') assert.equal(f.calls[0].signal?.aborted, true);
    f.source.dispose();
  }
});
test('a share link carries this server and the invitation\'s own code, never its private note', async () => {
  const f = fixture(() => list());
  try {
    const link = await f.source.link(row, live()), parsed = parseJoinLink(link);
    assert.deepEqual([parsed?.server, parsed?.id, parsed?.code], [origin, serverId, code]);
    assert.equal(link.includes('Private'), false);
    await assert.rejects(f.source.link({...row, state: 'accepted'}, live()), /no longer usable/);
  } finally {f.source.dispose();}
  const expired = fixture(() => list({...wire, expiresAt: '2000-01-01T00:00:00Z'}));
  try {await assert.rejects(expired.source.link(row, live()), /no longer usable/);} finally {expired.source.dispose();}
});
test('the typed code is the invitation\'s own code, and showing it again shows the same one', async () => {
  const f = fixture(() => list());
  try {
    const first = await f.source.shortCode(row, 'one', live()), again = await f.source.shortCode(row, 'two', live());
    assert.equal(first.shortCode, code); assert.deepEqual(first, again);
    await assert.rejects(f.source.shortCode(row, '', live()), /invitation_operation_required/);
  } finally {f.source.dispose();}
});
test('revoking reads the invitation back as revoked, and an edit sends who it is for and what it shares', async () => {
  let state = 'open';
  const f = fixture((_path, method, body) => method === 'DELETE' ? (state = 'revoked', undefined) : method === 'PATCH' ? {...wire, ...(body as object)} : list({...wire, state}));
  try {
    const edited = await f.source.edit(row, {role: 'admin', allowedLibraries: [], expectedRevision: 1, expiresAt: row.expiresAt, operationId: 'edit'}, live());
    assert.equal(edited.role, 'admin'); assert.deepEqual(f.calls[0].body, {role: 'admin', allLibraries: true, libraries: [], note: ''});
    const revoked = await f.source.revoke(row, 'revoke', live());
    assert.equal(revoked.state, 'revoked'); assert.equal(f.calls[1].method, 'DELETE'); assert.equal(f.calls[1].path, '/v1/admin/invitations/invitation');
  } finally {f.source.dispose();}
});
test('creation returns its code, and an owner refusal closes all later requests', async () => {
  const f = fixture(() => wire);
  try {
    const value = await f.source.create({role: 'member', allowedLibraries: ['library'], expiresInHours: 24, operationId: 'create-key', notes: row.notes}, live());
    assert.equal(value.code, code); assert.equal(value.notes, row.notes);
    assert.deepEqual(f.calls[0].body, {role: 'member', allLibraries: false, libraries: ['library'], note: row.notes, expiresInDays: 1});
  } finally {f.source.dispose();}
  const refusal = Object.assign(new Error('owner permission ended'), {status: 403}), denied = fixture(() => {throw refusal;});
  try {
    await assert.rejects(denied.source.read(query, '', live()), refusal);
    await assert.rejects(denied.source.link(row, live()), {name: 'AbortError'}); assert.equal(denied.calls.length, 1);
  } finally {denied.source.dispose();}
});
