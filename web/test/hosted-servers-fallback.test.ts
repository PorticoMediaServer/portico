import test from 'node:test';
import assert from 'node:assert/strict';
import {fallbackFor, isLocalRoute} from '../src/screens/accounts/hosted-fallback.ts';
import type {DirectoryItem} from '../src/services/directory-flow.ts';

const server: DirectoryItem = {id: 'srv_' + 'a'.repeat(43), name: 'Cottage', baseUrl: 'https://66-94-102-39.ptc-bbbbbbbbbbbbbbbbbbbq.portico.direct:32500', publicKey: 'p'.repeat(43), policyRevision: 1, secureConnection: 'ready', endpointVerified: true, lastSeenAt: null, presence: {lastSeenAt: null}, ownedByMe: false, role: 'member', status: 'active'};
const lan = 'https://192-168-1-20.ptc-bbbbbbbbbbbbbbbbbbbq.portico.direct:32500';

test('P2-66 D-4: one fallback, worded for the case, never followed by itself', () => {
  const opened: string[] = [], open = (address: string) => {opened.push(address);};
  const none = new Map<string, string>(), known = new Map([[server.id, lan]]);
  const update = fallbackFor({status: 'update'}, server, known, open);
  assert.equal(update?.kind, 'direct'); assert.deepEqual(opened, []);
  update!.onPress(); assert.deepEqual(opened, [server.baseUrl + '/']);
  assert.equal(fallbackFor({status: 'waiting'}, server, known, open)?.kind, 'local');
  assert.equal(fallbackFor({status: 'unreachable'}, server, none, open), undefined);
  assert.equal(fallbackFor({status: 'account-waiting'}, server, known, open), undefined, 'only the owner gets a way in while Portico Accounts are away');
  assert.equal(fallbackFor({status: 'account-waiting'}, {...server, ownedByMe: true}, known, open)?.kind, 'local');
  assert.equal(fallbackFor({status: 'account-waiting'}, {...server, ownedByMe: true}, none, open)?.kind, 'direct');
  for (const status of [undefined, 'identity', 'clock'] as const) assert.equal(fallbackFor({status}, server, known, open), undefined, String(status));
  assert.equal(fallbackFor({status: 'waiting'}, server, new Map([[server.id, server.baseUrl!]]), open), undefined, 'a public address is not a local one');
});

test('local routes are private addresses, direct names encoding one, and home-network names', () => {
  for (const origin of [lan, 'https://192.168.1.20:32500', 'https://10.0.0.4', 'https://172.20.1.1:32500', 'https://[fd12::1]:32500', 'http://portico.local:32500']) assert.equal(isLocalRoute(origin), true, origin);
  for (const origin of [server.baseUrl!, 'https://172.32.0.1', 'https://example.com', 'not a url']) assert.equal(isLocalRoute(origin), false, origin);
});
