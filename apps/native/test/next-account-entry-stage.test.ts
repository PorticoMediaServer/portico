// @fabric-react
import * as accountWork from '../src-next/platform/account-work';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {routeOrigin} from '../../../packages/client-core/src/route-identity';
import {NativeLocalInvitation} from '../src-next/platform/local-invitation';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as tvApprovalCredentials from '../src-next/screens/accounts/tv-approval-credentials';
import * as tvApprovalFinalization from '../src-next/screens/accounts/tv-approval-finalization';
import * as setupOutcome from '../src-next/platform/setup-outcome';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as directoryChooser from '../src-next/screens/accounts/directory-chooser';
import * as qrScanner from '../src-next/platform/qr-scanner';
import type {NativeAccountEntryStage} from '../src-next/screens/accounts/AccountEntry';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const deferred = <T,>() => {let resolve!: (value: T) => void, reject!: (reason: unknown) => void; const promise = new Promise<T>((accept, refuse) => {resolve = accept; reject = refuse;}); return {promise, resolve, reject};};
type FixtureOptions = Readonly<{providers?: boolean; setupRequired?: boolean; providerTransaction?: any}>;

async function fixture(initialStage: 'welcome' | 'address' = 'welcome', options: FixtureOptions = {}) {
  const events: string[] = [], stages: NativeAccountEntryStage[] = [], back = new Set<() => boolean>();
  let tree: any, phase = 'render', complete = 0, observed = true, providerCalls = 0, prepareCalls = 0;
  const finishes: ReturnType<typeof deferred<any>>[] = [];
  const credentials = {synchronize: async () => {}, getSnapshot: () => ({session: undefined})};
  const available = {password: true, registration: false, recovery: false, providers: options.providers
    ? [{id: 'google', enabled: true, native: false, nativeClientIds: []}] : []};
  class Portico {
    async prepare() {prepareCalls++; return available;}
    async provider() {providerCalls++; return options.providerTransaction;}
    dispose() {}
  }
  class SignIn {
    static parseInvitationEntry = NativeLocalInvitation.parseEntry;
    constructor() {events.push('create-command');}
    async inspect() {events.push('inspect'); return options.setupRequired
      ? {id: 'public-server', name: 'Public fixture', signIn: 'portico', setupRequired: true, hostedConfigured: true}
      : {id: 'public-server', name: 'Public fixture', signIn: 'local'};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {events.push('confirm-identity');}
    serverOrigin() {return 'https://public.example';}
    async prepareSetup() {return {id: 'public-server', name: 'Public fixture', signIn: 'portico', hostedConfigured: true};}
    async prepareHostedSetup() {return 'https://hosted.example/approve';}
    finishHostedSetup() {const held = deferred<any>(); finishes.push(held); return held.promise;}
    serverDescriptor() {return {id: 'public-server', name: 'Public fixture', signIn: 'local'};}
    dispose() {events.push('dispose-command');}
  }
  // These adapters forward the screen's dependency lists to the real hooks.
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), {'./ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, '../../shell/StartupBoot': {BootWaitLine: 'BootWaitLine'}, 
    '../../platform/tv-menu': {useTVMenuCapture() {}},
    './tv-code-opening': tvCodeOpening,
    '../../../../../packages/client-core/src/join-link': joinLink,
    '../../../../../packages/client-core/src/short-invitation-code': invitationCode,
    '../../platform/qr-scanner': qrScanner,
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'PhoneAccountSignIn'},
    './tv-approval-finalization': tvApprovalFinalization, './tv-approval-credentials': tvApprovalCredentials,
    react: {...React, default: React,
      // The harness forwards the component’s dependency array unchanged.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      useLayoutEffect: (create: () => void | (() => void), deps: readonly unknown[]) => React.useLayoutEffect(() => {phase = 'layout'; try {return create();} finally {phase = 'render';}}, deps),
      // eslint-disable-next-line react-hooks/exhaustive-deps
      useEffect: (create: () => void | (() => void), deps: readonly unknown[]) => React.useEffect(() => {phase = 'passive'; try {return create();} finally {phase = 'render';}}, deps)},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: {addEventListener: (_: string, handler: () => boolean) => {back.add(handler); return {remove: () => back.delete(handler)};}}, Platform: {isTV: false}, NativeModules: {PorticoAccountAuth: {canOpenBrowser: options.providers === true}}, Linking: {openURL: async () => {}}, View: 'View', Text: 'Text'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src': {},
    '../../../../../packages/client-core/src/sign-in': {}, '../../../../../packages/client-core/src/route-identity': {routeOrigin},
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    '../../platform/setup-outcome': setupOutcome,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './PhoneSetupApproval': {PhoneSetupApproval: 'PhoneSetupApproval'},
    './directory-chooser': directoryChooser,
    '../../kit/states': {StateLayout: 'Layout'}, '../../kit/controls': {Button: 'Button', TextField: 'Field'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {accountCredentials: () => credentials}}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn}, '../../platform/portico-sign-in': {NativePorticoSignIn: Portico},
    '../../platform/server-connections': {browseNearby: async () => {events.push('discovery'); return [];}}, '../../platform/tv-sign-in': {}, '../../platform/account-authority': {},
    '../../tv/auth': {}, './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'}, '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'}, './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneFrontDoor': {PhoneFrontDoor: 'PhoneFrontDoor'}, 'react-native-qrcode-svg': {default: 'QR'},
  });
  const onStageChange = (...args: NativeAccountEntryStage[]) => {
    assert.equal(phase, 'layout'); assert.equal(args.length, 1, 'the observer receives only the stage, never fields or authority');
    stages.push(args[0]!); events.push('stage:' + args[0]);
  };
  const host = createFabricHookHost(() => {tree = AccountEntry({initialStage, onComplete: () => {complete++;}, onStageChange: observed ? onStageChange : undefined});});
  await act(() => host.mount());
  return {host, stages, events, back, tree: () => tree, complete: () => complete, providerCalls: () => providerCalls, prepareCalls: () => prepareCalls, finishes,
    omitObserver: async () => {observed = false; await host.rerender();}};
}

