// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import * as core from '../../../packages/client-core/src';
import * as serverProbe from '../../../packages/client-core/src/sign-in';
import * as localRoutes from '../../../packages/contracts/src/routes';
import {CredentialSessionService, type CredentialRecord} from '../../../packages/client-core/src/credentials';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../packages/i18n/src';
import * as security from '../../../packages/client-core/src/hosted-security';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as identity from '../../../packages/client-core/src/route-identity';
import * as accountWork from '../src-next/platform/account-work';
import * as abortSignals from '../src-next/platform/abort-signals';
import * as localInvitation from '../src-next/platform/local-invitation';
import * as tvOpening from '../src-next/screens/accounts/tv-code-opening';
import * as outcome from '../src-next/platform/setup-outcome';
import * as chooser from '../src-next/screens/accounts/directory-chooser';
import {nativeHostedSource} from '../src-next/platform/server-connections';
import {NativeAccountWorkspace} from '../src-next/platform/account-workspace';
import {hostedUrl} from '../src-next/platform/account-authority';
import {NativeModules} from './react-native-stub.mjs';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const deferred = <T,>() => {let resolve!: (value: T) => void; const promise = new Promise<T>(yes => {resolve = yes;}); return {promise, resolve};};
const grant = (familyId = 'family', accountId = 'account', accessToken = 'old-access'): core.HostedSession => ({
  account: {id: accountId, username: 'Viewer', displayName: 'Viewer'}, familyId, accessToken, refreshToken: 'fixture-refresh-' + familyId,
  expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString(),
});
/** Actual coordinator + in-memory credential storage and issuing API. Actual HttpLocalApi/
 * native transport consumes synthetic HTTP responses below; no native secure store or live Hosted. */
const storageHold = () => ({...deferred<void>(), entered: deferred<void>()});
async function fixture() {
  let record: CredentialRecord<string> | undefined, holdLoad: Awaited<ReturnType<typeof storageHold>> | undefined;
  let renewal: () => Promise<core.HostedSession> = async () => grant('family', 'account', 'renewed-access');
  const renewals: string[] = [], revocations: string[] = [];
  const owner = new CredentialSessionService({authorityFromSession: value => value.refreshToken!,
    refresh: async authority => {renewals.push(authority); return renewal();}, revoke: async authority => {revocations.push(authority);}},
  {load: async () => {const hold = holdLoad; holdLoad = undefined; if (hold) {hold.entered.resolve(); await hold.promise;} return structuredClone(record);},
    save: async value => {record = structuredClone(value);}}, async () => 'fixture-renewal-request');
  await owner.synchronize(); await owner.authenticate(async () => grant());
  const original = owner.getSnapshot().session!;
  return {owner, original, renewals, revocations, record: () => structuredClone(record),
    renewal: (work: typeof renewal) => {renewal = work;},
    install: async (session: core.HostedSession) => owner.authenticate(async () => session),
    external: (session: core.HostedSession) => {record = {version: 1, active: {session, authority: session.refreshToken!, context: {}}, revocations: []};},
    holdLoad: () => holdLoad = storageHold(),
  };
}
type F = Awaited<ReturnType<typeof fixture>>;
type Reply = {status: number; headers: Record<string, string>; body: string};
const response = (status = 200, value: unknown = {items: [], unread: 0}): Reply => ({status, headers: {'Content-Type': 'application/json'},
  body: JSON.stringify(status >= 400 ? {error: {code: status === 401 ? 'session_ended' : 'unavailable', message: 'Synthetic refusal'}} : value)});
const transportOwners = new WeakSet<object>();
function transport(t: any, work: (url: string, method: string, headers: Record<string, string>, body: string | null) => Promise<Reply>) {
  const runtime = NativeModules.PorticoRuntime, original = runtime.networkRequest;
  runtime.networkRequest = (_id: string, url: string, method: string, headers: Record<string, string>, body: string | null) => work(url, method, headers, body);
  if (!transportOwners.has(t)) {transportOwners.add(t); t.after(() => {runtime.networkRequest = original;});}
}
function workspace(f: F) {
  return new NativeAccountWorkspace({accountCredentials: () => f.owner} as any,
    {accountId: 'account', familyId: 'family'}, () => true);
}
const source = (f: F) => nativeHostedSource(hostedUrl, f.owner, 'account', accountWork.accountExpectation(f.original));

