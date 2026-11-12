import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsPeopleClient} from '../src/settings-people.ts';
import {parseMemberLimits} from '../src/server-administration.ts';

type Call = {path: string; method?: string; body?: any};
const member = (id: string, extra: Record<string, unknown> = {}) => ({id, username: id, role: 'member', disabled: false, createdAt: '2026-11-01T00:00:00Z', allLibraries: true, libraries: [], twoFactorEnabled: false, ...extra});
const members = [member('owner', {role: 'owner'}), member('amy', {email: 'amy@example.test', allLibraries: false, libraries: ['lib-films']}), member('ben', {disabled: true}), member('cam', {role: 'admin'})];
const query = {search: '', role: 'all', status: 'all', sort: 'person', direction: 'asc'} as const;
/** A rebuilt server: members, the owner's stream limits (or a refusal for an administrator), and what is playing. */
function server(options: {limits?: unknown; limitsStatus?: number} = {}) {
  const calls: Call[] = [];
  let limits: any = options.limits ?? {perPerson: 2, people: {amy: 4, cam: 0}, total: 10};
  const api = {request: async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    if (path === '/v1/admin/members') return {items: members} as T;
    if (path.startsWith('/v1/admin/members/')) return (method === 'DELETE' ? undefined : {...members.find(m => m.id === decodeURIComponent(path.split('/')[4]))!, ...(body as object)}) as T;
    if (path === '/v1/admin/playback/stream-limits') {
      if (options.limitsStatus) throw Object.assign(new Error('refused'), {status: options.limitsStatus});
      if (method === 'PUT') limits = body;
      return limits as T;
    }
    if (path === '/v1/admin/sessions') return {items: [{username: 'amy', kind: 'audio', state: 'playing'}, {username: 'cam', kind: 'vod', state: 'paused'}]} as T;
    throw Object.assign(new Error('not found'), {status: 404});
  }};
  return {api, calls, limits: () => limits};
}

test('the table reads every account once and builds its rows from the members, the stream limits and what is playing', async () => {
  const s = server(), client = new SettingsPeopleClient(s.api, () => true, () => [{id: 'lib-films', name: 'Films'}]);
  const page = await client.read(query);
  assert.deepEqual(page.items.map(row => row.accountId), ['amy', 'ben', 'cam', 'owner']);
  assert.equal(page.nextCursor, ''); assert.equal(page.totalCount, 4); assert.equal(page.matchedCount, 4);
  const amy = page.items[0], cam = page.items[2], ben = page.items[1];
  assert.deepEqual([amy.libraryNames, amy.maxStreamsMode, amy.effectiveMaxStreams, amy.liveState, amy.email], [['Films'], 'limit', 4, 'listening', 'amy@example.test']);
  assert.deepEqual([cam.maxStreamsMode, cam.effectiveMaxStreams, cam.liveState], ['unlimited', null, 'watching']);
  assert.deepEqual([ben.status, ben.maxStreamsMode, ben.effectiveMaxStreams, ben.libraryNames], ['off', 'inherit', 2, []]);
  assert.equal(new Set(s.calls.map(call => call.path)).size, s.calls.length, 'one read of each');
  client.dispose();
});

test('search, role, status and sorting happen in the app', async () => {
  const client = new SettingsPeopleClient(server().api, () => true);
  const ids = async (q: Partial<typeof query> & Record<string, string>) => (await client.read({...query, ...q} as any)).items.map(row => row.accountId);
  assert.deepEqual(await ids({search: 'AMY@'}), ['amy']);
  assert.deepEqual(await ids({role: 'admin'}), ['cam']);
  assert.deepEqual(await ids({status: 'off'}), ['ben']);
  assert.deepEqual(await ids({status: 'now'}), ['amy', 'cam']);
  assert.deepEqual(await ids({sort: 'role'}), ['owner', 'cam', 'amy', 'ben']);
  assert.deepEqual(await ids({sort: 'active', direction: 'desc'}), ['amy', 'cam', 'ben', 'owner']);
  const page = await client.read({...query, status: 'off'});
  assert.equal(page.matchedCount, 1); assert.equal(page.totalCount, 4);
  assert.equal((await client.read(query, 'more')).items.length, 0, 'there is never a second page');
  client.dispose();
});

