// @fabric-react
import {createTVMenuPolicy} from '../src-next/platform/tv-menu-policy';
import * as accountWork from '../src-next/platform/account-work';
import * as tvCodeOpening from '../src-next/screens/accounts/tv-code-opening';
import * as tvApprovalCredentials from '../src-next/screens/accounts/tv-approval-credentials';
import * as tvApprovalFinalization from '../src-next/screens/accounts/tv-approval-finalization';
import * as setupOutcome from '../src-next/platform/setup-outcome';
import * as passwordPolicy from '../../../packages/client-core/src/password-strength';
import * as joinLink from '../../../packages/client-core/src/join-link';
import * as invitationCode from '../../../packages/client-core/src/short-invitation-code';
import * as qrScanner from '../src-next/platform/qr-scanner';
import * as profileManagement from '../../../packages/client-core/src/profile-management';
import * as identityClient from '../../../packages/client-core/src/identity-client';
import * as profileAvatars from '../src-next/platform/profile-avatars';
import * as lifetime from '../src-next/screens/settings-live/lifetime';
import * as shellModel from '../src-next/shell/model';
import * as accountFormValues from '../src-next/screens/accounts/account-form-values';
import {profileArtColors} from '@portico/design';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {routeOrigin} from '../../../packages/client-core/src/route-identity';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const profile = {id: 'locked', name: 'Actual locked profile', art: 'mint', primary: false, pinRequired: true};
const theme = createTheme({isTV: true, width: 1920}), menuCalls: string[] = [];
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
function backOwner() {
  const listeners = new Set<() => boolean>();
  return {listeners, api: {addEventListener: (_: string, handler: () => boolean) => {listeners.add(handler); return {remove: () => listeners.delete(handler)};}}};
}
const shared = (back: Awaited<ReturnType<typeof backOwner>>) => ({
  react: {...React, default: React},
  'react-native': {AppState: {currentState: 'active', addEventListener: () => ({remove() {}})}, BackHandler: back.api, Platform: {OS: 'ios', isTV: true}, Dimensions: {get: () => ({width: 1920, height: 1080})}, NativeModules: {}, TVEventControl: {enableTVMenuKey: () => menuCalls.push('enable'), disableTVMenuKey: () => menuCalls.push('disable')}, View: 'View', Text: 'Text', ScrollView: 'ScrollView'},
  '../../../../../packages/i18n/src': {defaultI18n},
  '../../../../../packages/client-core/src/route-identity': {routeOrigin},
  '../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'PIN refused'})},
  '../../kit/theme': {useTheme: () => theme}, '../../kit/states': {StateLayout: 'Layout'},
  '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'Field'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'Picker'},
  '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/Icon': {Icon: 'Icon'},
  '../../../platform/offline-downloads': {offlineDownloads: {getSnapshot: () => ({entries: []}), remove: async () => {}}, offlineOwnerKey: () => 'owner'},
  './sign-out-downloads': {removeCapturedDownloads: async () => 0},
  '../../tv/auth': {TVProfilePINForm: 'PIN', TVForgotPINHandoff: 'Forgot', TVFrontDoor: 'Front', TVCodePresenter: 'Code',
    TVCredentialForm: 'Credentials', TVVerificationForm: 'Factor', TVRequiredSetupHandoff: 'Required', TVWhosWatching: 'WhosWatching'},
    '../../tv/auth/TVIdentityCodeForm': {TVIdentityCodeForm: 'IdentityCode'},
});

