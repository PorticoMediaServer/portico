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

async function mount(account: {username: string; hasPassword: boolean; twoStep: boolean; providers?: readonly ('google' | 'apple')[]}, options: {failFirst?: boolean} = {}) {
  const calls: unknown[][] = [], opened: string[] = [];
  let snapshot: any = {phase: 'portico'}, reads = 0;
  const listeners = new Set<() => void>();
  const controller = {getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);}, dispose() {},
    porticoPassword: async (...args: unknown[]) => {calls.push(['porticoPassword', ...args]);},
    porticoProvider: async (provider: string, code: string | (() => Promise<string>)) => {calls.push(['porticoProvider', provider, typeof code === 'function' ? 'asks-after' : code]); if (typeof code === 'function') pending.push(code());}};
  const pending: Promise<string>[] = [];
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
    './PhonePINEntry': {PhonePINEntry: 'PINEntry'}, './ProviderButton': {ProviderButton: 'ProviderButton'},
  });
  let tree: any;
  const host = createFabricHookHost(() => {tree = PhoneProfileRecovery({controller, profileName: 'Alex', username: 'demo', onOpen() {}, onClose() {}, portico});});
  await act(() => host.mount()); await act(async () => {});
  return {calls, opened, pending, tree: () => tree, host, set: async (next: any) => {snapshot = next; await act(() => listeners.forEach(listener => listener()));}};
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

const providerButtons = (tree: any) => nodes(tree).filter(node => node.type === 'ProviderButton');
const texts = (tree: any) => nodes(tree).filter(node => node.type === 'Text').map(node => node.props.children);
// Refusals are said in the kit's shared error line, never a page-coloured Text.
const errorLines = (tree: any) => nodes(tree).filter(node => node.type === 'FormError').map(node => node.props.children);

test('an account that signs in only with Google or Apple confirms it with a fresh provider sign-in; Apple first; Set a password stays', async () => {
  const app = await mount({username: 'ada', hasPassword: false, twoStep: false, providers: ['google', 'apple']});
  try {
    assert.equal(app.tree().props.testID, 'accounts-pin-recovery-portico-provider');
    assert.equal(app.tree().props.body, 'accounts.profiles.porticoProviderLead:{"username":"ada"}');
    assert.deepEqual(providerButtons(app.tree()).map(node => [node.props.provider, node.props.label]),
      [['apple', 'accounts.profiles.porticoProviderApple'], ['google', 'accounts.profiles.porticoProviderGoogle']]);
    assert.ok(texts(app.tree()).includes('accounts.profiles.porticoProviderChecked'));
    assert.equal(nodes(app.tree()).some(node => node.type === 'TextField'), false, 'no password is asked of an account with none');
    await act(() => providerButtons(app.tree())[1].props.onPress());
    assert.deepEqual(app.calls, [['porticoProvider', 'google', '']], 'two-step off: no code is asked');
    await act(() => nodes(app.tree()).find(node => node.type === 'Button' && node.props.children === 'accounts.profiles.porticoSetPassword').props.onPress());
    assert.deepEqual(app.opened, ['https://web.getportico.tv/account?tab=security']);
  } finally {await app.host.unmount();}
});

test('with two-step on, the code is asked after the provider returns, and only then', async () => {
  const app = await mount({username: 'ada', hasPassword: false, twoStep: true, providers: ['apple']});
  try {
    await act(() => providerButtons(app.tree())[0].props.onPress());
    assert.deepEqual(app.calls, [['porticoProvider', 'apple', 'asks-after']]);
    await act(async () => {});
    assert.equal(app.tree().props.testID, 'accounts-pin-recovery-portico-provider-code');
    await act(() => field(app.tree(), 'accounts.profiles.porticoTwoStep').props.onChangeText('123456'));
    await act(() => app.tree().props.primaryAction.onPress());
    assert.equal(await app.pending[0], '123456', 'the code typed now goes to Services with the provider proof');
  } finally {await app.host.unmount();}
});

test('Services\' refusals say what happened: Google and Apple each in their own words, a device that cannot open them, an unfinished sign-in', async () => {
  for (const [launch, code, sentence] of [
    ['google', 'provider_sign_in_not_recent', 'accounts.profiles.porticoGoogleNotRecent'],
    ['apple', 'provider_sign_in_not_recent', 'accounts.profiles.porticoAppleNotRecent'],
    ['google', 'provider_unavailable_here', 'accounts.profiles.porticoProviderUnavailable'],
    ['apple', 'provider_return_incomplete', 'accounts.profiles.porticoProviderIncomplete'],
    ['apple', 'provider_cancelled', 'accounts.profiles.porticoProviderIncomplete'],
  ] as const) {
    const app = await mount({username: 'ada', hasPassword: false, twoStep: false, providers: ['google', 'apple']});
    try {
      await act(() => providerButtons(app.tree()).find(node => node.props.provider === launch).props.onPress());
      await app.set({phase: 'portico', error: Object.assign(new Error('refused'), {code})});
      assert.ok(errorLines(app.tree()).includes(sentence), `${launch} ${code}`);
      assert.equal(providerButtons(app.tree()).length, 2, `${code}: a fresh attempt is offered`);
    } finally {await app.host.unmount();}
  }
});

test('a password added meanwhile: the password form takes over with Services\' sentence; no providers at all: set a password', async () => {
  let app = await mount({username: 'ada', hasPassword: false, twoStep: false, providers: ['google']});
  try {
    await app.set({phase: 'portico', error: Object.assign(new Error('exists'), {code: 'account_password_exists'})});
    assert.equal(app.tree().props.testID, 'accounts-pin-recovery-portico');
    assert.ok(field(app.tree(), 'accounts.profiles.porticoPassword'));
    assert.ok(errorLines(app.tree()).includes('accounts.profiles.porticoPasswordExists'));
  } finally {await app.host.unmount();}
  app = await mount({username: 'ada', hasPassword: false, twoStep: false, providers: []});
  try {assert.equal(app.tree().props.testID, 'accounts-pin-recovery-portico-password-needed');}
  finally {await app.host.unmount();}
});
