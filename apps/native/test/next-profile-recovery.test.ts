import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeProfileRecovery} from '../src-next/platform/profile-recovery';
import type {DirectSnapshot} from '../../../packages/client-core/src/profile-management';
import type {HttpLocalApi} from '../../../packages/client-core/src';

function snapshot(canManage = false, accountId = 'account'): DirectSnapshot {
  return {authority: 'local', serverId: 'server', canManage,
    capabilities: {manageAccount: canManage, managePeople: false, manageServer: false, manageSignIn: false},
    account: {id: accountId, username: 'member', primaryProfileId: 'main', role: 'member', revision: 1, disabled: false, allowedLibraries: []},
    profiles: [{id: 'main', name: 'Main', primary: true, position: 0, art: 'blue', revision: 1, pinRevision: 1, pinRequired: true},
      {id: 'alex', name: 'Alex', primary: false, position: 1, art: 'violet', revision: 1, pinRevision: 1, pinRequired: true}]} as unknown as DirectSnapshot;
}
function session(accountId = 'account', role = 'account', family = 'temporary-family') {
  return {accessToken: 'temporary-access', refreshToken: 'temporary-refresh', deviceId: 'device', installationId: 'installation',
    expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z',
    sessionFamilyId: family, tokenGeneration: '1',
    viewer: {authority: 'local', serverId: 'server', accountId, profileId: 'main', role}};
}
/** The raw `POST /v1/direct/sign-in` shape the real client parses. */
function raw(accountId = 'account', role = 'account', profiles = snapshot(role === 'account', accountId).profiles, family = 'temporary-family') {
  return {...snapshot(role === 'account', accountId), profiles, session: session(accountId, role, family)};
}
/** The parsed result for ports that already return DirectSignIn. */
function signedIn(accountId = 'account', role = 'account', family = 'temporary-family') {
  return {kind: 'signed-in' as const, snapshot: snapshot(role === 'account', accountId), session: session(accountId, role, family),
    accountScoped: role === 'account', passwordChangeRequired: false};
}
const viewerProfiles = [{...snapshot(true).profiles[0]!, pinRequired: false}];
function fixture(canManage = false, kind: 'local' | 'portico' = 'local') {
  const calls: {kind: string; path: string; body: unknown}[] = [];
  let answer: unknown = raw(), current = true, release: (() => void) | undefined;
  const source = {baseUrl: 'https://server.test', request: async (path: string, _method: string, body: unknown) => {
    calls.push({kind: 'selected', path, body});
    if (release) await new Promise<void>(done => {release = done;});
    return answer;
  }} as unknown as HttpLocalApi;
  const temporary = {request: async (path: string, _method: string, body: unknown) => {calls.push({kind: 'temporary', path, body});}} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: source, snapshot: snapshot(canManage), profileId: 'alex', selectedFamilyId: 'current-family', kind,
    authorityCurrent: () => current, temporary: () => temporary});
  return {flow, calls, answer: (value: unknown) => {answer = value;}, cancelAuthority: () => {current = false;}, hold: () => {release = () => {};}, release: () => release?.()};
}

test('a fresh password proves one reset before its temporary family retires', async () => {
  const f = fixture();
  await f.flow.password('private-password'); assert.equal(f.flow.getSnapshot().phase, 'pin');
  await f.flow.reset('2468'); assert.equal(f.flow.getSnapshot().phase, 'done');
  assert.deepEqual(f.calls.map(({kind, path}) => [kind, path]), [
    ['selected', '/v1/direct/sign-in'], ['temporary', '/v1/direct/profiles/alex/pin-reset'], ['temporary', '/v1/sessions/current'],
  ]);
  assert.deepEqual(f.calls[1]!.body, {pin: '2468'}); f.flow.dispose();
});

test('an actual management capability resets without minting a temporary family', async () => {
  const f = fixture(true); assert.equal(f.flow.getSnapshot().phase, 'pin');
  await f.flow.reset('2468'); assert.equal(f.calls.length, 1); assert.equal(f.calls[0]!.path, '/v1/direct/profiles/alex/pin-reset');
  f.flow.dispose();
});

test('a viewer credential is never accepted and its family is retired', async () => {
  const f = fixture(); f.answer(raw('account', 'member', viewerProfiles));
  await f.flow.password('private-password');
  assert.equal(f.flow.getSnapshot().phase, 'password'); assert.ok(f.flow.getSnapshot().error);
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']);
  f.flow.dispose();
});

