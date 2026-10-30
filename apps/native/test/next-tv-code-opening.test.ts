// @fabric-react
import * as accountWork from '../src-next/platform/account-work';
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {buttonSpec, focusSpec} from '@portico/design';
import * as controlModel from '../src-next/kit/controls/control-model';
import * as focusRegistry from '../src-next/kit/focus/registry';
import * as hostRef from '../src-next/kit/focus/useHostRef';
import {CredentialSessionService, type CredentialRecord} from '../../../packages/client-core/src/credentials';
import type {HostedSession} from '../../../packages/client-core/src';
import * as tvApprovalGrant from '../src-next/platform/tv-approval-grant';
import * as tvApprovalCredentials from '../src-next/screens/accounts/tv-approval-credentials';
import * as tvApproval from '../src-next/screens/accounts/tv-approval-finalization';
import {ApiError} from '../../../packages/client-core/src/index.ts';
import {SetupCodeService, type SetupCodeJournal, type SetupCodeSnapshot} from '../../../packages/client-core/src/setup-code.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {createTVCodeOpening, type TVCodeOpening, type TVCodeKind} from '../src-next/screens/accounts/tv-code-opening';
import {useTVBusyEntryRevision} from '../src-next/tv/auth/entry-revision';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as setupOutcome from '../src-next/platform/setup-outcome';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as directoryChooser from '../src-next/screens/accounts/directory-chooser';
import {routeOrigin} from '../../../packages/client-core/src/route-identity';

const deferred = <T,>() => {let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;}); return {promise, resolve, reject};};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const settle = async () => {for (let i = 0; i < 4; i++) await act(() => {});};
type Grant = HostedSession;
const approvedGrant = (): Grant => ({account: {id: 'account', username: 'Viewer', displayName: 'Viewer'}, familyId: 'family', accessToken: 'fixture-access', refreshToken: 'fixture-refresh', expiresAt: new Date(Date.now() + 3600000).toISOString(), refreshExpiresAt: new Date(Date.now() + 86400000).toISOString()});

/** Real durable service + real opening + real presenter. The issuing API/storage
 * are in-memory boundaries; this does not exercise native hosts, crypto or HTTP. */
function fixture() {
  let owner: TVCodeOpening | undefined, stage = 'tv', alive = true, source = {};
  let view: {opening: TVCodeOpening; state: Omit<SetupCodeSnapshot<Grant>, 'grant'>} | undefined;
  let error: unknown, busy = false, ids = 0, failNext = false;
  let holdNext: ReturnType<typeof deferred<void>> | undefined, holdCompletion: ReturnType<typeof deferred<void>> | undefined;
  let failCompletion = false;
  const journals = new Map<string, SetupCodeJournal<Grant>>();
  const services: {service: SetupCodeService<Grant>; callbacks: (() => void)[]; cancels: number}[] = [];
  const calls = {create: 0, poll: 0, cancel: 0, accept: 0, credentials: 0};
  let poll: 'denied' | 'pending' | 'approved' = 'denied';
  let acceptHold: ReturnType<typeof deferred<void>> | undefined;
  function newService(kind: TVCodeKind) {
    const authority = kind === 'local' ? 'local:fixture' : 'https://accounts.example#tv-x25519-v1';
    const service = new SetupCodeService<Grant>(authority, {
      async create() {
        const code = ++calls.create === 1 ? 'ABCD-EFGH' : 'JKMN-PQRS';
        const verificationUri = (kind === 'local' ? 'https://local.example' : 'https://accounts.example') + '/device';
        return {deviceCode: 'd'.repeat(48), userCode: code, verificationUri, verificationUriComplete: verificationUri + '#code=' + code,
          expiresAt: new Date(Date.now() + 600_000).toISOString(), expiresIn: 600, interval: 5};
      },
      async poll() {calls.poll++; if (poll === 'approved') return approvedGrant();
        throw new ApiError(400, poll === 'denied' ? 'access_denied' : 'authorization_pending', 'fixture response', false);},
      async cancel() {calls.cancel++;},
    }, {async change(update) {
      const hold = holdNext; holdNext = undefined; if (hold) await hold.promise;
      if (failNext) {failNext = false; throw new Error('fixture storage unavailable');}
      const old = journals.get(authority), next = update(old);
      if (old?.current?.grant && !next.current) {const hold = holdCompletion; holdCompletion = undefined; if (hold) await hold.promise; if (failCompletion) {failCompletion = false; throw new Error('fixture completion journal unavailable');}}
      journals.set(authority, next); return next;
    }}, async () => `00000000-0000-4000-8000-${String(++ids).padStart(12, '0')}`,
    {requestTimeoutMs: 15_000, tickMs: 1000, replaceBeforeMs: 60_000, random: () => 0});
    const entry = {service, callbacks: [] as (() => void)[], cancels: 0}; services.push(entry);
    const subscribe = service.subscribe, cancel = service.cancel.bind(service);
    service.subscribe = callback => {entry.callbacks.push(callback); return subscribe(callback);};
    service.cancel = async () => {entry.cancels++; await cancel();};
    return service;
  }
  function mount(kind: TVCodeKind, originalSource = source) {
    const service = newService(kind);
    const opening = createTVCodeOpening({kind, service, source: originalSource}, {
      owns: value => owner === value,
      current: original => alive && stage === 'tv' && original.source === source,
      publish: (opening, state) => {view = {opening, state};},
      busy: value => {busy = value;}, error: value => {error = value;},
      replace: async (original, current) => {if (current()) await mount(original.kind, original.source).start();},
      credentials: async (_, current) => {calls.credentials++; if (current()) {opening.retire(); owner = undefined; stage = 'credentials';}},
      accept: async (original, grant, current) => {
        if (!current()) return; calls.accept++; if (acceptHold) await acceptHold.promise;
        if (!current()) return;
        const completed = await original.service.complete(grant);
        if (current() && completed) {opening.retire(); owner = undefined; stage = 'done';}
      },
    });
    owner?.retire(); owner = opening; stage = 'tv'; error = undefined;
    view = {opening, state: {phase: 'idle'}};
    return opening;
  }
  return {mount, newService, calls, services, view: () => view!, error: () => error, busy: () => busy, stage: () => stage,
    failCompletion: () => {failCompletion = true;}, holdCompletion: () => holdCompletion = deferred<void>(),
    poll: (value: typeof poll) => {poll = value;}, failNext: () => {failNext = true;},
    holdNext: () => holdNext = deferred<void>(), holdAccept: () => acceptHold = deferred<void>(),
    changeSource: () => {source = {};},
    back() {owner?.retire(true); owner = undefined; stage = 'welcome'; busy = false;},
    unmount() {owner?.retire(); owner = undefined; alive = false;},
    cleanup() {owner?.retire(); for (const {service} of services) service.pause();},
  };
}

