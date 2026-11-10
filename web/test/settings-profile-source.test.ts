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
test('failed mutation removes stale revisions until a fresh read', async () => {
  const f = fixture();await f.source.load();f.fail();await assert.rejects(f.source.rename('Change'));
  assert.equal(f.source.getSnapshot().document, undefined);await assert.rejects(f.source.rename('Retry'), {code: 'forbidden'});assert.equal(f.calls.length, 2);f.source.dispose();
});

test('unreadable quiet profile keeps confirmed document and returns the original failure',async()=>{
 const f=fixture();await f.source.load();const before=f.source.getSnapshot();f.fail();const result=await f.source.refresh();assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal((result.error as Error).message,'offline');assert.equal(f.source.getSnapshot(),before);f.source.dispose();
});


// PR-1: a remembered main profile past its management hour is refused with profile_pin_manage before anything changes.
// The list stays and the page doesn't fail (no snapshot error); the shell's manage gate asks for the PIN.
