// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import * as startupRecovery from '../src-next/shell/startup-recovery.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {restoredShellLifecycleStub} from './helpers/lifecycle-stub.mjs';

type RestoreCall = {signal: AbortSignal; installationOwner: object; onAuthorityEnded: (error?: unknown) => void; onSessionRenewed: () => void};
const failure = Object.assign(new Error('test connection'), {code: 'network'});
class SignInEnded extends Error {hint = {serverId: 'server', familyId: 'family'};}
class IdentityFailure extends Error {}
const initialViewer = {session: {sessionFamilyId: 'family', viewer: {serverId: 'server', accountId: 'account', profileId: 'profile'}}};

async function shell(read: (call: RestoreCall, index: number) => Promise<unknown>, overrides: Record<string, unknown> = {}) {
  const paths = new Set<(value: boolean | undefined) => void>(), foreground = new Set<(value: string) => void>(), selections = new Set<() => void>(), links = new Set<() => void>();
  const calls: RestoreCall[] = [], errors: unknown[] = [], focus: number[] = [];
  let online: boolean | undefined = true, viewer = initialViewer, fallbacks = 0;
  let incoming: {id: string; current: () => boolean; dismiss: () => void} | undefined;
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), {...restoredShellLifecycleStub(), '../kit/accounts/ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../platform/negotiation': {negotiationRefusal: () => undefined}, './boot-timing': {logBootMilestone: () => {}}, './restore-trace': {restoreTrace: () => ({waiting: 'start', elapsedMs: 0, marks: []})}, './NativeShareLinks': {NativeShareLinks: () => null}, './NativeContentLinks': {NativeContentLinks: () => null}, '../platform/content-links': {nativeContentLinks: {acquire: () => () => {}, subscribe: () => () => {}, getSnapshot: () => undefined, take: () => false}}, '../platform/share-links': {nativeShareLinks: {acquire: () => () => {}, subscribe: () => () => {}, getSnapshot: () => undefined, take: () => false}}, 
    react: {...React, default: React},
    'react-native': {Platform: {OS: 'ios'}, View: 'View', Text: 'Text', Image: 'Image', findNodeHandle: (node: {tag: number}) => node.tag,
      AccessibilityInfo: {setAccessibilityFocus: (tag: number) => {focus.push(tag);}},
      AppState: {currentState: 'active', addEventListener: (_: string, listener: (value: string) => void) => {foreground.add(listener); return {remove: () => foreground.delete(listener)};}}},
    '../kit/states': {DownloadsStillPlay: 'DownloadsStillPlay', StateView: 'StateView', StateLayout: 'StateLayout'}, '../kit/artwork': {StandardBackdrop: 'StandardBackdrop', CoverFan: 'CoverFan'},
    '../kit/controls': {Focusable: 'Focusable'}, '../kit/overlays/TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'}, '../kit/Icon': {Icon: 'Icon'},
    '../kit/theme': {useTheme: () => ({platform: 'phone', isTV: false, colors: {ink: '#071018'}, type: {}})},
    '../../../../packages/client-core/src/presentation/errors': {presentError: (err?: {code?: string}) => err?.code === 'records_unreadable'
      ? {category: 'unknown', action: 'try-again', actionLabel: 'Try again', title: 'Unreadable', body: 'Unreadable'}
      : {category: 'unreachable', action: 'try-again', title: 'Unavailable', body: 'Unavailable'}},
    '../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../platform/server-connections': {deviceOnline: () => online, onNetworkChange: (listener: (value: boolean | undefined) => void) => {paths.add(listener); return () => {paths.delete(listener);};}},
    '../platform/invitation-links': {nativeInvitationLinks: {getSnapshot: () => incoming,
      subscribe: (listener: () => void) => {links.add(listener); return () => {links.delete(listener);};}, acquire: () => () => {}}},
    '../platform/installation': {beginNativeInstallation: (signal: AbortSignal) => {
      let disposed = false; return {current: () => !disposed && !signal.aborted, dispose: () => {disposed = true;}};
    }},
    './assembly': {createNativeShellBindings: () => ({})}, './bootstrap': {restoreNativeShellRuntime: (_: unknown, call: RestoreCall) => {calls.push(call); return read(call, calls.length);}},
    './remembered-downloads-opening': {createRememberedDownloadsOpening: async () => {fallbacks++; return undefined;}},
    './NativeShell': {NativeShell: 'NativeShell'}, './StartupBoot': {StartupBoot: 'StartupBoot'}, './after-entry': {afterEntry: {request() {}, clear() {}, take() {}}}, './startup-recovery': startupRecovery,
    './SecureRecordsRecovery': {SecureRecordsRecovery: 'SecureRecordsRecovery'}, '../platform/boot-host': {takeLaunchStartedAt: (now = Date.now()) => now},
    '../platform/accounts': {nativeAccounts: {snapshot: async () => ({servers: []}), onSettled: () => () => {}, onFence: () => () => {}, forgetServer: async () => {}}},
    '../platform/app-session-storage': {readNativeSelectedViewer: async () => viewer, subscribeNativeAppSessions: (listener: () => void) => {selections.add(listener); return () => {selections.delete(listener);};}},
    '../platform/sign-in-ended': {NativeSignInEnded: SignInEnded}, '../platform/identity-failure': {NativeIdentityFailure: IdentityFailure},
    '../platform/home-action-diagnostics': {homeActionDiagnostics: {error: (_: string, error: unknown) => {errors.push(error);}, lifetime() {}}},
    '../platform/secure-records': {isSecureRecordsLost: (error: {code?: string}) => error?.code === 'secure_records_key_lost', secureRecordsRecoveredNotice: () => undefined, acknowledgeSecureRecordsRecovered() {}},
    '../pages/shared/accounts/AccountEntryPage': {AccountEntryPage: 'AccountEntry'}, '../pages/shared/accounts/ServersRoutePage': {ServersRoutePage: 'ServersRoute'},
    './first-library': {PhoneFirstLibrary: 'PhoneFirstLibrary'}, '../pages/shared/accounts/ServerIdentityFailurePage': {ServerIdentityFailurePage: 'ServerIdentityFailure'},
    './settings-appearance': {NativeSettingsAppearance: 'NativeSettingsAppearance'},
    '../pages/touch/downloads/RememberedDownloadsPage': {RememberedDownloadsPage: 'RememberedDownloadsScreen', RememberedDownloadsShortcut: 'RememberedDownloadsShortcut'},
    '../screens/downloads/presentation': {downloadEntryIdentity: () => ''},
    ...overrides,
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = RestoredShell();});
  await act(() => host.mount());
  return {host, calls, errors, focus, tree: () => tree, content: () => tree.props.children, fallbacks: () => fallbacks,
    network(value: boolean | undefined) {online = value; for (const listener of [...paths]) listener(value);},
    foreground(value: string) {for (const listener of [...foreground]) listener(value);},
    openInvitation() {
      let live = true; incoming = {id: 'invitation', current: () => live, dismiss: () => {live = false; incoming = undefined; for (const listener of [...links]) listener();}};
      for (const listener of [...links]) listener();
    },
    selectProfile(profileId: string) {viewer = {session: {...initialViewer.session, viewer: {...initialViewer.session.viewer, profileId}}}; for (const listener of [...selections]) listener();},
    listenerCount: () => paths.size + foreground.size + selections.size + links.size};
}

