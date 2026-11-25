import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const profiles = [
  {id: 'main', name: 'Demo', initial: 'D', color: 'blue' as const, primary: true, pinRequired: false, disabled: false},
  {id: 'alex', name: 'Alex', initial: 'A', color: 'mint' as const, primary: false, pinRequired: true, disabled: false},
];

async function loadGateView() {
  return screenModule(new URL('../src-next/pages/shared/accounts/ProfileGateView.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/artwork': {ProfilePicker: 'Picker'},
    '../../../kit/accounts/RememberProfileRow': {RememberProfileRow: 'Remember'},
    '../../../shell/StartupBoot': {BootWaitLine: 'Wait'},
    '../../../tv/auth': {
      TVForgotPINHandoff: 'TVForgot',
      TVPickerRememberRow: 'TVPickerRemember',
      TVProfilePINForm: 'TVPIN',
      TVWhosWatching: 'TVWho',
    },
    '../../touch/accounts/ProfilePINPage': {ProfilePINPage: 'PhoneProfilePIN'},
    '../../touch/accounts/ProfilesPage': {ProfilesPage: 'PhoneWhosWatching'},
    '../../touch/accounts/ProfileRecoveryPage': {ProfileRecoveryPage: 'Recovery'},
  });
}

test('ProfilePickerStage renders consistent touch and TV picker trees for lifecycle and entry flows', async () => {
  const {ProfilePickerStage} = await loadGateView();
  const status = React.createElement('Status', null, 'Failed');
  const onSelect = () => {};
  const onRememberChange = () => {};
  const onManage = () => {};
  const onBack = () => {};

  for (const flow of [
    {testID: 'lifecycle-profiles', waitTestID: 'lifecycle-profile-wait', rememberFocusId: 'lifecycle-remember-profile'},
    {testID: 'accounts-entry', waitTestID: 'accounts-profile-wait', rememberFocusId: 'accounts-remember-profile'},
  ]) {
    const touchTree = ProfilePickerStage({
      tv: false,
      ...flow,
      serverName: 'Home Server',
      focusScope: 'tv.auth.profiles.s1',
      busy: false,
      profiles,
      preferredProfileId: 'main',
      onSelect,
      onManage,
      onAdd: onManage,
      canRemember: true,
      remember: true,
      onRememberChange,
      status,
      onBack,
    });
    assert.equal(touchTree.type, 'PhoneWhosWatching');
    assert.equal(touchTree.props.testID, flow.testID);
    assert.equal(touchTree.props.onBack, onBack);
    assert.equal(touchTree.props.picker.type, 'Picker');
    assert.equal(touchTree.props.picker.props.focusScope, 'tv.auth.profiles.s1');
    assert.equal(touchTree.props.picker.props.title, defaultI18n.t('auth.whosWatching'));
    assert.equal(touchTree.props.picker.props.description, defaultI18n.t('accounts.profiles.serverContext', {server: 'Home Server'}));
    assert.equal(touchTree.props.picker.props.onManage, onManage);
    assert.equal(touchTree.props.picker.props.onAdd, onManage);
    const touchChildren = nodes(touchTree.props.children);
    const remember = touchChildren.find(n => n.type === 'Remember');
    assert.equal(remember?.props.focusId, flow.rememberFocusId);
    assert.equal(remember?.props.underFaces, true);
    const wait = touchChildren.find(n => n.type === 'Wait');
    assert.equal(wait?.props.testID, flow.waitTestID);
    assert.equal(wait?.props.serverName, 'Home Server');

    const tvTree = ProfilePickerStage({
      tv: true,
      ...flow,
      serverName: 'Home Server',
      focusScope: 'tv.auth.profiles.s1',
      busy: true,
      profiles,
      preferredProfileId: 'alex',
      onSelect,
      canRemember: true,
      remember: false,
      onRememberChange,
      status,
    });
    assert.equal(tvTree.type, 'TVWho');
    assert.equal(tvTree.props.testID, flow.testID);
    const tvChildren = nodes(tvTree.props.children);
    const tvPicker = tvChildren.find(n => n.type === 'Picker');
    assert.equal(tvPicker?.props.title, defaultI18n.t('accounts.profiles.onServer', {server: 'Home Server'}));
    assert.equal(tvPicker?.props.description, undefined);
    assert.equal(tvPicker?.props.preferredProfileId, 'alex');
    const tvRemember = tvChildren.find(n => n.type === 'TVPickerRemember');
    assert.equal(tvRemember?.props.checked, false);
    assert.equal(tvRemember?.props.busy, true);
    const tvWait = tvChildren.find(n => n.type === 'Wait');
    assert.equal(tvWait?.props.testID, flow.waitTestID);
    assert.equal(tvWait?.props.active, true);
  }

  // Remember row is omitted when the server disallows remembering.
  const noRemember = ProfilePickerStage({
    tv: false,
    testID: 'accounts-entry',
    waitTestID: 'accounts-profile-wait',
    rememberFocusId: 'accounts-remember-profile',
    serverName: 'Home Server',
    focusScope: 'tv.auth.profiles.s1',
    busy: false,
    profiles,
    onSelect,
    canRemember: false,
    remember: true,
    onRememberChange,
  });
  assert.equal(nodes(noRemember.props.children).some(n => n.type === 'Remember'), false);
});