async function presenter(f: Awaited<ReturnType<typeof fixture>>, kind: TVCodeKind) {
  const entries: unknown[] = [];
  const {TVCodePresenter} = await screenModule(new URL('../src-next/tv/auth/TVCodePresenter.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-qrcode-svg': {default: 'QR'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/controls': {Button: 'Button'},
    '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/theme': {nativeType, useTheme: () => createTheme({isTV: true, width: 1920})},
    '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus: (activation: unknown, address: unknown) => {entries.push({activation, address});}},
    '../../platform/native-blur': {BlurView: 'Blur', glassRecipe: () => ({nativeBlur: false})},
    './entry-revision': {useTVBusyEntryRevision}, './TVAuthLayout': {TVAuthLayout: 'Layout'},
  });
  let tree: any;
  const host = createFabricHookHost(() => {
    const {opening, state} = f.view();
    const terminal = state.phase === 'denied' || state.phase === 'error' || f.error();
    const wrapped = TVCodePresenter({mode: kind, serverName: 'Fixture server', body: 'Fixture consent', ...state, busy: f.busy(),
      onNewCode: terminal ? () => {void opening.newCode();} : undefined,
      onTypeCredentials: () => {void opening.credentials();}, onBack: () => {if (opening.current()) f.back();}});
    tree = wrapped.props.children.type(wrapped.props.children.props);
  });
  await act(() => host.mount());
  return {host, entries, tree: () => tree, button: (id: string) => nodes(tree).find(n => n.type === 'Button' && n.props.focusId === id),
    refresh: () => act(() => host.rerender())};
}

async function accountEntry(f: Awaited<ReturnType<typeof fixture>>, prepare: () => Promise<Record<string, unknown>> = async () => ({}), approvedAccounts?: unknown) {
  let tree: any, completed = 0;
  const back = new Set<() => boolean>(); let appStateChange: (value: string) => void = () => {};
  // Observe actual hook references/state publications without relying on hook
  // positions. Capability bookkeeping is deliberately not a TV UI gate.
  const refs = new Set<{current: unknown}>(), writes: unknown[] = [];
  const observedReact = {...React, default: React,
    useRef: (value: unknown) => {const ref = React.useRef(value); refs.add(ref); return ref;},
    useState: (value: any) => {const [state, set] = React.useState(value); return [state, (next: any) => {writes.push(next); set(next);}];},
  };
  const accounts: AuthSource[] = [];
  class AuthSource {
    disposed = 0;
    constructor() {accounts.push(this);}
    async prepare() {return prepare();}
    dispose() {this.disposed++;}
  }
  class LocalSource {
    static parseInvitationEntry() {return undefined;}
    async inspect() {return {id: 'fixture', name: 'Fixture server', signIn: 'local'};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {}
    serverDescriptor() {return {id: 'fixture', name: 'Fixture server', signIn: 'local'};}
    localTVSignIn() {return f.newService('local');}
    async acceptLocalTVSession() {throw new Error('unexpected grant in denied-entry vector');}
    dispose() {}
  }
  const modules: Record<string, unknown> = {
    react: observedReact,
    'react-native': {Platform: {isTV: true}, AppState: {currentState: 'active', addEventListener: (_: string, listener: (value: string) => void) => {appStateChange = listener; return {remove() {}};}},
      BackHandler: {addEventListener: (_: string, fn: () => boolean) => {back.add(fn); return {remove: () => back.delete(fn)};}},
      TVEventControl: {enableTVMenuKey() {}, disableTVMenuKey() {}}, View: 'View', Text: 'Text', Image: 'Image', NativeModules: {}},
    'react-native-qrcode-svg': {default: 'QR'}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/route-identity': {routeOrigin},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'Fixture failure', category: 'unavailable'})},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    '../../../../../packages/client-core/src/join-link': joinLink,
    '../../../../../packages/client-core/src/short-invitation-code': invitationCode,
    '../../kit/theme': {useTheme: () => createTheme({isTV: true, width: 1920})}, '../../kit/states': {StateLayout: 'Layout'},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'Field'}, '../../kit/artwork': {ProfilePicker: 'Picker'},
    '../../tv/auth': {TVCodePresenter: 'Code', TVFrontDoor: 'Front', TVCredentialForm: 'Credentials'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: approvedAccounts ?? {accountCredentials: () => ({synchronize: async () => {}, getSnapshot: () => ({}), subscribe: () => () => {}})}},
    '../../platform/server-sign-in': {NativeServerSignIn: LocalSource}, '../../platform/setup-outcome': setupOutcome,
    '../../platform/portico-sign-in': {NativePorticoSignIn: AuthSource},
    '../../platform/server-connections': {browseNearby: async () => []},
    '../../platform/tv-sign-in': {createNativePorticoTVSignIn: () => f.newService('portico')},
    './directory-chooser': directoryChooser, '../../platform/tv-menu': {useTVMenuCapture() {}}, './tv-code-opening': tvCodeOpening, './tv-approval-finalization': tvApproval, './tv-approval-credentials': tvApprovalCredentials,
    '../../../../../packages/client-core/src': {HttpHostedApi: class {async servers() {return {items: []};}}},
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, './ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneFrontDoor': {PhoneFrontDoor: 'PhoneFrontDoor'},
  };
  // Unvisited native/credential branches may import names, but an unexpected
  // invocation fails loudly; no omitted guard/service is silently a no-op.
  const strict = new Proxy(modules, {get: (all, module: string) => all[module] ?? new Proxy({}, {
    get: (_, name: string) => function unexpectedBoundary() {throw new Error(`Unvisited fixture boundary: ${module}:${name}`);},
  })});
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), strict);
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {completed++;}});});
  await act(() => host.mount());
  return {host, background: () => appStateChange('background'), tree: () => tree, completed: () => completed, back, accounts, refs, writes, AuthSource,
    async enter(kind: TVCodeKind) {
      if (kind === 'portico') await act(() => {tree.props.onPortico();});
      else {
        await act(() => {tree.props.onLocal();});
        await act(() => {nodes(tree.props.beforeActions).find(n => n.type === 'Field').props.onChangeText('https://local.example');});
        await act(() => {tree.props.primaryAction.onPress();});
      }
      assert.equal(tree.type, 'Code');
    }};
}

