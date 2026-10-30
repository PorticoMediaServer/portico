import test from 'node:test';
import assert from 'node:assert/strict';
import type {DirectSnapshot} from '@core/profile-management.ts';
import {ProfileSettingsSource} from '../src/screens/settings/account-profile/source.ts';
import {changeProfileSetting, profileSettingState} from '../src/screens/settings/account-profile/rows.ts';

function fixture(observer?: (snapshot: DirectSnapshot) => void) {
  let current = true, hold: Promise<void> | undefined, release!: () => void, fail = false;
  const document = {
    authority: 'local', serverId: 'server', canManage: true,
    capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
    account: {id: 'account', username: 'actual', primaryProfileId: 'main', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [
      {id: 'main', name: 'Actual', primary: true, position: 0, art: 'blue', revision: 2, pinRevision: 1, pinRequired: false},
      {id: 'child', name: 'Child', primary: false, position: 1, art: 'mint', revision: 7, pinRevision: 1, pinRequired: true},
    ],
  };
  const calls: {path: string; method?: string; body?: unknown; signal?: AbortSignal}[] = [];
  const source = new ProfileSettingsSource({request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    calls.push({path, method, body, signal});await hold;
    if (fail) throw new Error('offline');
    return structuredClone(document) as T;
  }}, {serverId: 'server', accountId: 'account', profileId: 'main'}, () => current, undefined, observer);
  return {source, calls, retire: () => {current = false;}, hold: () => {hold = new Promise(resolve => {release = resolve;});}, release: () => {hold = undefined;release();},
    fail: () => {fail = true;}, change: (fn: (value: typeof document) => void) => fn(document)};
}

test('profile source reads checked account data once and edits selected revision with readback', async () => {
  const f = fixture();await f.source.load();await f.source.load();
  assert.equal(f.calls.length, 1);assert.equal(f.source.selected()?.name, 'Actual');
  f.source.select('child');await f.source.rename('New name');
  assert.equal(f.calls[1].path, '/v1/direct/profiles/child');
  assert.deepEqual(f.calls[1].body, {name: 'New name', expectedRevision: 7});
  assert.equal(f.calls[2].path, '/v1/direct');
  assert.equal(f.source.getSnapshot().busy, false);f.source.dispose();
});
test('order writes the entire actual revision-bound list', async () => {
  const f = fixture();await f.source.load();await f.source.move('child', -1);
  assert.deepEqual(f.calls[1].body, {profiles: [{id: 'child', revision: 7}, {id: 'main', revision: 2}]});f.source.dispose();
});
test('a profile without current management cannot select another profile or write', async () => {
  const f = fixture();f.change(d => {d.canManage = false;Object.keys(d.capabilities).forEach(key => {d.capabilities[key as keyof typeof d.capabilities] = false;});});
  await f.source.load();f.source.select('child');assert.equal(f.source.selected()?.id, 'main');
  await assert.rejects(f.source.rename('Denied'), {code: 'forbidden'});assert.equal(f.calls.length, 1);f.source.dispose();
});
test('wrong account or server, missing viewer and disabled account expose no profile document', async () => {
  type Document = Parameters<ReturnType<typeof fixture>['change']>[0] extends (value: infer T) => void ? T : never;
  for (const corrupt of [
    (d: Document) => {d.account.id = 'other';}, (d: Document) => {d.serverId = 'other';},
    (d: Document) => {d.profiles[0].id = 'other';d.account.primaryProfileId = 'other';}, (d: Document) => {d.account.disabled = true;},
  ]) {
    const f = fixture();f.change(corrupt);await f.source.load();assert.equal(f.source.getSnapshot().document, undefined);assert.ok(f.source.getSnapshot().error);f.source.dispose();
  }
});
test('late reads after viewer retirement never publish private facts', async () => {
  const f = fixture();f.hold();const task = f.source.load();f.retire();f.release();await task;
  assert.equal(f.source.getSnapshot().document, undefined);f.source.dispose();
});
test('a second mutation and selection cannot race a pending edit', async () => {
  const f = fixture();await f.source.load();f.hold();const task = f.source.rename('First');
  f.source.select('child');assert.equal(f.source.selected()?.id, 'main');
  await assert.rejects(f.source.color('mint'), {code: 'conflict'});f.release();await task;f.source.dispose();
});
test('failed mutation removes stale revisions until a fresh read', async () => {
  const f = fixture();await f.source.load();f.fail();await assert.rejects(f.source.rename('Change'));
  assert.equal(f.source.getSnapshot().document, undefined);await assert.rejects(f.source.rename('Retry'), {code: 'forbidden'});assert.equal(f.calls.length, 2);f.source.dispose();
});
test('retirement during busy notification prevents the command and dispose aborts reads', async () => {
  const f = fixture();await f.source.load();f.source.subscribe(() => {if (f.source.getSnapshot().busy) f.retire();});
  await f.source.rename('Late');assert.equal(f.calls.length, 1);f.source.dispose();assert.equal(f.calls[0].signal?.aborted, true);
});
test('row projection uses the selected real profile and account, never spec examples', async () => {
  const f = fixture();assert.deepEqual(profileSettingState(f.source, 'profile.name'), {present: false});
  await f.source.load();f.source.select('child');
  assert.equal(profileSettingState(f.source, 'profile.name')?.value, 'Child');
  assert.equal(profileSettingState(f.source, 'profile.color')?.value, 'mint');
  assert.equal(profileSettingState(f.source, 'profile.username')?.displayValue, 'actual');
  assert.deepEqual(profileSettingState(f.source, 'profile.picture'), {present: true, disabled: false});
  assert.equal(changeProfileSetting(f.source, 'profile.color', 'invented'), undefined);
  assert.equal(f.calls.length, 1);f.retire();assert.deepEqual(profileSettingState(f.source, 'profile.username'), {present: false});f.source.dispose();
});

