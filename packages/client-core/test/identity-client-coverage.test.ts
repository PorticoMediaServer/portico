import {test} from 'node:test';
import assert from 'node:assert/strict';
import {IdentityClient,parseTwoFactorEnrolment,AVATAR_MAX_BYTES} from '../src/identity-client.ts';

const restrictions = {
  profileId: 'child', ratingSystem: 'mpaa', maximumAgeRating: 'PG', maximumAge: 8,
  allowUnrated: false, blockedLabels: [], allowDownloads: true, allowLiveTv: true,
  allowDvr: false, allowWatchTogether: true, revision: 4,
};
const avatar = {
  profileId: 'child', version: 3, updatedAt: '2026-09-17T00:00:00Z',
  sourceMime: 'image/png', url: '/v1/profiles/child/avatar?v=3',
};
const device = {
  id: 'd1', installationId: 'i'.repeat(32), name: 'TV', platform: 'tvos', app: 'Portico',
  appVersion: '1', ip: '192.168.1.2', firstSeen: '2026-09-01T00:00:00Z', lastSeen: '2026-09-17T00:00:00Z',
  lastProfileId: '', rememberAccount: true, current: true, sessions: 1,
};
const remembered = [{accountId: 'a', username: 'sam', displayName: 'Sam', avatarVersion: 1, automaticSignIn: false, lastUsed: '2026-09-17T00:00:00Z'}];

function client(responses: Record<string, unknown>, calls: {path: string; method: string; body: unknown}[] = []) {
  const api = {
    request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
      calls.push({path, method, body});
      // A response may be keyed by method and path where one path answers two methods.
      if (method + ' ' + path in responses) return responses[method + ' ' + path] as T;
      if (path in responses) return responses[path] as T;
      throw new Error('unexpected ' + path);
    },
  };
  return {api, calls, client: new IdentityClient(api)};
}

test('an enrolment is bounded: secret, address and recovery-code limits', () => {
  const ok = {
    secret: 'JBSWY3DPEHPK3PXPJBSWY3DP',
    uri: 'otpauth://totp/Portico:sam?secret=JBSWY3DPEHPK3PXPJBSWY3DP&issuer=Portico',
    recoveryCodes: ['aaaa-bbbb'],
  };
  assert.equal(parseTwoFactorEnrolment({...ok, secret: 'A'.repeat(16)}).secret.length, 16);
  assert.equal(parseTwoFactorEnrolment({...ok, secret: 'A'.repeat(128)}).secret.length, 128);
  assert.throws(() => parseTwoFactorEnrolment({...ok, secret: 'A'.repeat(15)}));
  assert.throws(() => parseTwoFactorEnrolment({...ok, secret: 'A'.repeat(129)}));
  assert.throws(() => parseTwoFactorEnrolment({...ok, secret: ''}));
  assert.throws(() => parseTwoFactorEnrolment({...ok, uri: 'otpauth://totp/x?' + 'y'.repeat(1024)}));
  assert.deepEqual(parseTwoFactorEnrolment({...ok, recoveryCodes: []}).recoveryCodes, []);
  assert.throws(() => parseTwoFactorEnrolment({...ok, recoveryCodes: Array.from({length: 21}, (_, i) => 'code-' + i + 'xx')}));
});

test('restrictions round-trip behind the profile id, saved as a full replacement', async () => {
  const calls: {path: string; method: string; body: unknown}[] = [];
  const {client: c} = client({'/v1/direct/profiles/p%2F1/restrictions': restrictions}, calls);
  // Direct call uses the same path for GET; PUT tested below with a second client.
  assert.equal((await c.restrictions('p/1')).revision, 4);
  assert.equal(calls[0].path, '/v1/direct/profiles/p%2F1/restrictions');
  const putCalls: {path: string; method: string; body: unknown}[] = [];
  const put = client({'/v1/direct/profiles/child/restrictions': restrictions}, putCalls);
  const current = await put.client.restrictions('child');
  await put.client.saveRestrictions(current, {allowUnrated: true});
  assert.equal(putCalls[1].method, 'PUT');
  assert.equal((putCalls[1].body as {expectedRevision: number}).expectedRevision, 4);
});

test('PIN recovery, avatar removal and single-avatar parsing', async () => {
  const calls: {path: string; method: string; body: unknown}[] = [];
  const {client: c} = client(
    {
      '/v1/direct/pin-recovery': {password: true, authenticator: false, recoveryCode: true},
      '/v1/direct/profiles/p%2F1/avatar': undefined,
    },
    calls,
  );
  assert.deepEqual(await c.pinRecovery(), {password: true, authenticator: false, recoveryCode: true});
  await c.removeAvatar('p/1');
  assert.deepEqual(calls[1], {path: '/v1/direct/profiles/p%2F1/avatar', method: 'DELETE', body: undefined});
  assert.equal(c.avatar(avatar).version, 3);
  assert.throws(() => c.avatar({...avatar, sourceMime: 'image/gif'}));
});