test('Portico TV credential prepare publishes readiness only for its own prepared source', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), hold = deferred<Record<string, unknown>>(), available = {password: true, providers: []};
  const entry = await accountEntry(f, () => hold.promise);
  try {
    await entry.enter('portico'); t.mock.timers.tick(0); await settle();
    await act(() => {entry.tree().props.onTypeCredentials();}); await settle();
    const source = entry.accounts[0];
    assert.equal([...entry.refs].filter(ref => ref.current === source).length, 1, 'pending source has no readiness marker');
    assert.ok(!entry.writes.includes(available)); assert.equal(entry.tree().type, 'Code');
    hold.resolve(available); await settle();
    assert.equal(entry.tree().type, 'Credentials'); assert.equal(entry.tree().props.busy, false);
    assert.equal([...entry.refs].filter(ref => ref.current === source).length, 2, 'account and readiness name the same original source');
    assert.equal(entry.writes.filter(value => value === available).length, 1);
  } finally {await entry.host.unmount(); f.cleanup();}
});

for (const refusal of [false, true]) {
  test(`Portico TV delayed prepare ${refusal ? 'rejection' : 'success'} after Back cannot mark or alter prepared successor`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture(), oldHold = deferred<Record<string, unknown>>(); let calls = 0;
    const ownOptions = {password: true, providers: []}, staleOptions = {password: false, providers: []};
    const entry = await accountEntry(f, () => ++calls === 1 ? oldHold.promise : Promise.resolve(ownOptions));
    try {
      await entry.enter('portico'); t.mock.timers.tick(0); await settle();
      await act(() => {entry.tree().props.onTypeCredentials();}); await settle();
      const original = entry.accounts[0];
      await act(() => {assert.equal([...entry.back][0](), true);});
      await entry.enter('portico'); t.mock.timers.tick(0); await settle();
      await act(() => {entry.tree().props.onTypeCredentials();}); await settle();
      const successor = entry.accounts[1], shown = entry.tree().props;
      assert.equal(entry.tree().type, 'Credentials');
      assert.equal([...entry.refs].filter(ref => ref.current === successor).length, 2);
      if (refusal) oldHold.reject(new Error('late original prepare')); else oldHold.resolve(staleOptions);
      await settle();
      assert.equal(entry.tree().props, shown); assert.equal(successor.disposed, 0);
      assert.equal([...entry.refs].filter(ref => ref.current === successor).length, 2);
      assert.equal([...entry.refs].filter(ref => ref.current === original).length, 0);
      assert.ok(!entry.writes.includes(staleOptions));
    } finally {await entry.host.unmount(); f.cleanup();}
  });
}

test('Portico TV prepare requires source identity as well as original opening; replacement marker is preserved', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(); let hold = deferred<Record<string, unknown>>();
  const entry = await accountEntry(f, () => hold.promise);
  try {
    await entry.enter('portico'); t.mock.timers.tick(0); await settle();
    await act(() => {entry.tree().props.onTypeCredentials();}); await settle();
    const first = entry.accounts[0], account = [...entry.refs].find(ref => ref.current === first)!;
    hold.resolve({password: true, providers: []}); await settle();
    const marker = [...entry.refs].find(ref => ref !== account && ref.current === first)!;
    assert.ok(marker);
    await act(() => {entry.tree().props.onBack();}); t.mock.timers.tick(0); await settle();
    hold = deferred<Record<string, unknown>>();
    // Retain an earlier marker to prove replacement clears it before prepare.
    marker.current = first;
    await act(() => {entry.tree().props.onTypeCredentials();}); await settle();
    const pending = entry.accounts[1]; assert.equal(account.current, pending); assert.equal(marker.current, undefined);
    // Model independent account-source replacement while the TV opening stays
    // current: the old prepare may touch neither its successor nor its marker.
    const successor = new entry.AuthSource(); account.current = successor; marker.current = successor;
    const shown = entry.tree().props, staleOptions = {password: false, providers: []};
    hold.resolve(staleOptions); await settle();
    assert.equal(entry.tree().type, 'Code'); assert.deepEqual(entry.tree().props.status, shown.status);
    assert.equal(entry.tree().props.busy, false, 'the still-current TV opening settles its own transition');
    assert.equal(account.current, successor); assert.equal(marker.current, successor);
    assert.equal(successor.disposed, 0); assert.equal(pending.disposed, 0); assert.ok(!entry.writes.includes(staleOptions));
  } finally {await entry.host.unmount(); f.cleanup();}
});

test('Portico TV current prepare refusal leaves readiness unready and settlement with its opening', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), hold = deferred<Record<string, unknown>>(), entry = await accountEntry(f, () => hold.promise);
  try {
    await entry.enter('portico'); t.mock.timers.tick(0); await settle();
    await act(() => {entry.tree().props.onTypeCredentials();}); await settle();
    const source = entry.accounts[0];
    hold.reject(new Error('current prepare unavailable')); await settle();
    assert.equal(entry.tree().type, 'Code'); assert.equal(entry.tree().props.busy, false);
    assert.equal([...entry.refs].filter(ref => ref.current === source).length, 1, 'failed preparation has no readiness marker');
    assert.equal(source.disposed, 0); assert.ok(entry.tree().props.onNewCode, 'original helper presents explicit recovery');
  } finally {await entry.host.unmount(); f.cleanup();}
});