test('quiet profile refresh bypasses cached load and preserves selected target',async()=>{
 const f=fixture();await f.source.load();f.source.select('child');const before=f.source.getSnapshot();f.change(d=>{d.profiles[1].name='Renamed';d.profiles[1].revision=8;});assert.deepEqual(await f.source.refresh(),{kind:'adopted'});assert.equal(f.source.getSnapshot().selectedId,'child');assert.equal(f.source.selected()?.name,'Renamed');assert.equal(f.source.getSnapshot().busy,before.busy);assert.equal(f.source.getSnapshot().error,before.error);assert.equal(f.calls.length,2);f.source.dispose();
});

test('selection, buffered input admission, write admission and disposal refuse late quiet profile adoption',async()=>{
 for(const action of ['selection','input','write','dispose'] as const){const f=fixture();await f.source.load();f.hold();const pending=f.source.refresh();if(action==='selection')f.source.select('child');if(action==='input')f.source.setQuietInputActive(true);if(action==='dispose')f.source.dispose();let write:Promise<void>|undefined;if(action==='write')write=f.source.rename('Mine');const before=f.source.getSnapshot();f.release();assert.deepEqual(await pending,{kind:'deferred'});if(action!=='write')assert.equal(f.source.getSnapshot(),before);await write;f.source.dispose();}
});

test('unreadable quiet profile keeps confirmed document and returns the original failure',async()=>{
 const f=fixture();await f.source.load();const before=f.source.getSnapshot();f.fail();const result=await f.source.refresh();assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal((result.error as Error).message,'offline');assert.equal(f.source.getSnapshot(),before);f.source.dispose();
});


test('strict direct observations synchronously retire the original shell before Profile publication, even during input settlement',async()=>{
 for(const loss of ['missing','disabled','management'] as const){let f!:ReturnType<typeof fixture>,observations=0;const publications:unknown[]=[];
  f=fixture(snapshot=>{observations++;assert.equal(f.source.getSnapshot().document,undefined);assert.equal(snapshot.account.disabled,loss==='disabled');f.retire();});
  if(loss==='missing')f.change(d=>{d.profiles.splice(0,1);d.account.primaryProfileId='child';d.profiles[0].primary=true;d.profiles[0].position=0;});
  if(loss==='disabled')f.change(d=>{d.account.disabled=true;});
  if(loss==='management')f.change(d=>{d.canManage=false;for(const key of Object.keys(d.capabilities))d.capabilities[key as keyof typeof d.capabilities]=false;});
  f.hold();const pending=f.source.load();f.source.setQuietInputActive(true);f.source.subscribe(()=>{publications.push(f.source.getSnapshot().document);});f.release();await pending;
  assert.equal(observations,1);assert.deepEqual(publications,[]);assert.equal(f.source.getSnapshot().document,undefined);f.source.dispose();
 }
});

test('quiet direct authority observation retires synchronously before adopting an ineligible document',async()=>{
 let f!:ReturnType<typeof fixture>,retire=false,observations=0;f=fixture(snapshot=>{observations++;if(retire){assert.equal(snapshot.account.disabled,true);assert.equal(f.source.getSnapshot().document?.account.disabled,false);f.retire();}});
 await f.source.load();const before=f.source.getSnapshot();let publications=0;f.source.subscribe(()=>publications++);f.change(d=>{d.account.disabled=true;});retire=true;
 assert.deepEqual(await f.source.refresh(),{kind:'deferred'});assert.equal(observations,2);assert.equal(publications,0);assert.equal(before.document?.account.disabled,false);assert.equal(f.source.getSnapshot().document,undefined);f.source.dispose();
});

test('cancelled, superseded, disposed and malformed Profile reads cannot feed direct authority observation',async()=>{
 for(const action of ['input','selection','newer','dispose','malformed'] as const){let observations=0;const f=fixture(()=>{observations++;});await f.source.load();assert.equal(observations,1);f.hold();const pending=f.source.refresh();
  if(action==='input')f.source.setQuietInputActive(true);if(action==='selection')f.source.select('child');if(action==='dispose')f.source.dispose();
  let newer:Promise<unknown>|undefined;if(action==='newer')newer=f.source.refresh();if(action==='malformed')f.change(d=>{d.serverId='foreign';});
  f.release();await pending;await newer;assert.equal(observations,action==='newer'?2:1);f.source.dispose();
 }
});
