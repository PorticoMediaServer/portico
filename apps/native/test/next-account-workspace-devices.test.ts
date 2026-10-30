// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src';
import * as sessions from '../../../packages/client-core/src/account-sessions';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const texts = (node: any) => nodes(node).filter(item => item.type === 'Text').map(item => [item.props.children].flat().join(''));
const t = defaultI18n.t;

const row = (patch: Record<string, unknown>) => ({id: 'other', mode: 'native', status: 'active', current: false, createdAt: '2026-10-01T10:00:00Z', expiresAt: '2026-11-01T10:00:00Z',
  deviceName: null, platform: null, app: null, lastUsedAt: null, ...patch});

async function mount(state: Record<string, unknown>) {
  const calls: string[] = [];
  let snapshot: any = {page: 'devices', phase: 'ready', ...state};
  const listeners = new Set<() => void>();
  const source = {
    getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    current: () => true, dispose() {}, menu() {}, open: async () => {},
    revokeSession: async (value: any) => {calls.push('one:' + (typeof value === 'string' ? value : value.id));},
    revokeOtherSessions: async () => {calls.push('others');},
  };
  const {PhoneAccountWorkspace} = await screenModule(new URL('../src-next/screens/accounts/PhoneAccountWorkspace.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
    react: {...React, default: React},
    'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {Button: 'Button'},
    '../../kit/overlays': {BottomSheet: 'Sheet'},
    '../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../../kit/artwork': {ColourBackdrop: 'Backdrop'},
    '../../../../../packages/client-core/src/account-sessions': sessions,
    '../../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    './PhoneAccountSecurity': {PhoneAccountSecurity: 'Security'},
  });
  let tree: any;
  const createSource = () => source;
  const host = createFabricHookHost(() => {tree = PhoneAccountWorkspace({createSource, username: 'dana', onDone() {}, onFind() {}, onJoin() {}, onSignOut() {}});});
  await act(() => host.mount());
  return {calls, tree: () => tree, host, set: async (next: Record<string, unknown>) => {snapshot = {...snapshot, ...next}; await act(() => listeners.forEach(listener => listener()));}};
}
const button = (tree: any, testID: string) => nodes(tree).find(node => node.type === 'Button' && node.props.testID === testID);
const sheet = (tree: any) => nodes(tree).find(node => node.type === 'Sheet');

test('the Devices list names each sign-in from its reported facts and leaves unknown facts out', async () => {
  const app = await mount({sessions: [
    row({id: 'here', current: true, mode: 'native', deviceName: 'Dana’s iPhone', platform: 'ios', app: 'Portico', lastUsedAt: new Date().toISOString()}),
    row({id: 'tv', deviceName: 'Living room TV', platform: 'tvos', lastUsedAt: new Date(Date.now() - 5 * 60e3).toISOString()}),
    row({id: 'unknown', mode: 'browser'}),
  ]});
  try {
    const shown = texts(app.tree());
    for (const expected of ['Dana’s iPhone', t('device.thisDevice'), 'Living room TV', t('services.workspace.appAppleTV'), t('web.hostedAccount.aBrowser')]) assert.ok(shown.includes(expected), expected);
    assert.ok(shown.some(text => text.startsWith('last used')), 'last used is shown when known');
    const unknown = nodes(app.tree()).find(node => node.props?.testID === 'account-device-unknown');
    assert.equal(texts(unknown).some(text => text.startsWith('last used')), false, 'an unknown last use is left out, not guessed');
    // This device signs out from the menu; its row offers no Sign out, as drawn.
    assert.equal(button(app.tree(), 'account-device-sign-out-here'), undefined);
    assert.ok(button(app.tree(), 'account-device-sign-out-tv'));
  } finally {await app.host.unmount();}
});

test('Sign out all other devices asks first, then makes the one request', async () => {
  const app = await mount({sessions: [row({id: 'here', current: true}), row({id: 'tv'})]});
  try {
    await act(() => button(app.tree(), 'account-devices-sign-out-others').props.onPress());
    assert.equal(sheet(app.tree()).props.open, true);
    assert.equal(sheet(app.tree()).props.title, t('web.hostedAccount.signOutOthersTitle'));
    assert.ok(texts(sheet(app.tree())).includes(t('services.workspace.signOutOthersAppBody')));
    assert.deepEqual(app.calls, []);
    await act(() => button(sheet(app.tree()).props.footer, 'account-workspace-confirm-action').props.onPress());
    assert.deepEqual(app.calls, ['others']);
  } finally {await app.host.unmount();}
});

test('while Sign out all other devices is unconfirmed, only its Try again acts', async () => {
  const app = await mount({sessions: [row({id: 'here', current: true}), row({id: 'tv'})], retryOthers: true, mutationFailure: 'Not confirmed'});
  try {
    assert.equal(button(app.tree(), 'account-devices-sign-out-others').props.disabled, true);
    assert.equal(button(app.tree(), 'account-device-sign-out-tv').props.disabled, true);
    const retry = nodes(app.tree()).find(node => node.type === 'Button' && node.props.children === t('action.tryAgain'));
    await act(() => retry.props.onPress());
    assert.deepEqual(app.calls, ['others']);
  } finally {await app.host.unmount();}
});

test('Sign out all other devices is off when no other sign-in is listed or follows', async () => {
  const app = await mount({sessions: [row({id: 'here', current: true})], sessionsNext: false});
  try {
    assert.equal(button(app.tree(), 'account-devices-sign-out-others').props.disabled, true);
    await app.set({sessionsNext: true});
    assert.equal(button(app.tree(), 'account-devices-sign-out-others').props.disabled, false);
  } finally {await app.host.unmount();}
});