for (const kind of ['local', 'portico'] as const) {
  test(`${kind}: actual AccountEntry owns initial storage settlement after Back, not a stale generic run-finally`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture(), entry = await accountEntry(f);
    try {
      const hold = f.holdNext(); await entry.enter(kind); assert.equal(entry.tree().props.busy, true);
      await act(() => {assert.equal([...entry.back][0](), true);});
      await entry.enter(kind === 'local' ? 'portico' : 'local'); t.mock.timers.tick(0); await settle();
      const successor = entry.tree().props; assert.equal(successor.busy, false);
      hold.reject(new Error('late original initial storage')); await settle();
      assert.equal(entry.tree().props, successor); assert.equal(entry.completed(), 0);
    } finally {await entry.host.unmount(); f.cleanup();}
  });

  test(`${kind}: actual AccountEntry action/subscription stay with their rendered opening through Back and successor`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture(); const entry = await accountEntry(f);
    try {
      await entry.enter(kind); t.mock.timers.tick(0); await settle(); t.mock.timers.tick(5000); await settle();
      assert.equal(typeof entry.tree().props.onNewCode, 'function');
      const old = entry.tree().props, hold = f.holdNext(); old.onNewCode(); old.onNewCode(); await settle();
      assert.equal(f.services[0].cancels, 1);
      await act(() => {assert.equal([...entry.back][0](), true);});
      assert.equal(entry.tree().type, 'Front', 'hardware Back leaves the pending original synchronously');
      await entry.enter(kind === 'local' ? 'portico' : 'local'); t.mock.timers.tick(0); await settle();
      const successor = entry.tree().props;
      hold.reject(new Error('late original cancellation')); await settle();
      old.onNewCode(); old.onTypeCredentials(); old.onBack(); await settle();
      assert.equal(entry.tree().props, successor, 'retained handlers do not set busy/error, close or replace successor');
      f.services[0].service.getSnapshot = () => {throw new Error('old AccountEntry source read');};
      f.services[0].callbacks[0](); assert.equal(entry.completed(), 0);
      await entry.host.unmount(); const last = f.services.at(-1)!;
      last.service.getSnapshot = () => {throw new Error('unmounted AccountEntry source read');}; last.callbacks[0]();
    } finally {await entry.host.unmount(); f.cleanup();}
  });

  test(`${kind}: denied service stays stopped until rendered New code; one cancellation/replacement and fresh QR/poll`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture(); let p: Awaited<ReturnType<typeof presenter>> | undefined;
    try {
      await f.mount(kind).start(); t.mock.timers.tick(0); await settle();
      const firstCode = f.view().state.userCode;
      t.mock.timers.tick(5000); await settle(); assert.equal(f.view().state.phase, 'denied');
      t.mock.timers.tick(60_000); await settle(); assert.equal(f.calls.create, 1); assert.equal(f.calls.poll, 1, 'no automatic retry after denial');
      p = await presenter(f, kind);
      const order = nodes(p.tree()).find(n => n.type === 'Region').props.order;
      assert.deepEqual(order, kind === 'local' ? ['credentials', 'new-code'] : ['credentials', 'new-code', 'back']);
      assert.equal(nodes(p.tree()).find(n => n.type === 'Region').props.entry, 'new-code');
      assert.deepEqual((p.entries.at(-1) as any).address, {regionId: `tv.auth.${kind}.code`, childId: 'new-code'});
      assert.ok(!nodes(p.tree().props.aside).some(n => n.type === 'Text' && n.props.children === defaultI18n.t('activation.codeRefreshes')));
      const oldPress = p.button('new-code').props.onPress;
      oldPress(); oldPress(); await settle(); t.mock.timers.tick(0); await settle();
      assert.equal(f.services[0].cancels, 1, 'double select owns one cancellation'); assert.equal(f.services.length, 2); assert.equal(f.calls.create, 2);
      assert.equal(f.view().state.phase, 'waiting'); assert.notEqual(f.view().state.userCode, firstCode); await p.refresh();
      assert.equal(nodes(p.tree().props.aside).find(n => n.type === 'QR').props.value, f.view().state.verificationUriComplete);
      const before = {...f.calls}; oldPress(); await settle(); assert.deepEqual(f.calls, before, 'retained old action cannot target successor');
      f.services[0].service.getSnapshot = () => {throw new Error('retired source must not be read');};
      for (const oldCallback of f.services[0].callbacks) oldCallback(); assert.deepEqual(f.calls, before);
      f.poll('pending'); t.mock.timers.tick(5000); await settle(); assert.equal(f.calls.poll, 2, 'replacement really resumes polling');
    } finally {await p?.host.unmount(); f.cleanup();}
  });

  test(`${kind}: initial durable error offers explicit New code; failed cancellation does not auto-retry`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture(); let p: Awaited<ReturnType<typeof presenter>> | undefined;
    try {
      f.failNext(); await f.mount(kind).start(); assert.equal(f.view().state.phase, 'error');
      p = await presenter(f, kind); assert.ok(p.button('new-code')); t.mock.timers.tick(60_000); await settle(); assert.equal(f.calls.create, 0);
      f.failNext(); p.button('new-code').props.onPress(); await settle();
      assert.equal(f.services.length, 1); assert.equal(f.services[0].cancels, 1); assert.ok(f.error());
      t.mock.timers.tick(60_000); await settle(); assert.equal(f.services[0].cancels, 1); assert.equal(f.calls.create, 0);
      await p.refresh(); p.button('new-code').props.onPress(); await settle(); t.mock.timers.tick(0); await settle();
      assert.equal(f.services[0].cancels, 2, 'only a second explicit press retries failed durable cancellation');
      assert.equal(f.services.length, 2); assert.equal(f.calls.create, 1); assert.equal(f.view().state.phase, 'waiting');
    } finally {await p?.host.unmount(); f.cleanup();}
  });

  test(`${kind}: Back during cancellation retires synchronously; late storage refusal cannot alter other-mode successor`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture(); let p: Awaited<ReturnType<typeof presenter>> | undefined;
    try {
      await f.mount(kind).start(); t.mock.timers.tick(0); await settle(); t.mock.timers.tick(5000); await settle();
      p = await presenter(f, kind); const oldPress = p.button('new-code').props.onPress;
      const hold = f.holdNext(); oldPress(); await settle(); f.back();
      const other = kind === 'local' ? 'portico' : 'local'; await f.mount(other).start(); t.mock.timers.tick(0); await settle();
      const successor = f.view(), before = {...f.calls};
      hold.reject(new Error('late original storage error')); await settle(); oldPress(); await settle();
      assert.equal(f.view(), successor); assert.equal(f.error(), undefined); assert.equal(f.busy(), false); assert.equal(f.stage(), 'tv');
      assert.deepEqual(f.calls, before); assert.equal(f.services[0].cancels, 1, 'Back does not cancel an in-flight original twice');
      f.services[0].service.getSnapshot = () => {throw new Error('old callback source');}; f.services[0].callbacks[0]();
    } finally {await p?.host.unmount(); f.cleanup();}
  });

  test(`${kind}: source replacement and credentials/unmount refuse retained subscription before read/adopt`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture();
    try {
      const opening = f.mount(kind); await opening.start(); t.mock.timers.tick(0); await settle();
      const old = f.services[0]; f.changeSource(); old.service.getSnapshot = () => {throw new Error('source witness changed');};
      old.callbacks[0](); await opening.newCode(); assert.equal(old.cancels, 0); assert.equal(f.calls.accept, 0);
      const next = f.mount(kind); await next.start(); await next.credentials(); assert.equal(f.stage(), 'credentials');
      const current = f.services[1]; current.service.getSnapshot = () => {throw new Error('credential departure');}; current.callbacks[0]();
      await next.newCode(); assert.equal(current.cancels, 1); assert.equal(f.calls.credentials, 1);
      const last = f.mount(kind); await last.start(); const lastService = f.services.at(-1)!; f.unmount();
      lastService.service.getSnapshot = () => {throw new Error('unmounted source');}; lastService.callbacks[0]();
      await last.newCode(); assert.equal(lastService.cancels, 0, 'unmount pauses without abandoning durable operation'); assert.equal(f.calls.accept, 0);
    } finally {f.cleanup();}
  });

  test(`${kind}: unmount during accepted grant await refuses completion and further publication`, async t => {
    t.mock.timers.enable({apis: ['Date', 'setTimeout']});
    const f = fixture();
    try {
      f.poll('approved'); const hold = f.holdAccept(); await f.mount(kind).start(); t.mock.timers.tick(0); await settle();
      t.mock.timers.tick(5000); await settle(); assert.equal(f.calls.accept, 1);
      const shown = f.view(); f.unmount(); hold.resolve(); await settle();
      assert.equal(f.stage(), 'tv'); assert.equal(f.view(), shown); assert.equal(f.services[0].service.getSnapshot().phase, 'approved', 'retired UI does not complete a grant after its await');
    } finally {f.cleanup();}
  });
}