test('the real root keeps initial failures behind Boot, retries its captured installation, and pauses for the chooser', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const f = await shell(async () => {throw failure;});
  try {
    assert.equal(f.tree().props.active, true); assert.equal(f.content(), null); assert.equal(f.calls.length, 1);
    for (const ms of [2_000, 4_000, 8_000]) await act(() => t.mock.timers.tick(ms));
    assert.equal(f.tree().props.active, true); assert.equal(f.calls.length, 4);
    assert.ok(f.calls.every(call => call.installationOwner === f.calls[0]!.installationOwner), 'retries keep the captured installation');
    await act(() => t.mock.timers.tick(1_000));
    const recovery = f.content(); assert.equal(f.tree().props.active, false); assert.equal(recovery.props.waiting.retryAt, 29_000);
    recovery.props.titleRef.current = {tag: 7}; f.tree().props.onHidden(); assert.deepEqual(f.focus, [7]);
    const retainedRetry = recovery.props.onRetry, retainedFocus = f.tree().props.onHidden;
    await act(() => recovery.props.onChoose()); assert.equal(f.content().type, 'ServersRoute');
    const calls = f.calls.length;
    await act(() => {retainedRetry(); retainedFocus(); f.network(false); f.network(true); f.foreground('background'); f.foreground('active'); t.mock.timers.tick(60_000);});
    assert.equal(f.calls.length, calls); assert.equal(f.content().type, 'ServersRoute'); assert.deepEqual(f.focus, [7], 'the covered recovery cannot reclaim accessibility focus');
    await act(() => f.content().props.onBack()); assert.equal(f.calls.length, calls + 1); assert.equal(f.tree().props.active, false);
    assert.ok(f.errors.every(error => error === failure), 'the real errors are retained for diagnostics');
  } finally {await f.host.unmount();}
  assert.equal(f.listenerCount(), 0);
});

