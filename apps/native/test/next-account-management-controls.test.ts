// @fabric-react
import {profileEditorFields} from './helpers/profile-editor-fields.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {fabricHost, React as FabricReact} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import * as values from '../src-next/screens/accounts/account-form-values.ts';

const theme = {isTV: false, colors: {text: '#fff', textSecondary: '#ccc', textTertiary: '#aaa', ink: '#071018', danger: '#d33', lineStrong: '#678', raised: '#123'}, type: {body: {}, bodyStrong: {}, caption: {}, captionStrong: {}}};
const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children), ...nodes(value.props.beforeActions), ...nodes(value.props.footer)] : [];
const button = (tree: any, label: string) => nodes(tree).find(value => value.type === 'Button' && [value.props.children].flat().includes(label));
const byID = (tree: any, id: string) => nodes(tree).find(value => value.props.testID === id);

async function fixture() {
  const calls: any[][] = [], factories: Record<string, any> = {};
  const profiles = [{id: 'profile', name: 'Profile', art: 'violet', primary: true, revision: 1, pinRevision: 1, pinRequired: false, position: 0}];
  const restrictions = {profileId: 'profile', revision: 1, ratingSystem: 'ratings', maximumAgeRating: '', maximumAge: -1, allowUnrated: true, blockedLabels: [], allowDownloads: true, allowLiveTv: false, allowDvr: false, allowWatchTogether: true};
  const session = {sessionFamilyId: 'family', viewer: {serverId: 'server', accountId: 'account', profileId: 'profile'}};
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: '1', viewerFence: 'fence'};
  const route = {kind: 'profiles'}, nav = {route, authority, revision: 1};
  const {AccountRoute, ServersRoute} = await screenModule(new URL('../src-next/screens/accounts/AccountRoutes.tsx', import.meta.url), {'../../../../../packages/contracts/src/routes': await import('../../../packages/contracts/src/routes.ts'), 
    react: {...React, default: React},
    'react-native': {NativeModules: {}, Platform: {isTV: false, OS: 'ios'}, Dimensions: {get: () => ({width: 390, height: 844})}, BackHandler: {addEventListener: () => ({remove() {}})}, Text: 'Text', View: 'View'},
    '@portico/design': {profileArtColors: {blue: 'blue', violet: 'violet', mint: 'mint', coral: 'coral', gold: 'gold', slate: 'slate', rose: 'rose', sky: 'sky'}},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({title: 'Error', body: error.message})},
    '../../../../../packages/client-core/src/profile-management': {PROFILE_ART: ['blue', 'violet', 'mint', 'coral', 'gold', 'slate', 'rose', 'sky'],
      ManagedProfilesClient: class {async list() {return profiles;} async create(...args: unknown[]) {calls.push(['create', ...args]);} async edit(...args: unknown[]) {calls.push(['edit', ...args]);}}},
    '../../../../../packages/client-core/src/identity-client': {IdentityClient: class {
      async avatars() {return [];} async restrictions() {return restrictions;} async ratingSystems() {return [{id: 'ratings', values: []}];}
      async saveRestrictions(original: any, change: any) {calls.push(['restrictions', original, change]); return {...original, ...change, revision: original.revision + 1};}
    }},
    '../../../../../packages/client-core/src': {HttpHostedApi: class {}},
    '../../../../../packages/client-core/src/route-identity': {routeOrigin: (value: string) => new URL(value).origin},
    '../../platform/accounts': {nativeAccounts: {snapshot: async () => ({selectedServerId: 'server', servers: []}),
      porticoTVConsent: async () => ({review: async (code: string) => {calls.push(['review', code]); return {deviceName: 'TV', platform: 'TV', appVersion: '1', userCode: code};}, dispose() {}})}},
    '../../platform/app-session-storage': {nativeAppSessions: {}, subscribeNativeAppSessions: () => () => {}},
    '../../platform/profile-selection': {nativeProfileSelection: () => ({dispose() {}})},
    '../../platform/profile-recovery': {NativeProfileRecovery: class {}, temporaryRecoveryApi: () => ({})},
    '../../platform/account-authority': {hostedUrl: 'https://accounts.example.invalid'},
    '../../platform/profile-avatars': {profileAvatarUpload: () => ({}), profilePictureSource: () => undefined, profilePicturePickerAvailable: () => false},
    '../../platform/server-connections': {nativeNetworkFetch: () => {throw new Error('unexpected network');}},
    '../settings-live/ProfileEditorFields': await profileEditorFields(React, theme),
    '../settings-live/lifetime': {NativeScreenLifetime: class {current = () => true; dispose() {} request = (work: () => unknown) => work();}},
    '../../shell/model': {sameAuthority: (a: unknown, b: unknown) => a === b},
    '../../shell/route-registry': {nativeShellRoutes: {registerRoute(kind: string, factory: unknown) {factories[kind] = factory;}}},
    '../../kit/states': {StateLayout: 'StateLayout', StateView: 'StateView'}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop', ProfilePicker: 'ProfilePicker'},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../../platform/offline-downloads': {offlineDownloads: {}, offlineOwnerKey: () => 'owner'}, '../../kit/theme': {useTheme: () => theme},
    './AccountEntry': {AccountEntry: 'AccountEntry'}, './downloads-entry': {createAccountDownloadsEntry: () => ({dispose() {}})},
    './PhonePINEntry': {PhonePINEntry: 'PhonePINEntry'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'PhoneProfileRecovery'},
    './PhoneServerChooser': {PhoneServerChooser: 'PhoneServerChooser'}, './TVTrustedCertificates': {TVTrustedCertificates: 'TVTrustedCertificates'}, './TVServerChooser': {TVServerChooser: 'TVServerChooser'},
    './sign-out-downloads': {removeCapturedServerDownloads: async () => {}}, '../../tv/auth': {TVProfilePINForm: 'TVProfilePINForm', TVForgotPINHandoff: 'TVForgotPINHandoff'},
    './account-form-values': values,
  });
  const context = {account: {profiles, canManage: true, capabilities: {manageAccount: true}, account: {id: 'account', username: 'person', primaryProfileId: 'profile'}, serverId: 'server'}, session: () => session,
    authority: () => authority, current: () => true, subscribeAuthority: () => () => {}, api: {baseUrl: 'https://server.example.invalid'},
    navigation: {getSnapshot: () => nav, subscribe: () => () => {}, back() {}}};
  let tree: any;
  const host = createFabricHookHost(() => {const child = AccountRoute({context, route}); tree = child.type(child.props);});
  return {AccountRoute, ServersRoute, context, calls, host, tree: () => tree, factories, nav};
}

