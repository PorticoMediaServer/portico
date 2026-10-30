// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {downloadEntryIdentity} from '../src-next/screens/downloads/presentation';
import {createTheme} from '../src-next/kit/theme';
import * as serverConnections from '../src-next/platform/server-connections';
import * as secureRecords from '../src-next/platform/secure-records';
import * as startupRecovery from '../src-next/shell/startup-recovery';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

type Deferred = {promise: Promise<undefined>; resolve: () => void};

function modules(state: {
  reads: () => unknown; hints: (listener: () => void) => () => void; restores: Deferred[]; deferred: boolean;
  failure?: unknown; onFallback?: () => void;
}) {
  return {
    './restore-trace': {restoreTrace: () => ({waiting: 'start', elapsedMs: 0, marks: []})}, './boot-timing': {logBootMilestone: () => {}}, '../platform/negotiation': {negotiationRefusal: () => undefined}, '../screens/accounts/ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'},
    react: {...React, default: React},
    '../platform/share-links': {nativeShareLinks: {acquire: () => () => {}, subscribe: () => () => {}, getSnapshot: () => undefined, take: () => false}},
    '../screens/share/NativeShareLinks': {NativeShareLinks: () => null},
    'react-native': {Platform: {OS: 'ios'}, View: 'View', Text: 'Text', AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}},
    '../platform/invitation-links': {nativeInvitationLinks: {getSnapshot: () => undefined, subscribe: () => () => {}, acquire: () => () => {}}},
    '../platform/installation': {beginNativeInstallation: (signal?: AbortSignal) => {
      let disposed = false;
      return {current: () => !disposed && !signal?.aborted, dispose: () => {disposed = true;}};
    }},
    './remembered-downloads-opening': {createRememberedDownloadsOpening: async () => {state.onFallback?.(); return undefined;}},
    '../screens/downloads/RememberedDownloadsScreen': {RememberedDownloadsScreen: 'RememberedDownloadsScreen', RememberedDownloadsShortcut: 'RememberedDownloadsShortcut'},
    '../screens/downloads/presentation': {downloadEntryIdentity},
    '../kit/states': {HomeSkeleton: 'HomeSkeleton', StateView: 'StateView', StateLayout: 'StateLayout'},
    '../kit/artwork': {StandardBackdrop: 'StandardBackdrop', CoverFan: 'CoverFan'},
    '../kit/controls': {Focusable: 'Focusable'},
    '../kit/overlays/TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'},
    '../kit/Icon': {Icon: 'Icon'},
    '../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../platform/server-connections': serverConnections,
    '../platform/home-action-diagnostics': {homeActionDiagnostics: {error() {}, lifetime() {}}},
    '../platform/secure-records': secureRecords,
    './SecureRecordsRecovery': {SecureRecordsRecovery: 'SecureRecordsRecovery'}, '../platform/boot-host': {takeLaunchStartedAt: (now = Date.now()) => now},
    './StartupBoot': {StartupBoot: 'StartupBoot'},
    './startup-recovery': startupRecovery,
    '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({title: 'failed', body: 'failed'})},
    './assembly': {createNativeShellBindings: () => ({})},
    './bootstrap': {restoreNativeShellRuntime: () => {
      if (state.failure !== undefined) return Promise.reject(state.failure);
      let resolve: () => void = () => {};
      const promise = new Promise<undefined>(done => {resolve = () => done(undefined);});
      state.restores.push({promise, resolve});
      return state.deferred ? promise : Promise.resolve(undefined);
    }},
    './NativeShell': {NativeShell: 'NativeShell'},
    '../platform/sign-in-ended': {NativeSignInEnded: class extends Error {}},
    '../platform/accounts': {nativeAccounts: {snapshot: async () => ({servers: []}), onSettled: () => () => {}, onFence: () => () => {}}},
    '../platform/app-session-storage': {readNativeSelectedViewer: state.reads, subscribeNativeAppSessions: state.hints},
    '../screens/accounts/PhoneFirstLibrary': {PhoneFirstLibrary: 'PhoneFirstLibrary'},
    '../screens/accounts/AccountEntry': {AccountEntry: 'AccountEntry'},
    '../screens/accounts/AccountRoutes': {ServersRoute: 'ServersRoute'},
    '../../../../packages/i18n/src': {defaultI18n},
    '../platform/identity-failure': {NativeIdentityFailure: class extends Error {}},
    '../screens/accounts/ServerIdentityFailure': {ServerIdentityFailure: 'ServerIdentityFailure'},
    '../screens/settings/NativeSettingsAppearance': {NativeSettingsAppearance: 'NativeSettingsAppearance'},
  };
}

