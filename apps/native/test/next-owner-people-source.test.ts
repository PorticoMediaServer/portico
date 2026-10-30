import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = () => {let resolve!: (value: any) => void; const promise = new Promise<any>(next => {resolve = next;}); return {promise, resolve};};
const row = {accountId: 'member-fixture', username: 'member', role: 'member', revision: 2, limitsRevision: 3};
const person = {...row, disabled: false, notes: '', limits: {allowRemote: true}};
const page = (items = [row]) => ({items, totalCount: items.length, matchedCount: items.length, nextCursor: '', limit: 40});
const tick = async () => {await Promise.resolve(); await Promise.resolve();};
async function fixture(deferPerson = false, callerRole = 'owner') {
  const reads: any[] = [], details: any[] = [], deviceReads: any[] = [], writes: any[] = [], reported: unknown[] = [];
  let active = true, failNotes = false;
  const preview = {accountId: row.accountId, username: row.username, revision: 4, role: 'member', profiles: 2, devices: 1, savedResources: 3};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/people-source.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../../packages/contracts/src/routes': {routePath: (id: string) => id === 'get_admin_access_devices' ? '/v1/admin/access/devices' : '/v1/libraries'},
    '../../../../../../packages/client-core/src/library-list': {parseLibraryList: (raw: unknown) => raw},
    '../../../../../../packages/client-core/src/settings-people': {SettingsPeopleClient: class {
      read(query: any, cursor: any, signal: any) {const next = deferred(); reads.push({query, cursor, signal, ...next}); return next.promise;}
      person(accountId: string, signal: AbortSignal) {
        if (!deferPerson) return Promise.resolve(person);
        const next = deferred(); details.push({accountId, signal, ...next}); return next.promise;
      }
      async notes(captured: any, notes: any) {writes.push({kind: 'notes', captured, notes}); if (failNotes) throw new Error('fixture_write_failed');}
      async role(captured: any, role: any) {writes.push({kind: 'role', captured, role});}
      async limits(captured: any, limits: any) {writes.push({kind: 'limits', captured, limits});}
      async bulk(rows: any, action: any, access: any, operationId: any) {writes.push({kind: 'bulk', rows, action, access, operationId});}
      dispose() {}
    }},
    '../../../../../../packages/client-core/src/direct-account': {PeopleInvitationsClient: class {
      async create(input: any) {writes.push({kind: 'invite-create', input}); return {...input, id: 'invitation-fixture', revision: 1, state: 'pending'};}
      async edit(id: string, input: any) {writes.push({kind: 'invite-edit', id, input}); return {...input, id, revision: input.expectedRevision + 1, state: 'pending'};}
      async link(captured: any) {writes.push({kind: 'invite-link', captured}); return {code: 'synthetic-fixture-code'};}
      async shortCode(captured: any, operationId: string) {writes.push({kind: 'invite-short-code', captured, operationId}); return {serverId: 'srv', expiresAt: '2099-01-01T00:00:00Z', shortCode: '7QK29MXPABCD1234'};}
    }, privatePersonNotes: (value: string) => value.trim() || null},
    '../../../../../../packages/client-core/src/server-administration': {
      parseAccessDevicePage: (raw: unknown) => raw,
      signOutMemberDevice: async (_: unknown, accountId: string, deviceId: string) => {writes.push({kind: 'signout-device', accountId, deviceId});},
      signOutMemberDevices: async (_: unknown, accountId: string) => {writes.push({kind: 'signout-all', accountId});},
      previewMemberDeletion: async () => preview,
      deleteMember: async (_: any, captured: any) => {writes.push({kind: 'delete', captured});},
    },
    '../lifetime': {NativeScreenLifetime: class {live = true; request = (call: any, signal: any) => call(signal); current = () => active && this.live; dispose = () => {this.live = false;};}, screenScopeEnded: () => new Error('ended')},
    './storage/space-source': {nativeStorageOwner: (candidate: any) => active && candidate.account.account.role === 'owner'},
  });
  const context = {authority: () => ({serverId: 'server', accountId: 'owner-fixture', profileId: 'primary'}), api: {baseUrl: 'http://selected-server.invalid:8096', request(path: string, method: string, body: unknown, signal: AbortSignal) {const next = deferred(); deviceReads.push({path, method, body, signal, ...next}); return next.promise;}}, account: {serverId: 'server', account: {id: 'owner-fixture', role: callerRole, primaryProfileId: 'primary', disabled: false}, capabilities: {managePeople: true}}};
  const source = new module.NativePeopleSource(context, () => 'operation-fixture', (error: unknown) => reported.push(error));
  const load = async () => {const pending = source.load(); reads.at(-1).resolve(page()); await pending;};
  return {source, reads, details, deviceReads, writes, reported, preview, load, retire: () => {active = false;}, failNotes: () => {failNotes = true;}};
}

