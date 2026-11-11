import {test} from 'node:test';
import assert from 'node:assert/strict';
import {ServerBackupSource, notMadeHere} from '../src/server-admin/server-backups.ts';

const listed = {id: 'bkp_1', createdAt: '2026-11-10T03:00:00Z', scheduled: true, version: '3.0.0', size: 4096, credentials: [], pictures: 0, artwork: false};
const page = (more: Record<string, unknown> = {}) => ({items: [listed], folder: '/srv/backups', folderWritable: true, readable: true, readableMeans: [], ...more});
const settings = {name: 'backups', values: {days: [0, 1, 2, 3, 4, 5, 6], hour: 3, keep: 7, folder: '', includeArtwork: false}, defaults: {}, etag: '"1"'};
type Call = {path: string; method: string; body?: unknown};
function server(answer: (call: Call) => unknown) {
  const calls: Call[] = [];
  const api = {request: async <T,>(path: string, method = 'GET', body?: unknown) => {
    const call = {path, method, ...(body === undefined ? {} : {body})}; calls.push(call);
    const out = answer(call); if (out instanceof Error) throw out; return out as T;
  }};
  return {api, calls};
}
const reads = (call: Call) => call.path === '/v1/admin/backups' ? page() : call.path === '/v1/admin/settings/backups' ? settings : undefined;
const refusal = (status: number, code: string, message = '') => Object.assign(new Error(message), {status, code});

test('the backups page lists what the server holds, in the list\'s shape, with the backups settings', async () => {
  const {api} = server(reads);
  const source = new ServerBackupSource(api, () => true, () => {});
  await source.load();
  const state = source.getSnapshot();
  assert.equal(state.backups?.folder, '/srv/backups');
  assert.deepEqual(state.list.map(b => [b.id, b.kind, b.bytes, b.serverVersion]), [['bkp_1', 'scheduled', 4096, '3.0.0']]);
  assert.equal(state.settings?.keep, 7);
});

test('a restore sends the backup and the owner\'s password; one not made here is confirmed with the typed word, then staged', async () => {
  let confirmed = false;
  const {api, calls} = server(call => {
    if (call.path === '/v1/admin/backups/restore') {
      const body = call.body as Record<string, unknown>;
      if (!body.notMadeHereConfirmed) return refusal(409, 'backup_not_made_here', 'This backup wasn’t made by this server.');
      confirmed = true; return {playing: 2, canRestart: true};
    }
    return call.path === '/v1/admin/backups' ? page(confirmed ? {pendingRestore: {backup: 'bkp_1', stagedAt: '2026-11-11T20:00:00Z'}} : {}) : reads(call);
  });
  const source = new ServerBackupSource(api, () => true, () => {});
  await source.load();
  const refused = await source.restore({id: 'bkp_1'}, {password: 'pw', code: '123456'}).catch(error => error);
  assert.equal(notMadeHere(refused), 'This backup wasn’t made by this server.');
  const staged = await source.restore({id: 'bkp_1'}, {password: 'pw', notMadeHereConfirmed: true});
  assert.deepEqual(staged, {playing: 2, canRestart: true});
  const sent = calls.filter(c => c.path === '/v1/admin/backups/restore').map(c => c.body);
  assert.deepEqual(sent, [{id: 'bkp_1', currentPassword: 'pw', code: '123456'}, {id: 'bkp_1', currentPassword: 'pw', notMadeHereConfirmed: true}]);
  assert.equal(source.getSnapshot().backups?.pendingRestore?.backup, 'bkp_1');
  assert.equal(source.getSnapshot().staged?.playing, 2);
});

test('a wrong password leaves the page open; a signed-out answer ends it', async () => {
  let answer: Error = refusal(403, 'current_password_incorrect', 'The current password is not correct.');
  const {api} = server(call => call.path === '/v1/admin/backups/restore' ? answer : reads(call));
  const source = new ServerBackupSource(api, () => true, () => {});
  await source.load();
  await assert.rejects(source.restore({path: '/media/old.portico-backup'}, {password: 'wrong'}), /not correct/);
  assert.ok(source.getSnapshot().backups, 'still showing the backups');
  answer = refusal(401, 'unauthorized');
  await assert.rejects(source.restore({id: 'bkp_1'}, {password: 'pw'}));
  assert.equal(source.getSnapshot().backups, undefined, 'ended');
});

test('Back up now follows the backup until it is listed; Restart now asks the server to restart', async () => {
  let looks = 0;
  const {api, calls} = server(call => {
    if (call.method === 'POST' && call.path === '/v1/admin/backups') return {id: 'bkp_2'};
    if (call.path === '/v1/admin/backups') return page(++looks < 3 ? {running: {id: 'bkp_2', startedAt: '2026-11-11T20:00:00Z', pagesLeft: 10}} : {});
    if (call.path.startsWith('/v1/admin/restart')) return {playing: 0, canRestart: true, restarting: true};
    return reads(call);
  });
  const source = new ServerBackupSource(api, () => true, () => {}, 1);
  await source.backupNow();
  assert.equal(source.getSnapshot().backups?.running, undefined);
  assert.ok(looks >= 3, 'read again while it ran');
  assert.equal(await source.restart(), true);
  assert.ok(calls.some(c => c.method === 'POST' && c.path === '/v1/admin/restart?confirm=true'));
  assert.equal(source.getSnapshot().restarting, true);
});
