// @fabric-react
// The phone's move of a member's account to a Portico Account (P2-66), on #3158's controller: the
// way in on the server's page, the confirm built from trusted names, and the TV's hand-off.
import * as accountWork from '../src-next/platform/account-work';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {routeOrigin} from '../../../packages/client-core/src/route-identity';
import * as serverAddress from '../../../packages/client-core/src/server-address';
import {NativeLocalInvitation} from '../src-next/platform/local-invitation';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as tvApprovalCredentials from '../src-next/screens/accounts/tv-approval-credentials';
import * as tvApprovalFinalization from '../src-next/screens/accounts/tv-approval-finalization';
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
import {moveOfferDays} from '../../../packages/client-core/src/sign-in';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
// The pickers and the PIN screen are shared pieces whose slots hold what layouts kept in beforeActions.
const parts = (tree: any): any[] => tree?.type === 'PhoneWhosWatching' ? [...nodes(tree.props.picker), ...nodes(tree.props.children)] : tree?.type === 'PhoneProfilePIN' ? [tree, ...nodes(tree.props.remember), ...nodes(tree.props.status), ...nodes(tree.props.hints)] : nodes(tree?.props.beforeActions);
type FixtureOptions = Readonly<{providers?: boolean; setupRequired?: boolean; providerTransaction?: any; inspectFails?: (origin: string) => boolean; tv?: boolean; session?: boolean}>;
/** A stand-in for the member move controller (`platform/member-account-move.ts`, #3158): it records what the screens ask of it. */
function moveController(kind: string, calls: string[]) {
  let snapshot: any = {phase: kind, busy: false}; const listeners = new Set<() => void>();
  return {calls, kind, getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    set(next: any) {snapshot = {busy: false, ...next}; for (const listener of listeners) listener();},
    preparePortico: async () => {calls.push('preparePortico');}, redeemPortico: async () => {calls.push('redeemPortico');}, changePorticoAccount: () => {calls.push('changePorticoAccount');},
    cancel: () => {calls.push('cancel');}, dispose: () => {calls.push('dispose');}, takeAcceptedResult: () => undefined, readCodeInput: (value: string) => value};
}