test('TV entry Forgot PIN keeps the same account operation and returns to the same pad with empty boxes', async () => {
  const back = backOwner(), calls: string[] = [];
  menuCalls.length = 0;
  const service = {subscribe: () => () => {}, getSnapshot: () => ({phase: 'pending'}), start: async () => {},
    cancel: async () => {calls.push('cancel-code');}, pause: () => {calls.push('pause-code');}};
  class SignIn {
    constructor() {calls.push('create');}
    async inspect() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    needsIdentityCode() {return false;}
    async inspectLocalIdentity() {return {};}
    async confirmLocal() {}
    serverDescriptor() {return {id: 'server', name: 'Verified server', signIn: 'local'};}
    serverOrigin() {return 'https://verified.example';}
    localTVSignIn() {return service;}
    async signInLocal() {calls.push('credentials'); return {kind: 'profiles', profiles: [profile]};}
    async chooseProfile() {calls.push('select');}
    dispose() {calls.push('dispose');}
  }
  const menu = await screenModule(new URL('../src-next/platform/tv-menu.ts', import.meta.url), {
    react: shared(back).react, 'react-native': shared(back)['react-native'], './tv-menu-policy': {createTVMenuPolicy},
  });
  const {AccountEntry} = await screenModule(new URL('../src-next/screens/accounts/AccountEntry.tsx', import.meta.url), {'./ServerNeedsUpdate': {ServerNeedsUpdate: 'ServerNeedsUpdate'}, '../../platform/negotiation': {negotiationRefusal: () => undefined}, '../../shell/StartupBoot': {BootWaitLine: 'BootWaitLine'}, 
    '../../platform/tv-menu': menu,
    './tv-code-opening': tvCodeOpening,
    './tv-approval-credentials': tvApprovalCredentials, './tv-approval-finalization': tvApprovalFinalization,
    '../../../../../packages/client-core/src/join-link': joinLink, '../../../../../packages/client-core/src/short-invitation-code': invitationCode,
    '../../platform/qr-scanner': qrScanner, './LegalAgreement': {LegalAgreement: 'LegalAgreement'}, './PhoneAccountForm': {PhoneAccountForm: 'Layout'}, './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'}, './PhoneAccountSignIn': {PhoneAccountSignIn: 'PhoneAccountSignIn'},
    ...shared(back), '../../../../../packages/client-core/src': {},
    '../../../../../packages/client-core/src/sign-in': {},
    '../../../../../packages/client-core/src/hosted-security': {MFARequiredError: class extends Error {}},
    '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {}}, '../../platform/server-sign-in': {NativeServerSignIn: SignIn},
    '../../platform/portico-sign-in': {}, '../../platform/server-connections': {browseNearby: async () => []},
    '../../platform/tv-sign-in': {}, '../../platform/account-authority': {}, 'react-native-qrcode-svg': {default: 'QR'},
    './DirectoryAccessRefused': {DirectoryAccessRefused: 'Refused'}, '../../platform/hosted-sign-in-unavailable': {HostedSignInUnavailable: class extends Error {}}, './directory-refusal': {directoryAccessRefused: () => false, directoryClockSkew: () => false, directoryIdentityUnconfirmed: () => false}, './TVNoServers': {TVNoServers: 'TVNoServers'}, './EndedMoveCodeAction': {EndedMoveCodeAction: 'EndedMoveCodeAction'}, './TVOfflineServers': {TVOfflineServers: 'TVOfflineServers'}, './PhoneAccountWorkspace': {PhoneAccountWorkspace: 'PhoneAccountWorkspace'}, '../../platform/account-workspace': {NativeAccountWorkspace: class {constructor() {throw new Error('unexpected_account_workspace');}}}, './PhoneNoServers': {PhoneNoServers: 'PhoneNoServers'}, './PhoneFrontDoor': {PhoneFrontDoor: 'Phone'},
    './PhonePINEntry': {PhonePINEntry: 'PhonePIN'},
    './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'},
    '../../../../../packages/client-core/src/password-strength': passwordPolicy,
    '../../platform/setup-outcome': setupOutcome,
    './PhoneOwnerReset': {PhoneOwnerReset: 'PhoneOwnerReset'},
    './PhoneSetupApproval': {PhoneSetupApproval: 'PhoneSetupApproval'},
    './directory-chooser': {directoryRows: () => [], directoryFailure: () => 'other', directoryOfflineBody: () => ({key: 'web.chooser.unreachableBodyNever'})},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = AccountEntry({onComplete: () => {calls.push('complete');}});});
  await act(() => host.mount());
  try {
    assert.deepEqual(menuCalls, ['disable'], 'the root front door permits the native Menu exit');
    await act(() => tree.props.onLocal());
    assert.equal(menuCalls.at(-1), 'enable', 'a non-root entry retains Menu delivery');
    await act(() => nodes(tree.props.beforeActions).find(n => n.type === 'Field').props.onChangeText('https://verified.example'));
    await act(() => tree.props.primaryAction.onPress());
    assert.equal(tree.type, 'Code');
    const codeStatus = tree.props.status, statusChildren = React.Children.toArray(codeStatus.props.children);
    assert.equal(statusChildren.some(child => typeof child === 'string'), false, 'the TV code status never hands a bare string to a View');
    assert.equal(statusChildren.some(child => React.isValidElement<{children?: React.ReactNode}>(child) && child.props.children === defaultI18n.t('activation.codeRefreshes')), false,
      'the refresh note is drawn once, in the code card (auth.html .codecard .ref), not repeated over the buttons');
    await act(() => tree.props.onTypeCredentials());
    assert.equal(tree.type, 'Credentials');
    await act(() => {tree.props.onUsernameChange('person'); tree.props.onPasswordChange('test password');});
    await act(() => tree.props.onSubmit());
    assert.equal(tree.type, 'WhosWatching', 'the TV picker is the centred composition, with no Back or management buttons');
    assert.equal(tree.props.secondaryAction, undefined);
    const picker = nodes(tree.props.children).find(n => n.type === 'Picker');
    // The shared profile region owns the first-face entry (next-family-focus-ids runs
    // the real picker): the caller supplies the explicit TV opening identity and the
    // server's face order, and never invents a remembered preference before focus.
    assert.equal(picker.props.focusScope, 'tv.auth.profiles.server', 'the TV picker opens its explicit profile region');
    assert.equal(picker.props.profiles[0].id, profile.id, 'the fixed entry resolves to the server\'s first offered profile');
    assert.equal(picker.props.preferredProfileId, undefined, 'no remembered face exists before this operation focuses one');
    await act(async () => {picker.props.onFocusChange(profile.id); await host.rerender();});
    const focused = nodes(tree.props.children).find(n => n.type === 'Picker');
    assert.equal(focused.props.preferredProfileId, profile.id, 'the original operation remembers the face it actually focused');
    await act(() => focused.props.onSelect(profile.id));
    assert.equal(tree.type, 'PIN');
    await act(() => tree.props.onCodeChange('12'));
    const baseline = [...calls];
    await act(() => tree.props.onForgotPIN());
    assert.equal(tree.type, 'Forgot');
    assert.equal(tree.props.profileName, profile.name);
    assert.equal(tree.props.url, 'https://verified.example/settings/profile');
    assert.equal(tree.props.body, 'next.auth.tv.forgotPINBody');
    assert.deepEqual(calls, baseline, 'opening the public hand-off has no sign-out, selection, polling or operation retirement');
    await act(() => tree.props.onBack());
    assert.equal(tree.type, 'PIN'); assert.equal(tree.props.profile.id, profile.id); assert.equal(tree.props.code, '');
    await act(() => tree.props.onForgotPIN());
    await act(() => {assert.equal([...back.listeners][0]!(), true);});
    assert.equal(tree.type, 'PIN'); assert.equal(tree.props.code, '');
    assert.deepEqual(calls, baseline);
  } finally {await host.unmount();}
  assert.equal(calls.filter(c => c === 'dispose').length, 1, 'the operation retires only at actual entry unmount');
  assert.equal(menuCalls.at(-1), 'disable', 'unmount returns Menu delivery to the next owner');
});