test('People search retires the previous page and cursor before a late answer can publish', async () => {
  const f = await fixture(); const first = f.source.load('old-cursor');
  f.source.select('people', {...f.source.getSnapshot().query, search: 'new query'});
  assert.equal(f.reads[0].signal.aborted, true);
  assert.equal(f.reads[1].cursor, '');
  const newer = page([{...row, username: 'new result'}]); f.reads[1].resolve(newer); await tick();
  f.reads[0].resolve(page()); await first;
  assert.equal(f.source.getSnapshot().people, newer);
  assert.equal(f.source.getSnapshot().query.search, 'new query');
  f.source.dispose();
});

test('refresh retires pending person details and a stale finally cannot clear the next opening', async () => {
  const f = await fixture(true); await f.load();
  const first = f.source.open(row);
  assert.equal(f.source.getSnapshot().opening, true);
  const refresh = f.source.load();
  assert.equal(f.details[0].signal.aborted, true);
  assert.equal(f.source.getSnapshot().opening, false);
  assert.equal(f.source.getSnapshot().selected, undefined, 'the unfinished sheet closes rather than becoming empty');
  const refreshedRow = {...row, revision: 5};
  f.reads.at(-1).resolve(page([refreshedRow])); await refresh;
  const next = f.source.open(refreshedRow);
  f.details[0].resolve(person); await first;
  assert.equal(f.source.getSnapshot().opening, true);
  assert.equal(f.source.getSnapshot().selected, refreshedRow);
  assert.equal(f.source.getSnapshot().person, undefined);
  const freshPerson = {...person, revision: 5};
  f.details[1].resolve(freshPerson); await next;
  assert.equal(f.source.getSnapshot().opening, false);
  assert.equal(f.source.getSnapshot().person, freshPerson);
  f.source.dispose();
});

test('People mutations require the current selected person and preserve the authoritative revisions', async () => {
  const f = await fixture(); await f.load(); await f.source.open(row);
  assert.equal(await f.source.enabled({...person}, false), false, 'an equal-looking object is not the selected revision');
  assert.equal(f.writes.length, 0);
  await f.source.enabled(person, false);
  assert.equal(f.writes.length, 1);
  assert.equal(f.writes[0].rows[0].revision, person.revision);
  assert.equal(f.writes[0].rows[0].limitsRevision, person.limitsRevision);
  assert.equal(f.writes[0].action, 'disable');
  assert.equal(f.writes[0].operationId, 'operation-fixture');
  f.source.dispose(); await f.source.enabled(person, true); assert.equal(f.writes.length, 1);
});

test('deleting a person needs the exact fresh preview and never reuses it', async () => {
  const f = await fixture(); await f.load(); await f.source.open(row);
  const preview = await f.source.previewDeletion(person);
  assert.equal(preview, f.preview);
  assert.equal(await f.source.remove(person, {...preview}), false);
  assert.equal(await f.source.remove(person, preview), true);
  assert.equal(f.writes[0].captured, preview);
  assert.equal(await f.source.remove(person, preview), false);
  assert.equal(f.writes.length, 1); f.source.dispose();
});