async function fixture(initialStage: 'welcome' | 'address' = 'welcome', options: FixtureOptions = {}) {
  const events: string[] = [], stages: NativeAccountEntryStage[] = [], inspected: string[] = [], back = new Set<() => boolean>();
  let tree: any, phase = 'render', complete = 0, observed = true, providerCalls = 0, prepareCalls = 0;
  const moveCalls: string[] = [], moves: ReturnType<typeof moveController>[] = [], signedOut: string[] = [];
  const credentials = {synchronize: async () => {}, getSnapshot: () => ({session: options.session ? {account: {id: 'acct_member', username: 'member'}, familyId: 'family'} : undefined})};
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
    async inspect(origin: string) {events.push('inspect'); inspected.push(origin); if (options.inspectFails?.(origin)) throw new Error('refused ' + origin); return options.setupRequired
      ? {id: 'public-server', name: 'Public fixture', signIn: 'portico', setupRequired: true, hostedConfigured: true}
      : {id: 'public-server', name: 'Public fixture', signIn: 'portico', move: 'to_portico', moveEndsAt: new Date(Date.now() + 5 * 86_400_000).toISOString()};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {events.push('confirm-identity');}
    // As on a Portico Accounts server before any sign-in connects to it: no API, so no origin from it.
    serverOrigin(): string {throw Object.assign(new Error('native_sign_in_authority_changed'), {code: 'identity_mismatch'});}
    inspectedOrigin() {return 'https://public.example';}
    serverDescriptor() {return {id: 'public-server', name: 'Verified name', signIn: 'portico', move: 'to_portico', moveEndsAt: new Date(Date.now() + 5 * 86_400_000).toISOString()};}
    async createMemberMove(kind: string) {events.push('move:' + kind); const move = moveController(kind, moveCalls); moves.push(move); return move;}
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
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: {addEventListener: (_: string, handler: () => boolean) => {back.add(handler); return {remove: () => back.delete(handler)};}}, Platform: {isTV: options.tv === true}, NativeModules: {PorticoAccountAuth: {canOpenBrowser: options.providers === true}}, Linking: {openURL: async () => {}}, View: 'View', Text: 'Text'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src': {},
    '../../../../../packages/client-core/src/sign-in': {moveOfferDays}, '../../../../../packages/client-core/src/route-identity': {routeOrigin},
    '../../../../../packages/client-core/src/server-address': serverAddress,
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError}, '../../kit/theme': {useTheme: () => createTheme({isTV: options.tv === true, width: options.tv ? 1920 : 390})},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './NativeSetup': {NativeSetup: 'NativeSetup'},
    './directory-chooser': directoryChooser,
    '../../kit/states': {StateLayout: 'Layout'}, '../../kit/controls': {Button: 'Button', TextField: 'Field'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {accountCredentials: () => credentials, signOutPorticoAccount: async (scope: {accountId: string}) => {signedOut.push(scope.accountId);}}}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn}, '../../platform/portico-sign-in': {NativePorticoSignIn: Portico},
    '../../platform/server-connections': {browseNearby: async () => {events.push('discovery'); return [];}}, '../../platform/tv-sign-in': {}, '../../platform/account-authority': {},
    '../../tv/auth': {TVMoveHandoff: 'TVMoveHandoff'}, './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, './PhoneProfilePIN': {PhoneProfilePIN: 'PhoneProfilePIN'}, './PhoneWhosWatching': {PhoneWhosWatching: 'PhoneWhosWatching'}, '../../shell/after-entry': {afterEntry: {request() {}, clear() {}, take() {}}}, './RememberProfileRow': {RememberProfileRow: 'Remember'}, './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'}, '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'}, './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneFrontDoor': {PhoneFrontDoor: 'PhoneFrontDoor'}, 'react-native-qrcode-svg': {default: 'QR'},
  });
  const onStageChange = (...args: NativeAccountEntryStage[]) => {
    assert.equal(phase, 'layout'); assert.equal(args.length, 1, 'the observer receives only the stage, never fields or authority');
    stages.push(args[0]!); events.push('stage:' + args[0]);
  };
  const host = createFabricHookHost(() => {tree = AccountEntry({initialStage, onComplete: () => {complete++;}, onStageChange: observed ? onStageChange : undefined});});
  await act(() => host.mount());
  return {host, stages, events, inspected, back, moves, moveCalls, signedOut, tree: () => tree, complete: () => complete, providerCalls: () => providerCalls, prepareCalls: () => prepareCalls,
    omitObserver: async () => {observed = false; await host.rerender();}};
}

const providerFields = (tree: any) => parts(tree).filter((node: any) => node.type === 'Field');
async function atServer(f: Awaited<ReturnType<typeof fixture>>) {
  await act(() => providerFields(f.tree())[0]!.props.onChangeText('https://public.example'));
  await act(() => f.tree().props.primaryAction.onPress());
}
const label = (node: any) => [node.props.children].flat().join('');

test('the phone says the server switched, then offers the old sign-in with its days and the owner\'s code (P2-66)', async () => {
  const f = await fixture('address');
  try {
    await atServer(f);
    assert.equal(f.stages.at(-1), 'uses-portico');
    const page = f.tree();
    assert.match(page.props.body, /Public fixture now uses Portico Accounts/);
    const links = nodes(page.props.children).filter((node: any) => node.type === 'Button').map(label);
    assert.deepEqual(links.slice(0, 2), [defaultI18n.t('accounts.move.oldPasswordAction', {days: 5}), defaultI18n.t('accounts.move.haveCode')]);
    await act(() => nodes(page.props.children).find((node: any) => node.type === 'Button' && label(node) === defaultI18n.t('accounts.move.haveCode')).props.onPress());
    assert.deepEqual(f.events.filter(event => event.startsWith('move:')), ['move:code'], 'a move to Portico Accounts takes the owner\'s code too');
  } finally {await f.host.unmount();}
});