async function approvedAccounts() {
  const calls: string[] = [], tokens: string[] = [], activeWrites: HostedSession[] = []; let record: CredentialRecord<string> | undefined, verificationError: unknown;
  let verificationHold: ReturnType<typeof deferred<void>> | undefined;
  const credentials = new CredentialSessionService({authorityFromSession: session => session.familyId, refresh: async () => approvedGrant(), revoke: async family => {calls.push(`revoke:${family}`);}},
    {load: async () => record, save: async value => {if (value.active) activeWrites.push(structuredClone(value.active.session)); record = structuredClone(value);}}, async () => 'test-request');
  await credentials.synchronize();
  const modules: Record<string, unknown> = {
    'react-native': {NativeModules: {}},
    '../../../../packages/client-core/src': {HttpLocalApi: class {token: string; constructor(_origin: string, token: string) {this.token = token;} async request() {tokens.push(this.token); calls.push('verify'); if (verificationHold) await verificationHold.promise; if (verificationError) throw verificationError; return {account: {id: 'account'}};}}},
    '../../../../packages/client-core/src/sign-in': {ServerSignInProbe: class {cancel() {}}},
    './tv-approval-grant': tvApprovalGrant, './credentials': {createCredentials: () => credentials}, './account-authority': {hostedUrl: 'https://accounts.example'},
    '../../../../packages/contracts/src/hosted/routes': {routePath: () => '/v1/me'},
    '../../../../packages/contracts/src/hosted/decoders': {decodeMeGetResponse: (value: unknown) => value},
  };
  const {NativeAccounts} = await screenModule(new URL('../src-next/platform/accounts.ts', import.meta.url), new Proxy(modules, {get: (all, key: string) => all[key] ?? {}}));
  const native = new NativeAccounts(), actualAdopt = native.adoptPorticoTVSession.bind(native); let uncertainOnce: unknown;
  native.adoptPorticoTVSession = async (...args: any[]) => {calls.push('admit'); const value = await actualAdopt(...args); if (uncertainOnce) {const error = uncertainOnce; uncertainOnce = undefined; throw error;} return value;};
  return {native, credentials, calls, tokens, activeWrites, fail: (value: unknown) => {verificationError = value;},
    hold: () => verificationHold = deferred<void>(), uncertain: (error: unknown = new Error('Adoption outcome unavailable')) => {uncertainOnce = error;}};
}
async function approvalPresenter(entry: Awaited<ReturnType<typeof accountEntry>>) {
  const {TVCodePresenter} = await screenModule(new URL('../src-next/tv/auth/TVCodePresenter.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text'}, 'react-native-qrcode-svg': {default: 'QR'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/controls': {Button: 'Button'}, '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'},
    '../../kit/theme': {useTheme: () => createTheme({isTV: true, width: 1920}), nativeType}, '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus() {}},
    '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false}), BlurView: 'Blur'}, './entry-revision': {useTVBusyEntryRevision}, './TVAuthLayout': {TVAuthLayout: 'Layout'},
  });
  let tree: any; const host = createFabricHookHost(() => {const wrapped = TVCodePresenter(entry.tree().props); tree = wrapped.props.children.type(wrapped.props.children.props);});
  await act(() => host.mount());
  return {host, region: () => nodes(tree).find(node => node.type === 'Region'), refresh: () => act(() => host.rerender()), button: (id: string) => nodes(tree).find(node => node.type === 'Button' && node.props.focusId === id)};
}
async function approveAccount(t: any, f: Awaited<ReturnType<typeof fixture>>, entry: Awaited<ReturnType<typeof accountEntry>>) {
  await entry.enter('portico'); t.mock.timers.tick(0); await settle();
  f.poll('approved'); t.mock.timers.tick(5000); await settle();
}