test('a failed People mutation remains visible, is not retried, and retired handlers cannot write', async () => {
  const f = await fixture(); await f.load(); await f.source.open(row); f.failNotes();
  assert.equal(await f.source.notes(person, ' private note '), false);
  assert.equal(f.writes.length, 1); assert.equal(f.writes[0].notes, 'private note');
  assert.equal(f.source.getSnapshot().error.message, 'fixture_write_failed');
  assert.equal(f.reads.length, 1, 'failure cannot be erased by an automatic refresh');
  f.retire(); await f.source.notes(person, 'later'); assert.equal(f.writes.length, 1); f.source.dispose();
});

const device = {id: 'device-fixture', accountId: row.accountId, name: 'Tablet'};
test('Devices reads only the captured person, pages explicitly, and signs out exact devices or all', async () => {
  const f = await fixture(); await f.load(); await f.source.open(row);
  const first = f.source.loadDevices(person);
  const url = new URL(f.deviceReads[0].path, 'http://fixture.invalid');
  assert.equal(url.pathname, '/v1/admin/access/devices'); assert.equal(url.searchParams.get('account'), person.accountId); assert.equal(url.searchParams.get('limit'), '40');
  f.deviceReads[0].resolve({items: [device], nextCursor: 'next'}); await first;
  assert.equal(f.deviceReads.length, 1, 'no eager paging');
  const more = f.source.loadDevices(person, true); assert.equal(new URL(f.deviceReads[1].path, 'http://fixture.invalid').searchParams.get('cursor'), 'next');
  f.deviceReads[1].resolve({items: [{...device, id: 'second'}], nextCursor: ''}); await more;
  assert.equal(f.source.getSnapshot().devices.items.length, 2);
  assert.equal(await f.source.signOutDevices(person, {...device}), false, 'must use the captured published row');
  assert.equal(await f.source.signOutDevices(person, device), true);
  assert.deepEqual(f.writes, [{kind: 'signout-device', accountId: person.accountId, deviceId: device.id}]);
  f.deviceReads.at(-1).resolve({items: [device], nextCursor: ''}); await tick();
  assert.equal(await f.source.signOutDevices(person), true);
  assert.deepEqual(f.writes.at(-1), {kind: 'signout-all', accountId: person.accountId});
  f.source.dispose();
});

test('Devices rejects another account, cursor loops and replies from closed detail openings', async () => {
  const f = await fixture(); await f.load(); await f.source.open(row);
  let pending = f.source.loadDevices(person); f.deviceReads.at(-1).resolve({items: [{...device, accountId: 'another'}], nextCursor: ''}); await pending;
  assert.equal(f.source.getSnapshot().devices, undefined); assert.equal(f.source.getSnapshot().devicesError.message, 'native_people_device_scope_mismatch');
  pending = f.source.loadDevices(person); f.deviceReads.at(-1).resolve({items: [device], nextCursor: 'loop'}); await pending;
  pending = f.source.loadDevices(person, true); f.deviceReads.at(-1).resolve({items: [], nextCursor: 'loop'}); await pending;
  assert.equal(f.source.getSnapshot().devicesError.message, 'native_people_device_cursor_loop');
  pending = f.source.loadDevices(person); f.source.closePerson(); assert.equal(f.deviceReads.at(-1).signal.aborted, true);
  f.deviceReads.at(-1).resolve({items: [device], nextCursor: ''}); await pending;
  assert.equal(f.source.getSnapshot().devices, undefined); assert.equal(await f.source.signOutDevices(person, device), false); assert.equal(f.writes.length, 0);
  f.source.dispose();
});

test('Library access uses both selected revisions and preserves unlisted library IDs', async () => {
  const f = await fixture(); await f.load(); await f.source.open(row);
  await f.source.access(person, ['existing-not-listed', 'library-fixture']);
  assert.equal(f.writes[0].kind, 'bulk'); assert.equal(f.writes[0].action, 'access');
  assert.deepEqual(f.writes[0].access, {allowedLibraries: ['existing-not-listed', 'library-fixture']});
  assert.equal(f.writes[0].rows[0].revision, person.revision); assert.equal(f.writes[0].rows[0].limitsRevision, person.limitsRevision);
  f.source.dispose();
});