test('an administrator, who may not read the stream limits, gets the table with the stream cells unknown and stays signed in', async () => {
  const s = server({limitsStatus: 403}), client = new SettingsPeopleClient(s.api, () => true);
  const page = await client.read(query);
  assert.equal(page.items.every(row => !row.streamsKnown && row.effectiveMaxStreams === null), true);
  assert.equal((await client.read(query)).items.length, 4, 'the client was not closed');
  client.dispose();
});

test('notes and role are one PATCH each, and the stream count is set in the owner limits without touching anything else in them', async () => {
  const s = server(), client = new SettingsPeopleClient(s.api, () => true);
  const amy = await client.person('amy');
  assert.equal(amy.limits.maxStreams, 4); assert.equal(amy.effectiveMaxStreams, 4);
  await client.notes(amy, 'Sister'); await client.role(amy, 'admin');
  assert.deepEqual(s.calls.filter(call => call.method === 'PATCH').map(call => call.body), [{notes: 'Sister'}, {role: 'admin'}]);
  const saved = await client.limits(amy, {...amy.limits, maxStreamsMode: 'inherit', maxStreams: 0});
  assert.deepEqual(s.limits(), {perPerson: 2, people: {cam: 0}, total: 10});
  assert.deepEqual([saved.limits.maxStreamsMode, saved.effectiveMaxStreams], ['inherit', 2]);
  const before = s.calls.length;
  await assert.rejects(client.limits(amy, {...amy.limits, allowRemote: false}), /not available/);
  assert.equal(s.calls.length, before, 'a limit the server does not keep is refused before anything is sent');
  await assert.rejects(client.role(amy, 'owner'));
  client.dispose();
});

test('bulk is one request per account, and bulk access refuses what the server does not keep before sending anything', async () => {
  const s = server(), client = new SettingsPeopleClient(s.api, () => true);
  const rows = (await client.read(query)).items.filter(row => row.role === 'member');
  s.calls.length = 0;
  assert.deepEqual(await client.bulk(rows, 'disable'), ['amy', 'ben']);
  assert.deepEqual(s.calls.map(call => [call.method, call.path, call.body]), [['PATCH', '/v1/admin/members/amy', {disabled: true}], ['PATCH', '/v1/admin/members/ben', {disabled: true}]]);
  s.calls.length = 0;
  await client.bulk(rows, 'access', {allowedLibraries: ['lib-films'], maxStreamsMode: 'limit', maxStreams: 1});
  assert.deepEqual(s.calls.filter(call => call.method === 'PATCH').map(call => call.body), [{allLibraries: false, libraries: ['lib-films']}, {allLibraries: false, libraries: ['lib-films']}]);
  assert.deepEqual(s.limits().people, {amy: 1, ben: 1, cam: 0});
  s.calls.length = 0;
  await assert.rejects(client.bulk(rows, 'access', {allowRemote: false}), /not available/);
  assert.equal(s.calls.length, 0);
  await client.bulk(rows.slice(0, 1), 'remove');
  assert.deepEqual(s.calls.map(call => [call.method, call.path]), [['DELETE', '/v1/admin/members/amy']]);
  client.dispose();
});

test('authority handover rejects late private data and disposal aborts pending requests', async () => {
  let current = true;
  const pending: {path: string; resolve: (value: unknown) => void; signal?: AbortSignal}[] = [];
  const client = new SettingsPeopleClient({request: (path, _method, _body, signal) => new Promise((resolve, reject) => {
    pending.push({path, resolve, signal}); signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
  }) as any}, () => current);
  const late = client.read(query); current = false;
  for (const request of pending.splice(0)) request.resolve(request.path === '/v1/admin/members' ? {items: [member('amy')]} : {});
  await assert.rejects(late, /settings_scope_closed/);
  current = true; const second = client.person('amy'); client.dispose();
  assert.equal(pending.every(request => request.signal?.aborted), true);
  await assert.rejects(second);
});

