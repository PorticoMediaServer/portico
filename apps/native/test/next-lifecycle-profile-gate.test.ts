// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {createTheme} from '../src-next/kit/theme';
import {SessionLifecycle, type LifecyclePorts, type SelectInput} from '../../../packages/client-core/src/session-lifecycle-runtime';
import {ApiError} from '../../../packages/client-core/src';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
// The picker draws in its layout's beforeActions; the PIN screen is one shared piece whose slots hold the rest.
const parts = (tree: any): any[] => tree?.type === 'PhoneWhosWatching' ? [...nodes(tree.props.picker), ...nodes(tree.props.children)] : tree?.type === 'PhoneProfilePIN' ? [tree, ...nodes(tree.props.remember), ...nodes(tree.props.status), ...nodes(tree.props.hints)] : nodes(tree?.props.beforeActions);
const settle = async () => {for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve));};
const server = {serverId: 's1', name: 'Portico demo', signIn: 'local' as const};
const viewer = (profileId: string, familyId: string) => ({accountId: 'a1', profileId, role: 'owner', familyId});
const profiles = [{id: 'main', name: 'Demo', primary: true, pinRequired: false}, {id: 'alex', name: 'Alex', primary: false, pinRequired: true}];

async function gate(rememberProfiles: boolean, isTV = false, refuse = false) {
  const selects: SelectInput[] = [];
  const ports = {
    readRecords: async () => ({outcome: 'ok' as const, servers: 1, held: {server, viewer: viewer('main', 'f1'), remembered: false, profilePinRequired: false}}),
    resetRecords: async () => {}, readHeld: async () => ({servers: 1}),
    connect: async () => ({profiles, rememberProfiles}),
    select: async (input: SelectInput) => {
      selects.push(input);
      if (refuse) throw new ApiError(403, 'profile_pin_invalid', 'That PIN is not correct.');
      return {viewer: viewer(input.profileId, 'f2'), remembered: input.remember};
    },
    storeSelect: async () => {}, signOutServer: async () => ({servers: 0}), signOutAccount: async () => ({servers: 0}), forgetServer: async () => ({servers: 0}),
    clearMarker: async () => {}, fenceShell: () => {}, drainRevocations: () => {}, observeRecords: () => () => {}, classifyConnect: () => undefined,
    connection: () => undefined, adopt: () => undefined, lendInstallation: () => {}, releaseConnection: () => {},
  } satisfies LifecyclePorts & Record<string, unknown>;
  const lifecycle = new SessionLifecycle(ports);
  lifecycle.start(); await settle();
  let tree: any;
  const {LifecycleProfileGate} = await screenModule(new URL('../src-next/screens/accounts/LifecycleProfileGate.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {BackHandler: {addEventListener: () => ({remove() {}})}, Text: 'Text', View: 'View'},
    '../../kit/states': {StateLayout: 'Layout'}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../kit/artwork': {ProfilePicker: 'Picker'},
    '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': {useTheme: () => createTheme({isTV, width: isTV ? 1920 : 390})},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/presentation/errors': {presentError},
    '../../../../../packages/client-core/src': {HttpHostedApi: class {}}, '../../../../../packages/client-core/src/route-identity': {routeOrigin: (v: string) => v},
    '../../shell/StartupBoot': {BootWaitLine: 'Wait'}, '../../tv/auth': {TVForgotPINHandoff: 'TVForgot', TVProfilePINForm: 'TVPIN', TVWhosWatching: 'TVWho', TVPickerRememberRow: 'TVPickerRemember'},
    '../../platform/native-session-lifecycle': {nativeSessionLifecycle: () => ({lifecycle, ports})},
    '../../platform/profile-recovery': {NativeProfileRecovery: class {}, temporaryRecoveryApi: () => undefined},
    '../../platform/accounts': {nativeAccounts: {}}, '../../platform/account-authority': {hostedUrl: 'https://hosted.example'},
    '../../platform/server-connections': {nativeNetworkFetch: fetch}, './profile-pin-failure': {profilePinFailureText: () => undefined}, './PhonePINEntry': {PhonePINEntry: 'PIN'}, './PhoneProfilePIN': {PhoneProfilePIN: 'PhoneProfilePIN'}, './PhoneWhosWatching': {PhoneWhosWatching: 'PhoneWhosWatching'}, '../../shell/after-entry': {afterEntry: {request() {}, clear() {}, take() {}}}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'}, './RememberProfileRow': {RememberProfileRow: 'Remember'},
  });
  const host = createFabricHookHost(() => {tree = LifecycleProfileGate();});
  await act(() => host.mount());
  return {lifecycle, selects, host, tree: () => tree};
}

