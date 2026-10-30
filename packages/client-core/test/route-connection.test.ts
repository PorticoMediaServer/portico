import {test} from 'node:test';
import assert from 'node:assert/strict';
import {discoveredRoutes, gatewayFailure} from '../src/route-connection.ts';

const pin = {serverId: 'srv-1', publicKey: 'A'.repeat(43), fingerprint: 'fp-1'};
const now = Date.now();
const good = {
  baseUrl: 'http://192.168.1.8:32500',
  serverId: 'srv-1',
  fingerprint: 'fp-1',
  port: 32500,
  path: '/',
  expiresAt: now + 60000,
};

test('a single good record yields one LAN candidate', () => {
  assert.deepEqual(discoveredRoutes([good], pin, now), [
    {baseUrl: 'http://192.168.1.8:32500', class: 'lan', insecure: true, quality: 'probe_required', generation: '1'},
  ]);
});

test('more than 64 records yields nothing, however good', () => {
  const many = Array.from({length: 65}, () => ({...good}));
  assert.deepEqual(discoveredRoutes(many, pin, now), []);
});

test('a conflicting group contributes nothing', () => {
  // Another serverId under the same fingerprint poisons the group.
  assert.deepEqual(discoveredRoutes([good, {...good, serverId: 'other'}], pin, now), []);
  // A bad path poisons the group too.
  assert.deepEqual(discoveredRoutes([good, {...good, path: '/other'}], pin, now), []);
  // A bad port poisons the group.
  assert.deepEqual(discoveredRoutes([good, {...good, port: 0}], pin, now), []);
  assert.deepEqual(discoveredRoutes([good, {...good, port: 70000}], pin, now), []);
  // Two distinct port|path combos poison the group (otherwise plausible records included).
  assert.deepEqual(
    discoveredRoutes([good, {...good, baseUrl: 'http://192.168.1.8:32501', port: 32501}], pin, now),
    [],
  );
  // Records under other fingerprints are ignored, not poisoning.
  assert.equal(discoveredRoutes([good, {...good, fingerprint: 'other', serverId: 'other'}], pin, now).length, 1);
});

test('expiry is bounded: finite, future, within two minutes', () => {
  for (const bad of [NaN, Infinity, now, now - 1, now + 120001]) {
    assert.deepEqual(discoveredRoutes([{...good, expiresAt: bad}], pin, now), [], String(bad));
  }
  assert.equal(discoveredRoutes([{...good, expiresAt: now + 1}], pin, now).length, 1);
  assert.equal(discoveredRoutes([{...good, expiresAt: now + 120000}], pin, now).length, 1);
});

test('only private hosts with matching ports survive; duplicates collapse; max eight', () => {
  assert.deepEqual(discoveredRoutes([{...good, baseUrl: 'https://public.example:32500'}], pin, now), []);
  assert.deepEqual(discoveredRoutes([{...good, baseUrl: 'http://192.168.1.8:9999'}], pin, now), []);
  assert.deepEqual(discoveredRoutes([good, {...good}], pin, now).length, 1);
  const eight = Array.from({length: 10}, (_, i) => ({
    ...good,
    baseUrl: `http://192.168.1.${10 + i}:32500`,
    port: 32500,
  }));
  // Same port+path across distinct hosts does not poison (one combo), but output caps at 8.
  // To keep one combo, vary only the host octets while keeping port/path identical:
  // the group check uses port|path only, so 10 records share one combo and survive to the cap.
  const out = discoveredRoutes(eight, pin, now);
  assert.equal(out.length, 8);
});

test('a proxy error page for a down server is a lost route; Portico answers are not', () => {
  const res = (status: number, type?: string) => new Response(status === 204 ? null : 'x', {status, headers: type ? {'content-type': type} : {}});
  assert.equal(gatewayFailure(res(502)), true);
  assert.equal(gatewayFailure(res(503, 'text/html')), true);
  assert.equal(gatewayFailure(res(504, 'text/plain; charset=utf-8')), true);
  // Portico's own 503s (visibility_building, unavailable features) are JSON answers.
  assert.equal(gatewayFailure(res(503, 'application/json')), false);
  assert.equal(gatewayFailure(res(503, 'application/json; charset=utf-8')), false);
  // A proxy's own JSON dialect is still the proxy talking, not the server.
  assert.equal(gatewayFailure(res(503, 'application/problem+json')), true);
  assert.equal(gatewayFailure(res(502, 'application/vnd.proxy.error+json')), true);
  assert.equal(gatewayFailure(res(500)), false);
  assert.equal(gatewayFailure(res(404, 'text/html')), false);
  assert.equal(gatewayFailure(res(200, 'application/json')), false);
});

import {RouteConnection,type RouteProbeFailure} from '../src/route-connection.ts';
import {RouteError} from '../src/route-identity.ts';
// Promotion only ever moves to a secure route (P2-69), so nearby HTTPS stands in for recovered LAN.
const secureGood = {...good, baseUrl: 'https://192.168.1.8:32500'};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