test('TV profile-switch hand-off preserves its current family, selection controller and server PIN attempts', async () => {
  const back = backOwner(), attempts: unknown[][] = [];
  let created = 0, disposed = 0, exited = 0;
  const current = {sessionFamilyId: 'current-family', viewer: {serverId: 'server', accountId: 'account', profileId: 'current'}};
  const {AccountRoute} = await screenModule(new URL('../src-next/screens/accounts/AccountRoutes.tsx', import.meta.url), {'../../../../../packages/contracts/src/routes': await import('../../../packages/contracts/src/routes.ts'), 
    '@portico/design': {profileArtColors}, './account-form-values': accountFormValues,
    './TVServerChooser': {TVServerChooser: 'TVServerChooser'}, './TVTrustedCertificates': {TVTrustedCertificates: 'TVTrustedCertificates'}, './PhoneServerChooser': {PhoneServerChooser: 'PhoneServerChooser'},
    '../../../../../packages/client-core/src/profile-management': profileManagement, '../../../../../packages/client-core/src/identity-client': identityClient,
    '../../platform/profile-avatars': profileAvatars, '../settings-live/ProfileEditorFields': {ProfileColorPicker: 'ProfileColorPicker', ProfileRestrictionsFields: 'ProfileRestrictionsFields'},
    '../settings-live/lifetime': lifetime, '../../shell/model': shellModel,
    ...shared(back), '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {}}, '../../platform/app-session-storage': {},
    '../../platform/profile-selection': {nativeProfileSelection: (server: string, family: string) => {
      assert.equal(server, 'server'); assert.equal(family, current.sessionFamilyId); created++;
      return {select: async (...args: unknown[]) => {attempts.push(args); throw new Error('refused');}, dispose: () => {disposed++;}};
    }}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}}, './downloads-entry': {createAccountDownloadsEntry: () => {throw new Error('downloads_unavailable_in_fixture');}}, './AccountEntry': {AccountEntry: 'Entry'}, './PhonePINEntry': {PhonePINEntry: 'PhonePIN'},
    '../../../../../packages/client-core/src': {HttpHostedApi: 'HttpHostedApi'},
    '../../platform/profile-recovery': {NativeProfileRecovery: 'Recovery', temporaryRecoveryApi: () => ({})},
    '../../platform/account-authority': {hostedUrl: 'https://hosted.test'},
    '../../platform/server-connections': {nativeNetworkFetch: async () => ({})},
    './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'PhoneProfileRecovery'},
  });
  const authority = {serverId: 'server', accountId: 'account', profileId: 'current', epoch: '1', viewerFence: 'f'.repeat(64)};
  const route = {kind: 'profiles'};
  const snapshot = {authority, route, destination: {kind: 'settings'}, revision: 1};
  const navigationListeners = new Set<() => void>();
  const context = {session: () => current, current: () => true, serverName: 'Verified server',
    api: {baseUrl: 'https://verified.example', request: async () => {throw new Error('no_reads_in_fixture');},
      requestRaw: async () => {throw new Error('no_reads_in_fixture');}, mediaUrl: () => undefined,
      subscribeRoute: () => () => {}, getRouteSnapshot: () => null, getRouteConnection: () => undefined},
    account: {authority: 'local', serverId: 'server', canManage: true,
      capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
      account: {id: 'account', username: 'owner', primaryProfileId: 'profile', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
      profiles: [profile], session: {sessionFamilyId: current.sessionFamilyId}},
    authority: () => authority, subscribeAuthority: () => () => {},
    navigation: {getSnapshot: () => snapshot, isCurrent: () => true, getIntent: () => 'enter', open: () => true,
      subscribe: (listener: () => void) => {navigationListeners.add(listener); return () => {navigationListeners.delete(listener);};},
      back: () => {exited++;}}};
  let tree: any;
  // ProfileRoute's fragment renders its destination first and sheets/dialogs after it.
  const screen = (node: any): any => Array.isArray(node) ? screen(node[0])
    : node?.type === React.Fragment ? screen(node.props.children) : node;
  const host = createFabricHookHost(() => {const opened = AccountRoute({route, context}); tree = screen(opened.type(opened.props));});
  await act(() => host.mount());
  try {
    await act(() => nodes(tree.props.children).find(n => n.type === 'Picker').props.onSelect(profile.id));
    await act(() => tree.props.onSubmit('1234'));
    assert.deepEqual(attempts, [[profile.id, '1234']]); assert.equal(tree.props.code, '');
    // F-PROFILE-28: a wrong PIN leaves the four boxes filled (error colour) for 400 ms, then empties them.
    await act(() => tree.props.onCodeChange('4321'));
    await act(() => tree.props.onSubmit('4321'));
    assert.equal(tree.props.code, '4321', 'the filled boxes are held while the error shows');
    assert.equal(tree.props.error !== undefined, true);
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 450)); await host.rerender();});
    assert.equal(tree.props.code, '', 'the boxes empty after the hold');
    attempts.pop();
    await act(() => tree.props.onCodeChange('99'));
    await act(() => tree.props.onForgotPIN());
    assert.equal(tree.type, 'Forgot'); assert.equal(tree.props.url, 'https://verified.example/settings/profile');
    await act(() => {assert.equal([...back.listeners][0]!(), true);});
    assert.equal(tree.type, 'PIN'); assert.equal(tree.props.code, ''); assert.equal(exited, 0, 'hardware Back returns to the pad, not the previous route');
    await act(() => tree.props.onForgotPIN());
    await act(() => tree.props.onBack());
    assert.equal(tree.type, 'PIN'); assert.equal(tree.props.code, '');
    assert.equal(context.session(), current); assert.equal(created, 1); assert.equal(disposed, 0); assert.equal(exited, 0);
    assert.deepEqual(attempts, [[profile.id, '1234']], 'public hand-off neither selects a profile nor retries/resets server attempts');
    await act(() => tree.props.onSubmit('5678'));
    assert.deepEqual(attempts, [[profile.id, '1234'], [profile.id, '5678']]);
  } finally {await host.unmount();}
  assert.equal(disposed, 1);
});


