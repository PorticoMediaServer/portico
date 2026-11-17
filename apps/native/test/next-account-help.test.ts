// @fabric-react
import * as accountWork from '../src-next/platform/account-work';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as serverAddress from '../../../packages/client-core/src/server-address';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as directoryChooser from '../src-next/screens/accounts/directory-chooser';

test('phone Portico Forgot password hands off directly without a native reset form', async () => {
  const forgotten: (() => boolean)[] = [], stages: string[] = [];
  let linkFails = false;
  class Portico {
    async prepare() {return {password: true, registration: true, recovery: true, providers: []};}
    async forgot(current: () => boolean) {assert.equal(current(), true); forgotten.push(current);}
    async password() {throw new Error('not used');}
    dispose() {}
  }
  const account = {synchronize: async () => {}, getSnapshot: () => ({session: undefined})};
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), {'./ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, '../../shell/StartupBoot': {BootWaitLine: 'BootWaitLine'}, 
    '../../platform/tv-menu': {useTVMenuCapture() {}},
    './tv-code-opening': tvCodeOpening,
    '../../../../../packages/client-core/src/join-link': joinLink,
    '../../../../../packages/client-core/src/short-invitation-code': invitationCode,
    '../../platform/qr-scanner': {qrScannerAvailable: () => false, scanQRCode: async () => undefined},
    './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'PhoneAccountSignIn'},
    './tv-approval-finalization': {}, './tv-approval-credentials': {},
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: {addEventListener: () => ({remove() {}})}, Linking: {openURL: async () => {if (linkFails) throw new Error('no_browser');}}, Platform: {isTV: false},
      NativeModules: {PorticoAccountAuth: {canOpenBrowser: false, canSignInApple: false, bundleId: 'com.example', newRequestId: async () => 'request'}},
      View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src': {HttpHostedApi: class {}},
    '../../../../../packages/client-core/src/sign-in': {}, '../../../../../packages/client-core/src/route-identity': {routeOrigin: (value: string) => value},
    '../../../../../packages/client-core/src/server-address': serverAddress,
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './NativeSetup': {NativeSetup: 'NativeSetup'},
    './directory-chooser': directoryChooser,
    '../../kit/states': {StateLayout: 'Layout'}, '../../kit/controls': {Button: 'Button', TextField: 'Field'}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {accountCredentials: () => account}},
    '../../platform/server-sign-in': {NativeServerSignIn: class {}}, '../../platform/portico-sign-in': {NativePorticoSignIn: Portico},
    '../../platform/server-connections': {browseNearby: async () => [], nativeNetworkFetch: async () => {}},
    '../../platform/tv-sign-in': {}, '../../platform/account-authority': {hostedUrl: 'https://web.example'},
    '../../tv/auth': {TVCredentialForm: 'TVCredentials', TVCodePresenter: 'TVCode', TVVerificationForm: 'TVFactor', TVRequiredSetupHandoff: 'TVRequired',
      TVFrontDoor: 'TVFrontDoor', TVProfilePINForm: 'TVPIN', TVForgotPINHandoff: 'TVForgot'},
    './PhonePINEntry': {PhonePINEntry: 'PhonePIN'}, './PhoneProfilePIN': {PhoneProfilePIN: 'PhoneProfilePIN'}, './PhoneWhosWatching': {PhoneWhosWatching: 'PhoneWhosWatching'}, '../../shell/after-entry': {afterEntry: {request() {}, clear() {}, take() {}}}, './RememberProfileRow': {RememberProfileRow: 'Remember'}, './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'}, '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'}, './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneFindServer': {PhoneFindServer: 'PhoneFindServer'}, './PhoneServerSignIn': {PhoneServerSignIn: 'PhoneServerSignIn'}, 'react-native-qrcode-svg': {default: 'QR'},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {}, onStageChange: stage => {stages.push(stage);}});});
  await act(() => host.mount());
  try {
    // The first screen is the Portico Account page itself (auth.html frame 4); Forgot password? prepares on use.
    assert.equal(tree.type, 'PhoneAccountSignIn'); assert.equal(tree.props.passwordEnabled, true); assert.equal(tree.props.recovery, true);
    // R-21: a link that cannot open is shown through the sign-in error state, never swallowed.
    assert.equal(tree.props.error, null);
    linkFails = true; await act(() => tree.props.onOpenLink('https://web.example/terms')); linkFails = false;
    assert.notEqual(tree.props.error, undefined);
    await act(() => tree.props.onForgot());
    assert.equal(forgotten.length, 1, 'the prepared account source opens hosted reset once');
    assert.equal(typeof forgotten[0], 'function', 'handoff receives a lifetime guard, never an ignored email');
    assert.equal(tree.type, 'PhoneAccountSignIn'); assert.equal(tree.props.recovery, true);
    assert.equal(stages.includes('forgot'), false, 'no intermediate native email form');
    assert.equal(stages.includes('forgot-sent'), false, 'no claim that this app sent an email');
    await act(() => tree.props.onLocal());
    assert.equal(tree.type, 'PhoneFindServer');
  } finally {await host.unmount();}
});
