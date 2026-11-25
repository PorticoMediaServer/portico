// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {setInstallationIdentity} from '../../../packages/client-core/src/installation.ts';
import {beginRestoreTrace, markRestore, restoreTrace} from '../src-next/shell/restore-trace.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

test('every restore step before Home is traced during restoreNativeShellRuntime', async () => {
  const app = await standardHomeApp({mountUI: false});
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    runtime = await restoreNativeShellRuntime(context => createNativeShellBindings(context), {onAuthorityEnded() {}});
    assert.ok(runtime);
    const steps = restoreTrace().marks.map(([step]) => step);
    for (const step of ['installation', 'connection', 'outbox', 'first-reads', 'settings', 'bindings', 'publish']) {
      assert.ok(steps.includes(step), `restore trace recorded step "${step}" (saw: ${steps.join(', ')})`);
    }
  } finally {
    try {runtime?.dispose();} finally {try {await app.unmount();} finally {app.disposeLoader();}}
  }
});

test('E-mobile-01: RestoredShell complete() restarts a live or stale-claim root, logs handoff, and logs restore timeouts', async () => {
  const warnings: string[] = [];
  const originalWarn = console.warn;
  console.warn = (...args: unknown[]) => {warnings.push(args.map(String).join(' '));};

  let installationGenerations = 0;
  let staleClaim = false;
  let recoveryOptions: any;
  const noopStore = {acquire: () => () => {}, subscribe: () => () => {}, getSnapshot: () => undefined};

  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {
      AccessibilityInfo: {setAccessibilityFocus() {}},
      AppState: {currentState: 'active', addEventListener: () => ({remove() {}})},
      Image: 'Image',
      Platform: {OS: 'android', isTV: false},
      Text: 'Text',
      View: 'View',
      findNodeHandle: () => null,
    },
    '../kit/states': {DownloadsStillPlay: 'DownloadsStillPlay', StateLayout: 'StateLayout'},
    '../kit/overlays/TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'},
    '../kit/theme': {useTheme: () => ({isTV: false, platform: 'phone', colors: {textSecondary: '#aaa'}, type: {bodyStrong: {}}})},
    '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({category: 'other', title: 'Err', body: 'Body'})},
    '../platform/server-connections': {deviceOnline: () => true, onNetworkChange: () => () => {}},
    './assembly': {createNativeShellBindings: () => ({})},
    './bootstrap': {restoreNativeShellRuntime: async () => undefined},
    './restore-trace': {restoreTrace},
    './boot-timing': {logBootMilestone() {}},
    '../platform/negotiation': {negotiationRefusal: () => undefined},
    '../kit/accounts/ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'},
    './NativeShell': {NativeShell: 'NativeShell'},
    '../platform/accounts': {nativeAccounts: {onSettled: () => () => {}, onFence: () => () => {}, snapshot: async () => ({servers: []})}},
    '../platform/app-session-storage': {readNativeSelectedViewer: async () => null, subscribeNativeAppSessions: () => () => {}},
    '../pages/shared/accounts/AccountEntryPage': {AccountEntryPage: 'AccountEntryPage'},
    '../pages/shared/accounts/ServersRoutePage': {ServersRoutePage: 'ServersRoutePage'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (k: string) => k}},
    '../platform/sign-in-ended': {NativeSignInEnded: class extends Error {}},
    '../platform/identity-failure': {NativeIdentityFailure: class extends Error {}},
    './first-library': {PhoneFirstLibrary: 'PhoneFirstLibrary'},
    '../pages/shared/accounts/ServerIdentityFailurePage': {ServerIdentityFailurePage: 'ServerIdentityFailurePage'},
    '../platform/invitation-links': {nativeInvitationLinks: noopStore},
    '../platform/share-links': {nativeShareLinks: noopStore},
    './NativeShareLinks': {NativeShareLinks: 'NativeShareLinks'},
    './NativeContentLinks': {NativeContentLinks: 'NativeContentLinks'},
    '../platform/content-links': {nativeContentLinks: noopStore},
    '../platform/installation': {
      beginNativeInstallation: () => {
        const gen = ++installationGenerations;
        let disposed = false;
        return {generation: gen, current: () => !disposed && !staleClaim && gen === installationGenerations, dispose: () => {disposed = true;}};
      },
    },
    './remembered-downloads-opening': {createRememberedDownloadsOpening: async () => undefined},
    '../pages/touch/downloads/RememberedDownloadsPage': {RememberedDownloadsPage: 'RememberedDownloadsPage', RememberedDownloadsShortcut: 'RememberedDownloadsShortcut'},
    '../screens/downloads/presentation': {downloadEntryIdentity: () => ''},
    './settings-appearance': {NativeSettingsAppearance: 'NativeSettingsAppearance'},
    '../platform/home-action-diagnostics': {homeActionDiagnostics: {lifetime() {}, error() {}}},
    '../platform/secure-records': {acknowledgeSecureRecordsRecovered() {}, isSecureRecordsLost: () => false, secureRecordsRecoveredNotice: () => undefined},
    './SecureRecordsRecovery': {SecureRecordsRecovery: 'SecureRecordsRecovery'},
    '../platform/boot-host': {takeLaunchStartedAt: () => 0},
    './StartupBoot': {StartupBoot: 'StartupBoot'},
    '../platform/native-session-lifecycle': {
      choosingProfile: () => false,
      lifecycleSettled: async () => ({kind: 'signedOut'}),
      nativeSessionLifecycle: () => ({
        lifecycle: {start() {}, subscribe: () => () => {}, getSnapshot: () => ({state: {kind: 'signedOut'}})},
        ports: {lendInstallation() {}, adopt: () => undefined, failure: () => undefined},
      }),
    },
    '../platform/session-lifecycle-native': {NativeProfileGated: class extends Error {}, NativeProfileMoved: class extends Error {}, nativeProfileGated: () => false},
    '../pages/shared/accounts/ProfileGatePage': {ProfileGatePage: 'ProfileGatePage'},
    '../pages/touch/accounts/ManageUnlockPage': {ManageUnlockPage: 'ManageUnlockPage'},
    './startup-recovery': {
      createNativeStartupRecovery: (opts: any) => {
        recoveryOptions = opts;
        queueMicrotask(() => opts.onReady(undefined));
        return {dispose() {}, pause() {}, resume() {}, finish() {}, retryNow: () => true, networkChanged() {}, foregroundChanged() {}};
      },
      startupRetrySeconds: () => 1,
      startupWait: () => ({stage: 'ready', offline: false, pending: false}),
    },
    './after-entry': {afterEntry: {take: () => undefined}},
  });

  let tree: any;
  const host = createFabricHookHost(() => {tree = RestoredShell();});
  try {
    await act(() => host.mount());
    await act(() => new Promise(r => setImmediate(r)));
    assert.equal(installationGenerations, 1);

    // Timeout logs portico_restore_timeout with restoreTrace payload.
    beginRestoreTrace();
    markRestore('connection');
    recoveryOptions.onAttemptTimeout();
    assert.ok(warnings.some(line => line.startsWith('portico_restore_timeout ')), 'logs portico_restore_timeout');

    // A finished pick on a live root logs portico_home_handoff and restarts the root (next generation).
    const entry = () => nodes(tree).find(n => n.type === 'AccountEntryPage');
    assert.ok(entry(), 'AccountEntryPage rendered');
    await act(() => entry().props.onComplete());
    assert.ok(warnings.some(line => line.startsWith('portico_home_handoff ')), 'logs portico_home_handoff');
    assert.equal(installationGenerations, 2, 'live root restarted to a fresh installation generation');

    // Even when the live root's installation claim went stale, complete() retires it and increments attempt.
    await act(() => new Promise(r => setImmediate(r)));
    const staleComplete = entry().props.onComplete;
    staleClaim = true;
    await act(() => staleComplete());
    staleClaim = false;
    assert.equal(installationGenerations, 3, 'stale-claim root is retired and replaced, never left waiting');
  } finally {
    console.warn = originalWarn;
    await host.unmount();
  }
});

