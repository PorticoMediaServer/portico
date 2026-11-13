import test from 'node:test';
import assert from 'node:assert/strict';
import {OwnerRemoteSettings} from '../src/server-admin/remote-settings.ts';

function fixture() {
 const remote = {advertisedInterface: '', interfaces: [{name: 'eth0', addresses: ['10.0.0.2']}], remoteAccess: 'on', remoteSecurity: 'enhanced', secureConnections: 'preferred', signInFromOutside: 'anyone', portMapping: 'automatic', publicPort: 32500, ipv6: false, findAtHome: true, dlna: false, ownAddress: {enabled: false, domain: '', certificateFile: '', keyFile: ''}, allowedAddresses: [], certificateWarningDays: 14};
 const network = {homeNetworks: ['10.0.0.0/24'], homeNetworksDefault: true, trustedProxies: [], webOrigins: ['https://tools.example'], builtInWebOrigins: [], warnings: []};
 const status = {thisDevice: {atHome: true, address: '10.0.0.3'}, refused: [{address: 'media.example:443', reason: 'address', fixes: ['allow']}], warnings: [], remoteSecurity: {chosen: 'enhanced', active: 'enhanced'}, addresses: [], certificate: {state: 'none'}, identityCode: 'actual-server'};
 const calls: {path: string; method?: string; options?: any}[] = [];
 let failure: {status: number; code: string} | undefined, current = true;
 const api = {
  request: async <T>(path: string, method?: string): Promise<T> => {calls.push({path, method});return (path.endsWith('blocked-networks') ? {items: [{id: 'b', network: '10.2.3.4', blockedUntil: '2026-11-13T18:00:00Z'}]} : status) as T;},
  requestRaw: async (path: string, method: string, options?: any) => {
   calls.push({path, method, options});
   if (method === 'GET') return {status: 200, headers: {ETag: path.endsWith('network') ? '"8"' : '"7"'}, body: path.endsWith('network') ? structuredClone(network) : structuredClone(remote)};
   if (failure) return {status: failure.status, headers: {}, body: {error: {code: failure.code, message: 'This would refuse your connection.'}}};
   return {status: 200, headers: {}, body: {}};
  },
 };
 const source = new OwnerRemoteSettings(api, () => current);
 return {source, calls, remote, fail: (value?: typeof failure) => {failure = value;}, retire: () => {current = false;}};
}

test('Remote loads its actual documents without an outside check; saves preserve settings and the exact version', async () => {
 const f = fixture();try {
  await f.source.readConfiguration();
  assert.equal(f.source.read('remote.interface')?.current, 'all');
  assert.equal(f.source.read('remote.mapping')?.current, 'auto');
  assert.equal(f.calls.some(call => call.method !== 'GET'), false);
  await f.source.save('remote.publicPort', 33443);
  const write = f.calls.at(-1)!;assert.equal(write.path, '/v1/admin/settings/remote-access');
  assert.equal(write.options.headers['If-Match'], '"7"');
  assert.equal(write.options.body.publicPort, 33443);assert.equal(write.options.body.certificateWarningDays, 14);
  assert.equal('interfaces' in write.options.body, false);
  await f.source.save('remote.trustedProxies', ['10.0.0.4']);
  const network = f.calls.at(-1)!;assert.equal(network.options.headers['If-Match'], '"8"');
  assert.deepEqual(network.options.body, {trustedProxies: ['10.0.0.4'], webOrigins: ['https://tools.example']}, 'the guessed home networks must stay automatic');
  await f.source.save('remote.homeNets', []);assert.deepEqual(f.calls.at(-1)?.options.body.homeNetworks, []);
  await f.source.save('remote.interface', 'eth0');
  const selected = f.calls.findLast(call => call.method === 'PUT')!;assert.equal(selected.options.body.advertisedInterface, 'eth0');
  assert.equal(selected.options.body.publicPort, 32500);assert.equal('interfaces' in selected.options.body, false);
 } finally {f.source.dispose();}
});

test('a route refusal requires an explicit confirmation; stale confirmation refreshes once without replay', async () => {
 const f = fixture();try {
  await f.source.readConfiguration();f.fail({status: 409, code: 'would_refuse_this_route'});
  await assert.rejects(f.source.save('remote.access', 'off'), {code: 'would_refuse_this_route'});
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
  assert.equal(f.source.getSnapshot().confirmation?.message, 'This would refuse your connection.');
  f.fail({status: 412, code: 'changed_elsewhere'});await f.source.confirm();
  assert.equal(f.calls.filter(call => call.method === 'PUT').length, 2);
  const confirmed = f.calls.findLast(call => call.method === 'PUT')!;
  assert.equal(confirmed.options.body.confirmRefusal, true);assert.equal(confirmed.options.headers['If-Match'], '"7"');
  assert.equal(f.source.getSnapshot().confirmation, undefined);assert.equal((f.source.getSnapshot().error as {code: string}).code, 'changed_elsewhere');
  f.fail();await f.source.saveOwnAddress({enabled: true, domain: 'media.example', certificateFile: 'cert.pem', keyFile: 'key.pem'});
  assert.deepEqual(f.calls.findLast(call => call.method === 'PUT')?.options.body.ownAddress, {enabled: true, domain: 'media.example', certificateFile: 'cert.pem', keyFile: 'key.pem'});
  const count = f.calls.length;f.retire();await f.source.check();await assert.rejects(f.source.save('remote.access', 'off'), {name: 'AbortError'});assert.equal(f.calls.length, count);
 } finally {f.source.dispose();}
});