test('AccountEntry reports initial/current stages in layout before discovery, preserving real commands and reporting Back without reopening policy', async () => {
  const f = await fixture();
  try {
    assert.deepEqual(f.stages, ['welcome']); assert.deepEqual(f.events, ['stage:welcome']); assert.equal(f.tree().type, 'PhoneFrontDoor'); assert.equal(f.prepareCalls(), 0);
    await act(() => f.tree().props.onLocal());
    assert.deepEqual(f.stages, ['welcome', 'address']);
    assert.ok(f.events.indexOf('stage:address') < f.events.indexOf('discovery'), 'capture retirement precedes address-stage passive work');
    await act(() => nodes(f.tree().props.beforeActions).find(node => node.type === 'Field').props.onChangeText('https://public.example'));
    assert.deepEqual(f.stages, ['welcome', 'address'], 'field changes are not observer events');
    await act(() => f.tree().props.primaryAction.onPress());
    assert.deepEqual(f.stages, ['welcome', 'address', 'local']); assert.ok(f.events.includes('inspect')); assert.ok(f.events.includes('confirm-identity'));
    const fields = nodes(f.tree().props.beforeActions).filter(node => node.type === 'Field');
    await act(() => {fields[0].props.onChangeText('fixture-user'); fields[1].props.onChangeText('fixture-password');});
    assert.deepEqual(f.stages, ['welcome', 'address', 'local']);
    await act(() => {assert.equal([...f.back][0]!(), true);});
    assert.deepEqual(f.stages, ['welcome', 'address', 'local', 'welcome'], 'the observer reports genuine Back; the public-launch gate owns its monotonic retirement');
    assert.equal(f.complete(), 0); assert.equal(f.prepareCalls(), 0, 'local entry and Back never contact Services');
    await f.omitObserver(); await act(() => f.tree().props.onLocal());
    assert.deepEqual(f.stages, ['welcome', 'address', 'local', 'welcome'], 'the optional observer does not alter navigation when absent');
    assert.equal(f.tree().type, 'Layout');
  } finally {await f.host.unmount();}
  assert.equal(f.back.size, 0);
});

