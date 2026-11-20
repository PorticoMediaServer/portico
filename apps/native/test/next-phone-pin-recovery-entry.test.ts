// @fabric-react
import {FormError, formErrorModule} from './helpers/form-error.mjs';
import * as accountWork from '../src-next/platform/account-work';
import * as tvApprovalCredentials from '../src-next/screens/accounts/tv-approval-credentials';
import * as tvApprovalFinalization from '../src-next/screens/accounts/tv-approval-finalization';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as qrScanner from '../src-next/platform/qr-scanner';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {routeOrigin} from '../../../packages/client-core/src/route-identity';
import * as serverAddress from '../../../packages/client-core/src/server-address';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const profile = {id: 'locked', name: 'Actual locked profile', art: 'mint', primary: false, pinRequired: true};
const theme = createTheme({isTV: false, width: 390});
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
// The pickers and the PIN screen are shared pieces whose slots hold what layouts kept in beforeActions.
const parts = (tree: any): any[] => tree?.type === 'PhoneWhosWatching' ? [...nodes(tree.props.picker), ...nodes(tree.props.children)] : tree?.type === 'PhoneProfilePIN' ? [tree, ...nodes(tree.props.remember), ...nodes(tree.props.status), ...nodes(tree.props.hints)] : nodes(tree?.props.beforeActions);
function backOwner() {
  const listeners = new Set<() => boolean>();
  return {listeners, api: {addEventListener: (_: string, handler: () => boolean) => {listeners.add(handler); return {remove: () => listeners.delete(handler)};}}};
}

