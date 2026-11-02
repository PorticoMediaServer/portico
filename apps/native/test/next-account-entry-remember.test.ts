// @fabric-react
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

const theme = createTheme({isTV: false, width: 390});
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
function backOwner() {
  const listeners = new Set<() => boolean>();
  return {listeners, api: {addEventListener: (_: string, handler: () => boolean) => {listeners.add(handler); return {remove: () => listeners.delete(handler)};}}};
}


const open = {id: 'open', name: 'Open profile', art: 'blue', primary: true, pinRequired: false};
const locked = {id: 'locked', name: 'Locked profile', art: 'mint', primary: false, pinRequired: true};

/** The phone sign-in picker (Who's watching after a password), against a server that does or doesn't let a device remember a profile. */
async function entry(canRemember: boolean) {
  const back = backOwner(), calls: string[] = [], contexts: string[] = [];
  let complete = 0;
  class SignIn {
    constructor() {calls.push('create');}
    async inspect() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {}
    serverDescriptor() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    async signInLocal() {return {kind: 'profiles', profiles: [open, locked], canRemember};}
    async chooseProfile(id: string, pin?: string, remember?: boolean) {
      calls.push(`select:${id}:${pin ?? ''}:${remember === true}`);
      if (pin !== undefined && pin !== '1234') throw new Error('profile_pin_invalid');
    }
    profileRecovery() {throw new Error('not used');}
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
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'PhoneAccountSignIn'},
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
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'Field'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
    '../../kit/Icon': {Icon: 'Icon'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {}}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn},
    '../../platform/portico-sign-in': {}, '../../platform/server-connections': {browseNearby: async () => []},
    '../../platform/tv-sign-in': {}, '../../platform/account-authority': {}, '../../tv/auth': {},
    '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'},
    './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './PhoneFrontDoor': {PhoneFrontDoor: 'Phone'},
    './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, './RememberProfileRow': {RememberProfileRow: 'Remember'},
    './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './NativeSetup': {NativeSetup: 'NativeSetup'},
    './directory-chooser': {directoryRows: () => [], directoryFailure: () => 'other', directoryOfflineBody: () => ({key: 'web.chooser.unreachableBodyNever'})},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {complete++;}});});
  await act(() => host.mount());
  await act(() => tree.props.onLocal());
  await act(() => nodes(tree.props.beforeActions).find(n => n.type === 'Field').props.onChangeText('https://verified.example'));
  await act(() => tree.props.primaryAction.onPress());
  const credentials = nodes(tree.props.beforeActions).filter(n => n.type === 'Field');
  await act(() => {credentials[0].props.onChangeText('person'); credentials[1].props.onChangeText('test password');});
  await act(() => tree.props.primaryAction.onPress());
  const remember = () => nodes(tree.props.beforeActions).find(n => n.type === 'Remember');
  const picker = () => nodes(tree.props.beforeActions).find(n => n.type === 'Picker');
  return {calls, host, tree: () => tree, remember, picker, complete: () => complete};
}

test('the sign-in picker offers Remember this profile on this device under the faces only where the server allows it', async () => {
  for (const allowed of [true, false]) {
    const f = await entry(allowed);
    try {
      assert.equal(!!f.remember(), allowed);
      if (allowed) assert.equal(f.remember().props.checked, false, 'unticked until chosen');
    } finally {await f.host.unmount();}
  }
});

test('a ticked no-PIN profile is remembered when chosen; unticked it is not', async () => {
  for (const ticked of [true, false]) {
    const f = await entry(true);
    try {
      if (ticked) await act(() => f.remember().props.onChange(true));
      await act(() => f.picker().props.onSelect('open'));
      assert.deepEqual(f.calls.filter(call => call.startsWith('select:')), [`select:open::${ticked}`]);
      assert.equal(f.complete(), 1);
    } finally {await f.host.unmount();}
  }
});

test('a ticked PIN profile sends nothing until its PIN: the tick carries to the PIN page and goes with a correct PIN only', async () => {
  const f = await entry(true);
  try {
    await act(() => f.remember().props.onChange(true));
    await act(() => f.picker().props.onSelect('locked'));
    assert.deepEqual(f.calls.filter(call => call.startsWith('select:')), [], 'choosing a PIN profile sends nothing');
    assert.equal(f.remember().props.checked, true, 'the PIN page arrives ticked');
    const pin = nodes(f.tree().props.beforeActions).find(n => n.type === 'PhonePIN');
    await act(() => pin.props.onComplete('0000'));
    assert.equal(f.complete(), 0, 'a wrong PIN opens nothing');
    await act(() => pin.props.onComplete('1234'));
    assert.deepEqual(f.calls.filter(call => call.startsWith('select:')), ['select:locked:0000:true', 'select:locked:1234:true']);
    assert.equal(f.complete(), 1);
  } finally {await f.host.unmount();}
});

test('the PIN page offers no checkbox where the server does not remember, and sends no remember', async () => {
  const f = await entry(false);
  try {
    await act(() => f.picker().props.onSelect('locked'));
    assert.equal(f.remember(), undefined);
    const pin = nodes(f.tree().props.beforeActions).find(n => n.type === 'PhonePIN');
    await act(() => pin.props.onComplete('1234'));
    assert.deepEqual(f.calls.filter(call => call.startsWith('select:')), ['select:locked:1234:false']);
  } finally {await f.host.unmount();}
});