test('queued sign-outs wait drainDelayMs after reconnect instead of blocking Home', async () => {
  const {createNativeLifecyclePorts} = await import('../src-next/platform/session-lifecycle-native.ts');
  let clock = 0;
  const scheduled: Array<{ms: number; run: () => void}> = [];
  const drained: string[] = [];
  const session: any = {
    accessToken: 'a',
    installationId: 'inst_1',
    expiresAt: '2099-01-01T00:00:00Z',
    sessionFamilyId: 'fam_1',
    tokenGeneration: '1',
    authorizationHorizon: '2099-01-02T00:00:00Z',
    viewer: {serverId: 'srv_1', accountId: 'acc_1', profileId: 'prof_1', authority: 'local', role: 'member'},
  };
  const routeConn = {dispose() {}, getSnapshot: () => ({phase: 'ready'})};
  const fakeApi: any = {
    baseUrl: 'https://server.invalid',
    getRouteConnection: () => routeConn,
    setAccessToken() {},
    request: async (path: string) => {
      if (path === '/v1/me') return {account: {id: 'acc_1', username: 'u', role: 'member'}, profile: {id: 'prof_1', name: 'P', isPrimary: true, pinRequired: false, color: 'blue'}, role: 'member', canManageAccount: true};
      if (path === '/v1/profiles') return {items: [{id: 'prof_1', name: 'P', isPrimary: true, pinRequired: false, color: 'blue'}]};
      return {};
    },
  };
  const selectedEntry: any = {
    signIn: 'local',
    connection: {version: '1', logicalOrigin: 'https://server.invalid', session, pin: {serverId: 'srv_1', publicKey: 'pk', fingerprint: 'fp'}, routes: {paired: ['https://server.invalid']}},
  };
  const ports = createNativeLifecyclePorts({
    fenceShell() {},
    drainDelayMs: 10_000,
    scheduler: {
      now: () => clock,
      setTimeout: (fn: () => void, ms: number) => {const item = {ms, run: fn}; scheduled.push(item); return item as any;},
      clearTimeout: () => {},
    },
    sessions: {
      read: async () => ({version: 1, revision: 1, selectedServerId: 'srv_1', servers: [selectedEntry]}),
      selected: async () => selectedEntry,
      setRemembered: async () => {},
      setProfilePinRequired: async () => {},
    } as any,
    readServerDisplays: async () => [{serverId: 'srv_1', name: 'Home', fingerprint: 'fp'}] as any,
    readSignInHint: async () => undefined,
    beginInstallation: () => ({
      generation: 1,
      current: () => true,
      dispose() {},
      prepare: async () => ({
        saved: {recovered: false, installation: 'inst_1', viewer: {serverUrl: 'https://server.invalid', session}},
        connection: selectedEntry.connection,
        env: {} as any,
        identity: {installationId: 'inst_1', name: 'Test', appVersion: '1', platform: 'android', app: 'Portico'},
      }),
    }) as any,
    restoreConnection: async () => ({api: fakeApi, session}),
    readSelectedViewer: async () => ({serverUrl: 'https://server.invalid', session}) as any,
    purgeStaleRoutes: (r: any) => r,
    drainRevocations: async (serverId: string) => {drained.push(serverId); return 1;},
  });

  const conn = await ports.reconnect('srv_1', new AbortController().signal);
  assert.ok(conn);
  assert.deepEqual(drained, [], 'revocations do not drain synchronously during reconnect');
  assert.equal(scheduled.length, 1);
  assert.equal(scheduled[0].ms, 10_000, 'revocation drain waits drainDelayMs');
  clock += scheduled[0].ms;
  scheduled[0].run();
  await new Promise(r => setImmediate(r));
  assert.deepEqual(drained, ['srv_1'], 'revocations drain once the delay elapses');
});