test('the phone sign-in picker offers the account recovery, keeps its own operation and shows profile PIN copy', async () => {
  const back = backOwner(), calls: string[] = [], contexts: string[] = [];
  let disposed = 0, complete = 0;
  const controllers: any[] = [];
  const controller = () => {const value = {phase: 'pin', disposed: false, getSnapshot: () => ({phase: value.phase}), subscribe: () => () => {}, dispose: () => {if (value.disposed) return; value.disposed = true; disposed++; calls.push('dispose-recovery');}}; controllers.push(value); return value;};
  class SignIn {
    constructor() {calls.push('create');}
    async inspect() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {}
    serverDescriptor() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    async signInLocal() {calls.push('credentials'); return {kind: 'profiles', profiles: [profile]};}
    async chooseProfile(id: string, pin?: string) {calls.push(`select:${id}:${pin}`); if (pin !== '1234') throw new Error('profile_pin_invalid');}
    profileRecovery(id: string) {calls.push(`recovery:${id}`); return controller();}
    dispose() {calls.push('dispose');}
  }
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
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, '../../kit/accounts/AccountForm': {AccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, '../../pages/touch/accounts/SignInPage': {SignInPage: 'PhoneAccountSignIn'},
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: back.api, Platform: {isTV: false}, Dimensions: {get: () => ({width: 390, height: 844})}, NativeModules: {}, View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    'react-native-qrcode-svg': {default: 'QR'},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src': {},
    '../../../../../packages/client-core/src/sign-in': {},
    '../../../../../packages/client-core/src/route-identity': {routeOrigin},
    '../../../../../packages/client-core/src/server-address': serverAddress,
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: (_: unknown, context: string) => {contexts.push(context); return {body: `${context} failure`, silent: false};}},
    '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateLayout: 'Layout'},
    '../../kit/controls': {FormError, Button: 'Button', Focusable: 'Focusable', TextField: 'Field'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {}}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn},
    '../../platform/portico-sign-in': {}, '../../platform/server-connections': {browseNearby: async () => []},
    '../../platform/tv-sign-in': {}, '../../platform/account-authority': {}, '../../tv/auth/code-retry': {useCodeRetry: () => {}}, '../../tv/auth': {},
    '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'},
    './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './PhoneFindServer': {PhoneFindServer: 'PhoneFindServer'}, '../../pages/touch/accounts/ServerSignInPage': {ServerSignInPage: 'PhoneServerSignIn'},
    './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, '../../pages/touch/accounts/ProfilePINPage': {ProfilePINPage: 'PhoneProfilePIN'}, '../../pages/touch/accounts/ProfilesPage': {ProfilesPage: 'PhoneWhosWatching'}, '../../shell/after-entry': {afterEntry: {request() {}, clear() {}, take() {}}}, './RememberProfileRow': {RememberProfileRow: 'Remember'},
    './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './NativeSetup': {NativeSetup: 'NativeSetup'},
    './directory-chooser': {directoryRows: () => [], directoryFailure: () => 'other', directoryOfflineBody: () => ({key: 'web.chooser.unreachableBodyNever'})},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {complete++;}});});
  await act(() => host.mount());
  try {
    await act(() => tree.props.onLocal());
    await act(() => tree.props.onAddressChange('https://verified.example'));
    await act(() => tree.props.onContinue());
    await act(() => {tree.props.onUsernameChange('person'); tree.props.onPasswordChange('test password');});
    await act(() => tree.props.onSignIn());
    await act(() => parts(tree).find(n => n.type === 'Picker').props.onSelect(profile.id));
    assert.equal(tree.type, 'PhoneProfilePIN', 'the protected profile uses the shared phone PIN screen');
    assert.equal(typeof tree.props.onForgot, 'function', 'it offers Forgot PIN');
    const retiredPIN = tree.props;

    await act(() => tree.props.onForgot());
    assert.equal(tree.type, 'Recovery');
    assert.equal(tree.props.profileName, profile.name);
    assert.equal(tree.props.username, 'person');
    assert.equal(tree.props.controller, controllers[0]);
    const stale = tree.props;
    await act(() => stale.onOpen('1234'));
    assert.equal(complete, 0, 'a presenter cannot open before the live controller is done');
    assert.ok(calls.includes('recovery:locked'));
    assert.equal(calls.filter(call => call === 'dispose').length, 0, 'opening recovery keeps the sign-in operation');
    assert.equal(disposed, 0);

    await act(() => tree.props.onClose());
    assert.equal(tree.type, 'PhoneProfilePIN');
    assert.equal(tree.props.value, '');
    assert.equal(disposed, 1, 'closing recovery retires its controller');
    await act(() => {retiredPIN.onChange('1234'); retiredPIN.onSubmit('1234');});
    assert.equal(tree.props.value, '', 'callbacks from the PIN form before recovery cannot edit its successor');
    assert.equal(complete, 0, 'the retired form cannot submit its PIN');

    await act(() => tree.props.onForgot());
    const successor = tree.props;
    controllers[0].phase = 'done';
    await act(() => {stale.onOpen('1234'); stale.onClose();});
    assert.equal(tree.props.controller, controllers[1], 'stale callbacks preserve the successor');
    assert.equal(controllers[1].disposed, false);
    assert.equal(complete, 0);
    controllers[1].phase = 'done';
    await act(() => successor.onOpen('1234'));
    const selected = calls.filter(call => call.startsWith('select:')).length;
    await act(() => {successor.onOpen('1234'); successor.onClose(); stale.onOpen('1234');});
    assert.equal(calls.filter(call => call.startsWith('select:')).length, selected, 'retired callbacks cannot select again');
    assert.ok(calls.includes('select:locked:1234'), 'the chosen new PIN opens the profile through the same operation');
    assert.equal(complete, 1);

    await act(() => tree.props.onForgot());
    const replaced = tree.props;
    controllers.at(-1).phase = 'done';
    await act(() => {[...back.listeners][0]!();}); // synchronously retire recovery
    await act(() => {[...back.listeners][0]!();}); // the PIN page back to Who's watching
    await act(() => {[...back.listeners][0]!();}); // reset the sign-in operation
    await act(() => tree.props.onLocal());
    await act(() => tree.props.onAddressChange('https://successor.example'));
    await act(() => tree.props.onContinue());
    await act(() => {tree.props.onUsernameChange('person'); tree.props.onPasswordChange('test password');});
    await act(() => tree.props.onSignIn());
    await act(() => parts(tree).find(n => n.type === 'Picker').props.onSelect(profile.id));
    await act(() => tree.props.onForgot());
    const newController = tree.props.controller;
    await act(() => {replaced.onOpen('1234'); replaced.onClose();});
    assert.equal(tree.props.controller, newController, 'callbacks from the replaced operation cannot clear its successor');
    assert.equal(newController.disposed, false);
    assert.equal(complete, 1);
    const unmounted = tree.props;
    controllers.at(-1).phase = 'done';
    await host.unmount();
    await act(() => {unmounted.onOpen('1234'); unmounted.onClose();});
    assert.equal(complete, 1, 'unmounted recovery cannot select');
  } finally {await host.unmount();}
});

