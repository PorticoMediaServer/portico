// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import * as React from 'react';
import {ApiError} from '../../../packages/client-core/src';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {profilePinFailureText} from '../src-next/screens/accounts/profile-pin-failure';
import {createTheme} from '../src-next/kit/theme.ts';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

// Android walk, 31 Oct: a wrong profile PIN read "Your server sent something Portico couldn't
// read", because the shared presenter takes any `*_invalid` code for an unreadable reply.

test('a wrong profile PIN says the PIN is not right', () => {
  const wrong = new ApiError(403, 'profile_pin_invalid', 'The request could not be completed.');
  assert.equal(presentError(wrong, 'profiles').body, defaultI18n.t('accounts.profiles.pinFailed'));
  assert.equal(profilePinFailureText(wrong), defaultI18n.t('accounts.profiles.pinFailed'));
});

test('a locked PIN says how long to wait when the server said', () => {
  const locked = Object.assign(new ApiError(403, 'profile_pin_locked', 'Locked.'), {retryAfterSeconds: 29.2});
  assert.equal(profilePinFailureText(locked), defaultI18n.t('accounts.profiles.pinLocked', {seconds: 30}));
  assert.equal(profilePinFailureText(new ApiError(403, 'profile_pin_locked', 'Locked.')), defaultI18n.t('accounts.profiles.pinFailed'));
});

test('anything else keeps the shared presentation', () => {
  for (const other of [new ApiError(503, 'server_busy', 'Busy.'), new Error('x'), undefined, null, 'profile_pin_invalid'])
    assert.equal(profilePinFailureText(other), undefined);
});

test('useLifecycleProfileGate presents PIN-specific failure text (wrong & locked) before shared presentation', async () => {
  let submitSnapshot: any = {phase: 'idle'};
  const pickSnapshot = {phase: 'idle'};
  let lifecycleSnapshot: any = {
    state: {
      kind: 'enteringPin',
      server: {serverId: 'srv', name: 'Home', signIn: 'local'},
      profile: {id: 'p1', name: 'Alex', primary: false, pinRequired: true},
      canRemember: true,
      rejected: false,
    },
  };
  const lifecycle: any = {
    subscribe: () => () => {},
    getSnapshot: () => lifecycleSnapshot,
    actions: {
      pickProfile: {subscribe: () => () => {}, getSnapshot: () => pickSnapshot, start() {}},
      submitPin: {subscribe: () => () => {}, getSnapshot: () => submitSnapshot, start() {}, reset() {}},
    },
    otherProfiles() {},
    cancelSwitch() {},
  };
  const {useLifecycleProfileGate} = await screenModule(new URL('../src-next/pages/shared/accounts/useLifecycleProfileGate.ts', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../../packages/client-core/src': {HttpHostedApi: class {}},
    '../../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../../../platform/native-session-lifecycle': {nativeSessionLifecycle: () => ({lifecycle, ports: {connection: () => undefined}})},
    '../../../platform/profile-recovery': {NativeProfileRecovery: class {}, temporaryRecoveryApi: () => ({})},
    '../../../platform/accounts': {nativeAccounts: {}},
    '../../../platform/account-authority': {hostedUrl: 'https://hosted.test'},
    '../../../platform/server-connections': {nativeNetworkFetch: async () => ({})},
    '../../../shell/after-entry': {afterEntry: {clear() {}, request() {}}},
    '../../../screens/accounts/profile-pin-failure': {profilePinFailureText},
    '../../../platform/tv-menu': {useTVMenuCapture() {}},
    '../../../kit/navigation/back': {registerBack: () => () => {}},
  });

  let view: any;
  const host = createFabricHookHost(() => {view = useLifecycleProfileGate();});
  try {
    const locked = Object.assign(new ApiError(403, 'profile_pin_locked', 'Locked.'), {retryAfterSeconds: 29.2});
    submitSnapshot = {phase: 'failed', failure: {cause: locked}};
    await act(() => host.mount());
    assert.equal(view.failureText, defaultI18n.t('accounts.profiles.pinLocked', {seconds: 30}), 'locked PIN shows retry-after message');

    submitSnapshot = {phase: 'failed', failure: {cause: new ApiError(403, 'profile_pin_invalid', 'Wrong.')}};
    await act(() => host.rerender());
    assert.equal(view.failureText, defaultI18n.t('accounts.profiles.pinFailed'), 'wrong PIN shows pinFailed message');
  } finally {
    await host.unmount();
  }
});