test('profile Add and Rename submit the chosen color and preselect the current color', async () => {
  const f = await fixture(); await act(() => f.host.mount());
  try {
    await act(() => button(f.tree(), 'profile.add').props.onPress());
    await act(() => byID(f.tree(), 'profile-add-name').props.onChangeText('Cinema'));
    const picker = byID(f.tree(), 'profile-add-colors'); assert.equal(picker.props.value, 'blue');
    await act(() => picker.props.onChange('mint'));
    await act(() => button(byID(f.tree(), 'profile-add'), 'action.save').props.onPress());
    assert.deepEqual(f.calls.find(call => call[0] === 'create'), ['create', 'Cinema', 'mint']);
    await act(() => byID(f.tree(), 'profile-rename').props.onPress());
    const rename = byID(f.tree(), 'profile-rename-colors'); assert.equal(rename.props.value, 'violet');
    await act(() => rename.props.onChange('gold'));
    const dialog = nodes(f.tree()).find(value => value.type === 'Dialog' && value.props.testID === 'profile-rename');
    await act(() => button(dialog, 'action.save').props.onPress());
    assert.deepEqual(f.calls.find(call => call[0] === 'edit')?.[2], {name: 'Profile', art: 'gold'});
  } finally {await f.host.unmount();}
});

test('PIN editor has New and Repeat cell rows and keeps the mismatch guard', async () => {
  const f = await fixture(); await act(() => f.host.mount());
  try {
    await act(() => byID(f.tree(), 'profile-pin').props.onPress());
    assert.equal(byID(f.tree(), 'profile-pin-new').type, 'PhonePINEntry');
    assert.equal(byID(f.tree(), 'profile-pin-again').type, 'PhonePINEntry');
    assert.equal(byID(f.tree(), 'profile-pin-again').props.autoFocus, false);
    await act(() => {byID(f.tree(), 'profile-pin-new').props.onChange('1234'); byID(f.tree(), 'profile-pin-again').props.onChange('4567');});
    await act(() => button(byID(f.tree(), 'profile-pin-edit'), 'action.save').props.onPress());
    assert.equal(f.calls.filter(call => call[0] === 'edit').length, 0);
    assert.ok(nodes(byID(f.tree(), 'profile-pin-edit')).some(value => value.props.children === 'profile.pinMismatch'));
    await act(() => byID(f.tree(), 'profile-pin-again').props.onChange('1234'));
    await act(() => button(byID(f.tree(), 'profile-pin-edit'), 'action.save').props.onPress());
    assert.deepEqual(f.calls.find(call => call[0] === 'edit')?.[2], {pin: '1234'});
  } finally {await f.host.unmount();}
});

