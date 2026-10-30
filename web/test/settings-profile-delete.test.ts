import test from 'node:test';
import assert from 'node:assert/strict';
import {ProfileSettingsSource} from '../src/screens/settings/account-profile/source.ts';

const lifetime = '0123456789abcdef0123456789abcdef';
const otherLifetime = 'fedcba9876543210fedcba9876543210';
const retirement = (state: string, lifetimeValue = lifetime) => ({profileId: 'child', lifetime: lifetimeValue, state});
const preview = {profileId: 'child', name: 'Child', revision: 7, primary: false, lifetime, playlists: 2};

function fixture(answers: Array<unknown | Error | (() => Promise<unknown>)>) {
  let current = true, statuses: string[] = [];
  const calls: {path: string; method?: string; body?: unknown}[] = [];
  const document = () => ({
    authority: 'local', serverId: 'server', canManage: true,
    capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
    account: {id: 'account', username: 'actual', primaryProfileId: 'main', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
    profiles: statuses.includes('removed')
      ? [{id: 'main', name: 'Actual', primary: true, position: 0, art: 'blue', revision: 2, pinRevision: 1, pinRequired: false}]
      : [
        {id: 'main', name: 'Actual', primary: true, position: 0, art: 'blue', revision: 2, pinRevision: 1, pinRequired: false},
        {id: 'child', name: 'Child', primary: false, position: 1, art: 'mint', revision: 7, pinRevision: 1, pinRequired: true},
      ],
  });
  const source = new ProfileSettingsSource({request: async <T,>(path: string, method?: string, body?: unknown) => {
    calls.push({path, method, body});
    if (path === '/v1/direct') return structuredClone(document()) as T;
    const next = answers.shift();
    if (next === undefined) throw new Error('unexpected request ' + path);
    if (next instanceof Error) throw next;
    return (typeof next === 'function' ? await next() : structuredClone(next)) as T;
  }}, {serverId: 'server', accountId: 'account', profileId: 'main'}, () => current);
  return {source, calls, retire: () => {current = false;}, seen: () => statuses,
    watch: () => source.deleteProfile(preview, value => {statuses.push(value.state);})};
}

const deletionPath = '/v1/direct/profiles/child';
const statusPath = '/v1/direct/profiles/child/retirement?lifetime=' + lifetime;

test('deleting waits for the server purge, then publishes the list without the profile', async () => {
  const f = fixture([retirement('removing'), retirement('removing'), retirement('removed')]);
  await f.source.load();
  await f.watch();
  assert.deepEqual(f.seen(), ['removing', 'removing', 'removed']);
  assert.deepEqual(f.calls.map(call => [call.path, call.method]), [
    ['/v1/direct', 'GET'], [deletionPath, 'DELETE'], [statusPath, 'GET'], [statusPath, 'GET'], ['/v1/direct', 'GET'],
  ]);
  assert.deepEqual(f.calls[1]!.body, {expectedRevision: 7, lifetime, deleteSavedResources: true});
  assert.deepEqual(f.source.getSnapshot().document?.profiles.map(profile => profile.id), ['main']);
  assert.equal(f.source.getSnapshot().busy, false);
  f.source.dispose();
});

test('a blocked retirement is a failure the screen handles, never a removed profile', async () => {
  const f = fixture([retirement('blocked')]);
  await f.source.load();
  await assert.rejects(f.watch(), {code: 'retirement_blocked'});
  assert.deepEqual(f.seen(), ['blocked']);
  assert.ok(f.source.getSnapshot().error, 'the existing failure path owns the retry');
  assert.equal(f.source.getSnapshot().document, undefined);
  f.source.dispose();
});

test('another lifetime or a lost answer never becomes removed', async () => {
  for (const answer of [retirement('removed', otherLifetime), Object.assign(new Error('gone'), {status: 404})]) {
    const f = fixture([retirement('removing'), answer]);
    await f.source.load();
    await assert.rejects(f.watch());
    assert.deepEqual(f.seen(), ['removing']);
    assert.equal(f.source.getSnapshot().document, undefined);
    f.source.dispose();
  }
});

test('a retired viewer stops the waiting dialog and publishes nothing late', async () => {
  let release!: () => void;
  const held = new Promise<void>(resolve => {release = resolve;});
  const f = fixture([retirement('removing'), () => held.then(() => retirement('removed'))]);
  await f.source.load();
  const task = f.watch();
  await new Promise(resolve => setTimeout(resolve, 5));
  f.retire();
  f.source.dispose();
  release();
  await assert.rejects(task, {name: 'AbortError'});
  assert.deepEqual(f.seen(), ['removing']);
  assert.equal(f.source.getSnapshot().document, undefined);
  f.source.dispose();
});

test('a lost delete response is replayed with the same immutable body and lifetime', async () => {
  const f = fixture([Object.assign(new Error('offline'), {name: 'TypeError'}), retirement('removed')]);
  await f.source.load();
  await assert.rejects(f.watch());
  assert.deepEqual(f.seen(), [], 'a delete that never answered reports no state');
  await f.source.load();
  await f.watch();
  const deletes = f.calls.filter(call => call.method === 'DELETE');
  assert.equal(deletes.length, 2, 'the retry is a real second attempt, not a read');
  assert.deepEqual(deletes[0]!.body, deletes[1]!.body);
  assert.deepEqual(deletes[1]!.body, {expectedRevision: 7, lifetime, deleteSavedResources: true});
  assert.deepEqual(f.seen(), ['removed']);
  assert.deepEqual(f.source.getSnapshot().document?.profiles.map(profile => profile.id), ['main']);
  f.source.dispose();
});