test('ProfilesRoutePage renders PIN-specific text on both TV and touch PIN & switch stages', async () => {
  const locked = Object.assign(new ApiError(403, 'profile_pin_locked', 'Locked.'), {retryAfterSeconds: 29.2});
  const expectedLocked = defaultI18n.t('accounts.profiles.pinLocked', {seconds: 30});
  const targetProfile = {id: 'p2', name: 'Jordan', art: 'mint', primary: false, pinRequired: true};
  let viewState: any;

  const {ProfilesRoutePage} = await screenModule(new URL('../src-next/pages/shared/accounts/ProfilesRoutePage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../../packages/client-core/src/profile-management': {nextProfileArt: () => 'blue'},
    '../../../kit/controls': {Button: 'Button', TextField: 'TextField', FormError: 'FormError'},
    '../../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog'},
    '../../../kit/settings/ProfileEditorFields': {ProfileColorPicker: 'ProfileColorPicker'},
    '../../../kit/accounts/PhonePINEntry': {PhonePINEntry: 'PhonePINEntry'},
    '../../../kit/accounts/ProfileActionRow': {ProfileActionRow: 'ProfileActionRow'},
    '../../../kit/accounts/ProfilesParts': {
      ProfileActionsBlock: 'ProfileActionsBlock', ProfileDeletionBody: 'ProfileDeletionBody', ProfileDeletionText: 'ProfileDeletionText',
      ProfileDialogFooter: 'ProfileDialogFooter', ProfileEmptyBody: 'ProfileEmptyBody', ProfileFieldLabel: 'ProfileFieldLabel',
      ProfileList: 'ProfileList', ProfileListRow: 'ProfileListRow', ProfileManagementBlock: 'ProfileManagementBlock',
      ProfileNote: 'ProfileNote', ProfileNotice: 'ProfileNotice', ProfilePinFields: 'ProfilePinFields', ProfileRepeatField: 'ProfileRepeatField',
    },
    '../../../platform/profile-avatars': {profilePicturePickerAvailable: () => false},
    '../../../screens/accounts/profile-pin-failure': {profilePinFailureText},
    '../../../tv/auth': {TVProfileSwitchPage: 'TVProfileSwitchPage'},
    '../../touch/accounts/ProfileSwitchPage': {ProfileSwitchPage: 'ProfileSwitchPage', ProfileSwitchStatus: 'ProfileSwitchStatus'},
    '../../tv/accounts/ProfilesPage': {ProfilesPage: 'TVProfilesPage'},
    '../../../pages/shared/accounts/ProfileGateView': {ProfilePINStage: (props: any) => React.createElement('PINStage', props)},
    '../../../pages/shared/accounts/ProfileLimitsSheet': {ProfileLimitsSheet: 'ProfileLimitsSheet'},
    '../../../pages/shared/accounts/useProfiles': {useProfiles: () => viewState},
  });

  const baseView = {
    canRefresh: false, busy: false, busyRef: {current: false}, back() {}, backRef: {current() {}},
    face: () => ({initial: 'J', color: 'mint'}), avatarSource: () => undefined,
    profiles: [targetProfile], current: {viewer: {profileId: 'p1'}},
    pin: '1111', setPin() {}, setError() {}, setProfile() {}, setForgotPIN() {}, cancelWrongPin() {},
    choose: async () => {}, refreshProfiles: async () => {},
    additionCurrent: () => false, editorCurrent: () => false, deletionCurrent: () => false,
    addName: '', nameDraft: '', pinDraft: '', pinAgain: '', deleteName: '',
    error: locked,
  };
  const props: any = {
    context: {serverName: 'Home', api: {baseUrl: 'https://home.test'}, account: {serverId: 'srv', account: {username: 'u', role: 'member'}}},
    route: {kind: 'profiles'},
    onBack() {},
  };

  let tree: any;
  const host = createFabricHookHost(() => {tree = ProfilesRoutePage(props);});
  try {
    // 1) TV PIN stage
    viewState = {...baseView, tv: true, profile: targetProfile};
    await act(() => host.mount());
    let pinStage = nodes(tree).find(n => n.type === 'PINStage');
    assert.equal(pinStage.props.error.props.children, expectedLocked, 'TV PIN stage uses profilePinFailureText');

    // 2) TV picker status
    viewState = {...baseView, tv: true, profile: undefined};
    await act(() => host.rerender());
    const tvProfiles = nodes(tree).find(n => n.type === 'TVProfilesPage');
    const tvFormError = nodes(tvProfiles.props.status).find(n => n.type === 'FormError');
    assert.equal(tvFormError.props.children, expectedLocked, 'TV picker status uses profilePinFailureText');

    // 3) Touch PIN stage
    viewState = {...baseView, tv: false, profile: targetProfile};
    await act(() => host.rerender());
    pinStage = nodes(tree).find(n => n.type === 'PINStage');
    assert.equal(pinStage.props.status.props.children, expectedLocked, 'Touch PIN stage uses profilePinFailureText');

    // 4) Touch picker status
    viewState = {...baseView, tv: false, profile: undefined};
    await act(() => host.rerender());
    const touchSwitch = nodes(tree).find(n => n.type === 'ProfileSwitchPage');
    assert.equal(touchSwitch.props.status.props.failure, expectedLocked, 'Touch picker status uses profilePinFailureText');
  } finally {
    await host.unmount();
  }
});
