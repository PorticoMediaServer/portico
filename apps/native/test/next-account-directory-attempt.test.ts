// @fabric-react
import * as accountWork from '../src-next/platform/account-work';
import * as tvApprovalCredentials from '../src-next/screens/accounts/tv-approval-credentials';
import * as tvApprovalFinalization from '../src-next/screens/accounts/tv-approval-finalization';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as setupOutcome from '../src-next/platform/setup-outcome';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as qrScanner from '../src-next/platform/qr-scanner';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {nextRetryDelay} from '../src-next/screens/accounts/directory-chooser';
import {screenModule} from './helpers/screen-harness.mjs';

const theme = createTheme({isTV: false, width: 390});
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const directory = [
  {id: 'one', name: 'One', baseUrl: 'https://one.test', publicKey: 'key', policyRevision: 0, presence: {online: true, lastSeenAt: null}},
  {id: 'two', name: 'Two', baseUrl: 'https://two.test', publicKey: 'key', policyRevision: 0, presence: {online: true, lastSeenAt: null}},
];

test('a directory connect is fenced to its attempt: Choose another and a successor ignore its late result or error', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let appState: (state: string) => void = () => {};
  const pending: {server: {id: string; name: string}; resolve: (value: unknown) => void; reject: (error: unknown) => void}[] = [];
  class SignIn {
    connectPortico(server: {id: string; name: string}) {
      return new Promise((resolve, reject) => {pending.push({server, resolve, reject});});
    }
    serverDescriptor() {return {id: 'server', name: 'Server', signIn: 'portico'};}
    dispose() {}
  }
  class HostedApi {
    async servers() {return {items: directory};}
  }
  const account = {account: {id: 'account'}, familyId: 'family', accessToken: 'token'};
  const nativeAccounts = {accountCredentials: () => ({
    subscribe: () => () => {}, synchronize: async () => {}, getSnapshot: () => ({session: account}), accessSession: async () => account,
  })};
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), {'./ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, '../../shell/StartupBoot': {BootWaitLine: 'BootWaitLine'}, 
    '../../platform/tv-menu': {useTVMenuCapture() {}},
    './tv-code-opening': tvCodeOpening,
    // The real TV approval and invitation modules are loaded, so an unexpected
    // call cannot manufacture a successful opening or parse.
    './tv-approval-credentials': tvApprovalCredentials,
    './tv-approval-finalization': tvApprovalFinalization,
    '../../../../../packages/client-core/src/join-link': joinLink,
    '../../../../../packages/client-core/src/short-invitation-code': invitationCode,
    '../../platform/qr-scanner': qrScanner,
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'PhoneAccountSignIn'},
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: (_: string, listener: (state: string) => void) => {appState = listener; return {remove() {}};}}, BackHandler: {addEventListener: () => ({remove() {}})}, Platform: {isTV: false}, Dimensions: {get: () => ({width: 390, height: 844})},
      NativeModules: {}, View: 'View', Text: 'Text', ScrollView: 'ScrollView', Linking: {openURL: async () => {}}},
    'react-native-qrcode-svg': {default: 'QR'},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src': {HttpHostedApi: HostedApi},
    '../../../../../packages/client-core/src/sign-in': {ServerSignInProbe: class {}},
    '../../../../../packages/client-core/src/route-identity': {routeOrigin: (value: string) => value},
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'error', silent: false})},
    '../../kit/states': {StateLayout: 'Layout'}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'Field'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'}, '../../kit/theme': {useTheme: () => theme},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn},
    '../../platform/portico-sign-in': {NativePorticoSignIn: class {dispose() {}}},
    '../../platform/server-connections': {browseNearby: async () => [], nativeNetworkFetch: async () => ({})},
    '../../platform/tv-sign-in': {createNativePorticoTVSignIn: () => ({subscribe: () => () => {}, getSnapshot: () => ({phase: 'idle'}), start: async () => {}, cancel: async () => {}})},
    '../../platform/account-authority': {hostedUrl: 'https://hosted.test'},
    '../../tv/auth': {TVProfilePINForm: 'PIN', TVForgotPINHandoff: 'Forgot', TVFrontDoor: 'Front', TVCodePresenter: 'Code',
      TVCredentialForm: 'Credentials', TVVerificationForm: 'Factor', TVRequiredSetupHandoff: 'Required'},
    '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'},
    './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'}, './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneFrontDoor': {PhoneFrontDoor: 'Phone'},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    '../../platform/setup-outcome': setupOutcome,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './PhoneSetupApproval': {PhoneSetupApproval: 'PhoneSetupApproval'},
    './directory-chooser': {nextRetryDelay, directoryRows: (servers: readonly {id: string; name: string}[]) => servers.map(server => ({id: server.id, name: server.name, offline: false})),
      directoryFailure: (server: unknown, error: unknown) => !server ? 'other' : error === undefined ? 'opening' : 'waiting',
      directoryOfflineBody: () => ({key: 'web.chooser.unreachableBodyNever'})},
  });
  let tree: any, completed = 0;
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {completed++;}});});
  await act(() => host.mount());
  try {
    assert.equal(tree.type, 'Phone'); await act(() => tree.props.onPortico());
    await act(() => host.rerender());
    assert.equal(tree.type, 'Layout'); assert.equal(tree.props.title, defaultI18n.t('portico.landing.pickTitle'));
    const rows = () => nodes(tree.props.beforeActions).filter(node => node.type === 'Focusable');
    assert.equal(rows().length, 2);

    await act(async () => {rows()[0].props.onPress(); await host.rerender();});
    assert.equal(tree.props.testID, 'accounts-server-opening');
    await act(async () => {tree.props.primaryAction.onPress(); await host.rerender();});
    assert.equal(tree.type, 'Layout'); assert.equal(tree.props.title, defaultI18n.t('portico.landing.pickTitle'));
    await act(async () => {pending[0]!.resolve({kind: 'profiles', profiles: [{id: 'p'}]}); await Promise.resolve(); await host.rerender();});
    assert.equal(completed, 0); assert.equal(tree.type, 'Layout');

    await act(async () => {rows()[0].props.onPress(); await host.rerender();});
    assert.equal(tree.props.testID, 'accounts-server-opening');
    await act(async () => {tree.props.primaryAction.onPress(); await host.rerender();});
    await act(async () => {pending[1]!.reject(Object.assign(new Error('connection'), {code: 'server_unavailable'})); await Promise.resolve(); await host.rerender();});
    assert.equal(tree.type, 'Layout'); assert.notEqual(tree.props.testID, 'accounts-server-waiting'); assert.equal(completed, 0);

    await act(async () => {rows()[0].props.onPress(); await host.rerender();});
    assert.equal(tree.props.testID, 'accounts-server-opening');
    await act(async () => {tree.props.primaryAction.onPress(); await host.rerender();});
    await act(async () => {rows()[1].props.onPress(); await host.rerender();});
    assert.equal(tree.props.testID, 'accounts-server-opening');
    const successorTitle = tree.props.title;
    await act(async () => {pending[2]!.resolve({kind: 'profiles', profiles: [{id: 'p'}]}); await Promise.resolve(); await host.rerender();});
    assert.equal(tree.props.testID, 'accounts-server-opening'); assert.equal(tree.props.title, successorTitle); assert.equal(completed, 0);
    await act(async () => {pending[3]!.reject(Object.assign(new Error('connection'), {code: 'server_unavailable'})); await Promise.resolve(); await host.rerender();});
    assert.equal(tree.props.testID, 'accounts-server-waiting');
    assert.equal(tree.props.title, defaultI18n.t('web.chooser.waitingTitle', {server: 'Two'}));
    assert.ok(tree.props.retry, 'the waiting state carries its automatic retry schedule');
    assert.equal(tree.props.retry.label, defaultI18n.t('web.chooser.checkingAgain', {seconds: 20}));
    assert.equal(completed, 0);
    const advance = async (seconds: number) => {for (let i = 0; i < seconds; i++) await act(() => t.mock.timers.tick(1000));};
    const rejectLast = async () => {await act(() => pending.at(-1)!.reject(Object.assign(new Error('connection'), {code: 'server_unavailable'})));};
    for (const delay of [20, 40, 80, 160, 300, 300]) {
      const count = pending.length;
      await advance(delay - 1);
      assert.equal(pending.length, count, 'countdown does not connect early');
      await advance(1);
      assert.equal(pending.length, count + 1, `one retry at ${delay}s`);
      assert.equal(pending.at(-1)!.server.id, 'two');
      await rejectLast();
    }
    await advance(299);
    let count = pending.length;
    // Manual Check now invalidates the old tick before effects can clean it up.
    await act(() => {tree.props.primaryAction.onPress(); t.mock.timers.tick(1000);});
    assert.equal(pending.length, count + 1, 'manual/timer boundary issues one connection');
    await rejectLast();
    await advance(299);
    count = pending.length;
    await act(() => {appState('background'); t.mock.timers.tick(1000);});
    assert.equal(pending.length, count, 'background invalidates the expiry synchronously');
    await advance(400);
    assert.equal(pending.length, count, 'no requests while inactive');
    await act(() => appState('active'));
    await advance(1);
    assert.equal(pending.length, count + 1, 'foreground resumes the remaining second');
    await rejectLast();
    await advance(299);
    count = pending.length;
    await act(() => {tree.props.secondaryAction.onPress(); t.mock.timers.tick(1000);});
    assert.equal(pending.length, count, 'Choose another invalidates the expiry before passive cleanup');
    await act(() => rows()[0].props.onPress());
    await rejectLast();
    await advance(19);
    count = pending.length;
    await act(() => {tree.props.secondaryAction.onPress(); rows()[1]?.props.onPress(); t.mock.timers.tick(1000);});
    assert.equal(pending.length, count, 'a retired target cannot reconnect');
    await act(() => rows()[1].props.onPress());
    await rejectLast();
    await advance(19);
    count = pending.length;
    await host.unmount(); t.mock.timers.tick(1000);
    assert.equal(pending.length, count, 'unmount retires the timer');
  } finally {await host.unmount(); t.mock.timers.reset();}
});