for (const kind of ['routes', 'workspace'] as const) {
  test(`${kind}: cached unexpired bearer 401 + authoritative renewal refusal ends only the original canonical family`, async t => {
    const f = await fixture(), calls: string[] = [];
    f.renewal(async () => {throw new core.ApiError(401, 'session_ended', 'Ended', false);});
    transport(t, async (_url, method, headers) => {calls.push(method + ':' + new Headers(headers).get('Authorization')); return response(401);});
    const view = workspace(f); t.after(() => view.dispose());
    if (kind === 'routes') await assert.rejects(source(f).request('/v1/servers/server/routes', new AbortController().signal));
    else {await view.open('notifications'); assert.equal(view.getSnapshot().phase, 'ended');}
    assert.equal(f.owner.getSnapshot().phase, 'signedOut'); assert.equal(f.renewals.length, 1);
    assert.deepEqual(calls, ['GET:Bearer old-access']);
  });
  test(`${kind}: renewable 401 repeats only the same GET once with the renewed bearer`, async t => {
    const f = await fixture(), tokens: string[] = [], paths: string[] = [];
    transport(t, async (url, method, headers) => {assert.equal(method, 'GET'); paths.push(url); tokens.push(new Headers(headers).get('Authorization')!);
      return tokens.length === 1 ? response(401) : response();});
    const view = workspace(f); t.after(() => view.dispose());
    if (kind === 'routes') await source(f).request('/v1/servers/server/routes', new AbortController().signal);
    else {await view.open('notifications'); assert.equal(view.getSnapshot().phase, 'ready');}
    assert.deepEqual(tokens, ['Bearer old-access', 'Bearer renewed-access']); assert.equal(paths[0], paths[1]); assert.equal(f.renewals.length, 1);
  });
  test(`${kind}: second 401 stops, and 403/network/429 never rotate or sign out`, async t => {
    for (const status of [401, 403, 429, 'network'] as const) {
      const f = await fixture(); let calls = 0;
      transport(t, async () => {calls++; if (status === 'network') throw new Error('network failed'); return response(status);});
      const view = workspace(f);
      try {
        if (kind === 'routes') await assert.rejects(source(f).request('/v1/servers/server/routes', new AbortController().signal));
        else {await view.open('notifications'); assert.equal(view.getSnapshot().phase, 'failed');}
        assert.equal(calls, status === 401 ? 2 : 1); assert.equal(f.renewals.length, status === 401 ? 1 : 0);
        assert.equal(f.owner.getSnapshot().session?.familyId, 'family');
      } finally {view.dispose();}
    }
  });
  for (const replacement of ['same-account', 'other-account', 'ABA', 'unmount', 'storage-await'] as const) {
    test(`${kind}: held 401 cannot renew/end a ${replacement} successor`, async t => {
      const f = await fixture(), arrived = deferred<void>(), release = deferred<void>(); let calls = 0;
      transport(t, async () => {calls++; arrived.resolve(); await release.promise; return response(401);});
      const view = workspace(f), abort = new AbortController();
      const pending = kind === 'routes' ? source(f).request('/v1/servers/server/routes', abort.signal) : view.open('notifications');
      const settled = Promise.allSettled([pending]); await arrived.promise;
      let storage: Awaited<ReturnType<typeof storageHold>> | undefined;
      if (replacement === 'unmount') {abort.abort(); view.dispose();}
      else if (replacement === 'storage-await') {
        storage = f.holdLoad(); release.resolve();
        await storage.entered.promise; assert.equal(f.renewals.length, 0);
        // The original request is now actually waiting inside storage.load.
        f.external(grant('new-family', 'account', 'successor-access')); storage.resolve();
      } else {
        await f.install(grant('new-family', replacement === 'other-account' ? 'other' : 'account', 'successor-access'));
        if (replacement === 'ABA') await f.install(grant());
      }
      const priorRevocations = [...f.revocations];
      release.resolve(); await settled;
      assert.equal(calls, 1); assert.equal(f.renewals.length, 0); assert.deepEqual(f.revocations, priorRevocations);
      await f.owner.synchronize();
      assert.equal(f.owner.getSnapshot().session?.familyId, replacement === 'unmount' || replacement === 'ABA' ? 'family' : 'new-family');
      if (kind === 'workspace') assert.equal(view.getSnapshot().phase, 'ended'); view.dispose();
    });
  }
}