test('an entry starting at address reports address only, never a synthetic welcome', async () => {
  const f = await fixture('address');
  try {assert.deepEqual(f.stages, ['address']); assert.ok(f.events.indexOf('stage:address') < f.events.indexOf('discovery'));}
  finally {await f.host.unmount();}
});

const providerFields = (tree: any) => nodes(tree.props.beforeActions).filter((node: any) => node.type === 'Field');
const fieldByLabel = (tree: any, label: string) => providerFields(tree).find((node: any) => String(node.props.label).startsWith(label))!;

test('actual AccountEntry seeds the committed provider name once and a newer draft render cannot re-seed it', async () => {
  const f = await fixture('welcome', {providers: true, providerTransaction: {transactionId: 'provider-original', provider: 'google',
    status: 'pending', needsUsername: true, suggestedName: 'Suggested Name', mfaRequired: false, contactVerified: false}});
  try {
    assert.equal(f.tree().type, 'PhoneFrontDoor'); assert.equal(f.prepareCalls(), 0);
    await act(() => f.tree().props.onPortico());
    assert.equal(f.tree().type, 'PhoneAccountSignIn'); assert.equal(f.prepareCalls(), 1);
    await act(() => f.tree().props.onProvider('google'));
    assert.equal(f.providerCalls(), 1);
    assert.equal(f.stages.at(-1), 'provider');
    const name = defaultI18n.t('auth.yourName');
    assert.equal(fieldByLabel(f.tree(), name).props.value, 'Suggested Name', 'the draft this commit published takes the suggestion');
    // A newer draft render must leave the opening's once-per-transaction seed alone, even cleared.
    await act(() => fieldByLabel(f.tree(), name).props.onChangeText(''));
    assert.equal(fieldByLabel(f.tree(), name).props.value, '');
    await f.host.rerender();
    assert.equal(fieldByLabel(f.tree(), name).props.value, '', 'a later render never borrows its newer draft back into the original opening');
    assert.equal(f.providerCalls(), 1);
  } finally {await f.host.unmount();}
});

test('actual AccountEntry resumes the drawn setup opening once through its own guarded runner and catch stage', async () => {
  const f = await fixture('address', {setupRequired: true});
  try {
    await act(() => providerFields(f.tree())[0]!.props.onChangeText('https://public.example'));
    await act(() => f.tree().props.primaryAction.onPress());
    assert.equal(f.stages.at(-1), 'setup-confirm');
    await act(() => f.tree().props.primaryAction.onPress());
    assert.equal(f.stages.at(-1), 'setup-choice');
    await act(() => f.tree().props.primaryAction.onPress());
    assert.equal(f.stages.at(-1), 'setup-hosted');
    assert.equal(f.finishes.length, 1, 'the drawn opening resumes exactly once through the commit that drew it');
    // A newer commit (its runner now captures busy) must not run the original completion again.
    await f.host.rerender();
    assert.equal(f.finishes.length, 1);
    await act(() => f.finishes[0]!.reject(new Error('setup refused')));
    assert.equal(f.stages.at(-1), 'setup-hosted', 'the original run reports at the stage it captured');
    assert.ok(nodes(f.tree().props.beforeActions).some((node: any) => node.props.accessibilityRole === 'alert'), 'the original catch publishes its refusal');
    assert.equal(f.finishes.length, 1);
  } finally {await f.host.unmount();}
});