test('FX213 actual AccountEntry/Presenter retries an original approval through actual NativeAccounts credential admission', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  let presenter: Awaited<ReturnType<typeof approvalPresenter>> | undefined;
  try {
    accounts.fail(new ApiError(503, 'unavailable', 'Temporary refusal', true)); await approveAccount(t, f, entry);
    assert.equal(entry.tree().type, 'Code'); assert.equal(entry.tree().props.onNewCode, undefined); assert.ok(entry.tree().props.onRetry);
    presenter = await approvalPresenter(entry); assert.equal(presenter.button('retry-approval').props.disabled, false);
    const retained = entry.tree().props;
    accounts.fail(undefined); await act(() => {presenter!.button('retry-approval').props.onPress(); presenter!.button('retry-approval').props.onPress();}); await settle();
    assert.equal(accounts.credentials.getSnapshot().session?.familyId, 'family'); assert.equal(f.calls.create, 1); assert.equal(f.calls.poll, 1);
    assert.deepEqual(accounts.calls, ['admit', 'verify', 'admit', 'verify']); assert.notEqual(entry.tree().type, 'Code');
    retained.onRetry(); retained.onTypeCredentials(); retained.onBack(); await settle(); assert.notEqual(entry.tree().type, 'Code');
  } finally {await presenter?.host.unmount(); await entry.host.unmount(); f.cleanup();}
});
for (const uncertain of [false, true]) test(`FX213 actual entry retries journal only after ${uncertain ? 'uncertain' : 'successful'} adoption`, async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    if (uncertain) accounts.uncertain(); else f.failCompletion(); await approveAccount(t, f, entry);
    assert.ok(entry.tree().props.onRetry); assert.equal(entry.tree().props.credentialsDisabled, true); assert.equal(entry.tree().props.onNewCode, undefined);
    const old = entry.tree().props; old.onTypeCredentials(); await settle(); assert.equal(entry.tree().type, 'Code'); assert.equal(entry.accounts.length, 0);
    await act(() => {old.onRetry();}); await settle();
    assert.deepEqual(accounts.calls, ['admit', 'verify']); assert.equal(f.calls.cancel, 0); assert.notEqual(entry.tree().type, 'Code');
  } finally {await entry.host.unmount(); f.cleanup();}
});
for (const during of ['adoption', 'journal']) test(`FX213 actual background during ${during} retires original UI without successor navigation or adopted-family cancellation`, async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    const hold = during === 'adoption' ? accounts.hold() : f.holdCompletion(); await approveAccount(t, f, entry);
    assert.equal(entry.tree().type, 'Code'); const retained = entry.tree().props;
    await act(() => entry.background()); assert.equal(entry.tree().type, 'Front'); const successor = entry.tree();
    hold.resolve(); await settle(); retained.onTypeCredentials(); retained.onBack(); await settle();
    assert.equal(entry.tree().type, 'Front'); assert.equal(entry.completed(), 0); assert.equal(f.calls.cancel, 0);
    if (during === 'journal') {assert.equal(accounts.credentials.getSnapshot().session?.familyId, 'family'); assert.ok(!accounts.calls.includes('revoke:family'));}
    else assert.equal(accounts.credentials.getSnapshot().session, undefined);
    assert.equal(f.calls.create, 1); assert.equal(successor.type, 'Front');
  } finally {await entry.host.unmount(); f.cleanup();}
});
test('FX213 complete=false retires actual entry without a directory transition or new consent', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    await entry.enter('portico'); f.services[0].service.complete = async () => false;
    t.mock.timers.tick(0); await settle(); f.poll('approved'); t.mock.timers.tick(5000); await settle();
    assert.equal(entry.tree().type, 'Front'); assert.equal(f.calls.create, 1); assert.equal(f.calls.cancel, 0);
    assert.equal(accounts.credentials.getSnapshot().session?.familyId, 'family');
  } finally {await entry.host.unmount(); f.cleanup();}
});
test('FX213 actual Retry button honours 429 deadline with no automatic approval request', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  let presenter: Awaited<ReturnType<typeof approvalPresenter>> | undefined;
  try {
    accounts.fail(new ApiError(429, 'rate_limited', 'Wait', true, 10)); await approveAccount(t, f, entry); presenter = await approvalPresenter(entry);
    assert.equal(presenter.button('retry-approval').props.disabled, true); const retry = entry.tree().props.onRetry;
    accounts.fail(undefined); retry(); await settle(); assert.deepEqual(accounts.calls, ['admit', 'verify']);
    t.mock.timers.tick(10000); await settle(); await presenter.refresh(); assert.equal(presenter.button('retry-approval').props.disabled, false);
    assert.deepEqual(accounts.calls, ['admit', 'verify']); await act(() => retry()); await settle(); assert.equal(accounts.calls.filter(value => value === 'admit').length, 2);
  } finally {await presenter?.host.unmount(); await entry.host.unmount(); f.cleanup();}
});

test('FX213 adopted journal failure refuses direct credentials/New-code and Back preserves the exact canonical family', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    f.failCompletion(); await approveAccount(t, f, entry);
    const opening = [...entry.refs].find(ref => ref.current && typeof ref.current === 'object' && 'retryApproval' in ref.current)!.current as TVCodeOpening;
    await opening.newCode(); await opening.credentials(); await settle();
    assert.equal(entry.tree().type, 'Code'); assert.equal(entry.accounts.length, 0); assert.equal(f.calls.cancel, 0); assert.equal(f.calls.create, 1);
    await act(() => entry.tree().props.onBack()); await settle();
    assert.equal(entry.tree().type, 'Front'); assert.equal(f.calls.cancel, 0); assert.equal(accounts.credentials.getSnapshot().session?.familyId, 'family');
    assert.ok(!accounts.calls.includes('revoke:family'));
  } finally {await entry.host.unmount(); f.cleanup();}
});