test('a changed stored viewer retires its old read, rejects late publication and fences its callbacks', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const resolves: ((value: unknown) => void)[] = [], disposed: string[] = [];
  const f = await shell(() => new Promise(resolve => {resolves.push(resolve);}));
  try {
    const original = f.calls[0]!;
    await act(() => f.selectProfile('next-profile'));
    assert.equal(f.calls.length, 2); assert.equal(original.signal.aborted, true);
    const old = {dispose: () => {disposed.push('old');}}, current = {dispose: () => {disposed.push('current');}};
    await act(() => {resolves[0]!(old);}); assert.deepEqual(disposed, ['old']); assert.equal(f.content(), null);
    await act(() => {resolves[1]!(current);}); assert.equal(f.content().type, 'NativeSettingsAppearance'); assert.equal(f.content().props.source, current);
    await act(() => {original.onAuthorityEnded(failure); original.onSessionRenewed();});
    assert.equal(f.calls.length, 2); assert.equal(f.content().props.source, current);
  } finally {await f.host.unmount();}
  assert.equal(disposed.filter(value => value === 'current').length, 1); assert.equal(f.listenerCount(), 0);
});

for (const entry of ['chooser', 'invitation'] as const)
for (const exit of ['complete', 'back'] as const)
test(`a paused ${entry} owns selection publication until its own ${exit}`, async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const f = await shell(async () => {throw failure;});
  try {
    if (entry === 'chooser') {
      await act(() => t.mock.timers.tick(15_000));
      await act(() => f.content().props.onChoose());
    } else await act(() => f.openInvitation());
    const calls = f.calls.length, installation = f.calls[0]!.installationOwner;
    const page = f.content(); assert.equal(page.type, entry === 'chooser' ? 'ServersRoute' : 'AccountEntry');
    await act(() => f.selectProfile('published-profile'));
    assert.equal(f.calls.length, calls, 'the storage hint must not unmount the entry in the middle of its publication');
    assert.equal(f.content().type, page.type);
    await act(() => exit === 'complete' ? page.props.onComplete() : page.props.onBack());
    assert.equal(f.calls.length, calls + 1); assert.notEqual(f.calls.at(-1)!.installationOwner, installation, 'the old captured viewer is never resumed');
  } finally {await f.host.unmount();}
});