test('Hidden labels picker saves the chosen labels with the original restriction revision and feature flags', async () => {
  const f = await fixture(); await act(() => f.host.mount()); let sheet: any;
  await act(() => byID(f.tree(), 'profile-limits').props.onPress());
  const element = nodes(f.tree()).find(value => typeof value.type === 'function' && value.type.name === 'ProfileLimitsSheet');
  // The sheet draws ProfileRestrictionsFields once its draft has loaded. Mount the fields as their
  // own child component (their hooks are theirs), and read the sheet with the fields' drawn tree.
  let fieldsTree: any;
  const Fields = ({fields}: {fields: any}) => {fieldsTree = fields.type(fields.props); return null;};
  const Sheet = () => {
    sheet = element.type(element.props);
    const fields = nodes(sheet).find(value => typeof value.type === 'function' && value.type.name === 'ProfileRestrictionsFields');
    return fields ? FabricReact.createElement(Fields, {fields}) : null;
  };
  const view = () => ({...sheet, props: {...sheet.props, children: [sheet.props.children, fieldsTree]}});
  const host = await fabricHost(); await act(() => host.render(FabricReact.createElement(Sheet)));
  try {
    assert.equal(byID(view(), 'profile-hidden-labels').props.description, 'web.dvr.none');
    await act(() => byID(view(), 'profile-hidden-labels').props.onPress());
    const input = () => nodes(byID(view(), 'profile-hidden-labels-picker')).find(value => value.type === 'TextField');
    await act(() => input().props.onChangeText('Horror'));
    await act(() => button(byID(view(), 'profile-hidden-labels-picker'), 'web.metadata.addLabelChip').props.onPress());
    await act(() => button(byID(view(), 'profile-hidden-labels-picker'), 'action.done').props.onPress());
    assert.equal(byID(view(), 'profile-hidden-labels').props.description, 'Horror');
    await act(() => button(view().props.footer, 'limits.save').props.onPress());
    const saved = f.calls.find(call => call[0] === 'restrictions');
    assert.equal(saved?.[1].revision, 1);
    assert.deepEqual(saved?.[2], {ratingSystem: 'ratings', maximumAgeRating: '', allowUnrated: true, blockedLabels: ['Horror'], allowDownloads: true, allowLiveTv: false, allowDvr: false, allowWatchTogether: true});
  } finally {await host.unmount(); await f.host.unmount();}
});

test('Play on link intent starts at approval choice, normalizes entry and gates review', async () => {
  const f = await fixture();
  const route = f.AccountRoute({context: f.context, route: {kind: 'servers', initialPanel: 'consent-kind'}});
  assert.equal(route.props.initialPanel, 'consent-kind');
  let tree: any;
  const host = createFabricHookHost(() => {tree = f.ServersRoute(route.props);}); await act(() => host.mount());
  try {
    assert.equal(tree.props.title, 'settings.linkTV');
    await act(() => button(tree, 'account.portico').props.onPress());
    let input = nodes(tree).find(value => value.type === 'TextField');
    await act(() => input.props.onChangeText('abcd-efg'));
    assert.equal(tree.props.primaryAction.disabled, true);
    input = nodes(tree).find(value => value.type === 'TextField');
    await act(() => input.props.onChangeText('ab!cd efgh'));
    assert.equal(nodes(tree).find(value => value.type === 'TextField').props.value, 'ABCD-EFGH');
    assert.equal(tree.props.primaryAction.disabled, false);
    await act(() => tree.props.primaryAction.onPress());
    assert.deepEqual(f.calls.find(call => call[0] === 'review'), ['review', 'ABCD-EFGH']);
  } finally {await host.unmount();}
});

test('each committed account route opening gets its own component key', async () => {
  const f = await fixture(), view = f.factories.servers(f.context);
  const first = view.render({kind: 'servers'}), sameOpening = view.render({kind: 'servers'});
  assert.equal(first.key, sameOpening.key, 'panel-local state keeps the current opening');
  f.nav.revision++;
  const next = view.render({kind: 'servers', initialPanel: 'consent-kind'});
  assert.notEqual(first.key, next.key, 'a same-kind navigation opening remounts the original panel intent');
});

test('touch server chooser receives the existing certificate store and confirmation presenter', async () => {
  const f = await fixture(); let tree: any;
  const host = createFabricHookHost(() => {tree = f.ServersRoute({context: f.context});}); await act(() => host.mount());
  try {
    const chooser = nodes(tree).find(value => value.type === 'PhoneServerChooser');
    assert.equal(chooser.props.certificates.type, 'TVTrustedCertificates');
    assert.equal(chooser.props.certificates.props.context, f.context);
  } finally {await host.unmount();}
});

test('PIN cell presenter can edit New or Repeat without submitting and keeps the numeric native input', async () => {
  const {PhonePINEntry} = await screenModule(new URL('../src-next/screens/accounts/PhonePINEntry.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {View: 'View', TextInput: 'TextInput', StyleSheet: {absoluteFill: {position: 'absolute'}}},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../kit/theme': {useTheme: () => theme},
  });
  let tree: any;
  const host = createFabricHookHost(() => {const [value, setValue] = React.useState(''); tree = PhonePINEntry({value, onChange: setValue, label: 'Repeat PIN', autoFocus: false, testID: 'repeat'});});
  await act(() => host.mount());
  try {
    let input = nodes(tree).find(value => value.type === 'TextInput');
    assert.equal(input.props.keyboardType, 'number-pad'); assert.equal(input.props.autoFocus, false); assert.equal(input.props.accessibilityLabel, 'Repeat PIN');
    await act(() => input.props.onChangeText('12a345'));
    input = nodes(tree).find(value => value.type === 'TextInput'); assert.equal(input.props.value, '1234');
    assert.equal(nodes(tree).filter(value => value.props.testID?.startsWith('repeat-box-')).length, 4);
  } finally {await host.unmount();}
});