test('signed in, Continue names both accounts and stops on the confirm: only Move moves, Not you? keeps the proof, Cancel leaves', async () => {
  const f = await fixture('address', {session: true});
  try {
    await atServer(f);
    await act(() => nodes(f.tree().props.children).find((node: any) => node.type === 'Button' && label(node) === defaultI18n.t('accounts.move.haveCode')).props.onPress());
    const move = f.moves[0]!;
    await act(() => move.set({phase: 'portico', username: 'old-local-name'}));
    assert.equal(f.tree().type, 'MoveNotice'); assert.equal(f.tree().props.state.kind, 'to-portico');
    await act(() => f.tree().props.state.onContinue());
    assert.deepEqual(f.moveCalls, ['preparePortico'], 'signed in: prepared, never redeemed');
    await act(() => move.set({phase: 'confirm-portico', username: 'old-local-name', porticoAccount: {id: 'acct_member', username: 'member'}}));
    const confirm = f.tree().props.state;
    assert.deepEqual([confirm.kind, confirm.serverName, confirm.username, confirm.account], ['confirm', 'Verified name', 'old-local-name', 'member'],
      'the server\'s verified name, the server account and the held Portico Account');
    assert.deepEqual(f.moveCalls, ['preparePortico'], 'drawing the confirm sends nothing');
    await act(() => confirm.onMove());
    assert.deepEqual(f.moveCalls, ['preparePortico', 'redeemPortico']);
    await act(() => confirm.onNotYou());
    assert.equal(f.moveCalls[2], 'changePorticoAccount', 'the server proof stays; the account is let go');
    assert.equal(f.stages.at(-1), 'account-sign-out', 'and the held Portico Account is signed out to change it');
    await act(() => move.set({phase: 'portico', username: 'old-local-name'}));
    await act(() => f.tree().props.primaryAction.onPress());
    assert.deepEqual(f.signedOut, ['acct_member'], 'the held account is signed out');
    assert.equal(f.stages.at(-1), 'member-move', 'and the move, with its proof, asks for the next account');
    assert.equal(f.tree().props.state.kind, 'to-portico');
    assert.equal(f.moveCalls.includes('dispose'), false, 'the move was not thrown away');
  } finally {await f.host.unmount();}
});

test('Cancel on the confirm ends the move and returns to the server\'s page, having moved nothing', async () => {
  const f = await fixture('address', {session: true});
  try {
    await atServer(f);
    await act(() => nodes(f.tree().props.children).find((node: any) => node.type === 'Button' && label(node) === defaultI18n.t('accounts.move.haveCode')).props.onPress());
    await act(() => f.moves[0]!.set({phase: 'confirm-portico', username: 'old-local-name', porticoAccount: {id: 'acct_member', username: 'member'}}));
    await act(() => f.tree().props.state.onCancel());
    assert.ok(f.moveCalls.includes('dispose')); assert.equal(f.moveCalls.includes('redeemPortico'), false);
    assert.equal(f.stages.at(-1), 'uses-portico');
  } finally {await f.host.unmount();}
});

test('a TV states the switch and hands the move to a phone at the server\'s own sign-in page, with no password field', async () => {
  const f = await fixture('address', {tv: true});
  try {
    await atServer(f);
    const page = f.tree();
    assert.equal(page.props.primaryAction.id, 'portico');
    assert.deepEqual(page.props.additionalActions.map((item: any) => item.id), ['move-on-phone']);
    assert.equal(nodes(page).some((node: any) => node.type === 'Field'), false);
    await act(() => page.props.additionalActions[0].onPress());
    assert.equal(f.stages.at(-1), 'tv-move-handoff');
    assert.equal(f.tree().type, 'TVMoveHandoff');
    assert.deepEqual([f.tree().props.serverName, f.tree().props.url], ['Public fixture', 'https://public.example/sign-in']);
    assert.deepEqual(f.events.filter(event => event.startsWith('move:')), [], 'no move is started on the TV');
  } finally {await f.host.unmount();}
});

