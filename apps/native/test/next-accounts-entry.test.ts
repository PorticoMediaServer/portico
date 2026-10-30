import test from 'node:test';
import assert from 'node:assert/strict';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('normal product entry signs in locally through actual React/native bridges and restores its canonical selected profile', async () => {
  let kind = 'portico';
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: any, f: any) => {
    if (request.path === '/v1/system') return {status: 200, body: {id: f.pin.serverId, name: 'Synthetic living room', version: '1', move: null, signIn: kind,
      setupRequired: false, hostedConfigured: kind === 'portico', hostedAttached: kind === 'portico', apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []}};
    if (request.path === '/v1/direct/sign-in') {
      assert.equal(kind, 'local'); assert.equal(request.body.username, 'synthetic_user');
      assert.equal(request.body.password, 'synthetic_password');
      return {status: 200, body: {authority: 'local', serverId: f.pin.serverId, canManage: true,
        capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
        account: {id: 'account', username: 'synthetic_user', primaryProfileId: 'profile', role: 'owner', revision: 1, disabled: false, allowedLibraries: ['movies']},
        profiles: [{id: 'profile', name: 'Synthetic viewer', primary: true, position: 0, art: 'mint', revision: 1, pinRevision: 1, pinRequired: true}],
        session: {...f.session, sessionFamilyId: 'temporary_account', viewer: {...f.viewer, role: 'account'}}}};
    }
    if (request.path === '/v1/direct/profiles/profile/select') {
      assert.equal(request.body.pin, '1234'); assert.equal(request.body.installationId, f.installationId);
      return {status: 200, body: {session: {...f.session, sessionFamilyId: 'new_selected_family'}}};
    }
    if (request.path === '/v1/sessions/current') return {status: 204};
    return undefined;
  }});
  try {
    app.resetSyntheticStorage(); await app.mount();
    assert.ok(app.hosts().some((host: any) => host.name === 'Image' && host.props.accessibilityLabel === 'Portico'), 'signed-out phone renders the actual brand asset');
    // MOB-016 (connect.html 39a, auth.html frame 4): an ordinary signed-out phone opens
    // the two-choice front door; Portico Account sign-in is reached only by its button.
    assert.ok(app.hosts().some((host: any) => host.name === 'Text' && app.nativeLabel(host) === 'Welcome home.'), 'the actual phone front door is mounted through the standard entry');
    assert.equal(app.hosts().filter((host: any) => host.props.testID === 'accounts-welcome-portico' || host.props.testID === 'accounts-welcome-local').length, 2, 'both front-door choices are offered');
    assert.equal(app.hosts().some((host: any) => host.name === 'Text' && app.nativeLabel(host) === 'Sign in with your Portico Account to access servers shared with you.'), false, 'Portico Account sign-in waits for its explicit choice');
    assert.equal(app.requests.length, 0, 'empty local startup never synchronizes Portico credentials or contacts Hosted');
    await app.tapLabel('Use a local server'); await app.fill('Server address', 'https://standard-home.test'); await app.tapLabel('Continue');
    assert.ok(app.hosts().some((host: any) => app.nativeLabel(host) === 'Synthetic living room uses Portico Accounts'));
    assert.equal(app.hosts().some((host: any) => host.name === 'TextInput' && host.props.accessibilityLabel === 'Password'), false);
    assert.deepEqual(app.requests.map((request: any) => request.path), ['/v1/system']);
    await app.tapLabel('Back'); kind = 'local';
    await app.tapLabel('Use a local server'); await app.fill('Server address', 'https://standard-home.test'); await app.tapLabel('Continue');
    await app.fill('Username', 'synthetic_user'); await app.fill('Password', 'synthetic_password'); await app.tapLabel('Sign in');
    assert.ok(app.hosts().some((host: any) => host.name === 'Text' && app.nativeLabel(host) === 'Who’s watching on Synthetic living room?'));
    await app.tapLabel('Synthetic viewer'); await app.fill('Four-digit PIN', '1234');
    assert.equal(app.requests.filter((request: any) => request.path === '/v1/direct/profiles/profile/select').length, 1, 'fourth PIN digit submits exactly one selection');
    const {nativeAppSessions} = await import('../src-next/platform/app-session-storage.ts');
    const saved = await nativeAppSessions.selected();
    assert.equal(saved?.connection.session.sessionFamilyId, 'new_selected_family');
    assert.equal(saved?.signIn, 'local');
    assert.ok(app.card(), 'genuine normal Home restored after durable publication');
    assert.ok(app.requests.every((request: any) => request.url.startsWith('https://standard-home.test')));
    assert.equal(app.requests.filter((request: any) => request.path === '/v1/sessions/current').length, 1, 'temporary account family is retired once, viewer stays');
  } finally {
    try {
      await app.unmount();
    } finally {
      app.disposeLoader();
    }
  }
});