const viewer = {serverUrl: 'https://server.test', session: {sessionFamilyId: 'family', viewer: {serverId: 'server', accountId: 'account', profileId: 'profile'}}};

test('a storage hint before the entry completes does not unmount a settled entry; its onComplete restores once', async () => {
  let stored: unknown = undefined, hint: (() => void) | undefined;
  const restores: Deferred[] = [];
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), modules({
    reads: async () => stored, hints: listener => {hint = listener; return () => {};}, restores, deferred: false,
  }));
  let tree: any;
  const host = createFabricHookHost(() => {tree = RestoredShell({}).props.children;});
  await act(() => host.mount());
  try {
    assert.equal(tree.type, 'AccountEntry');
    // rememberNativeSession writes the selected record before NativeAccounts.remember returns.
    stored = viewer;
    await act(() => hint!());
    assert.equal(tree.type, 'AccountEntry', 'the same entry stays mounted');
    const restoresBefore = restores.length;
    await act(() => tree.props.onComplete());
    assert.equal(restores.length, restoresBefore + 1, 'the entry completion starts the one restore');
  } finally {await host.unmount();}
});

test('a storage hint while a restore is pending still fences and restarts it', async () => {
  let stored: unknown = undefined, hint: (() => void) | undefined;
  const restores: Deferred[] = [];
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), modules({
    reads: async () => stored, hints: listener => {hint = listener; return () => {};}, restores, deferred: true,
  }));
  let tree: any;
  const host = createFabricHookHost(() => {tree = RestoredShell({}).props.children;});
  await act(() => host.mount());
  try {
    assert.equal(restores.length, 1, 'the first restore is pending');
    stored = viewer;
    await act(() => hint!());
    assert.equal(restores.length, 2, 'a pending restore is fenced and restarted');
    await act(() => restores.forEach(item => item.resolve()));
    assert.equal(tree.type, 'AccountEntry');
  } finally {await host.unmount();}
});

/** The key service's own refusal: the records are there and their key is not. */
const lostRecordsKey = () => Object.assign(new Error('the saved records key is gone'), {code: secureRecords.SECURE_RECORDS_KEY_LOST});

test('a restore that reports the saved-records key lost opens the real recovery, not the downloads fallback', async () => {
  const restores: Deferred[] = [];
  let fallbacks = 0;
  const failure = lostRecordsKey();
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), modules({
    reads: async () => undefined, hints: () => () => {}, restores, deferred: false,
    failure, onFallback: () => {fallbacks++;},
  }));
  let tree: any;
  const host = createFabricHookHost(() => {tree = RestoredShell({}).props.children;});
  await act(() => host.mount());
  try {
    assert.equal(tree.type, 'SecureRecordsRecovery', 'the real classifier reads the key-loss code');
    assert.equal(fallbacks, 0, 'a lost key does not start the ordinary downloads fallback');
  } finally {await host.unmount();}
});

test('an unexpected restore failure is never misread as a lost saved-records key', async () => {
  const restores: Deferred[] = [];
  let fallbacks = 0;
  // Same shape, different code: only the real key-loss code may claim the records are lost.
  const failure = Object.assign(new Error('the shell could not be assembled'), {code: 'assembly_failed'});
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), modules({
    reads: async () => undefined, hints: () => () => {}, restores, deferred: false,
    failure, onFallback: () => {fallbacks++;},
  }));
  let tree: any;
  const host = createFabricHookHost(() => {tree = RestoredShell({}).props.children;});
  await act(() => host.mount());
  try {
    assert.notEqual(tree.type, 'SecureRecordsRecovery', 'an ordinary failure never opens the records recovery');
    assert.equal(tree.props.children.props.children.type, 'StateLayout', 'the ordinary restore recovery is drawn instead');
    assert.equal(fallbacks, 1, 'the ordinary failure keeps its downloads fallback');
  } finally {await host.unmount();}
});