test('foreground activity promotes recovered LAN without blanking the ready remote route or asking Hosted', async () => {
  let local = false, hosted = 0, release!: () => void;
  const probes: string[] = [];
  const remote = 'https://remote.example';
  const connection = new RouteConnection({pin, logicalOrigin: remote, memory: {paired: [remote]},
    fresh: async () => { hosted++; throw new Error('unexpected Hosted lookup'); },
    discover: async () => local ? [{...secureGood, expiresAt: Date.now() + 60000}] : [],
    probe: async origin => { probes.push(origin); if (origin === secureGood.baseUrl) await new Promise<void>(resolve => { release = resolve; }); return pin; },
  });
  try {
    assert.equal(await connection.resolve(), remote);
    local = true;
    connection.foregroundActivity(); await tick();
    assert.equal(connection.getSnapshot().phase, 'ready');
    assert.equal(connection.getSnapshot().origin, remote);
    release(); await tick();
    assert.equal(connection.getSnapshot().origin, secureGood.baseUrl);
    assert.equal(connection.getMemory().lastGood, secureGood.baseUrl);
    assert.equal(hosted, 0);
    assert.deepEqual(probes, [remote, secureGood.baseUrl]);
  } finally { connection.dispose(); }
});

test('failed local promotion keeps the remote route and throttles repeated activity', async () => {
  let local = false, attempts = 0;
  const remote = 'https://remote.example';
  const connection = new RouteConnection({pin, logicalOrigin: remote, memory: {paired: [remote]},
    discover: async () => local ? [{...secureGood, expiresAt: Date.now() + 60000}] : [],
    probe: async origin => { if (origin !== remote) { attempts++; throw new Error('LAN still down'); } return pin; },
  });
  try {
    await connection.resolve(); local = true;
    connection.foregroundActivity(); await tick();
    connection.foregroundActivity(); await tick();
    assert.equal(attempts, 1);
    assert.equal(connection.getSnapshot().origin, remote);
    assert.equal(connection.getSnapshot().phase, 'ready');
  } finally { connection.dispose(); }
});

test('a local proof arriving after disposal cannot promote the route', async () => {
  let local = false, release!: () => void;
  const remote = 'https://remote.example';
  const connection = new RouteConnection({pin, logicalOrigin: remote, memory: {paired: [remote]},
    discover: async () => local ? [{...secureGood, expiresAt: Date.now() + 60000}] : [],
    probe: async origin => { if (origin !== remote) await new Promise<void>(resolve => { release = resolve; }); return pin; },
  });
  await connection.resolve(); local = true;
  connection.foregroundActivity(); await tick();
  connection.dispose(); release(); await tick();
  assert.equal(connection.getSnapshot().origin, remote);
});

test('leaving the foreground cancels a held LAN promotion and keeps the working route', async () => {
  let local = false, release!: () => void;
  const remote = 'https://remote.example';
  const connection = new RouteConnection({pin, logicalOrigin: remote, memory: {paired: [remote]},
    discover: async () => local ? [{...secureGood, expiresAt: Date.now() + 60000}] : [],
    probe: async origin => { if (origin !== remote) await new Promise<void>(resolve => { release = resolve; }); return pin; },
  });
  try {
    await connection.resolve(); local = true;
    connection.foregroundActivity(); await tick();
    connection.setForeground(false); release(); await tick();
    assert.equal(connection.getSnapshot().origin, remote);
    assert.equal(connection.getSnapshot().phase, 'ready');
  } finally { connection.dispose(); }
});

test('probe diagnostics keep only closed categories before the ordinary race failure', async () => {
  const failures: RouteProbeFailure[] = [], origin = 'https://remote.example';
  const error = Object.assign(new SyntaxError('private proof body'), {code: 'https://private.example/secret', stack: 'private stack'});
  const connection = new RouteConnection({pin, logicalOrigin: origin, memory: {paired: [origin]},
    probe: async () => {throw error;}, onProbeFailure: failure => failures.push(failure)});
  try {
    await assert.rejects(connection.resolve(), (failure: unknown) => failure instanceof RouteError && failure.code === 'route_unavailable');
    assert.deepEqual(failures, [{code: 'other', name: 'SyntaxError'}]);
    assert.equal(Object.isFrozen(failures[0]), true);
    assert.equal(JSON.stringify(failures).includes('private'), false);
    assert.deepEqual(Object.keys(failures[0]!).sort(), ['code', 'name']);
  } finally {connection.dispose();}
});

test('throwing diagnostic observers cannot replace identity refusal or authorize lastGood', async () => {
  const origin = 'https://remote.example', failures: RouteProbeFailure[] = [];
  const connection = new RouteConnection({pin, logicalOrigin: origin, memory: {paired: [origin], lastGood: origin},
    probe: async () => {throw new RouteError('identity_mismatch', 'private pin details');},
    onProbeFailure: failure => {failures.push(failure);throw new Error('sink failed');}});
  try {
    await assert.rejects(connection.resolve(), (failure: unknown) => failure instanceof RouteError && failure.code === 'identity_mismatch');
    assert.deepEqual(failures, [{code: 'identity_mismatch', name: 'RouteError'}]);
    assert.equal(connection.getSnapshot().origin, null);
  } finally {connection.dispose();}
});

test('late failure from a retired probe is not published to diagnostics', async () => {
  const failures: RouteProbeFailure[] = [], origin = 'https://remote.example';
  let rejectProbe!: (error: Error) => void;
  const connection = new RouteConnection({pin, logicalOrigin: origin, memory: {paired: [origin]},
    probe: () => new Promise((_resolve, reject) => {rejectProbe = reject;}), onProbeFailure: failure => failures.push(failure)});
  const running = connection.resolve();
  const stopped = assert.rejects(running, (failure: unknown) => failure instanceof RouteError && failure.code === 'cancelled');
  try {
    connection.dispose();rejectProbe(new SyntaxError('private late response'));
    await stopped;await tick();
    assert.deepEqual(failures, []);
  } finally {connection.dispose();}
});