test('createNativeInstallationHost warns once when superseded and stays silent when disposed or aborted', async () => {
  const {createNativeInstallationHost} = await import('../src-next/platform/installation-host.ts');
  const warnings: string[] = [];
  const originalWarn = console.warn;
  console.warn = (...args: unknown[]) => {warnings.push(args.map(String).join(' '));};
  setInstallationIdentity(undefined);
  try {
    const begin = createNativeInstallationHost({platform: () => 'android', description: () => ({name: 'Phone', appVersion: '1'})});
    const first = begin();
    const second = begin(); // supersedes `first`
    assert.equal(first.current(), false);
    assert.equal(first.current(), false);
    assert.equal(warnings.filter(w => w.startsWith('portico_installation_invalid')).length, 1, 'warns exactly once on superseding claim');

    // Disposed claim stays silent.
    warnings.length = 0;
    second.dispose();
    assert.equal(second.current(), false);
    assert.equal(warnings.length, 0, 'disposed installation does not warn');

    // Aborted claim stays silent.
    const abort = new AbortController();
    const third = begin(abort.signal);
    abort.abort();
    assert.equal(third.current(), false);
    assert.equal(warnings.length, 0, 'aborted installation does not warn');
  } finally {
    console.warn = originalWarn;
  }
});

test('the trace names the step a restore is waiting on, with offsets only', () => {
  beginRestoreTrace(); markRestore('installation'); markRestore('connection');
  const trace = restoreTrace();
  assert.equal(trace.waiting, 'connection');
  assert.deepEqual(trace.marks.map(([step]) => step), ['installation', 'connection']);
  assert.deepEqual(Object.keys(trace).sort(), ['elapsedMs', 'marks', 'waiting']);
});