test('avatar lists are bounded at 64; an empty picture never sends', async () => {
  const {client: c} = client({'/v1/direct/profiles/avatars': {items: [avatar]}});
  assert.equal((await c.avatars()).length, 1);
  const tooMany = client({'/v1/direct/profiles/avatars': {items: Array.from({length: 65}, () => avatar)}});
  await assert.rejects(tooMany.client.avatars());
  const notList = client({'/v1/direct/profiles/avatars': {items: 'nope'}});
  await assert.rejects(notList.client.avatars());
  const upload = new IdentityClient({request: async () => undefined as never}, async () => avatar);
  await assert.rejects(upload.uploadAvatar('p-1', new Uint8Array(0), 'image/png'), /empty/);
  await assert.rejects(upload.uploadAvatar('p-1', new Uint8Array(AVATAR_MAX_BYTES + 1), 'image/png'), /smaller than 4 MB/);
});

test('remembered accounts are asked before sign-in and forgotten by id', async () => {
  const inst = 'i'.repeat(32);
  const calls: {path: string; method: string; body: unknown}[] = [];
  const forgetPath = '/v1/direct/remembered-accounts/a%2Fb';
  const {client: c} = client(
    {
      '/v1/auth/remembered-accounts': {items: remembered},
      '/v1/direct/remembered-accounts': {items: remembered},
      [forgetPath]: undefined,
    },
    calls,
  );
  assert.equal((await c.rememberedAccounts(inst)).length, 1);
  assert.equal((await c.rememberAccount(inst, true)).length, 1);
  assert.deepEqual(calls[1], {path: '/v1/direct/remembered-accounts', method: 'POST', body: {installationId: inst, automaticSignIn: true}});
  await c.forgetAccount('a/b', inst);
  assert.deepEqual(calls[2], {path: '/v1/direct/remembered-accounts/a%2Fb', method: 'DELETE', body: {installationId: inst}});
});

test('device edits go to the documented routes with exactly the documented bodies', async () => {
  const calls: {path: string; method: string; body: unknown}[] = [];
  const {client: c} = client(
    {
      'GET /v1/devices': {items: [device, {...device, id: 'd2', installationId: '', current: false}]},
      '/v1/devices': device,
      '/v1/devices/d1': device,
      '/v1/devices/d1/sessions/bind': undefined,
      '/v1/devices/d1/sessions': undefined,
    },
    calls,
  );
  // The list is asked for with no installation id in the address; another device's id comes back empty.
  const listed = await c.devices('i'.repeat(32));
  assert.equal(listed.length, 2);
  assert.deepEqual(calls[0], {path: '/v1/devices', method: 'GET', body: undefined});
  assert.equal(listed[1].installationId, '');
  assert.equal((await c.registerDevice({installationId: 'i'.repeat(32), name: 'TV', platform: 'tvos', app: 'Portico', appVersion: '1'})).id, 'd1');
  await c.bindDevice('d1', 'i'.repeat(32), 'fam-1');
  assert.deepEqual(calls[2], {path: '/v1/devices/d1/sessions/bind', method: 'POST', body: {installationId: 'i'.repeat(32), sessionFamilyId: 'fam-1'}});
  await c.editDevice('d1', {name: 'Den'});
  assert.deepEqual(calls[3].body, {name: 'Den'});
  assert.equal('approveDevice' in c,false);
  await c.signOutDevice('d1');
  assert.equal(calls[4].path, '/v1/devices/d1/sessions');
  await c.removeDevice('d1');
  assert.equal(calls[5].method, 'DELETE');
});

test('two-factor state, enrolment and verification ride the current password', async () => {
  const calls: {path: string; method: string; body: unknown}[] = [];
  const enrol = {
    secret: 'JBSWY3DPEHPK3PXPJBSWY3DP',
    uri: 'otpauth://totp/Portico:sam?secret=JBSWY3DPEHPK3PXPJBSWY3DP&issuer=Portico',
    recoveryCodes: ['aaaa-bbbb'],
  };
  const {client: c} = client(
    {
      '/v1/direct/two-factor': {enabled: false, pendingEnrolment: false, recoveryCodesRemaining: 0},
      '/v1/direct/two-factor/enrol': enrol,
      '/v1/direct/two-factor/verify': {enabled: true, pendingEnrolment: false, recoveryCodesRemaining: 8},
    },
    calls,
  );
  assert.equal((await c.twoFactor()).enabled, false);
  assert.equal((await c.enrolTwoFactor('secret')).secret.length > 0, true);
  assert.deepEqual(calls[1], {path: '/v1/direct/two-factor/enrol', method: 'POST', body: {password: 'secret'}});
  assert.equal((await c.verifyTwoFactor('secret', '123456')).enabled, true);
});

test('a server error answer surfaces; an unknown envelope is refused', async () => {
  const failing = new IdentityClient({
    request: async () => {
      throw Object.assign(new Error('gone'), {status: 404});
    },
  });
  await assert.rejects(failing.devices('i'.repeat(32)), /gone/);
  const bad = new IdentityClient({request: async () => ({items: [{...device, approvalState: 'maybe'}]}) as never});
  await assert.rejects(bad.devices('i'.repeat(32)));
});
