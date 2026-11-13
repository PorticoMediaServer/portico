import test from 'node:test';
import assert from 'node:assert/strict';
import {OwnerGeneralSettings, readBackgroundPause, setBackgroundPause} from '../src/server-admin/general-settings.ts';

function fixture() {
 const reads: string[] = [], writes: any[] = [];
 const defaults: Record<string, any> = {
  general: {name: 'Portico', backgroundSpeed: 'normal', updateCheck: 'automatic', heavyWork: {hours: 'any'}},
  storage: {warnPercent: 10, majorPercent: 5}, notices: {serverNoticesTo: 'owner', keepDays: 180},
 };
 const values = structuredClone(defaults);
 const remote = {remoteAccess: 'on', remoteSecurity: 'standard', certificateWarningDays: 30, allowedAddresses: ['media.example'], ownAddress: {domain: 'media.example'}, interfaces: [], interfaceNote: 'read-only'};
 let refused = false;
 const api = {
  request: async <T>(path: string, method?: string): Promise<T> => {
   reads.push(path);
   if (path === '/v1/admin/background/pause') return {pausedUntil: method === 'DELETE' ? undefined : '2026-11-13T18:00:00Z'} as T;
   const name = path.split('/').at(-1)!; assert.ok(defaults[name], path);
   return {name, etag: '"7"', values: values[name], defaults: defaults[name]} as T;
  },
  requestRaw: async (path: string, method: string, options?: any) => {
   if (method === 'GET') {reads.push(path);return {status: 200, headers: {ETag: '"9"'}, body: remote};}
   writes.push({path, ...options});
   if (refused) return {status: 412, headers: {}, body: {error: {code: 'changed_elsewhere'}}};
   const name = path.split('/').at(-1)!; if (values[name]) Object.assign(values[name], options.body);
   return {status: 200, headers: {}, body: {}};
  },
 };
 return {api, reads, writes, refuse: () => {refused = true;}};
}

test('General reads real documents and saves settings with their own ETags, preserving remote settings', async () => {
 const f = fixture(), source = new OwnerGeneralSettings(f.api);
 await source.readConfiguration();
 assert.equal(source.read('general.name')?.current, 'Portico');
 assert.equal(source.read('general.heavyWork')?.current, 'any');
 assert.equal(source.read('general.serverNoticesTo')?.current, 'owner');
 assert.equal(source.has('general.configFile'), false);
 assert.deepEqual(f.reads.sort(), ['general', 'notices', 'remote-access', 'storage'].map(n => '/v1/admin/settings/' + n).sort());
 await source.save('general.name', 'Den');
 assert.deepEqual(f.writes.at(-1).headers, {'If-Match': '"7"'});
 assert.deepEqual(f.writes.at(-1).body, {name: 'Den'});
 await source.save('general.alertCertificateDays', '14');
 assert.deepEqual(f.writes.at(-1).headers, {'If-Match': '"9"'});
 assert.deepEqual(f.writes.at(-1).body, {remoteAccess: 'on', remoteSecurity: 'standard', certificateWarningDays: 14, allowedAddresses: ['media.example'], ownAddress: {domain: 'media.example'}});
 f.refuse();const before = f.writes.length;
 await assert.rejects(source.save('general.serverNoticesTo', 'owner-and-admins'), {code: 'changed_elsewhere'});
 assert.equal(f.writes.length, before + 1);
});

test('General custom update address and heavy hours use the actual server fields, and pause is running state', async () => {
 const f = fixture(), source = new OwnerGeneralSettings(f.api);await source.readConfiguration();
 await source.save('general.updateChecks', 'custom');assert.equal(f.writes.length, 0);
 await source.readConfiguration();assert.equal(source.read('general.updateChecks')?.current, 'custom');
 await source.save('general.updateFeed', 'https://updates.example/releases');
 assert.deepEqual(f.writes.at(-1).body, {updateCheck: 'address', updateAddress: 'https://updates.example/releases'});
 await source.save('general.heavyWork', 'custom');await source.readConfiguration();
 await source.save('general.heavyStart', '1380');
 assert.deepEqual(f.writes.at(-1).body, {heavyWork: {hours: 'chosen', days: ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'], from: '23:00', to: '04:00'}});
 assert.equal(await readBackgroundPause(f.api), '2026-11-13T18:00:00Z');
 assert.equal(await setBackgroundPause(f.api, true), '2026-11-13T18:00:00Z');
 assert.equal(await setBackgroundPause(f.api, false), undefined);
});
