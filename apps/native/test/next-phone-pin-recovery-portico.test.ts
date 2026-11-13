// @fabric-react
// Forgot PIN on a Portico Accounts server, on a phone: the Portico Account's password (and its
// two-step code when that is on) is typed here and checked by Portico Services (owner's rule,
// 12 Nov). Stand-ins for the controller (client-core, #3075) and the account; never the real Services.
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {createTheme} from '../src-next/kit/theme';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.beforeActions)] : [];

async function mount(account: {username: string; hasPassword: boolean; twoStep: boolean}, options: {failFirst?: boolean} = {}) {
  const calls: unknown[][] = [], opened: string[] = [];
  let snapshot: any = {phase: 'portico'}, reads = 0;
  const listeners = new Set<() => void>();
  const controller = {getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}, dispose() {},
    porticoPassword: async (...args: unknown[]) => {calls.push(['porticoPassword', ...args]);}};
  const source = {accountId: 'acct', isCurrent: () => true, identity: async () => ({identity: {}})};
  const portico = async () => {reads++; if (options.failFirst && reads === 1) throw new Error('Portico Services cannot be reached'); return {source, account};};
  const {PhoneProfileRecovery} = await screenModule(new URL('../src-next/screens/accounts/PhoneProfileRecovery.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Linking: {openURL: async (url: string) => {opened.push(url);}}, Text: 'Text', View: 'View'},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: unknown) => values ? `${key}:${JSON.stringify(values)}` : key}},
    '../../../../../packages/client-core/src/presentation/errors': {presentError: (error: Error) => ({body: error.message})},
    '../../kit/states': {StateLayout: 'StateLayout'}, '../../kit/controls': {Button: 'Button', TextField: 'TextField', FormError: 'FormError'},
    '../../kit/theme': {useTheme: () => createTheme({isTV: false, width: 390})},
    '../../platform/profile-recovery': {nativePorticoRecovery: portico}, '../../platform/account-authority': {hostedUrl: 'https://web.getportico.tv'},
    './PhonePINEntry': {PhonePINEntry: 'PINEntry'},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = PhoneProfileRecovery({controller, profileName: 'Alex', username: 'demo', onOpen() {}, onClose() {}, portico});});
  await act(() => host.mount()); await act(async () => {});
  return {calls, opened, tree: () => tree, host, set: async (next: any) => {snapshot = next; await act(() => listeners.forEach(listener => listener()));}};
}
const field = (tree: any, label: string) => nodes(tree).find(node => node.type === 'TextField' && String(node.props.label).startsWith(label));

test('the Portico step asks for the Portico Account\'s password and, when it is on, its two-step code, and sends them to the controller only', async () => {
  const app = await mount({username: 'ada', hasPassword: true, twoStep: true});
  try {
    assert.equal(app.tree().props.testID, 'accounts-pin-recovery-portico');
    assert.equal(field(app.tree(), 'accounts.profiles.porticoPassword').props.label, 'accounts.profiles.porticoPassword:{"username":"ada"}');
    assert.ok(nodes(app.tree()).some(node => node.type === 'Text' && node.props.children === 'accounts.profiles.porticoChecked'), 'Portico Services checks it');
    await act(() => field(app.tree(), 'accounts.profiles.porticoPassword').props.onChangeText('secret'));
    assert.equal(app.tree().props.primaryAction.disabled, true, 'not without the code: an empty one would count as a wrong one');
    await act(() => field(app.tree(), 'accounts.profiles.porticoTwoStep').props.onChangeText('123456'));
    assert.equal(app.tree().props.primaryAction.disabled, false);
    await act(() => app.tree().props.primaryAction.onPress());
    assert.deepEqual(app.calls.map(call => call.slice(0, 3)), [['porticoPassword', 'secret', '123456']]);
    assert.equal((app.calls[0][3] as {accountId: string}).accountId, 'acct', 'the signed-in Portico Account\'s proof');
  } finally {await app.host.unmount();}
});

test('an account with no password is told to set one, also when Services refuses for that reason', async () => {
  const unset = await mount({username: 'ada', hasPassword: false, twoStep: false});
  try {
    assert.equal(unset.tree().props.body, 'accounts.profiles.porticoNeedsPassword');
    await act(() => unset.tree().props.primaryAction.onPress());
    assert.deepEqual(unset.opened, ['https://web.getportico.tv/account?tab=security']);
  } finally {await unset.host.unmount();}
  const refused = await mount({username: 'ada', hasPassword: true, twoStep: false});
  try {
    await refused.set({phase: 'portico', error: {code: 'account_password_required'}});
    assert.equal(refused.tree().props.body, 'accounts.profiles.porticoNeedsPassword');
  } finally {await refused.host.unmount();}
});

test('a failed read of the Portico Account is said in the step, and Try again reads it again', async () => {
  const app = await mount({username: 'ada', hasPassword: true, twoStep: false}, {failFirst: true});
  try {
    assert.ok(nodes(app.tree()).some(node => node.type === 'FormError' && node.props.children === 'Portico Services cannot be reached'), 'said in the shared error line');
    await act(() => app.tree().props.primaryAction.onPress()); await act(async () => {});
    assert.ok(field(app.tree(), 'accounts.profiles.porticoPassword'), 'read again, the form is shown');
    assert.equal(field(app.tree(), 'accounts.profiles.porticoTwoStep'), undefined, 'no code field when two-step is off');
  } finally {await app.host.unmount();}
});
