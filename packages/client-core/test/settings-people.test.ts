import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSettingsPeoplePage, SettingsPeopleClient, type SettingsPersonRow} from '../src/settings-people.ts';
import {parseMemberLimits} from '../src/server-administration.ts';

const person = (index: number): SettingsPersonRow => ({accountId: `person-${index}`, username: `person${index}`, email: `person${index}@example.test`, role: 'member', revision: index + 1, limitsRevision: index + 3,
  disabled: false, status: 'active', hasMoveCode:false,allowedLibraries: [], libraryNames: ['Actual library'], maxStreams: 2, maxStreamsMode:'limit',serverMaxStreams:null,effectiveMaxStreams:2,profileCount: 1, profileNames: [`Profile ${index}`], liveState: 'idle'});
const page = () => ({items: Array.from({length: 40}, (_, index) => person(index)), totalCount: 137, matchedCount: 120, nextCursor: 'opaque-cursor', limit: 40});
const query = {search: 'person 2', role: 'member', status: 'month', sort: 'active', direction: 'desc'} as const;

test('the People boundary keeps all accounts and rejects unreadable permissions, revisions and duplicates', () => {
  assert.equal(parseSettingsPeoplePage(page()).items.length, 40);
  for (const patch of [{role: 'superowner'}, {limitsRevision: 0}, {profileCount: 9}, {email: 'bad\u0000email'}, {status: 'approved'}]) {
    const raw = page(); Object.assign(raw.items[20], patch); assert.throws(() => parseSettingsPeoplePage(raw));
  }
  const duplicate = page(); duplicate.items[1] = duplicate.items[0]; assert.throws(() => parseSettingsPeoplePage(duplicate));
  assert.throws(() => parseSettingsPeoplePage({...page(), matchedCount: 138}));
});
test('search, filters and sorting are sent to the producer, with a bounded page and opaque cursor', async () => {
  const paths: string[] = [];
  const client = new SettingsPeopleClient({request: async path => {paths.push(path); return page() as any;}}, () => true);
  await client.read(query, 'cursor/+'); client.dispose();
  const url = new URL(paths[0], 'https://server.test');
  assert.equal(url.pathname, '/v1/admin/access/members');
  for (const [key, value] of Object.entries({q: 'person 2', role: 'member', status: 'month', sort: 'active', direction: 'desc', limit: '40', cursor: 'cursor/+'})) assert.equal(url.searchParams.get(key), value);
});
test('bulk access sends one transaction with the table revisions and checks the exact target set', async () => {
  const requests: {path: string; method?: string; body?: any}[] = [], people = Array.from({length: 120}, (_, index) => person(index));
  const client = new SettingsPeopleClient({request: async (path, method, body) => {requests.push({path, method, body}); return {action: 'access', accountIds: people.map(row => row.accountId)} as any;}}, () => true);
  assert.equal((await client.bulk(people, 'access', {maxStreams: 2}, 'op-actual')).length, 120);
  assert.equal(requests.length, 1); assert.equal(requests[0].method, 'POST'); assert.equal(requests[0].path, '/v1/admin/access/members/bulk');
  assert.deepEqual(requests[0].body.targets[77], {accountId: people[77].accountId, expectedRevision: people[77].revision, expectedLimitsRevision: people[77].limitsRevision});
  client.dispose();
  const wrong = new SettingsPeopleClient({request: async () => ({action: 'access', accountIds: ['unselected-person']}) as any}, () => true);
  await assert.rejects(wrong.bulk([person(0)], 'access', {maxStreams: 2})); wrong.dispose();
});
test('authority handover rejects late private data and disposal aborts pending requests', async () => {
  let current = true, answer!: (value: unknown) => void, signal: AbortSignal | undefined;
  const client = new SettingsPeopleClient({request: (_path, _method, _body, nextSignal) => {signal = nextSignal; return new Promise(resolve => {answer = resolve;}) as any;}}, () => current);
  const pending = client.read(query); current = false; answer(page()); await assert.rejects(pending, /settings_scope_closed/);
  current = true; const second = client.read(query); client.dispose(); assert.equal(signal!.aborted, true); answer(page()); await assert.rejects(second);
});
test('the account permission boundary preserves false token and away permissions and refuses invented defaults', () => {
  const limits = {accessTokens: false, maxProfiles: 2, allowRemote: false, conversion: 'none', maxStreams: 1, maxStreamsMode:'limit', remoteBitrateKbps: 0, allowUnrated: true};
  assert.equal(parseMemberLimits(limits).accessTokens, false); assert.equal(parseMemberLimits(limits).allowRemote, false);
  for (const key of ['accessTokens', 'maxProfiles', 'allowRemote', 'conversion','maxStreams','maxStreamsMode']) {const raw = {...limits} as Record<string, unknown>; delete raw[key]; assert.throws(() => parseMemberLimits(raw));}
});

test('quiet refresh is a strict parsed first page of the current query and keeps original failures',async()=>{
  const paths:string[]=[],error=new Error('original read failure');let mode='good',signal:AbortSignal|undefined;
  const client=new SettingsPeopleClient({request:async(path,_method,_body,s)=>{paths.push(path);signal=s;if(mode==='fail')throw error;return (mode==='bad'?{...page(),limit:0}:page()) as any;}},()=>true);
  const abort=new AbortController();const fresh=await client.refresh(query,abort.signal);
  assert.equal(fresh.nextCursor,'opaque-cursor');assert.equal(Object.isFrozen(fresh),true);
  const url=new URL(paths[0],'https://server.test');assert.equal(url.searchParams.has('cursor'),false);
  for(const [key,value] of Object.entries({q:'person 2',role:'member',status:'month',sort:'active',direction:'desc',limit:'40'}))assert.equal(url.searchParams.get(key),value);
  assert(signal);mode='bad';await assert.rejects(client.refresh(query));mode='fail';await assert.rejects(client.refresh(query),e=>e===error);client.dispose();
});