test('the account permission boundary preserves false token and away permissions and refuses invented defaults', () => {
  const limits = {accessTokens: false, maxProfiles: 2, allowRemote: false, conversion: 'none', maxStreams: 1, maxStreamsMode: 'limit', remoteBitrateKbps: 0, allowUnrated: true};
  assert.equal(parseMemberLimits(limits).accessTokens, false); assert.equal(parseMemberLimits(limits).allowRemote, false);
  for (const key of ['accessTokens', 'maxProfiles', 'allowRemote', 'conversion', 'maxStreams', 'maxStreamsMode']) {const raw = {...limits} as Record<string, unknown>; delete raw[key]; assert.throws(() => parseMemberLimits(raw));}
});

test('an account the server holds to limits shows them, and a save names only what changed', async () => {
  const read = {homeOnly: true, viewingHours: {timezone: 'America/Halifax', windows: [{days: [1, 2], startMinute: 960, endMinute: 1260}]}, maxRatingAge: 10, allowUnrated: false,
    blockedLabels: ['tag-horror'], remoteMaxBitrateKbps: 4000, maxProfiles: 3, allowWatchTogether: true, allowTokens: false};
  // The account's Downloads switch is on the member itself, not among its limits.
  const held = {...member('dee'), allowDownloads: false, limits: read};
  const calls: Call[] = [];
  const client = new SettingsPeopleClient({request: async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    if (path === '/v1/admin/members') return {items: [held, member('eli')]} as T;
    if (path === '/v1/admin/members/dee') return (method === 'PATCH' ? {...held, limits: {...read, ...(body as {limits?: object}).limits}} : held) as T;
    if (path === '/v1/admin/playback/stream-limits') return {} as T;
    return {items: []} as T;
  }}, () => true);
  const rows = (await client.read(query)).items;
  assert.deepEqual(rows.map(row => [row.accountId, row.accountLimits]), [['dee', true], ['eli', false]]);
  const dee = await client.person('dee');
  assert.deepEqual([dee.limits.allowRemote, dee.limits.maxContentRating, dee.limits.maximumAge, dee.limits.accessTokens, dee.limits.conversion, dee.limits.remoteBitrateKbps, dee.limits.maxProfiles],
    [false, 'PG', 10, false, 'server', 4000, 3]);
  assert.deepEqual(dee.limits.schedule, read.viewingHours); assert.deepEqual(dee.limits.tagPolicy.deniedLabels, ['tag-horror']);
  calls.length = 0;
  const saved = await client.limits(dee, {...dee.limits, allowRemote: true});
  assert.deepEqual(calls.map(call => [call.method, call.path]), [['PATCH', '/v1/admin/members/dee']], 'the stream limits are not touched');
  assert.deepEqual(calls[0].body, {limits: {homeOnly: false}}, 'the rating age 10 is not rewritten as PG\'s 8');
  assert.equal(saved.limits.allowRemote, true);
  calls.length = 0;
  await client.limits(dee, {...dee.limits, maxContentRating: 'PG-13', schedule: {timezone: 'America/Halifax', windows: []}});
  assert.deepEqual(calls[0].body, {limits: {maxRatingAge: 13, viewingHours: null}});
  calls.length = 0;
  await client.limits(dee, {...dee.limits, schedule: {timezone: '', windows: [{days: [6], startMinute: 0, endMinute: 600}]}});
  assert.equal(typeof calls[0].body.limits.viewingHours.timezone, 'string'); assert.ok(calls[0].body.limits.viewingHours.timezone, 'hours always carry a zone');
  calls.length = 0;
  // The server keeps no limit on converting: any change to it is refused, never sent.
  for (const conversion of ['audio', 'none'] as const) await assert.rejects(client.limits(dee, {...dee.limits, conversion}), /not available/);
  await assert.rejects(client.bulk(rows, 'access', {allowRemote: false}), /not available/, 'eli has no limits on the server');
  assert.equal(calls.length, 0);
  await client.bulk(rows.slice(0, 1), 'access', {allowRemote: true, maxContentRating: 'G', allowedLibraries: []});
  assert.deepEqual(calls.map(call => [call.method, call.path]), [['PATCH', '/v1/admin/members/dee']], 'one request, nothing read first');
  assert.deepEqual(calls[0].body, {allLibraries: true, libraries: [], limits: {homeOnly: false, maxRatingAge: 0}});
  client.dispose();
});
