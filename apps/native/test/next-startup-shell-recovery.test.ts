// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import * as startupRecovery from '../src-next/shell/startup-recovery.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

type RestoreCall = {signal: AbortSignal; installationOwner: object; onAuthorityEnded: (error?: unknown) => void; onSessionRenewed: () => void};
const failure = Object.assign(new Error('test connection'), {code: 'network'});
class SignInEnded extends Error {hint = {serverId: 'server', familyId: 'family'};}
class IdentityFailure extends Error {}
const initialViewer = {session: {sessionFamilyId: 'family', viewer: {serverId: 'server', accountId: 'account', profileId: 'profile'}}};

async function shell(read: (call: RestoreCall, index: number) => Promise<unknown>) {
  const paths = new Set<(value: boolean | undefined) => void>(), foreground = new Set<(value: string) => void>(), selections = new Set<() => void>(), links = new Set<() => void>();
  const calls: RestoreCall[] = [], errors: unknown[] = [], focus: number[] = [];
  let online: boolean | undefined = true, viewer = initialViewer, fallbacks = 0;
  let incoming: {id: string; current: () => boolean; dismiss: () => void} | undefined;
  const {RestoredShell} = await screenModule(new URL('../src-next/shell/RestoredShell.tsx', import.meta.url), {'../screens/accounts/ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../platform/negotiation': {negotiationRefusal: () => undefined}, './boot-timing': {logBootMilestone: () => {}}, './restore-trace': {restoreTrace: () => ({waiting: 'start', elapsedMs: 0, marks: []})}, '../screens/share/NativeShareLinks': {NativeShareLinks: () => null}, '../platform/share-links': {nativeShareLinks: {acquire: () => () => {}, subscribe: () => () => {}, getSnapshot: () => undefined, take: () => false}}, 
    react: {...React, default: React},
    'react-native': {Platform: {OS: 'ios'}, View: 'View', Text: 'Text', Image: 'Image', findNodeHandle: (node: {tag: number}) => node.tag,
      AccessibilityInfo: {setAccessibilityFocus: (tag: number) => {focus.push(tag);}},
      AppState: {currentState: 'active', addEventListener: (_: string, listener: (value: string) => void) => {foreground.add(listener); return {remove: () => foreground.delete(listener)};}}},
    '../kit/states': {StateView: 'StateView', StateLayout: 'StateLayout'}, '../kit/artwork': {StandardBackdrop: 'StandardBackdrop', CoverFan: 'CoverFan'},
    '../kit/controls': {Focusable: 'Focusable'}, '../kit/overlays/TVDesignCanvas': {TVDesignCanvas: 'TVDesignCanvas'}, '../kit/Icon': {Icon: 'Icon'},
    '../kit/theme': {useTheme: () => ({platform: 'phone', isTV: false, colors: {ink: '#071018'}, type: {}})},
    '../../../../packages/client-core/src/presentation/errors': {presentError: () => ({category: 'unreachable', action: 'try-again', title: 'Unavailable', body: 'Unavailable'})},
    '../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../platform/server-connections': {deviceOnline: () => online, onNetworkChange: (listener: (value: boolean | undefined) => void) => {paths.add(listener); return () => {paths.delete(listener);};}},
    '../platform/invitation-links': {nativeInvitationLinks: {getSnapshot: () => incoming,
      subscribe: (listener: () => void) => {links.add(listener); return () => {links.delete(listener);};}, acquire: () => () => {}}},
    '../platform/installation': {beginNativeInstallation: (signal: AbortSignal) => {
      let disposed = false; return {current: () => !disposed && !signal.aborted, dispose: () => {disposed = true;}};
    }},
    './assembly': {createNativeShellBindings: () => ({})}, './bootstrap': {restoreNativeShellRuntime: (_: unknown, call: RestoreCall) => {calls.push(call); return read(call, calls.length);}},
    './remembered-downloads-opening': {createRememberedDownloadsOpening: async () => {fallbacks++; return undefined;}},
    './NativeShell': {NativeShell: 'NativeShell'}, './StartupBoot': {StartupBoot: 'StartupBoot'}, './startup-recovery': startupRecovery,
    './SecureRecordsRecovery': {SecureRecordsRecovery: 'SecureRecordsRecovery'}, '../platform/boot-host': {takeLaunchStartedAt: (now = Date.now()) => now},
    '../platform/accounts': {nativeAccounts: {snapshot: async () => ({servers: []}), onSettled: () => () => {}, onFence: () => () => {}}},
    '../platform/app-session-storage': {readNativeSelectedViewer: async () => viewer, subscribeNativeAppSessions: (listener: () => void) => {selections.add(listener); return () => {selections.delete(listener);};}},
    '../platform/sign-in-ended': {NativeSignInEnded: SignInEnded}, '../platform/identity-failure': {NativeIdentityFailure: IdentityFailure},
    '../platform/home-action-diagnostics': {homeActionDiagnostics: {error: (_: string, error: unknown) => {errors.push(error);}, lifetime() {}}},
    '../platform/secure-records': {isSecureRecordsLost: (error: {code?: string}) => error?.code === 'secure_records_key_lost', secureRecordsRecoveredNotice: () => undefined, acknowledgeSecureRecordsRecovered() {}},
    '../screens/accounts/AccountEntry': {AccountEntry: 'AccountEntry'}, '../screens/accounts/AccountRoutes': {ServersRoute: 'ServersRoute'},
    '../screens/accounts/PhoneFirstLibrary': {PhoneFirstLibrary: 'PhoneFirstLibrary'}, '../screens/accounts/ServerIdentityFailure': {ServerIdentityFailure: 'ServerIdentityFailure'},
    '../screens/settings/NativeSettingsAppearance': {NativeSettingsAppearance: 'NativeSettingsAppearance'},
    '../screens/downloads/RememberedDownloadsScreen': {RememberedDownloadsScreen: 'RememberedDownloadsScreen', RememberedDownloadsShortcut: 'RememberedDownloadsShortcut'},
    '../screens/downloads/presentation': {downloadEntryIdentity: () => ''},
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
