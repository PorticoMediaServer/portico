import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@core/index.ts';
import {SettingsOwnerSwitch} from '../src/screens/settings/owner-switch.ts';
import {SignInSettingsSource} from '../src/screens/settings/signin-source.ts';
import {setInstallationIdentity} from '@core/installation.ts';

// The switch names this device for the owner's new sign-in.
setInstallationIdentity({installationId: 'i'.repeat(43), name: 'Test browser', platform: 'web', app: 'portico', appVersion: '1'});

// The owner's switch of the kind of sign-in on the rebuilt server (definition 02 rules 15 to 30), tested against
// answers recorded from server-next's sign_in_kind.go shapes. Nothing here runs a real switch.
const preview = {to: 'portico', startOver: false, signInAgain: 2, removed: 0, profilesRemoved: 0, profilesKept: 3, signedOut: 4, invitationsCancelled: 1,
  keysPaused: 0, keysRemoved: 0, porticoAddressRemoved: false,
  approval: {approvalId: 'apr_1', approvalUrl: 'https://web.example/approve/apr_1', code: 'K7QP', pollToken: 'poll-token', expiresAt: '2026-11-16T20:00:00Z', pollAfterMs: 5}};
const later = (minutes: number) => new Date(Date.now() + minutes * 60000).toISOString();
/** The owner's new sign-in as the rebuilt server answers it (server-next SignInResponse). */
const signedIn = () => ({session: {accessToken: 'access', expiresAt: later(15), sessionId: 'ses_1', tokenGeneration: '1', sessionExpiresAt: later(60 * 24 * 90), deviceId: 'dev_1',
  installationId: 'i'.repeat(43), refreshToken: 'refresh_token_0000000001', viewer: {accountId: 'owner', serverId: 'server', role: 'account'}},
  account: {id: 'owner', username: 'owner', role: 'owner'}, profiles: [{id: 'main', name: 'Owner', color: 'blue', isPrimary: true, pinRequired: false}]});

function fixture(signIn: 'local' | 'portico' = 'local', answers: Record<string, (body: unknown) => unknown> = {}) {
  let current = true, secure = true;
  const calls: {path: string; method?: string; body?: unknown}[] = [], retired: unknown[] = [];
  const api = new HttpLocalApi('https://server.example', '');
  api.request = async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    const answer = answers[path]; if (answer) return answer(body) as T;
    if (path === '/v1/admin/sign-in/preview') {const asked = body as {to: string; startOver?: boolean};return {...preview, to: asked.to, startOver: !!asked.startOver, ...(asked.startOver ? {signInAgain: 0, removed: 2, profilesRemoved: 2, profilesKept: 1} : {})} as T;}
    throw new Error(`Unexpected route ${path}`);
  };
  const controller = new SettingsOwnerSwitch(api, {serverId: 'server', hostedOrigin: 'https://web.example', isCurrent: () => current, signIn, serverName: () => 'Actual server',
    secureContextAllowed: () => secure, retireLateGrant: async session => {retired.push(session);}, createClaim: () => {throw new Error('Unexpected owner claim');}});
  return {controller, calls, retired, insecure: () => {secure = false;}, retire: () => {current = false;}};
}

test('Switch and Start over refuse HTTP before any request', async () => {
  for (const startOver of [false, true]) {
    const f = fixture();f.insecure();
    await assert.rejects(f.controller.preview(startOver), {code: 'secure_context_required'});
    assert.deepEqual(f.calls, []);f.controller.dispose();
  }
});

test('a preview draws the server\'s own numbers, asks Portico Services for an approval, and closing it sends nothing', async () => {
  const f = fixture();
  try {
    const shown = await f.controller.preview(false);
    assert.equal((f.calls[0]!.body as {returnUrl?: string}).to, 'portico');
    assert.deepEqual({people: shown.people, profiles: shown.profiles, invitations: shown.invitations, devices: shown.devices, serverName: shown.serverName},
      {people: 2, profiles: 3, invitations: 1, devices: 4, serverName: 'Actual server'});
    assert.equal(f.controller.approvalUrl(), 'https://web.example/approve/apr_1', 'only the Portico Account site this app knows');
    assert.equal(f.controller.needsProof(), true, 'a local server asks for the owner\'s password');
    await f.controller.cancelPreview();
    assert.equal(f.calls.length, 1, 'a preview holds nothing on the server');
    const over = await f.controller.previewDirection('local', true);
    assert.deepEqual({people: over.people, removed: over.removedProfiles, kept: over.retainedProfiles, devices: over.devices}, {people: 2, removed: 2, kept: 1, devices: undefined});
  } finally {f.controller.dispose();}
});

