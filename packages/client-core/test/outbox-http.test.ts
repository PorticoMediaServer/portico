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
