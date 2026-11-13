import test from 'node:test';
import assert from 'node:assert/strict';
import {systemAnswer} from '../../../packages/client-core/test/next-wire.ts';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

test('a moved member opens the native shell with the selected profile member session', async () => {
  const app = await standardHomeApp({setup: true, setupResponse: async (request: any, f: any) => {
    if (request.path === '/v1/system') return {status: 200, body: {...systemAnswer({id: f.pin.serverId, name: 'Moving server', signIn: 'portico'}), move: 'to_portico'}};
    if (request.path === '/v1/me') return {status: 200, body: {account: {id: 'account', username: 'fixture_member', role: 'member', hostedAccountId: 'portico_member'},
      profile: {id: 'profile', name: 'Move Member', isPrimary: true, pinRequired: false, color: 'blue'}, role: 'member', canManageAccount: true}};
    if (request.path === '/v1/auth/move/check') return {status: 200, body: {username: 'fixture_member', next: 'portico_account', ticket: 'server_move_ticket_0123456789abcdef'}};
    if (request.path === '/v1/auth/portico/challenge') return {status: 200, body: {serverId: f.pin.serverId, challenge: 'fresh_move_challenge_0123456789'}};
    if (request.path === '/v1/auth/move/complete') return {status: 200, body: {
      session: {...f.session, accessToken: 'moved_account_access', sessionFamilyId: 'moved_family', viewer: {...f.viewer, profileId: '', role: 'account'}},
      account: {id: 'account', username: 'fixture_member', role: 'member'},
      profiles: [{id: 'profile', name: 'Move Member', isPrimary: true, pinRequired: false, color: 'blue'}],
    }};
    if (request.path === '/v1/profiles/profile/select') return {status: 200, body: {...f.session, accessToken: 'moved_profile_access', refreshToken: undefined,
      sessionFamilyId: 'moved_family', viewer: {...f.viewer, role: 'member'}}};
    if (request.path === '/v1/auth/session') return {status: 204};
    return undefined;
  }});
  const {NativeModules} = await import('react-native');
  let credentials: string | null = null;
  NativeModules.PorticoRuntime.loadCredentials = async () => credentials;
  NativeModules.PorticoRuntime.saveCredentials = async (_origin: string, value: string) => {credentials = value;};
  const {nativeAccounts} = await import('../src-next/platform/accounts.ts');
  const originalIdentity = nativeAccounts.porticoIdentity;
  nativeAccounts.porticoIdentity = async (_held, serverId, challenge) => ({identity: {payload: Buffer.from(JSON.stringify({accountId: 'portico_member', serverId, challenge})).toString('base64url')}});
  const {NativeServerSignIn} = await import('../src-next/platform/server-sign-in.ts');
  const {nativeAppSessions} = await import('../src-next/platform/app-session-storage.ts');
  const {nativeShellDirectViewer} = await import('../src-next/shell/bootstrap-services.ts');
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap.ts');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  let runtime: Awaited<ReturnType<typeof restoreNativeShellRuntime>>;
  let operation: InstanceType<typeof NativeServerSignIn> | undefined;
  try {
    await nativeAccounts.authenticatePorticoAccount(async () => ({accessToken: 'synthetic_portico_access', refreshToken: 'synthetic_portico_refresh', familyId: 'portico_family',
      expiresAt: '2099-01-01T00:00:00Z', refreshExpiresAt: '2099-02-01T00:00:00Z', account: {id: 'portico_member', username: 'portico_member', displayName: 'Member'}}));
    operation = new NativeServerSignIn();
    // The screen lane enables move entry separately; exercise the adopted controller here.
    (operation.probe as any).readsMove = true;
    await operation.inspect('https://standard-home.test');
    const move = await operation.createMemberMove('password');
    await move.proveOldPassword('fixture_member', 'old password');
    await move.preparePortico(); await move.redeemPortico();
    const accepted = move.takeAcceptedResult();
    assert.ok(accepted);
    if (accepted.kind === 'profiles') await operation.chooseProfile('profile');
    const selected = await nativeAppSessions.selected();
    assert.equal(selected?.connection.session.viewer.role, 'member');
    assert.equal(selected?.connection.session.viewer.profileId, 'profile');
    assert.doesNotThrow(() => nativeShellDirectViewer(selected!.connection.session.viewer));
    operation.dispose(); operation = undefined;
    let shellRole: string | undefined;
    runtime = await restoreNativeShellRuntime(context => {shellRole = context.session()?.viewer.role; return createNativeShellBindings(context);}, {onAuthorityEnded: () => {}});
    assert.ok(runtime);
    assert.equal(shellRole, 'member');
    assert.equal(runtime.firstLibraryContext?.home.getSnapshot().phase, 'ready');
  } finally {
    runtime?.dispose(); nativeAccounts.porticoIdentity = originalIdentity; operation?.dispose();
    try {await app.unmount();} finally {app.disposeLoader();}
  }
});