for (const tv of [false, true]) test(`a wrong profile PIN uses its own error and retires its old input on ${tv ? 'TV' : 'phone'}`, async t => {
  const back = backOwner(), contexts: string[] = [];
  const platform = {isTV: false};
  let currentTheme = theme, attempts = 0;
  class SignIn {
    async inspect() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {}
    serverDescriptor() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    async signInLocal() {return {kind: 'profiles', profiles: [profile]};}
    async chooseProfile() {attempts++; throw new Error('profile_pin_invalid');}
    profileRecovery() {throw new Error('unexpected');}
    dispose() {}
  }
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
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, '../../kit/accounts/AccountForm': {AccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, '../../pages/touch/accounts/SignInPage': {SignInPage: 'PhoneAccountSignIn'},
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: back.api, Platform: platform, Dimensions: {get: () => ({width: 390, height: 844})}, NativeModules: {}, View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    'react-native-qrcode-svg': {default: 'QR'},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src': {},
    '../../../../../packages/client-core/src/sign-in': {},
    '../../../../../packages/client-core/src/route-identity': {routeOrigin},
    '../../../../../packages/client-core/src/server-address': serverAddress,
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: (_: unknown, context: string) => {contexts.push(context); return {body: `${context} failure`, silent: false};}},
    '../../kit/theme': {useTheme: () => currentTheme}, '../../kit/states': {StateLayout: 'Layout'},
    '../../kit/controls': {FormError, Button: 'Button', Focusable: 'Focusable', TextField: 'Field'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {}}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn},
    '../../platform/portico-sign-in': {}, '../../platform/server-connections': {browseNearby: async () => []},
    '../../platform/tv-sign-in': {}, '../../platform/account-authority': {}, '../../tv/auth/code-retry': {useCodeRetry: () => {}}, '../../tv/auth': {TVWhosWatching: 'TVProfiles', TVProfilePINForm: 'TVPIN'},
    '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'},
    './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './PhoneFindServer': {PhoneFindServer: 'PhoneFindServer'}, '../../pages/touch/accounts/ServerSignInPage': {ServerSignInPage: 'PhoneServerSignIn'},
    './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, '../../pages/touch/accounts/ProfilePINPage': {ProfilePINPage: 'PhoneProfilePIN'}, '../../pages/touch/accounts/ProfilesPage': {ProfilesPage: 'PhoneWhosWatching'}, '../../shell/after-entry': {afterEntry: {request() {}, clear() {}, take() {}}}, './RememberProfileRow': {RememberProfileRow: 'Remember'},
    './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './NativeSetup': {NativeSetup: 'NativeSetup'},
    './directory-chooser': {directoryRows: () => [], directoryFailure: () => 'other', directoryOfflineBody: () => ({key: 'web.chooser.unreachableBodyNever'})},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete() {}});});
  await act(() => host.mount());
  try {
    await act(() => tree.props.onLocal());
    await act(() => tree.props.onAddressChange('https://verified.example'));
    await act(() => tree.props.onContinue());
    await act(() => {tree.props.onUsernameChange('person'); tree.props.onPasswordChange('test password');});
    await act(() => tree.props.onSignIn());
    const select = parts(tree).find(n => n.type === 'Picker').props.onSelect;
    if (tv) {
      platform.isTV = true;
      currentTheme = createTheme({isTV: true, width: 1920});
      t.mock.timers.enable({apis: ['setTimeout']});
    }
    await act(() => select(profile.id));
    assert.equal(tree.type, tv ? 'TVPIN' : 'PhoneProfilePIN');
    const old = tree.props, setCode = tv ? 'onCodeChange' : 'onChange', code = tv ? 'code' : 'value';
    await act(() => old[setCode]('9999'));
    await act(() => tree.props.onSubmit('9999'));
    assert.equal(contexts.at(-1), 'profiles', 'the picker PIN failure is not presented as a generic sign-in failure');
    assert.ok(nodes(tv ? tree.props.error : tree.props.status)
      .some(n => (n.type === 'Text' || n.type === FormError) && n.props.children === 'profiles failure'));
    if (tv) {
      assert.equal(tree.props.code, '9999', 'TV briefly leaves the refused boxes filled');
      await act(() => tree.props.onCodeChange('1'));
      await act(() => t.mock.timers.tick(400));
      assert.equal(tree.props.code, '1', 'a new digit survives the old refusal deadline');
      await act(() => tree.props.onCodeChange('9999'));
      await act(() => tree.props.onSubmit('9999'));
      await act(() => t.mock.timers.tick(400));
      assert.equal(tree.props.code, '', 'untouched refused boxes still clear');
    } else {
      assert.equal(tree.props.value, '');
      assert.equal(tree.props.rejected, true);
    }
    await act(() => tree.props.onBack());
    const picker = tv ? nodes(tree.props.children) : parts(tree);
    await act(() => picker.find(node => node.type === 'Picker').props.onSelect(profile.id));
    const before = attempts;
    await act(() => {old[setCode]('1234'); old.onSubmit('1234'); old.onBack();});
    assert.equal(attempts, before, 'a previous form cannot submit to the same profile reopened on the same server');
    assert.equal(tree.props[code], '', 'the current form stays on its untouched PIN boxes');
  } finally {await host.unmount();}
});