test('FX213 a foreign canonical family adopted and logged out spends retained Retry before render', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    accounts.fail(new ApiError(503, 'unavailable', 'Temporary refusal', true)); await approveAccount(t, f, entry);
    const retained = entry.tree().props, service = f.services[0].service, complete = service.complete.bind(service); let completions = 0;
    service.complete = async grant => {completions++; return complete(grant);};
    // Deliberately do not render between foreign adoption, logout and retained Retry.
    await accounts.credentials.authenticateIfSignedOut(async () => ({...approvedGrant(), familyId: 'foreign-family', refreshToken: 'foreign-refresh'}));
    await accounts.credentials.logout();
    const before = [...accounts.calls]; accounts.fail(undefined); retained.onRetry(); retained.onTypeCredentials(); retained.onBack();
    assert.deepEqual(accounts.calls, before, 'old UI makes no /me/admit/revoke call after foreign family retirement');
    assert.equal(completions, 0); await settle(); assert.equal(entry.tree().type, 'Front'); assert.equal(f.calls.cancel, 0);
  } finally {await entry.host.unmount(); f.cleanup();}
});

const grantMutations: Record<string, (grant: HostedSession) => void> = {
  account: grant => {grant.account.id = 'replacement-account';},
  username: grant => {grant.account.username = 'Replacement';},
  displayName: grant => {grant.account.displayName = 'Replacement';},
  family: grant => {grant.familyId = 'replacement-family';},
  access: grant => {grant.accessToken = 'replacement-access';},
  refresh: grant => {grant.refreshToken = 'replacement-refresh';},
  expires: grant => {grant.expiresAt = new Date(Date.now() + 7200000).toISOString();},
  refreshExpires: grant => {grant.refreshExpiresAt = new Date(Date.now() + 172800000).toISOString();},
};
for (const [field, mutate] of Object.entries(grantMutations)) test(`FX213 held actual /me cannot admit or complete a grant with mutated ${field}`, async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    const hold = accounts.hold(); await approveAccount(t, f, entry);
    const service = f.services[0].service, grant = service.getSnapshot().grant!, complete = service.complete.bind(service); let completions = 0;
    service.complete = async value => {completions++; return complete(value);};
    mutate(grant); hold.resolve(); await settle();
    assert.equal(accounts.credentials.getSnapshot().session, undefined); assert.equal(completions, 0); assert.equal(accounts.activeWrites.length, 0);
    assert.deepEqual(accounts.tokens, ['fixture-access']); assert.ok(!accounts.calls.includes('revoke:replacement-family'));
    assert.equal(f.calls.cancel, 0); assert.equal(entry.tree().type, 'Front');
  } finally {await entry.host.unmount(); f.cleanup();}
});
for (const [field, mutate] of Object.entries(grantMutations)) test(`actual NativeAccounts independently captures immutable ${field} before /me`, async () => {
  const accounts = await approvedAccounts(), original = approvedGrant(), hold = accounts.hold();
  const pending = accounts.native.adoptPorticoTVSession(original);
  await new Promise(resolve => setImmediate(resolve)); mutate(original); hold.resolve();
  await assert.rejects(pending); await new Promise(resolve => setImmediate(resolve));
  assert.equal(accounts.credentials.getSnapshot().session, undefined); assert.equal(accounts.activeWrites.length, 0); assert.deepEqual(accounts.tokens, ['fixture-access']);
  assert.ok(accounts.calls.includes('revoke:family')); assert.ok(!accounts.calls.includes('revoke:replacement-family'));
});
for (const code of ['invalid_grant', 'cancelled']) for (const committed of [true, false]) test(`FX213 ${code}-shaped uncertain readback resolves to ${committed ? 'journal-only retry' : 'confirmed terminal replacement'}`, async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    const terminal = new ApiError(401, code, 'Unknown finalization outcome', false);
    if (committed) accounts.uncertain(terminal); else accounts.fail(terminal);
    const sync = accounts.credentials.synchronize.bind(accounts.credentials); let reads = 0;
    accounts.credentials.synchronize = async () => {if (++reads === 3) throw new Error('Protected readback unavailable'); await sync();};
    await approveAccount(t, f, entry);
    assert.ok(entry.tree().props.onRetry, 'uncertainty retains explicit readback recovery');
    assert.equal(entry.tree().props.onNewCode, undefined); assert.equal(entry.tree().props.credentialsDisabled, true);
    accounts.fail(undefined); await act(() => entry.tree().props.onRetry()); await settle();
    assert.equal(accounts.calls.filter(value => value === 'admit').length, 1); assert.equal(accounts.calls.filter(value => value === 'verify').length, 1);
    if (committed) {assert.notEqual(entry.tree().type, 'Code'); assert.equal(accounts.credentials.getSnapshot().session?.familyId, 'family');}
    else {assert.equal(entry.tree().type, 'Code'); assert.ok(entry.tree().props.onNewCode); assert.equal(entry.tree().props.onRetry, undefined); assert.equal(entry.tree().props.credentialsDisabled, false);}
    assert.equal(f.calls.cancel, 0);
  } finally {await entry.host.unmount(); f.cleanup();}
});

test('FX213 a grant mutated between failed verification and Retry cannot re-enter admission', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    accounts.fail(new ApiError(503, 'unavailable', 'Temporary refusal', true)); await approveAccount(t, f, entry);
    const retry = entry.tree().props.onRetry, grant = f.services[0].service.getSnapshot().grant!;
    grant.refreshToken = 'replacement-refresh'; accounts.fail(undefined); await act(() => retry()); await settle();
    assert.deepEqual(accounts.calls, ['admit', 'verify']); assert.equal(accounts.activeWrites.length, 0); assert.equal(f.calls.cancel, 0); assert.equal(entry.tree().type, 'Front');
  } finally {await entry.host.unmount(); f.cleanup();}
});
test('FX213 a grant mutated after adoption cannot journal-complete on retained Retry', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  try {
    f.failCompletion(); await approveAccount(t, f, entry);
    const retry = entry.tree().props.onRetry, service = f.services[0].service; let completions = 0;
    service.complete = async () => {completions++; return true;}; service.getSnapshot().grant!.familyId = 'replacement-family';
    await act(() => retry()); await settle(); assert.equal(completions, 0); assert.equal(f.calls.cancel, 0);
    assert.equal(accounts.credentials.getSnapshot().session?.familyId, 'family'); assert.equal(entry.tree().type, 'Front');
  } finally {await entry.host.unmount(); f.cleanup();}
});