for (const terminal of ['sign-in', 'identity', 'lost-records'] as const)
test(`${terminal} failures bypass quiet network retry and preserve their specific recovery`, async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const error = terminal === 'sign-in' ? new SignInEnded() : terminal === 'identity' ? new IdentityFailure()
    : Object.assign(new Error('saved key lost'), {code: 'secure_records_key_lost'});
  const f = await shell(async () => {throw error;});
  try {
    assert.equal(f.tree().props.active, false);
    assert.equal(f.content().type, terminal === 'sign-in' ? 'AccountEntry' : terminal === 'identity' ? 'ServerIdentityFailure' : 'SecureRecordsRecovery');
    await act(() => {f.network(false); f.network(true); f.foreground('background'); f.foreground('active'); t.mock.timers.tick(180_000);});
    assert.equal(f.calls.length, 1);
    if (terminal === 'lost-records') assert.equal(f.fallbacks(), 0, 'lost records never open an ordinary downloads fallback');
  } finally {await f.host.unmount();}
});

test('cold-launch offline settles once without immediate resume or shell bypass, and retry respects the profile gate', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  let state: {kind: string} = {kind: 'starting'};
  let starts = 0, resumes = 0;
  const listeners = new Set<() => void>();
  const setLifecycleState = (next: {kind: string}) => {state = next; for (const listener of [...listeners]) listener();};
  const lifecycle = {
    start() {starts++; if (state.kind === 'starting') queueMicrotask(() => setLifecycleState({kind: 'offline'}));},
    resume() {resumes++; setLifecycleState({kind: 'choosingProfile'});},
    report() {},
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => ({generation: 0, state}),
  };
  const lifecycleSettled = (life: typeof lifecycle) => new Promise<{kind: string}>(resolve => {
    const check = () => {
      const s = life.getSnapshot().state;
      if (s.kind === 'starting' || s.kind === 'connecting') return;
      off(); resolve(s);
    };
    const off = life.subscribe(check);
    check();
  });
  const f = await shell(async () => {throw new Error('must not bypass lifecycle');}, {
    '../platform/native-session-lifecycle': {
      nativeSessionLifecycle: () => ({lifecycle, ports: {lendInstallation() {}, adopt: () => undefined}}),
      lifecycleSettled,
      choosingProfile: (value: {kind: string}) => value.kind === 'choosingProfile' || value.kind === 'enteringPin',
    },
  });
  try {
    assert.equal(starts, 1);
    assert.equal(resumes, 0, 'cold launch offline does not immediately call lifecycle.resume()');
    assert.equal(f.calls.length, 0, 'offline never falls through to restoreNativeShellRuntime');
    await act(() => t.mock.timers.tick(2_000));
    assert.equal(resumes, 1, 'scheduled retry calls lifecycle.resume() once');
    assert.equal(f.calls.length, 0, 'choosingProfile throws NativeProfileChoice instead of bypassing the gate');
    assert.equal(typeof f.content().type, 'function', 'renders ProfileGateHost');
  } finally {await f.host.unmount();}
});

test('recordsLost and recordsUnreadable are handled directly without falling through to bootstrap', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  for (const kind of ['recordsLost', 'recordsUnreadable'] as const) {
    let state: {kind: string} = {kind: 'starting'};
    let resumes = 0;
    const listeners = new Set<() => void>();
    const setLifecycleState = (next: {kind: string}) => {state = next; for (const listener of [...listeners]) listener();};
    const lifecycle = {
      start() {if (state.kind === 'starting') queueMicrotask(() => setLifecycleState({kind}));},
      resume() {resumes++; setLifecycleState({kind: 'signedOut'});},
      report() {},
      subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
      getSnapshot: () => ({generation: 0, state}),
    };
    const lifecycleSettled = (life: typeof lifecycle) => new Promise<{kind: string}>(resolve => {
      const check = () => {
        const s = life.getSnapshot().state;
        if (s.kind === 'starting' || s.kind === 'connecting') return;
        off(); resolve(s);
      };
      const off = life.subscribe(check);
      check();
    });
    const f = await shell(async () => undefined, {
      '../platform/native-session-lifecycle': {
        nativeSessionLifecycle: () => ({lifecycle, ports: {lendInstallation() {}, adopt: () => undefined}}),
        lifecycleSettled,
        choosingProfile: (value: {kind: string}) => value.kind === 'choosingProfile' || value.kind === 'enteringPin',
      },
    });
    try {
      assert.equal(resumes, 0, `${kind}: cold launch does not immediately call resume`);
      assert.equal(f.calls.length, 0, `${kind}: does not fall through to restoreNativeShellRuntime`);
      if (kind === 'recordsLost') {
        assert.equal(f.content().type, 'SecureRecordsRecovery');
        assert.equal(f.fallbacks(), 0);
      } else {
        assert.equal(f.content().props.children.props.children.type, 'StateLayout');
        await act(() => f.content().props.children.props.children.props.primaryAction.onPress());
        assert.equal(resumes, 1, 'Try again on recordsUnreadable restarts and calls lifecycle.resume()');
      }
    } finally {await f.host.unmount();}
  }
});

