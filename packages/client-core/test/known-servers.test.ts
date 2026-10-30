import test from 'node:test';
import assert from 'node:assert/strict';
import {KnownDirectServers, MAX_KNOWN_SERVERS, keyValueKnownServerStorage, knownServerAddress, parseKnownServers} from '../src/known-servers.ts';
import {HttpHostedApi, parseServerPresence} from '../src/index.ts';
import {parseRemoteAccess} from '../src/remote-access.ts';

function memory() {
  const map = new Map<string, string>();
  return {getItem: (k: string) => map.get(k) ?? null, setItem: (k: string, v: string) => { map.set(k, v); }, map};
}

test('remembered direct servers: newest first, deduplicated, bounded, no credentials', async () => {
  let now = Date.parse('2026-09-23T10:00:00Z');
  const store = memory();
  const known = new KnownDirectServers(keyValueKnownServerStorage(store), () => now);
  await known.remember({serverId: 'srv-a', name: 'Home', address: 'http://192.168.1.10:32500', lastProfileId: 'p1', lastProfileName: 'Justin'});
  now += 1000;
  await known.remember({serverId: 'srv-b', name: 'Cabin', address: 'https://cabin.example.com/'});
  now += 1000;
  await known.remember({serverId: 'srv-a', name: 'Home server', address: 'http://192.168.1.10:32500'});
  const list = await known.list();
  assert.deepEqual(list.map(s => [s.serverId, s.name]), [['srv-a', 'Home server'], ['srv-b', 'Cabin']]);
  assert.equal(list[1]!.address, 'https://cabin.example.com');
  assert.equal(list[0]!.lastProfileId, undefined, 'a new sign-in record replaces the old one');
  await known.setProfile('srv-b', {id: 'p2', name: 'Kids'});
  assert.equal((await known.list())[1]!.lastProfileName, 'Kids');
  await known.forget('srv-a');
  assert.deepEqual((await known.list()).map(s => s.serverId), ['srv-b']);
  assert.ok(!/token|secret|password/i.test([...store.map.values()].join('')));
  for (let i = 0; i < MAX_KNOWN_SERVERS + 5; i++) { now += 1; await known.remember({serverId: `s${i}`, name: `S${i}`, address: 'http://10.0.0.1'}); }
  assert.equal((await known.list()).length, MAX_KNOWN_SERVERS);
  await assert.rejects(known.remember({serverId: 'x', name: 'X', address: 'ftp://nope'}));
});

test('remembered direct servers: tolerant of what is stored', () => {
  assert.deepEqual(parseKnownServers(undefined), []);
  assert.deepEqual(parseKnownServers({version: '2', items: []}), []);
  const parsed = parseKnownServers({version: '1', items: [{serverId: 'a', name: 'A', address: 'http://h', lastUsedAt: '2026-01-01T00:00:00Z'}, {serverId: 'bad id!', name: 'B', address: 'http://h', lastUsedAt: '2026-01-01T00:00:00Z'}, 'junk']});
  assert.deepEqual(parsed.map(s => s.serverId), ['a']);
  for (const bad of ['http://u:p@h', 'http://h/path', 'http://h?q=1', 'javascript:alert(1)', 42]) assert.equal(knownServerAddress(bad), undefined, String(bad));
});

test('hosted server presence is read defensively', async () => {
  assert.deepEqual(parseServerPresence({online: true, lastSeenAt: '2026-09-23T10:00:00Z'}), {online: true, lastSeenAt: '2026-09-23T10:00:00Z'});
  assert.deepEqual(parseServerPresence({online: false, lastSeenAt: null}), {online: false, lastSeenAt: null});
  assert.equal(parseServerPresence({online: 'yes'}), undefined);
  assert.equal(parseServerPresence(null), undefined);
  const api = new HttpHostedApi('https://account.example', 'token', async () => new Response(JSON.stringify({items: [
    {id: 's1', name: 'Home', baseUrl: 'https://s1', publicKey: 'k', policyRevision: 1, presence: {online: true, lastSeenAt: 'garbage'}},
    {id: 's2', name: 'Old', baseUrl: 'https://s2', publicKey: 'k', policyRevision: 1},
    {id: 's3', name: 'Odd', baseUrl: 'https://s3', publicKey: 'k', policyRevision: 1, presence: 'online'},
  ]}), {status: 200, headers: {'Content-Type': 'application/json'}}));
  const {items} = await api.servers();
  assert.deepEqual(items[0]!.presence, {online: true, lastSeenAt: null});
  assert.equal(items[1]!.presence, undefined);
  assert.equal('presence' in items[2]!, false);
});

test('remote settings round-trip ipv6Open and lanSharing (a save sends them back)', () => {
  const status = {authorityId: 'a', generation: '1', state: 'lan_only', ipv6: 'verified', config: {revision: '3', enabled: true, mapping: true, pcp: true, natpmp: true, upnp: true, publicPort: 32500, gateway: '', lanSharing: true, ipv6Open: true}, topology: {lan: [], public: [], bind: ''}, candidates: [], mappings: []};
  const parsed = parseRemoteAccess(status);
  assert.equal(parsed.config.ipv6Open, true);
  assert.equal(parsed.config.lanSharing, true);
  assert.equal(parsed.ipv6, 'verified');
  const older = parseRemoteAccess({...status, ipv6: undefined, config: {...status.config, ipv6Open: undefined, lanSharing: undefined}});
  assert.equal(older.config.ipv6Open, undefined);
  assert.throws(() => parseRemoteAccess({...status, config: {...status.config, ipv6Open: 'yes'}}));
});

test('a remembered server follows the address it was last proven at, keeping its place and profile', async () => {
  let now = Date.parse('2026-10-13T10:00:00Z');
  const store = memory();
  let writes = 0;
  const counting = {getItem: store.getItem, setItem: (k: string, v: string) => { writes++; store.setItem(k, v); }};
  const known = new KnownDirectServers(keyValueKnownServerStorage(counting), () => now);
  await known.remember({serverId: 'srv-a', name: 'Home', address: 'http://192.168.1.10:32500', lastProfileId: 'p1', lastProfileName: 'Ana'});
  now += 1000;
  await known.remember({serverId: 'srv-b', name: 'Cabin', address: 'https://cabin.example.com'});
  await known.setAddress('srv-a', 'http://192.168.1.44:32500');
  const list = await known.list();
  assert.deepEqual(list.map(s => s.serverId), ['srv-b', 'srv-a'], 'order is by last sign-in, not by this update');
  assert.equal(list[1]!.address, 'http://192.168.1.44:32500');
  assert.equal(list[1]!.lastProfileName, 'Ana');
  const before = store.map.get('portico.knownServers');
  await known.setAddress('srv-a', 'http://192.168.1.44:32500');
  await known.setAddress('srv-unknown', 'http://192.168.1.50:32500');
  await known.setAddress('srv-a', 'http://user:pw@192.168.1.60/path');
  assert.equal(store.map.get('portico.knownServers'), before, 'unchanged, unknown or unusable addresses change nothing');
});