test('the launch picker lists the server\'s profiles; a PIN profile opens the PIN screen with Remember when the server allows it', async () => {
  const g = await gate(true);
  try {
    assert.equal(g.tree().props.testID, 'lifecycle-profiles');
    const picker = parts(g.tree()).find(node => node.type === 'Picker');
    assert.deepEqual(picker.props.profiles.map((p: {id: string}) => p.id), ['main', 'alex']);
    assert.equal(picker.props.preferredProfileId, 'main');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().props.testID, 'lifecycle-profile-pin');
    const remember = parts(g.tree()).find(node => node.type === 'Remember');
    assert.ok(remember, 'Remember this profile on this device is offered');
    assert.equal(remember.props.checked, false, 'unticked until chosen');
    await act(() => remember.props.onChange(true));
    await g.host.rerender();
    const pin = parts(g.tree()).find(node => node.type === 'PhoneProfilePIN');
    await act(async () => {pin.props.onSubmit('1234'); await settle();});
    assert.deepEqual(g.selects.map(({serverId, profileId, pin, remember}) => ({serverId, profileId, pin, remember})), [{serverId: 's1', profileId: 'alex', pin: '1234', remember: true}]);
    const s = g.lifecycle.getSnapshot().state;
    assert.equal(s.kind, 'ready'); assert.equal(s.kind === 'ready' && s.remembered, true);
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('with Remember PIN for Off the PIN screen offers no Remember row and never sends remember', async () => {
  const g = await gate(false);
  try {
    const picker = parts(g.tree()).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    assert.equal(parts(g.tree()).some(node => node.type === 'Remember'), false);
    const pin = parts(g.tree()).find(node => node.type === 'PhoneProfilePIN');
    await act(async () => {pin.props.onSubmit('1234'); await settle();});
    assert.equal(g.selects[0]?.remember, false);
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('the current no-PIN profile opens without a request', async () => {
  const g = await gate(true);
  try {
    const picker = parts(g.tree()).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('main'); await settle();});
    assert.equal(g.selects.length, 0);
    assert.equal(g.lifecycle.getSnapshot().state.kind, 'ready');
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('on TV the PIN form carries Remember this profile on this device exactly when the server allows remembering', async () => {
  for (const allowed of [true, false]) {
    const g = await gate(allowed, true);
    try {
      const picker = nodes(g.tree().props.children).find(node => node.type === 'Picker');
      await act(async () => {picker.props.onSelect('alex'); await settle();});
      await g.host.rerender();
      assert.equal(g.tree().type, 'TVPIN');
      assert.equal(!!g.tree().props.remember, allowed);
      if (allowed) {await act(() => g.tree().props.remember.onChange(true)); await g.host.rerender();}
      await act(async () => {g.tree().props.onSubmit('1234'); await settle();});
      assert.equal(g.selects[0]?.remember, allowed);
    } finally {await g.host.unmount(); g.lifecycle.dispose();}
  }
});

const remembering = (g: Awaited<ReturnType<typeof gate>>) => parts(g.tree()).find(node => node.type === 'Remember');

test('Who\'s watching offers Remember this profile on this device under the faces only where the server allows it', async () => {
  for (const allowed of [true, false]) {
    const g = await gate(allowed);
    try {
      assert.equal(g.tree().props.testID, 'lifecycle-profiles');
      const row = remembering(g);
      assert.equal(!!row, allowed, allowed ? 'offered under the faces' : 'no checkbox when the server will not remember');
      if (row) assert.equal(row.props.checked, false, 'unticked until chosen');
    } finally {await g.host.unmount(); g.lifecycle.dispose();}
  }
});

test('a ticked no-PIN profile is remembered when chosen; unticked it is not', async () => {
  for (const ticked of [true, false]) {
    const g = await gate(true);
    try {
      if (ticked) {await act(() => remembering(g).props.onChange(true)); await g.host.rerender();}
      assert.equal(remembering(g).props.checked, ticked);
      const picker = parts(g.tree()).find(node => node.type === 'Picker');
      // The held profile is Demo; Alex has a PIN, so add a no-PIN second profile through a fresh gate below. Here, choose the current profile.
      await act(async () => {picker.props.onSelect('main'); await settle();});
      assert.equal(g.selects.length, ticked ? 1 : 0, ticked ? 'the tick on the current profile signs in afresh so the server can remember it' : 'an untouched tap just returns');
      if (ticked) assert.equal(g.selects[0].remember, true);
    } finally {await g.host.unmount(); g.lifecycle.dispose();}
  }
});

test('the picker\'s tick carries to the PIN page, is sent only with the PIN, and cancelling sends nothing', async () => {
  const g = await gate(true);
  try {
    await act(() => remembering(g).props.onChange(true)); await g.host.rerender();
    const picker = parts(g.tree()).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().props.testID, 'lifecycle-profile-pin');
    assert.equal(g.selects.length, 0, 'choosing a PIN profile sends nothing');
    assert.equal(remembering(g).props.checked, true, 'the PIN page arrives ticked');
    // Going back keeps the choice and sends nothing.
    // "‹ Profiles" on the PIN screen.
    await act(() => g.tree().props.onBack()); await g.host.rerender();
    assert.equal(g.tree().props.testID, 'lifecycle-profiles');
    assert.equal(g.selects.length, 0, 'cancelling sends nothing');
    assert.equal(remembering(g).props.checked, true);
    // Unticking on the PIN page is honoured.
    const again = parts(g.tree()).find(node => node.type === 'Picker');
    await act(async () => {again.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    await act(() => remembering(g).props.onChange(false)); await g.host.rerender();
    const pin = parts(g.tree()).find(node => node.type === 'PhoneProfilePIN');
    await act(async () => {pin.props.onSubmit('1234'); await settle();});
    assert.deepEqual(g.selects.map(({profileId, pin, remember}) => ({profileId, pin, remember})), [{profileId: 'alex', pin: '1234', remember: false}]);
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('on TV the faces carry the same checkbox only where the server allows remembering', async () => {
  for (const allowed of [true, false]) {
    const g = await gate(allowed, true);
    try {
      const row = nodes(g.tree().props.children).find(node => node.type === 'TVPickerRemember');
      assert.equal(!!row, allowed);
      if (!row) continue;
      assert.equal(row.props.checked, false);
      await act(() => row.props.onChange(true)); await g.host.rerender();
      const picker = nodes(g.tree().props.children).find(node => node.type === 'Picker');
      await act(async () => {picker.props.onSelect('main'); await settle();});
      assert.equal(g.selects[0]?.remember, true);
    } finally {await g.host.unmount(); g.lifecycle.dispose();}
  }
});

test('on TV a refused PIN stays in the boxes for 400 ms, then they empty for the next try (motion S.3)', async () => {
  const g = await gate(false, true, true);
  try {
    const picker = nodes(g.tree().props.children).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    await act(() => g.tree().props.onCodeChange('1111'));
    await act(async () => {g.tree().props.onSubmit('1111'); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().type, 'TVPIN');
    assert.equal(g.tree().props.code, '1111', 'the refused PIN is still shown');
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 450));});
    await g.host.rerender();
    assert.equal(g.tree().props.code, '', 'the boxes empty after 400 ms');
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('on TV a digit pressed during the 400 ms after a refusal is kept, not wiped', async () => {
  const g = await gate(false, true, true);
  try {
    const picker = nodes(g.tree().props.children).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    await act(() => g.tree().props.onCodeChange('1111'));
    await act(async () => {g.tree().props.onSubmit('1111'); await settle();});
    await g.host.rerender();
    await act(() => g.tree().props.onCodeChange('2'));
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 450));});
    await g.host.rerender();
    assert.equal(g.tree().props.code, '2', 'the next PIN started during the refusal survives the clear');
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('on a phone a refusal does not wipe the PIN field later', async () => {
  const g = await gate(false, false, true);
  try {
    const picker = parts(g.tree()).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    const entry = () => parts(g.tree()).find(node => node.type === 'PhoneProfilePIN');
    await act(() => entry().props.onChange('1111'));
    await act(async () => {entry().props.onSubmit('1111'); await settle();});
    await g.host.rerender();
    await act(() => entry().props.onChange('3'));
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 450));});
    await g.host.rerender();
    assert.equal(entry().props.value, '3', 'what the person typed after the refusal stays');
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('a refused PIN\'s message stays on the PIN page: Back to Who\'s watching shows none', async () => {
  const g = await gate(false, true, true);
  try {
    const picker = nodes(g.tree().props.children).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    await act(async () => {g.tree().props.onSubmit('1111'); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().type, 'TVPIN');
    assert.notEqual(g.tree().props.error, undefined, 'the PIN page says the PIN was refused');
    await act(async () => {g.tree().props.onBack(); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().type, 'TVWho');
    assert.equal(nodes(g.tree().props.children).some(node => node?.props?.accessibilityRole === 'alert'), false, 'Who\'s watching carries no refusal');
    await act(async () => {nodes(g.tree().props.children).find(node => node.type === 'Picker').props.onSelect('alex'); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().type, 'TVPIN');
    assert.equal(g.tree().props.error, undefined, 'the PIN page opened again starts with no message');
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});
