import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type LocalSession, type OutboxEntry} from '../src/index.ts';

const session = {accessToken: 'token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z', tokenGeneration: '1', sessionFamilyId: 'family',
  viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'member'}} as unknown as LocalSession;
const settle = async () => { for (let n = 0; n < 20; n++) await new Promise(resolve => setImmediate(resolve)); };

test('a page that is not the server\'s answer, such as a hotel sign-in page, does not count as saved', async () => {
  const kept = new Map<string, OutboxEntry>();
  const store = {list: async () => [...kept.values()], put: async (entry: OutboxEntry) => { kept.set(entry.id, entry); }, delete: async (id: string) => { kept.delete(id); }};
  let portal = true, saved = 0;
  const api = new HttpLocalApi('http://server.test', session.accessToken, async () => {
    if (portal) return new Response('<html>Sign in to the hotel network</html>', {status: 200, headers: {'Content-Type': 'text/html'}});
    saved++;
    return Response.json({operationId: 'op-1', revision: 2});
  });
  api.setOutboxSession(session);
  const outbox = api.configureOutbox(store);
  try {
    await assert.rejects(api.request('/v1/items/one/reading/mark', 'PUT', {operationId: 'op-1', expectedRevision: 1, read: true}), {code: 'sync_pending'});
    assert.equal(outbox.waiting(), 1, 'the edit is kept');
    portal = false; outbox.wake();
    await settle();
    assert.equal(saved, 1); assert.equal(outbox.waiting(), 0);
  } finally { outbox.dispose(); }
});

test('a device whose storage cannot keep the edit still sends it once and hears the answer', async () => {
  const broken = {list: async () => { throw new Error('storage is blocked'); }, put: async () => { throw new Error('storage is blocked'); }, delete: async () => {}};
  const sent: (string | null)[] = [];
  const api = new HttpLocalApi('http://server.test', session.accessToken, async (_input, init) => {
    sent.push(new Headers(init?.headers).get('Idempotency-Key'));
    return Response.json({operationId: 'op-2', revision: 2});
  });
  api.setOutboxSession(session);
  const outbox = api.configureOutbox(broken);
  try {
    assert.deepEqual(await api.request('/v1/items/one/reading/mark', 'PUT', {operationId: 'op-2', expectedRevision: 1, read: true}), {operationId: 'op-2', revision: 2});
    assert.deepEqual(sent, ['op-2']);
  } finally { outbox.dispose(); }
});

// The reader saves its place through the outbox with its own `content-type`. Sent beside the
// default one, fetch joined them ("application/json, application/json") and the server refused
// every place with 415, so reading never resumed where it stopped.
test('a place the reader saves through the outbox is sent with one content type', async () => {
  const kept = new Map<string, OutboxEntry>();
  const store = {list: async () => [...kept.values()], put: async (entry: OutboxEntry) => { kept.set(entry.id, entry); }, delete: async (id: string) => { kept.delete(id); }};
  const types: (string | null)[] = [];
  const api = new HttpLocalApi('http://server.test', session.accessToken, async (_input, init) => {
    types.push(new Headers(init?.headers).get('Content-Type'));
    return Response.json({position: 'place'});
  });
  api.setOutboxSession(session);
  const outbox = api.configureOutbox(store);
  const reading = api.reading({viewer: session.viewer, isCurrent: () => true});
  try {
    assert.deepEqual(await reading.setState('itm_book', {position: 'place'}), {position: 'place'});
    assert.deepEqual(types, ['application/json']);
  } finally { reading.dispose(); outbox.dispose(); }
});

// Area 6 rule 43: a title added to a playlist while the server is out of reach is kept and sent later,
// with the caller's own idempotency key, so a second send never adds it twice.
test('a title added to a playlist with no server is kept and sent when it is back', async () => {
  const kept = new Map<string, OutboxEntry>();
  const store = {list: async () => [...kept.values()], put: async (entry: OutboxEntry) => { kept.set(entry.id, entry); }, delete: async (id: string) => { kept.delete(id); }};
  let reachable = false;
  const sent: {path: string; method: string; key: string | null; body: unknown}[] = [];
  const api = new HttpLocalApi('http://server.test', session.accessToken, async (input, init) => {
    if (!reachable) throw new TypeError('Failed to fetch');
    sent.push({path: new URL(String(input)).pathname, method: init?.method ?? 'GET', key: new Headers(init?.headers).get('Idempotency-Key'), body: JSON.parse(String(init?.body))});
    return Response.json({added: 1, skipped: [], list: {id: 'lst_1'}});
  });
  api.setOutboxSession(session);
  const outbox = api.configureOutbox(store);
  try {
    await assert.rejects(api.requestRaw('/v1/lists/lst_1/entries', 'POST', {headers: {'Idempotency-Key': 'add-1'}, body: {items: ['itm_film']}}), {code: 'sync_pending'});
    assert.equal(outbox.waiting(), 1, 'the addition is kept');
    reachable = true; outbox.wake();
    await settle();
    assert.deepEqual(sent, [{path: '/v1/lists/lst_1/entries', method: 'POST', key: 'add-1', body: {items: ['itm_film']}}]);
    assert.equal(outbox.waiting(), 0);
  } finally { outbox.dispose(); }
});

test('every list change goes through the outbox; the old playlist routes are not listed', async () => {
  const {outboxKind} = await import('../src/outbox-routes.ts');
  for (const [method, path] of [['POST', '/v1/lists'], ['PATCH', '/v1/lists/lst_1'], ['DELETE', '/v1/lists/lst_1'], ['POST', '/v1/lists/lst_1/entries'],
    ['PATCH', '/v1/lists/lst_1/entries/ent_1'], ['DELETE', '/v1/lists/lst_1/entries/ent_1']]) assert.equal(outboxKind(path, method), 'list', `${method} ${path}`);
  assert.equal(outboxKind('/v1/lists/lst_1', 'GET'), undefined);
  assert.equal(outboxKind('/v1/playlists/lst_1', 'DELETE'), undefined);
});
