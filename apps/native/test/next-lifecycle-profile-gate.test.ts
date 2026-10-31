// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import {presentError} from '../../../packages/client-core/src/presentation/errors';
import {createTheme} from '../src-next/kit/theme';
import {SessionLifecycle, type LifecyclePorts, type SelectInput} from '../../../packages/client-core/src/session-lifecycle-runtime';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const settle = async () => {for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve));};
const server = {serverId: 's1', name: 'Portico demo', signIn: 'local' as const};
const viewer = (profileId: string, familyId: string) => ({accountId: 'a1', profileId, role: 'owner', familyId});
const profiles = [{id: 'main', name: 'Demo', primary: true, pinRequired: false}, {id: 'alex', name: 'Alex', primary: false, pinRequired: true}];

async function gate(rememberProfiles: boolean, isTV = false) {
  const selects: SelectInput[] = [];
  const ports = {
    readRecords: async () => ({outcome: 'ok' as const, servers: 1, held: {server, viewer: viewer('main', 'f1'), remembered: false, profilePinRequired: false}}),
    resetRecords: async () => {}, readHeld: async () => ({servers: 1}),
    connect: async () => ({profiles, rememberProfiles}),
    select: async (input: SelectInput) => {selects.push(input); return {viewer: viewer(input.profileId, 'f2'), remembered: input.remember};},
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
    '../../shell/StartupBoot': {BootWaitLine: 'Wait'}, '../../tv/auth': {TVForgotPINHandoff: 'TVForgot', TVProfilePINForm: 'TVPIN', TVWhosWatching: 'TVWho'},
    '../../platform/native-session-lifecycle': {nativeSessionLifecycle: () => ({lifecycle, ports})},
    '../../platform/profile-recovery': {NativeProfileRecovery: class {}, temporaryRecoveryApi: () => undefined},
    '../../platform/accounts': {nativeAccounts: {}}, '../../platform/account-authority': {hostedUrl: 'https://hosted.example'},
    '../../platform/server-connections': {nativeNetworkFetch: fetch}, './PhonePINEntry': {PhonePINEntry: 'PIN'}, './PhoneProfileRecovery': {PhoneProfileRecovery: 'Recovery'},
  });
  const host = createFabricHookHost(() => {tree = LifecycleProfileGate();});
  await act(() => host.mount());
  return {lifecycle, selects, host, tree: () => tree};
}

test('the launch picker lists the server\'s profiles; a PIN profile opens the PIN screen with Remember when the server allows it', async () => {
  const g = await gate(true);
  try {
    assert.equal(g.tree().props.testID, 'lifecycle-profiles');
    const picker = nodes(g.tree().props.beforeActions).find(node => node.type === 'Picker');
    assert.deepEqual(picker.props.profiles.map((p: {id: string}) => p.id), ['main', 'alex']);
    assert.equal(picker.props.preferredProfileId, 'main');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    assert.equal(g.tree().props.testID, 'lifecycle-profile-pin');
    const remember = nodes(g.tree().props.beforeActions).find(node => node.type === 'Focusable' && node.props.focusId === 'lifecycle-remember-profile');
    assert.ok(remember, 'Remember this profile on this device is offered');
    assert.equal(remember.props.accessibilityState.checked, false, 'unticked until chosen');
    await act(() => remember.props.onPress());
    await g.host.rerender();
    const pin = nodes(g.tree().props.beforeActions).find(node => node.type === 'PIN');
    await act(async () => {pin.props.onComplete('1234'); await settle();});
    assert.deepEqual(g.selects.map(({serverId, profileId, pin, remember}) => ({serverId, profileId, pin, remember})), [{serverId: 's1', profileId: 'alex', pin: '1234', remember: true}]);
    const s = g.lifecycle.getSnapshot().state;
    assert.equal(s.kind, 'ready'); assert.equal(s.kind === 'ready' && s.remembered, true);
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('with Remember PIN for Off the PIN screen offers no Remember row and never sends remember', async () => {
  const g = await gate(false);
  try {
    const picker = nodes(g.tree().props.beforeActions).find(node => node.type === 'Picker');
    await act(async () => {picker.props.onSelect('alex'); await settle();});
    await g.host.rerender();
    assert.equal(nodes(g.tree().props.beforeActions).some(node => node.props?.focusId === 'lifecycle-remember-profile'), false);
    const pin = nodes(g.tree().props.beforeActions).find(node => node.type === 'PIN');
    await act(async () => {pin.props.onComplete('1234'); await settle();});
    assert.equal(g.selects[0]?.remember, false);
  } finally {await g.host.unmount(); g.lifecycle.dispose();}
});

test('the current no-PIN profile opens without a request', async () => {
  const g = await gate(true);
  try {
    const picker = nodes(g.tree().props.beforeActions).find(node => node.type === 'Picker');
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