test('two real route-source failures share one canonical renewal; a late original 401 uses the newer token without another rotation', async t => {
  const f = await fixture(), gate = deferred<void>(), both = deferred<void>(); let old = 0;
  transport(t, async (_url, _method, headers) => {
    if (new Headers(headers).get('Authorization') === 'Bearer old-access') {if (++old === 2) both.resolve(); await gate.promise; return response(401);}
    return response();
  });
  const one = source(f), pending = Promise.all([one.request('/v1/servers/one/routes', new AbortController().signal), one.request('/v1/servers/two/routes', new AbortController().signal)]);
  await both.promise; gate.resolve(); await pending; assert.equal(f.renewals.length, 1); assert.equal(old, 2);
});

/** Actual Entry directory and credential lifecycle; unvisited command, native host and
 * presenter boundaries are explicit ports, never a replacement directory implementation. */
async function entry(t: any, f: F) {
  let tree: any; const stages: string[] = [], theme = createTheme({isTV: false, width: 390});
  class Auth {async prepare() {return {password: true, registration: false, recovery: false, providers: []};} dispose() {}}
  const modules: Record<string, any> = {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}, NativeModules: {}, View: 'View', Text: 'Text', Image: 'Image',
      AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: {addEventListener: () => ({remove() {}})}},
    'react-native-qrcode-svg': {default: 'QR'}, '../../../../../packages/client-core/src': core,
    '../../../../../packages/client-core/src/route-identity': identity, '../../../../../packages/client-core/src/hosted-security': security,
    '../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy, '../../../../../packages/client-core/src/join-link': joinLink,
    '../../../../../packages/client-core/src/short-invitation-code': invitationCode,
    '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateLayout: 'Layout'}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'Field'},
    '../../platform/accounts': {nativeAccounts: {accountCredentials: () => f.owner}}, '../../platform/account-work': accountWork,
    '../../platform/account-authority': {hostedUrl}, '../../platform/portico-sign-in': {NativePorticoSignIn: Auth},
    '../../platform/server-connections': await import('../src-next/platform/server-connections'), '../../platform/setup-outcome': outcome,
    '../../platform/tv-menu': {useTVMenuCapture() {}}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, './ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, './tv-code-opening': tvOpening, './directory-chooser': chooser, './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'SignIn'}, './PhoneFrontDoor': {PhoneFrontDoor: 'FrontDoor'},
    './PhoneNoServers': {PhoneNoServers: 'NoServers'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}},
  };
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), new Proxy(modules,
    {get: (all, name: string) => all[name] ?? new Proxy({}, {get: (_, key) => () => {throw new Error('Unvisited entry port ' + name + ':' + String(key));}})}));
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'require'); Object.defineProperty(globalThis, 'require', {configurable: true, value: () => 'wordmark'});
  t.after(() => {if (previous) Object.defineProperty(globalThis, 'require', previous); else Reflect.deleteProperty(globalThis, 'require');});
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {throw new Error('Unexpected navigation');}, onStageChange: (stage: string) => {stages.push(stage);}});});
  t.after(() => host.unmount()); await act(() => host.mount());
  assert.equal(tree.type, 'FrontDoor'); await act(() => tree.props.onPortico());
  return {host, stages, tree: () => tree, settle: async () => {for (let i = 0; i < 8; i++) await act(() => {});}};
}
test('actual AccountEntry directory remote revocation returns to the front door instead of a cached-bearer Retry loop', async t => {
  const f = await fixture(); let calls = 0;
  f.renewal(async () => {throw new core.ApiError(401, 'session_ended', 'Ended', false);});
  transport(t, async () => {calls++; return response(401);});
  const app = await entry(t, f); await app.settle();
  assert.equal(f.owner.getSnapshot().phase, 'signedOut'); assert.equal(f.renewals.length, 1); assert.equal(calls, 1);
  assert.equal(app.tree().type, 'FrontDoor'); assert.equal(app.stages.at(-1), 'welcome');
});
test('actual AccountEntry directory safely recovers once and retires its original view on a new family', async t => {
  const f = await fixture(), tokens: string[] = [];
  transport(t, async (_url, method, headers) => {assert.equal(method, 'GET'); tokens.push(new Headers(headers).get('Authorization')!); return tokens.length === 1 ? response(401) : response(200, {items: []});});
  const app = await entry(t, f); await app.settle(); assert.equal(app.tree().type, 'NoServers');
  assert.deepEqual(tokens, ['Bearer old-access', 'Bearer renewed-access']);
  await act(async () => {await f.install(grant('new-family', 'account', 'successor-access'));}); await app.settle();
  assert.equal(f.owner.getSnapshot().session?.familyId, 'new-family'); assert.equal(f.renewals.length, 1);
  assert.equal(app.tree().type, 'FrontDoor');
  assert.deepEqual(tokens, ['Bearer old-access', 'Bearer renewed-access'], 'replacement does not contact Services until explicitly selected');
  await act(() => app.tree().props.onPortico()); await app.settle();
  assert.equal(app.tree().type, 'NoServers');
  assert.ok(tokens.includes('Bearer successor-access')); assert.deepEqual(f.revocations, ['fixture-refresh-family']);
});

