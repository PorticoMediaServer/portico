import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime.ts';
import {createNativeAccountSecurityScope, nativeAccountSecurityAllowed, type NativeAccountSecurityKind} from '../src-next/screens/settings-live/native-account-security.ts';

function fixture() {
  const authListeners = new Set<() => void>(), navigationListeners = new Set<() => void>();
  let active = true, navigation: any = {revision: 1, route: {kind: 'shell', destination: {kind: 'settings'}}};
  let authority = {serverId: 'server', accountId: 'account', profileId: 'main', epoch: 'epoch', viewerFence: 'fence'};
  let session: any = {sessionFamilyId: 'family', installationId: 'installation-fixture', viewer: {serverId: 'server', accountId: 'account', profileId: 'main'}};
  const account: any = {serverId: 'server', account: {id: 'account', primaryProfileId: 'main', role: 'member', disabled: false}, canManage: true, capabilities: {manageAccount: true, manageSignIn: false}};
  const calls: {path: string; method: string; body: unknown; signal: AbortSignal}[] = [];
  let reply: () => Promise<unknown> = async () => ({accepted: true});
  const context: any = {account, current: () => active, authority: () => authority, session: () => session,
    subscribeAuthority: (listener: () => void) => {authListeners.add(listener); return () => authListeners.delete(listener);},
    navigation: {getSnapshot: () => navigation, subscribe: (listener: () => void) => {navigationListeners.add(listener); return () => navigationListeners.delete(listener);}},
    api: {request: async (path: string, method: string, body: unknown, signal: AbortSignal) => {calls.push({path, method, body, signal}); return reply();}},
  };
  const lifetime = new NativeScreenLifetime(context);
  const notify = () => {for (const listener of [...authListeners]) listener();};
  return {context, lifetime, account, calls,
    scope: (kind: NativeAccountSecurityKind = 'password') => createNativeAccountSecurityScope(context, lifetime, kind),
    setReply: (value: () => Promise<unknown>) => {reply = value;},
    route: () => {navigation = {...navigation, revision: navigation.revision + 1}; for (const listener of [...navigationListeners]) listener();},
    outsideSettings: () => {navigation = {revision: 2, route: {kind: 'shell', destination: {kind: 'home'}}};},
    profile: () => {session = {...session, viewer: {...session.viewer, profileId: 'secondary'}};},
    family: () => {session = {...session, sessionFamilyId: 'replacement'}; notify();},
    revoke: () => {account.canManage = false; account.capabilities.manageAccount = false; notify();},
    end: () => {active = false; notify();},
    close: () => lifetime.dispose(),
  };
}

test('Only a current main profile may manage account security; local members do not need server Sign-in powers', () => {
  const f = fixture();
  try {
    for (const role of ['member', 'admin', 'owner']) {
      f.account.account.role = role;
      assert.equal(nativeAccountSecurityAllowed(f.context, 'password'), true);
      assert.equal(nativeAccountSecurityAllowed(f.context, 'twoStep'), true);
      assert.equal(nativeAccountSecurityAllowed(f.context, 'devices'), true);
    }
    f.profile();
    for (const kind of ['password', 'twoStep', 'devices'] as const) assert.equal(nativeAccountSecurityAllowed(f.context, kind), false);
  } finally {f.close();}
});

test('Hosted-linked accounts can open account security under the same main-profile grant', () => {
  const f = fixture(); f.account.account.hostedAccountId = 'hosted-account';
  try {
    assert.equal(nativeAccountSecurityAllowed(f.context, 'devices'), true);
    for (const kind of ['password', 'twoStep'] as const) {assert.equal(nativeAccountSecurityAllowed(f.context, kind), true); f.scope(kind).dispose();}
    const scope = f.scope('devices'); scope.dispose(); assert.equal(f.calls.length, 0);
  } finally {f.close();}
});

test('Disabled accounts, absent management capability, and a non-Settings opening send nothing', () => {
  for (const denied of ['disabled', 'capability', 'route'] as const) {
    const f = fixture();
    try {
      if (denied === 'disabled') f.account.account.disabled = true;
      if (denied === 'capability') f.revoke();
      if (denied === 'route') f.outsideSettings();
      assert.throws(() => f.scope('devices'), {name: 'AbortError'}); assert.equal(f.calls.length, 0);
    } finally {f.close();}
  }
});

test('The guarded transport preserves the exact local request and caller signal', async () => {
  const f = fixture(), scope = f.scope();
  try {
    const body = {currentPassword: 'current-fixture', newPassword: 'replacement-fixture', code: 'factor-fixture'};
    const result = await scope.request('/v1/direct/password', 'POST', body);
    assert.deepEqual(result, {accepted: true}); assert.equal(f.calls.length, 1);
    assert.equal(f.calls[0].path, '/v1/direct/password'); assert.equal(f.calls[0].method, 'POST'); assert.equal(f.calls[0].body, body); assert.equal(f.calls[0].signal.aborted, false);
  } finally {scope.dispose(); f.close();}
});

for (const retirement of ['route', 'family', 'profile', 'revoke', 'end', 'dispose'] as const) test(`A pending security request is never adopted or followed by a write after ${retirement}`, async () => {
  const f = fixture(), scope = f.scope('twoStep'); let finish!: (value: unknown) => void;
  f.setReply(() => new Promise(resolve => {finish = resolve;}));
  try {
    const pending = scope.request('/v1/direct/two-factor', 'GET');
    const rejected = assert.rejects(pending, {name: 'AbortError'});
    if (retirement === 'dispose') scope.dispose(); else f[retirement]();
    finish({enabled: true}); await rejected;
    await assert.rejects(scope.request('/v1/direct/two-factor', 'DELETE', {password: 'fixture', code: 'fixture'}), {name: 'AbortError'});
    assert.equal(f.calls.length, 1);
    if (retirement !== 'profile') assert.equal(f.calls[0].signal.aborted, true);
  } finally {scope.dispose(); f.close();}
});

test('Closing and reopening refuses retained callbacks from the old security opening', async () => {
  const f = fixture(), previous = f.scope('devices');
  try {
    previous.dispose(); const next = f.scope('devices');
    await assert.rejects(previous.request('/v1/devices/old/sessions', 'DELETE'), {name: 'AbortError'});
    assert.equal(f.calls.length, 0);
    await next.request('/v1/devices', 'GET'); assert.equal(f.calls.length, 1); next.dispose();
  } finally {f.close();}
});

test('An operation-specific abort stops the actual local request without retiring its enclosing dialog', async () => {
  const f = fixture(), scope = f.scope('devices'), caller = new AbortController(); let finish!: (value: unknown) => void;
  f.setReply(() => new Promise(resolve => {finish = resolve;}));
  try {
    const pending = scope.request('/v1/devices', 'GET', undefined, caller.signal), rejected = assert.rejects(pending, {name: 'AbortError'});
    caller.abort(); assert.equal(f.calls[0].signal.aborted, true); finish({items: []}); await rejected;
    assert.equal(scope.current(), true);
  } finally {scope.dispose(); f.close();}
});