test('Invitation creation, edit and share require the current opening and use the selected server', async () => {
  const f = await fixture(); await f.load(); f.source.beginInvitation(); const opening = f.source.getSnapshot().invitationOpening;
  const draft = {notes: 'Private', role: 'member', allowedLibraries: ['library-fixture'], days: 3};
  assert.equal(await f.source.saveInvitation({...opening}, draft), false);
  assert.equal(await f.source.saveInvitation(opening, draft), true);
  assert.deepEqual(f.writes[0].input, {notes: 'Private', role: 'member', allowedLibraries: ['library-fixture'], expiresInHours: 72, operationId: 'operation-fixture'});
  const created = f.source.getSnapshot().invitationOpening; assert.notEqual(created, opening);
  f.reads.at(-1).resolve(page()); await tick();
  assert.equal(await f.source.invitationShareLink(opening), undefined);
  assert.equal(await f.source.invitationShareLink(created), 'http://selected-server.invalid:8096/join#code=synthetic-fixture-code');
  assert.equal(await f.source.saveInvitation(created, {...draft, role: 'admin'}), true);
  assert.equal(f.writes.at(-1).kind, 'invite-edit'); assert.equal(f.writes.at(-1).input.expectedRevision, 1);
  f.source.closeInvitation(); assert.equal(await f.source.invitationShareLink(created), undefined);
  f.source.dispose();
});

test('A new invitation opens its ready card; a waiting one opens it to share, and its short code needs that exact card', async () => {
  const f = await fixture(); await f.load(); f.source.beginInvitation(); const opening = f.source.getSnapshot().invitationOpening;
  assert.equal(await f.source.saveInvitation(opening, {notes: 'Private', role: 'member', allowedLibraries: [], days: 7}), true);
  const ready = f.source.getSnapshot().invitationOpening;
  assert.equal(ready.ready, 'created'); assert.equal(ready.invitation.id, 'invitation-fixture');
  f.reads.at(-1).resolve(page()); await tick();
  const signal = new AbortController().signal;
  const issued = await f.source.invitationShortCode(ready, 'code-operation', signal);
  assert.equal(issued.shortCode, '7QK29MXPABCD1234');
  assert.deepEqual(f.writes.filter((write: any) => write.kind === 'invite-short-code').map((write: any) => write.operationId), ['code-operation']);
  // A stale or plain opening never issues a code.
  await assert.rejects(f.source.invitationShortCode({...ready}, 'stale', signal));
  // Sharing needs the current opening; it opens the same card without issuing a code.
  f.source.shareInvitation({...ready});
  assert.equal(f.source.getSnapshot().invitationOpening, ready);
  f.source.shareInvitation(ready);
  const shared = f.source.getSnapshot().invitationOpening;
  assert.equal(shared.ready, 'shared'); assert.equal(shared.invitation, ready.invitation);
  assert.equal(f.writes.filter((write: any) => write.kind === 'invite-short-code').length, 1);
  await assert.rejects(f.source.invitationShortCode(ready, 'replaced-card', signal));
  // Closed, nothing issues.
  f.source.closeInvitation();
  await assert.rejects(f.source.invitationShortCode(shared, 'closed', signal));
  f.source.dispose();
});


test('administrator People lifetime permits member profile navigation but keeps existing owner commands closed', async () => {
  const f = await fixture(false, 'admin');
  assert.equal(f.source.current(), true); assert.equal(f.source.ownerControls(), false);
  await f.load(); await f.source.open(row); assert.equal(f.source.getSnapshot().person, person);
  assert.equal(await f.source.enabled(person, false), false); f.source.beginInvitation();
  assert.equal(f.source.getSnapshot().invitationOpening, undefined); assert.equal(f.writes.length, 0);
  f.retire(); assert.equal(f.source.current(), false); f.source.dispose();
});