for (const replacement of ['same-account', 'other-account', 'ABA', 'unmount', 'storage-await'] as const) {
  test(`actual AccountEntry: held directory 401 retires on ${replacement} and never renews a successor`, async t => {
    const f = await fixture(), arrived = deferred<void>(), release = deferred<void>(); let calls = 0;
    transport(t, async () => {if (++calls === 1) {arrived.resolve(); await release.promise; return response(401);} return response(200, {items: []});});
    const app = await entry(t, f); await arrived.promise;
    if (replacement === 'unmount') await app.host.unmount();
    else if (replacement === 'storage-await') {
      const read = f.holdLoad(); release.resolve(); await read.entered.promise;
      assert.equal(f.renewals.length, 0); f.external(grant('new-family', 'account', 'successor-access')); read.resolve();
    } else await act(async () => {
      await f.install(grant('new-family', replacement === 'other-account' ? 'other' : 'account', 'successor-access'));
      if (replacement === 'ABA') await f.install(grant());
    });
    const revocations = [...f.revocations]; release.resolve(); await app.settle();
    assert.equal(f.renewals.length, 0); assert.deepEqual(f.revocations, revocations);
    assert.equal(f.owner.getSnapshot().session?.familyId, ['unmount', 'ABA'].includes(replacement) ? 'family' : 'new-family');
    if (replacement !== 'unmount') {
      assert.equal(app.tree().type, 'FrontDoor');
      assert.equal(calls, 1, 'a retired directory read cannot automatically read for the replacement family');
      await act(() => app.tree().props.onPortico()); await app.settle();
      assert.equal(app.tree().type, 'NoServers');
      assert.equal(calls, 2, 'only the explicit selection reads the replacement directory');
    }
  });
}
for (const status of [403, 429, 'network'] as const) {
  test(`actual AccountEntry: ${status} retains its original canonical family without renewal`, async t => {
    const f = await fixture(); let calls = 0;
    transport(t, async () => {calls++; if (status === 'network') throw new Error('network failed'); return response(status);});
    const app = await entry(t, f); await app.settle();
    assert.equal(f.owner.getSnapshot().session?.familyId, 'family'); assert.equal(f.renewals.length, 0); assert.equal(calls, 1);
    assert.equal(app.stages.at(-1), 'servers');
  });
}