test('ProfileGateHost waits while connecting after NativeProfileMoved instead of immediately restarting', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  class ProfileMoved extends Error {viewer = {accountId: 'a', profileId: 'p2', role: 'member', familyId: 'f1'};}
  let state: {kind: string} = {kind: 'signedOut'};
  const listeners = new Set<() => void>();
  const setLifecycleState = (next: {kind: string}) => {state = next; for (const listener of [...listeners]) listener();};
  const lifecycle = {
    start() {},
    resume() {},
    report(event: {type: string}) {if (event.type === 'profileMoved') setLifecycleState({kind: 'connecting'});},
    subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    getSnapshot: () => ({generation: 0, state}),
  };
  const runtime = {dispose() {}};
  const f = await shell(async () => runtime, {
    '../platform/native-session-lifecycle': {
      nativeSessionLifecycle: () => ({lifecycle, ports: {lendInstallation() {}, adopt: () => undefined}}),
      lifecycleSettled: async () => state,
      choosingProfile: (value: {kind: string}) => value.kind === 'choosingProfile' || value.kind === 'enteringPin',
    },
    '../platform/session-lifecycle-native': {
      NativeProfileGated: class extends Error {},
      NativeProfileMoved: ProfileMoved,
      nativeProfileGated: () => false,
    },
  });
  try {
    assert.equal(f.calls.length, 1);
    await act(() => f.calls[0]!.onAuthorityEnded(new ProfileMoved()));
    // Render ProfileGateHost once while lifecycle is `connecting`
    let gateTree: any;
    const Gate = f.content().type;
    const gateProps = f.content().props;
    const gateHost = createFabricHookHost(() => {gateTree = Gate(gateProps);});
    await act(() => gateHost.mount());
    try {
      assert.equal(gateTree, null);
      assert.equal(f.calls.length, 1, 'ProfileGateHost must wait during connecting and not restart');
      await act(() => setLifecycleState({kind: 'choosingProfile'}));
      assert.equal(f.calls.length, 1, 'stays mounted while choosingProfile');
      await act(() => setLifecycleState({kind: 'signedOut'}));
      assert.equal(f.calls.length, 2, 'hands over once settled out of connecting/choosingProfile');
    } finally {await gateHost.unmount();}
  } finally {await f.host.unmount();}
});

test('ServerIdentityFailurePage onForget clears stale error so ServersRoute has no Back to the forgotten server', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date'], now: 0});
  const forgotten: unknown[] = [];
  const err = Object.assign(new IdentityFailure(), {server: {serverId: 's1', familyId: 'f1', fingerprint: 'fp'}});
  const f = await shell(async () => {throw err;}, {
    '../platform/accounts': {nativeAccounts: {
      snapshot: async () => ({servers: []}),
      onSettled: () => () => {},
      onFence: () => () => {},
      forgetServer: async (server: unknown) => {forgotten.push(server);},
    }},
  });
  try {
    assert.equal(f.content().type, 'ServerIdentityFailure');
    const page = f.content();
    await act(async () => {
      await page.props.onForget();
      page.props.onChoose();
    });
    assert.deepEqual(forgotten, [err.server]);
    assert.equal(f.content().type, 'ServersRoute');
    assert.equal(f.content().props.onBack, undefined, 'ServersRoute must not offer Back to the forgotten server');
  } finally {await f.host.unmount();}
});