test('a sign-in for another account is retired and cannot reset this profile', async () => {
  const f = fixture(); f.answer(raw('other-account'));
  await f.flow.password('private-password');
  assert.equal(f.flow.getSnapshot().phase, 'password'); assert.ok(f.flow.getSnapshot().error);
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']);
  f.flow.dispose();
});

test('a proof using the selected app family is refused without revoking it', async () => {
  const f = fixture(); f.answer(raw('account', 'account', undefined, 'current-family'));
  await f.flow.password('private-password');
  assert.equal(f.flow.getSnapshot().phase, 'password'); assert.ok(f.flow.getSnapshot().error);
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in']);
  f.flow.dispose();
});

test('a sign-in completed after cancellation is retired without publishing a reset grant', async () => {
  const f = fixture(); f.hold(); const pending = f.flow.password('private-password');
  await Promise.resolve(); f.flow.dispose(); f.release(); await pending;
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']);
  assert.notEqual(f.flow.getSnapshot().phase, 'pin');
});

test('a changed app authority after the proof is retired without a reset grant', async () => {
  const f = fixture(); f.hold(); const pending = f.flow.password('private-password');
  await Promise.resolve(); f.cancelAuthority(); f.release(); await pending;
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']);
  assert.notEqual(f.flow.getSnapshot().phase, 'pin'); f.flow.dispose();
});

test('abandoning a proved recovery retires only the temporary family', async () => {
  const f = fixture();
  await f.flow.password('private-password'); assert.equal(f.flow.getSnapshot().phase, 'pin');
  f.flow.dispose(); await new Promise<void>(done => setImmediate(done));
  assert.deepEqual(f.calls.map(value => value.path), ['/v1/direct/sign-in', '/v1/sessions/current']);
});

test('two-step verification completes before a new PIN can be set', async () => {
  const f = fixture(); f.answer({challenge: {token: 'challenge', expiresAt: '2099-01-01T00:00:00Z'}});
  await f.flow.password('private-password'); assert.equal(f.flow.getSnapshot().phase, 'challenge');
  await f.flow.reset('2468'); assert.equal(f.calls.length, 1);
  f.answer(raw()); await f.flow.code('123456'); assert.equal(f.flow.getSnapshot().phase, 'pin');
  assert.equal(f.calls[1]!.path, '/v1/auth/two-factor/challenge');
  await f.flow.reset('2468'); assert.equal(f.calls.at(-1)!.path, '/v1/sessions/current');
  f.flow.dispose();
});

test('a Portico reauthentication uses the Hosted-current fence and its proof family', async () => {
  const calls: string[] = [];
  const temporary = {request: async (path: string) => {calls.push('temporary:' + path);}} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: {baseUrl: 'https://server.test', request: async (path: string) => {calls.push('selected:' + path); return {};}} as unknown as HttpLocalApi,
    snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true, temporary: () => temporary,
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => true}),
    signIn: async () => signedIn() as never});
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'pin');
  await flow.reset('2468');
  assert.deepEqual(calls, ['temporary:/v1/direct/profiles/alex/pin-reset', 'temporary:/v1/sessions/current']);
  flow.dispose();
});

test('a Hosted account change after the Portico proof is retired', async () => {
  const calls: string[] = [];
  const temporary = {request: async (path: string) => {calls.push('temporary:' + path);}} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: {baseUrl: 'https://server.test', request: async () => ({})} as unknown as HttpLocalApi,
    snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true, temporary: () => temporary,
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => false}),
    signIn: async () => signedIn() as never});
  await flow.portico();
  assert.notEqual(flow.getSnapshot().phase, 'pin'); assert.ok(flow.getSnapshot().error);
  assert.deepEqual(calls, ['temporary:/v1/sessions/current']);
  flow.dispose();
});

test('a Hosted account change after phase pin refuses the reset and retires the proof family', async () => {
  let account = true; const calls: string[] = [];
  const temporary = {request: async (path: string) => {calls.push('temporary:' + path);}} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: {baseUrl: 'https://server.test', request: async () => ({})} as unknown as HttpLocalApi,
    snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true, temporary: () => temporary,
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => account}),
    signIn: async () => signedIn() as never});
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'pin');
  account = false;
  await flow.reset('2468');
  assert.equal(flow.getSnapshot().phase, 'portico'); assert.ok(flow.getSnapshot().error);
  assert.deepEqual(calls, ['temporary:/v1/sessions/current']);
  flow.dispose();
});