/** Public connectPortico -> actual consumeHostedCommand -> actual HTTP -> real owner.
 * Verified-route and command-producer ports are synthetic; this does not prove real signed routes
 * or invitation/member/owner/TV admission. No server-local selection operation is supplied. */
async function serverCommand(f: F, kind: string, produce?: (hosted: any) => Promise<any>) {
  const installation = {installationId: 'fixture-installation'}, pin = {serverId: 'server', publicKey: 'public', fingerprint: 'fingerprint'};
  const descriptor = {id: 'server', name: 'Server', version: '1', signIn: 'portico', move: 'to_portico', setupRequired: false, hostedConfigured: true, hostedAttached: true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
  const routes = {pin, dispose() {}}, api = {baseUrl: 'https://server.example', getRouteConnection: () => routes,
    request: async (path: string) => {assert.equal(path, localRoutes.routePath('get_system')); return descriptor;}};
  const selection = {revision: 1, selectedServerId: 'server', servers: [{connection: {pin, session: {sessionFamilyId: 'retained-server-local'}}}]}, before = structuredClone(selection);
  const modules: Record<string, any> = {react: {default: React},
    '../../../../packages/client-core/src': {...core, assertFirstContactIdentity() {}},
    '../../../../packages/client-core/src/sign-in': {...serverProbe, ServerSignInProbe: class {dispose() {} async inspect() {return descriptor;} getSnapshot() {return {origin: api.baseUrl};}}},
    '../../../../packages/contracts/src/routes': localRoutes,
    '../../../../packages/client-core/src/app-sessions': {knownServerRoutes: () => []},
    '../../../../packages/client-core/src/installation': {installationIdentity: () => installation},
    '../../../../packages/client-core/src/portico-servers': {porticoSignIn: produce ?? (async (hosted: any) => hosted.request('/v1/servers/server/identity', 'POST', {challenge: 'original-' + kind, requestId: 'original-operation'}))},
    '../../../../packages/client-core/src/server-connections': {inspectDirectServer: async () => pin, connectPairedServer: async () => ({api, connection: routes}),
      connectPairedPorticoServer: async () => ({api, connection: routes, hosted: {origin: hostedUrl, accountId: 'account'}}), connectHostedServer: async () => ({api, connection: routes, hosted: {origin: hostedUrl, accountId: 'account'}})},
    './accounts': {nativeAccounts: {}}, './account-work': accountWork, './abort-signals': abortSignals, './account-authority': {hostedUrl},
    './server-connections': await import('../src-next/platform/server-connections'),
    './server-display': {rememberNativeServerDisplay: async () => ({id: 'server', name: 'Server', signIn: 'portico'})},
    './app-session-storage': {nativeAppSessions: {read: async () => selection}, subscribeNativeAppSessions: () => () => {}},
    './member-account-move': {NativeMemberAccountMove: class {captureHosted: () => Promise<any>; constructor(ports: any) {this.captureHosted = ports.captureHosted;} dispose() {}}},
    './hosted-sign-in-unavailable': {hostedSignInUnavailable: () => undefined},
    './negotiation': await import('../src-next/platform/negotiation'),
    // The real local-invitation namespace: these closure tests never open one,
    // and an unexpected parse or format call would run the real guard.
    './local-invitation': localInvitation,
  };
  const {NativeServerSignIn} = await screenModule(new URL('../src-next/platform/server-sign-in.ts', import.meta.url), new Proxy(modules,
    {get: (all, name: string) => all[name] ?? new Proxy({}, {get: (_, key) => () => {throw new Error('Unvisited command port ' + name + ':' + String(key));}})}));
  const command = new NativeServerSignIn({accountCredentials: () => f.owner, onCancel: () => () => {}, admission: async () => ({})});
  return {command, start: () => command.connectPortico({id: 'server'}), member: async () => {await command.inspect(api.baseUrl); const move = await command.createMemberMove('password'); return move.captureHosted();}, selection: () => {assert.deepEqual(selection, before);}};
}
for (const kind of ['directory', 'invitation', 'member', 'owner', 'TV']) {
  test(`actual server-sign-in request closure never repeats ${kind} mutation when 401 renews the original family`, async t => {
    const f = await fixture(), sent: string[] = [];
    transport(t, async (_url, method, headers, body) => {assert.equal(method, 'POST'); assert.equal(new Headers(headers).get('Authorization'), 'Bearer old-access'); sent.push(body!); return response(401);});
    const operation = await serverCommand(f, kind);
    await assert.rejects(operation.start(), (error: unknown) => error instanceof core.ApiError && error.status === 401);
    assert.deepEqual(sent.map(value => JSON.parse(value)), [{challenge: 'original-' + kind, requestId: 'original-operation'}]);
    assert.equal(f.renewals.length, 1); assert.equal(f.owner.getSnapshot().session?.familyId, 'family'); operation.selection(); operation.command.dispose();
  });
}

for (const closure of ['official', 'member'] as const) {
  for (const cancel of [false, true]) {
    test(`actual ${closure} Hosted closure snapshots nested command JSON before held storage; cancel=${cancel}`, async t => {
      const f = await fixture(), caller = new AbortController(), issued = deferred<ReturnType<F['holdLoad']>>();
      const body = {challenge: 'original-challenge', requestId: 'original-request', target: {id: 'original-target', nested: ['original']}};
      const sent: {path: string; method: string; body: string | null}[] = [];
      transport(t, async (path, method, _headers, wire) => {sent.push({path, method, body: wire}); return response(401);});
      const invoke = (hosted: any) => {const hold = f.holdLoad(); issued.resolve(hold);
        return hosted.request('/v1/servers/server/identity', 'POST', body, caller.signal);};
      const operation = await serverCommand(f, 'snapshot', invoke);
      const member = closure === 'member' ? await operation.member() : undefined;
      const pending = closure === 'official' ? operation.start() : invoke(member.api);
      const settled = Promise.allSettled([pending]), hold = await issued.promise; await hold.entered.promise;
      assert.equal(sent.length, 0, 'canonical storage is actually holding original admission');
      body.challenge = 'successor-challenge'; body.requestId = 'successor-request'; body.target.id = 'successor-target'; body.target.nested[0] = 'successor';
      if (cancel) caller.abort(); hold.resolve(); const [result] = await settled;
      assert.equal(result!.status, 'rejected');
      if (cancel) {assert.deepEqual(sent, []); assert.equal(f.renewals.length, 0);}
      else {
        assert.deepEqual(sent, [{path: hostedUrl + '/v1/servers/server/identity', method: 'POST', body: JSON.stringify({challenge: 'original-challenge', requestId: 'original-request', target: {id: 'original-target', nested: ['original']}})}]);
        assert.equal(f.renewals.length, 1);
      }
      operation.selection(); operation.command.dispose();
    });
  }
  for (const body of [undefined, null]) {
    test(`actual ${closure} Hosted closure preserves ${body === undefined ? 'absent' : 'null'} command body`, async t => {
      const f = await fixture(), sent: (string | null)[] = [];
      transport(t, async (_path, _method, _headers, wire) => {sent.push(wire); return response(401);});
      const invoke = (hosted: any) => hosted.request('/v1/servers/server/identity', 'POST', body);
      const operation = await serverCommand(f, 'body-shape', invoke);
      if (closure === 'official') await assert.rejects(operation.start());
      else {const member = await operation.member(); await assert.rejects(invoke(member.api));}
      assert.deepEqual(sent, [body === undefined ? null : 'null']); assert.equal(f.renewals.length, 1);
      operation.selection(); operation.command.dispose();
    });
  }
}
