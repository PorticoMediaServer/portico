import test from 'node:test';
import assert from 'node:assert/strict';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

// E-mobile-01/25 (P2-74 r5): an owner account's non-primary profile is issued a "member" session by
// the server. The app compared that with the account's own role, read it as "the selection
// changed", and signed the brand-new session out. First run, pick, select 200, then Home, with no
// sign-out of the picked session.
test('first run: picking a non-primary profile of an owner account reaches Home and never signs the new session out', async () => {
  const guestViewer = (f: any) => ({...f.viewer, profileId: 'guest', role: 'member'});
  const deletes: string[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any, f: any) => {
    if (request.path === '/v1/system') return {status: 200, body: {id: f.pin.serverId, name: 'Synthetic living room', version: '1', move: null, signIn: 'local',
      setupRequired: false, hostedConfigured: false, hostedAttached: false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}};
    if (request.path === '/v1/direct/sign-in') return {status: 200, body: {authority: 'local', serverId: f.pin.serverId, canManage: true,
      capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
      account: {id: 'account', username: 'synthetic_user', primaryProfileId: 'profile', role: 'owner', revision: 1, disabled: false, allowedLibraries: ['movies']},
      profiles: [{id: 'profile', name: 'Synthetic viewer', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false},
        {id: 'guest', name: 'Synthetic guest', primary: false, position: 1, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}],
      session: {...f.session, sessionFamilyId: 'temporary_account', viewer: {...f.viewer, role: 'account'}}}};
    if (request.path === '/v1/direct/profiles/guest/select') return {status: 200, body: {session: {...f.session, sessionFamilyId: 'guest_family', viewer: guestViewer(f)}}};
    if (request.path === '/v1/me') return {status: 200, body: {viewer: guestViewer(f)}};
    if (request.path === '/v1/direct' && request.method === 'GET') return {status: 200, body: {authority: 'local', serverId: f.pin.serverId, canManage: false,
      capabilities: {manageAccount: false, managePeople: false, manageServer: false, manageSignIn: false},
      account: {id: 'account', username: 'synthetic_user', primaryProfileId: 'profile', role: 'owner', revision: 1, disabled: false, allowedLibraries: ['movies']},
      profiles: [{id: 'profile', name: 'Synthetic viewer', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: false},
        {id: 'guest', name: 'Synthetic guest', primary: false, position: 1, art: 'blue', revision: 1, pinRevision: 1, pinRequired: false}]}};
    if (request.path === '/v1/sessions/current' && request.method === 'DELETE') {deletes.push(String(request.headers?.authorization ?? request.headers?.Authorization ?? '')); return {status: 204};}
    return undefined;
  }});
  try {
    app.resetSyntheticStorage(); await app.mount();
    await app.tapLabel('Use a local server'); await app.fill('Server address', 'https://standard-home.test'); await app.tapLabel('Continue');
    await app.fill('Username', 'synthetic_user'); await app.fill('Password', 'synthetic_password'); await app.tapLabel('Sign in');
    assert.ok(app.hosts().some((host: any) => host.name === 'Text' && app.nativeLabel(host) === 'Who’s watching on Synthetic living room?'));
    await app.tapLabel('Synthetic guest');
    for (let i = 0; i < 40 && !(() => {try {return app.card();} catch {return false;}})(); i++) await new Promise(resolve => setTimeout(resolve, 25));
    assert.equal(app.requests.filter((request: any) => request.path === '/v1/direct/profiles/guest/select').length, 1, 'one selection, answered 200');
    const {nativeAppSessions} = await import('../src-next/platform/app-session-storage.ts');
    const saved = await nativeAppSessions.selected();
    assert.equal(saved?.connection.session.sessionFamilyId, 'guest_family', 'the picked profile is the stored session');
    assert.equal(saved?.connection.session.viewer.role, 'member');
    assert.ok(app.card(), 'Home is shown');
    assert.equal(deletes.length, 1, 'only the temporary account grant is ended, never the picked session');
  } finally {
    try {await app.unmount();} finally {app.disposeLoader();}
  }
});

test('the role a profile session carries: the account role for its primary profile, member for every other', async () => {
  const {profileSessionRole} = await import('../src-next/shell/account-binding.ts');
  const account = {role: 'owner' as const, primaryProfileId: 'main'};
  assert.equal(profileSessionRole(account, 'main'), 'owner');
  assert.equal(profileSessionRole(account, 'guest'), 'member');
  assert.equal(profileSessionRole({role: 'admin', primaryProfileId: 'a'}, 'a'), 'admin');
  assert.equal(profileSessionRole({role: 'member', primaryProfileId: 'a'}, 'b'), 'member');
});

test('after a successful select only a proven different identity ends the picked session, and every failure is named', async () => {
  const {readFileSync} = await import('node:fs');
  const read = (path: string) => readFileSync(new URL(`../src-next/${path}`, import.meta.url), 'utf8');
  const signIn = read('platform/server-sign-in.ts'), switching = read('platform/profile-selection.ts');
  assert.match(signIn, /logPickFailure\('sign-in', 'persist', error\);\n\s+if \(provenIdentityMismatch\(error\)\) \{try \{await this\.retire\(result\.session\);\} catch \{ \/\* the original error wins \*\/ \}\}\n\s+else this\.issued\.delete\(result\.session\.sessionFamilyId\);/);
  assert.match(switching, /if \(issued && !published\) \{refused = provenIdentityMismatch\(error\); logPickFailure\('switch', step, error\);\}/);
  assert.match(switching, /if \(issued && !published && refused && api\) \{/);
  assert.match(switching, /profileSessionRole\(account\.account, issued\.viewer\.profileId\) !== issued\.viewer\.role/);
  assert.match(read('shell/bootstrap-services.ts'), /profileSessionRole\(account\.account, viewer\.profileId\) !== viewer\.role\) this\.retireForRole\(\)/);
  assert.match(read('shell/account-binding.ts'), /profileSessionRole\(snapshot\.account, viewer\.profileId\) !== viewer\.role/);
});