test('a Hosted account change after a challenge refuses the code without a reset', async () => {
  let account = true; const calls: string[] = [];
  const flow = new NativeProfileRecovery({api: {baseUrl: 'https://server.test', request: async (path: string) => {calls.push(path); return {};}} as unknown as HttpLocalApi,
    snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true,
    temporary: () => ({request: async () => {}} as unknown as HttpLocalApi),
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => account}),
    signIn: async () => ({kind: 'challenge', token: 'challenge', expiresAt: '2099-01-01T00:00:00Z'}) as never});
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'challenge');
  account = false;
  await flow.code('123456');
  assert.equal(flow.getSnapshot().phase, 'portico'); assert.ok(flow.getSnapshot().error);
  assert.deepEqual(calls, []);
  flow.dispose();
});

test('a Hosted account change during the challenge retires the returned proof without a reset', async () => {
  let account = true, release: (() => void) | undefined, started: () => void = () => {};
  const start = new Promise<void>(done => {started = done;}), calls: string[] = [], temp: string[] = [];
  const source = {baseUrl: 'https://server.test', request: async (path: string) => {
    calls.push(path); started();
    await new Promise<void>(done => {release = done;});
    return raw();
  }} as unknown as HttpLocalApi;
  const temporary = {request: async (path: string) => {temp.push('temporary:' + path);}} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: source, snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true,
    temporary: () => temporary,
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => account}),
    signIn: async () => ({kind: 'challenge', token: 'challenge', expiresAt: '2099-01-01T00:00:00Z'}) as never});
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'challenge');
  const pending = flow.code('123456');
  await start; account = false; release!(); await pending;
  assert.equal(flow.getSnapshot().phase, 'portico'); assert.ok(flow.getSnapshot().error);
  assert.deepEqual(calls, ['/v1/auth/two-factor/challenge']);
  assert.deepEqual(temp, ['temporary:/v1/sessions/current']);
  flow.dispose();
});

test('a second reset during proof cleanup cannot use the selected family', async () => {
  let account = true, release: (() => void) | undefined, started: () => void = () => {};
  const start = new Promise<void>(done => {started = done;});
  const selected: string[] = [], temp: string[] = [];
  const selectedApi = {baseUrl: 'https://server.test', request: async (path: string) => {selected.push(path); return {};}} as unknown as HttpLocalApi;
  const temporary = {request: async (path: string) => {
    temp.push(path);
    if (path === '/v1/sessions/current' && !release) {started(); await new Promise<void>(done => {release = done;});}
  }} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: selectedApi, snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true,
    temporary: () => temporary,
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => account}),
    signIn: async () => signedIn() as never});
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'pin');
  account = false;
  const first = flow.reset('2468');
  await start;
  assert.equal(flow.getSnapshot().phase, 'portico'); assert.equal(flow.getSnapshot().busy, false);
  await flow.reset('1357');
  assert.deepEqual(selected, [], 'a reset never falls through to the selected app family');
  release!(); await first;
  assert.deepEqual(temp, ['/v1/sessions/current']);
  assert.notEqual(flow.getSnapshot().phase, 'done');
  flow.dispose();
});

test('a late proof cleanup does not overwrite a successor attempt', async () => {
  let account = true, release: (() => void) | undefined, started: () => void = () => {}, deletes = 0;
  const start = new Promise<void>(done => {started = done;});
  const selected: string[] = [], temp: string[] = [];
  const selectedApi = {baseUrl: 'https://server.test', request: async (path: string) => {selected.push(path); return {};}} as unknown as HttpLocalApi;
  const temporary = {request: async (path: string) => {
    temp.push(path);
    if (path === '/v1/sessions/current' && deletes++ === 0) {started(); await new Promise<void>(done => {release = done;});}
  }} as unknown as HttpLocalApi;
  const flow = new NativeProfileRecovery({api: selectedApi, snapshot: snapshot(false), profileId: 'alex', selectedFamilyId: 'current-family', kind: 'portico', authorityCurrent: () => true,
    temporary: () => temporary,
    portico: async () => ({hosted: {request: async () => ({})} as never, origin: 'https://hosted.test', accountCurrent: () => account}),
    signIn: async () => signedIn() as never});
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'pin');
  account = false;
  const first = flow.reset('2468');
  await start;
  assert.equal(flow.getSnapshot().phase, 'portico');
  account = true;
  await flow.portico(); assert.equal(flow.getSnapshot().phase, 'pin');
  release!(); await first;
  assert.equal(flow.getSnapshot().phase, 'pin', 'late cleanup does not overwrite the successor');
  await flow.reset('1357');
  assert.equal(flow.getSnapshot().phase, 'done');
  assert.deepEqual(selected, []);
  assert.deepEqual(temp, ['/v1/sessions/current', '/v1/direct/profiles/alex/pin-reset', '/v1/sessions/current']);
  flow.dispose();
});
