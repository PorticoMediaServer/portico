import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, parseAppSessionRecord, type AppSessionStorage, type AppSessionRecord, type AppServerSession} from '../src/app-sessions.ts';
import type {RememberedServer} from '../src/server-connections.ts';
const hosted = {origin: 'https://accounts.test', accountId: 'portico-account', key: {keyId: 'key', publicKey: 'k'.repeat(43)}};
function entry(id: string, portico = false): AppServerSession {
  return {signIn: portico ? 'portico' : 'local', connection: {version: '1', selectionId: id.padEnd(32, '_'), logicalOrigin: 'https://' + id + '.test',
    session: {accessToken: 'token-' + id, expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', sessionFamilyId: 'family-' + id, tokenGeneration: '1',
      viewer: {serverId: id, authority: 'local', accountId: 'membership-' + id, profileId: 'profile-' + id, role: 'member'}}, pin: {serverId: id, publicKey: 'p', fingerprint: 'f'}, routes: {paired: ['https://' + id + '.test']}, ...(portico ? {hosted} : {})}};
}
function storage(values: AppServerSession[] = []) {
  let raw: AppSessionRecord | undefined = values.length ? {version: 1, revision: 1, servers: values, selectedServerId: values[0].connection.pin.serverId} : undefined;
  const port: AppSessionStorage = {read: async () => structuredClone(raw), change: async update => {const next = update(structuredClone(raw)); raw = structuredClone(next);}};
  return new AppSessionStore(port);
}
test('absent normalizes; several server identities/profiles survive switches and scoped rotation', async () => {
  assert.deepEqual(parseAppSessionRecord(null), {version: 1, revision: 0, servers: []});
  const a = entry('a'), b = entry('b', true), store = storage([a, b]);
  const selected = store.selectedStorage(); assert.equal((await selected.read())?.pin.serverId, 'a');
  await store.select('b');
  await selected.change(value => ({...value!, session: {...value!.session, accessToken: 'rotated', tokenGeneration: '2'}}));
  const state = await store.read();
  assert.equal(state.selectedServerId, 'b'); assert.equal(state.servers.length, 2);
  assert.equal((await selected.read())?.session.accessToken, 'rotated');
  assert.equal((await store.selected())?.connection.session.accessToken, b.connection.session.accessToken);
});
test('server signout removes exactly captured family, retains others, and stale adapters cannot resurrect', async () => {
  const a = entry('a'), b = entry('b', true), store = storage([a, b]);
  const selected = store.connectionStorage('a');
  assert.deepEqual(await store.signOutServer('a', 'old-family'), []);
  const removed = await store.signOutServer('a', a.connection.session.sessionFamilyId);
  assert.equal(removed.length, 1); assert.equal((await store.read()).servers.length, 1);
  await assert.rejects(selected.change(() => a.connection), {code: 'cancelled'});
  assert.equal((await store.read()).servers[0].connection.pin.serverId, 'b');
});
test('Portico signout uses account service scope, never server-local account id; Local survives offline', async () => {
  const a = entry('a'), b = entry('b', true), c = entry('c', true), store = storage([a, b, c]);
  assert.equal((await store.signOutPorticoAccount({origin: hosted.origin, accountId: 'membership-b'})).length, 0);
  await store.select('c'); const removed = await store.signOutPorticoAccount(hosted);
  assert.deepEqual(removed.map(v => v.connection.pin.serverId), ['b', 'c']);
  assert.deepEqual((await store.read()).servers.map(v => v.connection.pin.serverId), ['a']);
  assert.equal((await store.read()).selectedServerId, undefined);
});
test('new sign-in must capture revision before credentials; signout fences late handoff', async () => {
  const store = storage(), a = entry('a');
  const stale = store.connectionStorage('a', {signIn: 'local', expectedRevision: 0});
  await store.signOutServer('a', 'family-a');
  await assert.rejects(stale.change(() => a.connection), {code: 'cancelled'});
  const current = await store.read();
  await store.connectionStorage('a', {signIn: 'local', expectedRevision: current.revision}).change(() => a.connection);
  assert.equal((await store.selected())?.connection.pin.serverId, 'a');
});
test('late same-family cleanup cannot remove replacement family and atomic failures preserve originals', async () => {
  const a = entry('a'), b = entry('b'), store = storage([a, b]);
  const replacement: RememberedServer = {...a.connection, selectionId: 'n'.repeat(32), session: {...a.connection.session, sessionFamilyId: 'new-family'}};
  await store.connectionStorage('a', {signIn: 'local', expectedRevision: 1}).change(() => replacement);
  await store.signOutServer('a', 'family-a');
  assert.equal((await store.selected())?.connection.session.sessionFamilyId, 'new-family');
  const before = await store.read();
  await assert.rejects(store.connectionStorage('b').change(() => {throw new Error('disk refused');}));
  assert.deepEqual(await store.read(), before);
});
test('invalid kind/account bindings, duplicate servers, Hosted profiles and mixed Hosted identities fail closed', () => {
  const a = entry('a'), b = entry('b', true);
  for (const servers of [[a, a], [{...b, signIn: 'local'}], [{...a, signIn: 'portico'}], [b, {...entry('c', true), connection: {...entry('c', true).connection, hosted: {...hosted, accountId: 'other'}}}],
    [{...a, connection: {...a.connection, session: {...a.connection.session, viewer: {...a.connection.session.viewer, authority: 'hosted'}}}}]]) {
    assert.throws(() => parseAppSessionRecord({version: 1, revision: 0, servers}), {code: 'invalid_saved_connection'});
  }
});

test('cancelled durable admission restores exact previous family and cannot overwrite a newer candidate', async () => {
  const a = entry('a'), b = entry('b'), store = storage([a, b]);
  const adapter = store.connectionStorage('a', {signIn: 'local', expectedRevision: 1});
  let previous: RememberedServer | undefined;
  const candidate = {...a.connection, selectionId: 'c'.repeat(32), session: {...a.connection.session, sessionFamilyId: 'candidate'}};
  await adapter.change(current => {previous = current; return candidate;});
  await adapter.change(current => current?.selectionId === candidate.selectionId ? previous : current);
  assert.equal((await adapter.read())?.session.sessionFamilyId, 'family-a');
  assert.equal((await store.read()).servers.length, 2);
  const newer = {...a.connection, selectionId: 'n'.repeat(32), session: {...a.connection.session, sessionFamilyId: 'newer'}};
  await store.connectionStorage('a', {signIn: 'local', expectedRevision: (await store.read()).revision}).change(() => newer);
  await adapter.change(current => current?.selectionId === candidate.selectionId ? previous : current);
  assert.equal((await adapter.read())?.session.sessionFamilyId, 'newer');
});

test('a rendered server choice consumes only its original saved-record revision', async () => {
  const store = storage([entry('a'), entry('b')]), rendered = await store.read();
  // The same id survives a family replacement; an id-only choice would select it.
  await store.connectionStorage('b').change(value => ({...value!, session: {...value!.session, accessToken: 'renewed', tokenGeneration: '2'}}));
  const replaced = await store.read();
  await assert.rejects(store.select('b', () => true, rendered.revision), {code: 'cancelled'});
  assert.deepEqual(await store.read(), replaced);
  await store.select('b', () => true, replaced.revision);
  const accepted = await store.read(); assert.equal(accepted.selectedServerId, 'b');
  await assert.rejects(store.select('a', () => true, replaced.revision), {code: 'cancelled'});
  assert.deepEqual(await store.read(), accepted);
  await assert.rejects(store.select('a', () => true, -1));
  assert.deepEqual(await store.read(), accepted);
});