test('the switch to Portico Accounts waits for the approval, then confirms with it and the owner\'s password, and ends the sign-in it answers', async () => {
  let polls = 0;
  const f = fixture('local', {
    '/v1/hosted/approvals/apr_1/poll': body => {assert.deepEqual(body, {pollToken: 'poll-token'});return ++polls < 3 ? {state: 'pending', pollAfterMs: 1} : {state: 'approved', approval: 'signed-approval', pollAfterMs: 1};},
    '/v1/admin/sign-in/switch': () => signedIn(),
  });
  try {
    await f.controller.preview(false);
    await f.controller.confirmPortico('', {currentPassword: 'old secret', code: '123456'}, async () => {});
    const sent = f.calls.find(call => call.path === '/v1/admin/sign-in/switch')!.body as Record<string, unknown>;
    assert.equal(polls, 3);
    assert.deepEqual({to: sent.to, approval: sent.approval, currentPassword: sent.currentPassword, code: sent.code, startOver: sent.startOver}, {to: 'portico', approval: 'signed-approval', currentPassword: 'old secret', code: '123456', startOver: undefined});
    assert.ok(sent.device, 'the new sign-in names this device');
    assert.equal(f.retired.length, 1, 'the owner signs in again with the Portico Account');
  } finally {f.controller.dispose();}
});

test('a declined approval confirms nothing and the dialog can try again', async () => {
  const f = fixture('portico', {'/v1/hosted/approvals/apr_1/poll': () => ({state: 'declined', pollAfterMs: 1})});
  try {
    await f.controller.preview(true);
    await assert.rejects(f.controller.confirmPortico('Actual server', {}, async () => {}), {code: 'approval_declined'});
    assert.ok(!f.calls.some(call => call.path === '/v1/admin/sign-in/switch'));
    assert.equal(f.controller.getSnapshot().confirmed, false);
    assert.equal(f.controller.needsProof(), false, 'a Portico Accounts server has no password to ask');
  } finally {f.controller.dispose();}
});

test('a move in progress is read from the Sign-in state; Finish now and the codes use the rebuilt routes', async () => {
  const state = {kind: 'local', owner: {id: 'owner', username: 'owner'}, move: {to: 'local', since: '2026-11-16T10:00:00Z', total: 3},
    waiting: [{id: 'usr_a', username: 'ada'}, {id: 'usr_b', username: 'max', codeExpiresAt: '2026-11-23T10:00:00Z'}]};
  const f = fixture('local', {
    '/v1/admin/sign-in': () => state,
    '/v1/admin/sign-in/finish': () => ({notMoved: 2}),
    '/v1/admin/members/usr_a/move-code': () => ({id: 'usr_a', username: 'ada', code: 'K7QP2MXD9HRT', expiresAt: '2026-11-23T10:00:00Z'}),
  });
  try {
    f.controller.refreshMove();await new Promise(resolve => setTimeout(resolve, 0));
    const move = f.controller.getMove()!;
    assert.deepEqual({to: move.to, stage: move.stage, people: move.people, waiting: move.waiting}, {to: 'local', stage: 'moving_to_local', people: 3, waiting: 2});
    assert.deepEqual((await f.controller.waiting()).items.map(row => row.accountId), ['usr_a', 'usr_b']);
    assert.deepEqual((await f.controller.codes(['usr_a'])).map(row => [row.accountId, row.code]), [['usr_a', 'K7QP2MXD9HRT']]);
    await f.controller.finishMove(move);
    assert.ok(f.calls.some(call => call.path === '/v1/admin/sign-in/finish' && call.method === 'POST'));
  } finally {f.controller.dispose();}
});

test('the mounted Sign-in source is inert and exposes only actual kind/owner facts', async () => {
  let calls = 0, current = true;
  const source = new SignInSettingsSource({request: async <T>() => {
    calls++;return {kind: 'local', owner: {id: 'owner', username: 'actual_owner'}, waiting: []} as T;
  }}, {serverId: 'server', signIn: 'local', isCurrent: () => current, capabilities: () => ({manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true}), onAuthorityChanged: () => {throw new Error('Unexpected authority change');}});
  try {
    assert.equal(calls, 0);await source.load();assert.equal(source.rowState('signin.ownerLocal')?.displayValue, 'actual_owner');
    assert.equal(source.rowState('signin.owner')?.present, false);await source.load();assert.equal(calls, 1);
    current = false;assert.equal(source.rowState('signin.ownerLocal')?.present, false);
  } finally {source.dispose();}
});