test('the TV\'s move hand-off: the server\'s own sign-in page as a QR and its address, Back only, and nothing else accepted', async () => {
  const {catalogue, enUS} = await import('../../../packages/i18n/src');
  const {nativeType} = await import('../src-next/kit/theme');
  const {stateSentenceParts} = await import('../src-next/kit/states/sentence-parts');
  const revision = await screenModule(new URL('../src-next/tv/auth/entry-revision.ts', import.meta.url), {react: {...React, default: React}});
  const listeners = new Set<() => boolean>(), backs: string[] = [];
  const {TVMoveHandoff} = await screenModule(new URL('../src-next/tv/auth/TVHandoff.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View', Text: 'Text', BackHandler: {addEventListener: (_: string, listener: () => boolean) => {listeners.add(listener); return {remove: () => listeners.delete(listener)};}}},
    'react-native-qrcode-svg': {default: 'QR'}, '../../../../../packages/i18n/src': {catalogue, defaultI18n, enUS}, '../../kit/controls': {Button: 'Button'},
    '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/theme': {nativeType, useTheme: () => createTheme({isTV: true, width: 1920})},
    '../../kit/states/sentence-parts': {stateSentenceParts}, '../../platform/native-blur': {BlurView: 'Blur', glassRecipe: () => ({nativeBlur: false})},
    '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus: () => {}}, './TVAuthLayout': {TVAuthLayout: 'Layout'}, './entry-revision': revision,
    '../../platform/account-authority': {hostedUrl: 'https://web.getportico.tv'}, '../../platform/forgot-pin-handoff': {forgotPINHandoff: () => ({url: '', body: 'next.auth.tv.forgotPINBody'})},
  });
  const draw = (url: string) => {const outer = TVMoveHandoff({serverName: 'Den', url, onBack: () => backs.push('back')}); const inner = [outer.props.children].flat()[0]; return inner.type(inner.props);};
  let tree: any;
  const host = createFabricHookHost(() => {tree = draw('https://den.example:32500/sign-in');});
  await act(() => host.mount());
  try {
    assert.deepEqual([tree.props.eyebrow, tree.props.heading, tree.props.body], ['Den', defaultI18n.t('next.auth.tv.moveOnPhone'), defaultI18n.t('next.auth.tv.moveBody', {server: 'Den'})]);
    const card = nodes(tree.props.aside);
    assert.equal(card.find((node: any) => node.type === 'QR').props.value, 'https://den.example:32500/sign-in');
    assert.ok(card.some((node: any) => node.props.children === 'den.example:32500/sign-in'));
    const buttons = nodes(tree).filter((node: any) => node.type === 'Button');
    assert.deepEqual(buttons.map((node: any) => node.props.children), [defaultI18n.t('action.back')]);
    buttons[0].props.onPress(); assert.deepEqual(backs, ['back']);
  } finally {await host.unmount();}
  for (const url of ['https://den.example/settings/profile', 'https://den.example/sign-in?code=1', 'https://den.example/sign-in#ticket=x'])
    assert.throws(() => draw(url), /native_tv_(handoff|move)_url_invalid/, url);
});

test('the phone\'s confirm reads as a question naming all three, with Move first, Not you? and Cancel', async () => {
  const {PhoneAccountMoveNotice} = await screenModule(new URL('../src-next/screens/accounts/PhoneAccountMoveNotice.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Image: 'Image', ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/artwork': {ColourBackdrop: 'Backdrop'}, '../../kit/controls': {Button: 'Button'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/theme': {nativeType: () => ({}), useTheme: () => createTheme({isTV: false, width: 390})},
  });
  const pressed: string[] = [];
  // The notice's wordmark is a bundled image; the harness has no asset loader.
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'require');
  Object.defineProperty(globalThis, 'require', {configurable: true, value: () => 'wordmark'});
  let tree: any;
  try {tree = PhoneAccountMoveNotice({state: {kind: 'confirm', serverName: 'Den', username: 'old-name', account: 'ada',
    onMove: () => pressed.push('move'), onNotYou: () => pressed.push('not-you'), onCancel: () => pressed.push('cancel')}});}
  finally {if (previous) Object.defineProperty(globalThis, 'require', previous); else delete (globalThis as {require?: unknown}).require;}
  const all = nodes(tree), texts = all.filter((node: any) => node.type === 'Text').map((node: any) => node.props.children);
  assert.ok(texts.includes(defaultI18n.t('accounts.move.porticoQuestion', {username: 'old-name', server: 'Den', account: 'ada'})));
  assert.ok(texts.includes(defaultI18n.t('accounts.move.porticoConfirm', {username: 'old-name', server: 'Den', account: 'ada'})));
  const buttons = all.filter((node: any) => node.type === 'Button');
  assert.deepEqual(buttons.map((node: any) => node.props.children), [defaultI18n.t('accounts.move.porticoAction'), defaultI18n.t('accounts.invitation.notYou'), defaultI18n.t('action.cancel')]);
  for (const button of buttons) button.props.onPress();
  assert.deepEqual(pressed, ['move', 'not-you', 'cancel']);
});