test('ProfilePINStage renders touch PIN, TV PIN, TV forgot-PIN handoff, and touch recovery consistently', async () => {
  const {ProfilePINStage} = await loadGateView();
  const profile = {id: 'alex', name: 'Alex', face: {initial: 'A', color: 'mint' as const}, primary: false, pinRequired: true, disabled: false};
  const onCodeChange = () => {};
  const onSubmit = () => {};
  const onBack = () => {};
  const onForgot = () => {};
  const onRememberChange = () => {};
  const errorEl = React.createElement('PinError', null, 'Wrong PIN');

  // Touch PIN for lifecycle, entry, and switch flows.
  const touchPin = ProfilePINStage({
    tv: false,
    testID: 'lifecycle-profile-pin',
    profile,
    code: '12',
    onCodeChange,
    onSubmit,
    busy: false,
    rejected: true,
    status: errorEl,
    canRemember: true,
    remember: true,
    onRememberChange,
    rememberFocusId: 'lifecycle-remember-profile',
    waitTestID: 'lifecycle-profile-wait',
    serverName: 'Home Server',
    onBack,
    onForgot,
    forgotDisabled: false,
  });
  assert.equal(touchPin.type, 'PhoneProfilePIN');
  assert.equal(touchPin.props.testID, 'lifecycle-profile-pin');
  assert.equal(touchPin.props.value, '12');
  assert.equal(touchPin.props.rejected, true);
  assert.equal(touchPin.props.remember.type, 'Remember');
  assert.equal(touchPin.props.remember.props.focusId, 'lifecycle-remember-profile');
  assert.equal(touchPin.props.hints.type, 'Wait');
  assert.equal(touchPin.props.hints.props.testID, 'lifecycle-profile-wait');

  // Switch PIN omits remember and wait line.
  const switchPin = ProfilePINStage({
    tv: false,
    testID: 'accounts-profile-switch',
    profile,
    code: '',
    onCodeChange,
    onSubmit,
    busy: false,
    rejected: false,
    onBack,
    onForgot,
  });
  assert.equal(switchPin.type, 'PhoneProfilePIN');
  assert.equal(switchPin.props.testID, 'accounts-profile-switch');
  assert.equal(switchPin.props.remember, undefined);
  assert.equal(switchPin.props.hints, undefined);

  // TV PIN shows hints only when busy, and remember only when server allows it.
  const tvPinIdle = ProfilePINStage({
    tv: true,
    profile,
    code: '123',
    onCodeChange,
    onSubmit,
    busy: false,
    rejected: false,
    error: errorEl,
    canRemember: true,
    remember: false,
    onRememberChange,
    waitTestID: 'accounts-profile-wait',
    serverName: 'Home Server',
    onBack,
    onForgot,
  });
  assert.equal(tvPinIdle.type, 'TVPIN');
  assert.equal(tvPinIdle.props.code, '123');
  assert.equal(tvPinIdle.props.error, errorEl);
  assert.deepEqual(tvPinIdle.props.remember, {checked: false, onChange: onRememberChange});
  assert.equal(tvPinIdle.props.hints, undefined);

  const tvPinBusy = ProfilePINStage({
    tv: true,
    profile,
    code: '1234',
    onCodeChange,
    onSubmit,
    busy: true,
    rejected: false,
    waitTestID: 'accounts-profile-wait',
    serverName: 'Home Server',
    onBack,
    onForgot,
  });
  assert.equal(tvPinBusy.props.hints.type, 'Wait');
  assert.equal(tvPinBusy.props.hints.props.testID, 'accounts-profile-wait');
  assert.equal(tvPinBusy.props.remember, undefined);

  // TV Forgot PIN handoff takes precedence over PIN form.
  const tvForgot = ProfilePINStage({
    tv: true,
    profile,
    code: '',
    onCodeChange,
    onSubmit,
    busy: false,
    rejected: false,
    onBack,
    onForgot,
    forgot: {profileName: 'Alex', serverAddress: 'https://home.example', server: {id: 's1', signIn: 'local'}},
  });
  assert.equal(tvForgot.type, 'TVForgot');
  assert.equal(tvForgot.props.profileName, 'Alex');
  assert.equal(tvForgot.props.serverAddress, 'https://home.example');
  assert.deepEqual(tvForgot.props.server, {id: 's1', signIn: 'local'});

  // Touch PIN recovery takes precedence over everything.
  const controller = {dispose() {}} as any;
  const recovery = ProfilePINStage({
    tv: false,
    code: '',
    onCodeChange,
    onSubmit,
    busy: false,
    rejected: false,
    onBack,
    onForgot,
    recovery: {controller, profileName: 'Alex', username: 'owner', onOpen: () => {}, onClose: () => {}},
  });
  assert.equal(recovery.type, 'Recovery');
  assert.equal(recovery.props.controller, controller);
  assert.equal(recovery.props.profileName, 'Alex');
  assert.equal(recovery.props.username, 'owner');
});