test('R-16: a PIN submit on a stale context says so instead of returning silently', async () => {
  const back = backOwner(), attempts: unknown[][] = [];
  let live = true, created = 0, disposed = 0, exited = 0;
  const current = {sessionFamilyId: 'current-family', viewer: {serverId: 'server', accountId: 'account', profileId: 'current'}};
  const {AccountRoute} = await screenModule(new URL('../src-next/screens/accounts/AccountRoutes.tsx', import.meta.url), {'../../../../../packages/contracts/src/routes': await import('../../../packages/contracts/src/routes.ts'), 
    '@portico/design': {profileArtColors}, './account-form-values': accountFormValues,
    './TVServerChooser': {TVServerChooser: 'TVServerChooser'}, './TVTrustedCertificates': {TVTrustedCertificates: 'TVTrustedCertificates'}, './PhoneServerChooser': {PhoneServerChooser: 'PhoneServerChooser'},
    '../../../../../packages/client-core/src/profile-management': profileManagement, '../../../../../packages/client-core/src/identity-client': identityClient,
    '../../platform/profile-avatars': profileAvatars, '../settings-live/ProfileEditorFields': {ProfileColorPicker: 'ProfileColorPicker', ProfileRestrictionsFields: 'ProfileRestrictionsFields'},
    '../settings-live/lifetime': lifetime, '../../shell/model': shellModel,
    ...shared(back), '../../platform/account-work': accountWork, '../../platform/accounts': {nativeAccounts: {}}, '../../platform/app-session-storage': {},
    '../../platform/profile-selection': {nativeProfileSelection: (server: string, family: string) => {
      assert.equal(server, 'server'); assert.equal(family, current.sessionFamilyId); created++;
      return {select: async (...args: unknown[]) => {attempts.push(args); throw new Error('refused');}, dispose: () => {disposed++;}};
    }}, '../../shell/route-registry': {nativeShellRoutes: {registerRoute() {}}}, './downloads-entry': {createAccountDownloadsEntry: () => {throw new Error('downloads_unavailable_in_fixture');}}, './AccountEntry': {AccountEntry: 'Entry'}, './PhonePINEntry': {PhonePINEntry: 'PhonePIN'},
    '../../../../../packages/client-core/src': {HttpHostedApi: 'HttpHostedApi'},
    '../../platform/profile-recovery': {NativeProfileRecovery: 'Recovery', temporaryRecoveryApi: () => ({})},
    '../../platform/account-authority': {hostedUrl: 'https://hosted.test'},
    '../../platform/server-connections': {nativeNetworkFetch: async () => ({})},
    './PhoneAccountMoveNotice': {PhoneAccountMoveNotice: 'MoveNotice'}, './PhoneMovePassword': {PhoneMovePassword: 'MovePassword'}, './PhoneMoveCode': {PhoneMoveCode: 'MoveCode'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'PhoneProfileRecovery'},
  });
  const authority = {serverId: 'server', accountId: 'account', profileId: 'current', epoch: '1', viewerFence: 'f'.repeat(64)};
  const route = {kind: 'profiles'};
  const snapshot = {authority, route, destination: {kind: 'settings'}, revision: 1};
  const navigationListeners = new Set<() => void>();
  const context = {session: () => current, current: () => live, serverName: 'Verified server',
    api: {baseUrl: 'https://verified.example', request: async () => {throw new Error('no_reads_in_fixture');},
      requestRaw: async () => {throw new Error('no_reads_in_fixture');}, mediaUrl: () => undefined,
      subscribeRoute: () => () => {}, getRouteSnapshot: () => null, getRouteConnection: () => undefined},
    account: {authority: 'local', serverId: 'server', canManage: true,
      capabilities: {manageAccount: true, managePeople: true, manageServer: true, manageSignIn: true},
      account: {id: 'account', username: 'owner', primaryProfileId: 'profile', role: 'owner', revision: 1, disabled: false, allowedLibraries: []},
      profiles: [profile], session: {sessionFamilyId: current.sessionFamilyId}},
    authority: () => authority, subscribeAuthority: () => () => {},
    navigation: {getSnapshot: () => snapshot, isCurrent: () => true, getIntent: () => 'enter', open: () => true,
      subscribe: (listener: () => void) => {navigationListeners.add(listener); return () => {navigationListeners.delete(listener);};},
      back: () => {exited++;}}};
  let tree: any;
  // ProfileRoute's fragment renders its destination first and sheets/dialogs after it.
  const screen = (node: any): any => Array.isArray(node) ? screen(node[0])
    : node?.type === React.Fragment ? screen(node.props.children) : node;
  const host = createFabricHookHost(() => {const opened = AccountRoute({route, context}); tree = screen(opened.type(opened.props));});
  await act(() => host.mount());
  try {
    await act(() => nodes(tree.props.children).find(n => n.type === 'Picker').props.onSelect(profile.id));
    live = false;
    await act(() => tree.props.onSubmit('1234'));
    assert.deepEqual(attempts, [], 'a stale context never selects');
    assert.notEqual(tree.props.error, undefined, 'the user is told, not left with a silent no-op');
  } finally {await host.unmount();}
});