test('FX213 real Retry Button/Focusable/registry retains its entry host through a 429 deadline and INERT wins', async t => {
  t.mock.timers.enable({apis: ['Date', 'setTimeout']});
  const f = fixture(), accounts = await approvedAccounts(), entry = await accountEntry(f, undefined, accounts.native);
  let presenter: Awaited<ReturnType<typeof approvalPresenter>> | undefined, driver: Awaited<ReturnType<typeof createFabricHookHost>> | undefined;
  try {
    accounts.fail(new ApiError(429, 'rate_limited', 'Wait', true, 10)); await approveAccount(t, f, entry); presenter = await approvalPresenter(entry);
    const contexts: {current: any}[] = [], registry = focusRegistry.createFocusRegistry(), theme = createTheme({isTV: true, width: 1920});
    const controlled: any = {...React, createContext: (current: any) => {const value = {current}; contexts.push(value); return value;}, useContext: (context: {current: any}) => context.current};
    controlled.default = controlled;
    const flatten = (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(flatten)) : value ?? {};
    const native = {Platform: {OS: 'android', isTV: true}, View: 'View', Text: 'Text', Pressable: 'Pressable', ActivityIndicator: 'Spinner',
      TVFocusGuideView: 'Guide', findNodeHandle: (host: any) => host.nativeTag, StyleSheet: {create: (value: any) => value, flatten, absoluteFill: {position: 'absolute'}}};
    const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
      react: controlled, 'react-native': native, '../theme': {useTheme: () => theme}, './registry': focusRegistry, './useHostRef': hostRef,
    });
    const paint = await screenModule(new URL('../src-next/kit/overlays/OverlayPaintContext.tsx', import.meta.url), {react: controlled});
    const actualRegion = presenter.region().props;
    contexts[0]!.current = registry; contexts[1]!.current = actualRegion.id;
    paint.OverlayPaintModeContext.current = paint.OVERLAY_PAINT_LIVE;
    registry.configure({id: actualRegion.id, entry: actualRegion.entry, order: actualRegion.order, enabled: actualRegion.enabled});
    const observation = {useNativeObservation: (_: unknown, ref?: unknown) => ({ref, textProps: {}, recordFocus() {}, recordDeclaredStyle() {}})};
    const {Focusable} = await screenModule(new URL('../src-next/kit/controls/Focusable.tsx', import.meta.url), {
      react: controlled, 'react-native': native, '@portico/design': {focusSpec}, '../theme': {useTheme: () => theme}, '../focus': focus,
      '../useMotion': {useMotion: () => ({transforms: false, reduce: true, timing: () => ({})})}, './control-model': controlModel,
      './useFocusedMenu': {useFocusedMenu() {}}, '../overlays/OverlayPaintContext': paint, '../dev-observation': observation,
      'react-native-reanimated': {default: {View: 'AnimatedView', createAnimatedComponent: (component: any) => component}, useAnimatedStyle: (fn: () => any) => fn(), useSharedValue: (value: number) => React.useRef({value}).current, withTiming: (value: number) => value},
    });
    const {Button} = await screenModule(new URL('../src-next/kit/controls/Button.tsx', import.meta.url), {
      react: controlled, 'react-native': native, '@portico/design': {buttonSpec}, './Focusable': {Focusable}, './control-model': controlModel,
      '../../platform/native-blur': {glassRecipe: () => ({nativeBlur: false}), BlurView: 'Blur'}, '../theme': {useTheme: () => theme}, '../Icon': {Icon: 'Icon'}, '../dev-observation': observation,
    });
    let pressable: any, focused = 0;
    driver = createFabricHookHost(() => {
      const button = Button.render(presenter!.button('retry-approval').props, null);
      const content = button.type.render(button.props, null), bound = Focusable.render(content.props, null);
      pressable = bound.type.render(bound.props, bound.props.ref);
    });
    await act(() => driver!.mount());
    const host = {nativeTag: 91, requestTVFocus() {focused++;}}, ref = pressable.props.ref;
    ref(host); await act(async () => {await driver!.flush();});
    assert.equal(actualRegion.entry, 'retry-approval'); assert.equal(registry.child({regionId: actualRegion.id, childId: actualRegion.entry}), host);
    assert.equal(registry.entryTarget(actualRegion.id), host); assert.equal(registry.focusRegion(actualRegion.id), true); assert.equal(focused, 1);
    assert.equal(pressable.props.disabled, false, 'disabled command remains a native read-only focus stop');
    assert.equal(pressable.props.accessibilityState.disabled, true); assert.equal(pressable.props.onPress, undefined);
    assert.deepEqual(accounts.calls, ['admit', 'verify']); accounts.fail(undefined);
    t.mock.timers.tick(10000); await settle(); await presenter.refresh(); await act(() => driver!.rerender());
    assert.equal(pressable.props.ref, ref); assert.equal(registry.entryTarget(actualRegion.id), host);
    assert.equal(Boolean(pressable.props.accessibilityState.disabled), false); assert.equal(typeof pressable.props.onPress, 'function'); assert.deepEqual(accounts.calls, ['admit', 'verify'], 'deadline never dispatches a request');
    paint.OverlayPaintModeContext.current = paint.OVERLAY_PAINT_INERT; await act(() => driver!.rerender());
    assert.equal(pressable.props.disabled, true); assert.equal(pressable.props.onPress, undefined); assert.equal(registry.child({regionId: actualRegion.id, childId: actualRegion.entry}), undefined);
    paint.OverlayPaintModeContext.current = paint.OVERLAY_PAINT_LIVE; await act(() => driver!.rerender());
    assert.equal(registry.entryTarget(actualRegion.id), host); assert.equal(pressable.props.ref, ref);
    await act(() => pressable.props.onPress()); await settle(); assert.equal(accounts.calls.filter(value => value === 'admit').length, 2);
  } finally {await driver?.unmount(); await presenter?.host.unmount(); await entry.host.unmount(); f.cleanup();}
});
