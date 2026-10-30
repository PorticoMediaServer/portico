// @fabric-react
import assert from 'node:assert/strict';
import test from 'node:test';
import {catalogue, defaultI18n, enUS} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';
import {stateSentenceParts} from '../src-next/kit/states/sentence-parts';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// Walks both the element props the shallow fixtures hand back and the host
// nodes React commits, so a case keeps the same lookup it always used.
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
const theme = createTheme({isTV: true, width: 1920});
const scope = (component: (props: any) => any, props: any) => {
  const tree = component(props);
  assert.equal(tree.type, 'Provider');
  const child = [tree.props.children].flat()[0];
  return child.type(child.props);
};
const handoffModule = async (react: unknown, back: unknown, entry = (..._args: unknown[]) => {}) => {
  const revision = await screenModule(new URL('../src-next/tv/auth/entry-revision.ts', import.meta.url), {react});
  return screenModule(new URL('../src-next/tv/auth/TVHandoff.tsx', import.meta.url), {
  react, 'react-native': {View: 'View', Text: 'Text', BackHandler: back}, 'react-native-qrcode-svg': {default: 'QR'},
  '../../../../../packages/i18n/src': {catalogue, defaultI18n, enUS}, '../../kit/controls': {Button: 'Button'},
  '../../kit/focus': {FocusProvider: 'Provider', FocusRegion: 'Region'}, '../../kit/theme': {nativeType, useTheme: () => theme},
  '../../kit/states/sentence-parts': {stateSentenceParts}, '../../platform/native-blur': {BlurView: 'Blur', glassRecipe: () => ({nativeBlur: true})},
  '../../screens/destinations/useEntryFocus': {useDestinationEntryFocus: entry}, './TVAuthLayout': {TVAuthLayout: 'Layout'}, './entry-revision': revision,
});
};

test('TV hand-off URLs contain only a public address, including the real profile-management query', async () => {
  const h = hooks(), {publicTVHandoffAddress} = await handoffModule(h.react, {});
  assert.equal(publicTVHandoffAddress('http://server.example:32500/settings?scope=account&section=profile').display,
    'server.example:32500/settings?scope=account&section=profile');
  assert.equal(publicTVHandoffAddress('https://server.example/settings/profile').url, 'https://server.example/settings/profile');
  assert.equal(publicTVHandoffAddress('https://server.example/settings?scope=server&section=live').url, 'https://server.example/settings?scope=server&section=live');
  for (const url of ['file:///settings/profile', 'javascript:alert(1)', 'https://user:password@server.example/settings/profile',
    'https://server.example/settings/profile#grant', 'https://server.example/settings/profile?token=private',
    'https://server.example/settings?scope=account&section=profile&token=private',
    'https://server.example/settings?scope=account&scope=profile', 'https://server.example/account?code=private', 'https://server.example/settings?scope=server&section=profile',
    'https://server.example/settings?scope=account&section=live', 'https://server.example/settings?scope=server&section=live&token=private',
    'https://server.example/settings?scope=server&section=live&section=live']) {
    assert.throws(() => publicTVHandoffAddress(url));
  }
});

test('signed-in profile management reuses the drawn QR/link part and returns through the actual Back owner', async () => {
  const calls: string[] = [], listeners = new Set<() => boolean>();
  const {TVManagementHandoff} = await handoffModule({...React, default: React}, {
    addEventListener: (_name: string, listener: () => boolean) => {listeners.add(listener); return {remove: () => listeners.delete(listener)};},
  });
  const url = 'https://server.example/settings?scope=account&section=profile';
  let tree: any;
  const host = createFabricHookHost(() => {tree = TVManagementHandoff({url, onBack: () => calls.push('back')});});
  await act(() => host.mount());
  try {
    assert.deepEqual(calls, [], 'showing the hand-off never invokes an account action');
    assert.equal(nodes(tree).find(node => node.type === 'QR').props.value, url);
    assert.equal(nodes(tree).filter(node => node.type === 'TextInput' || node.type === 'Button').length, 0);
    const address = nodes(tree).find(node => node.type === 'Text' && node.props.style?.fontWeight === '700');
    assert.equal(address.props.children, 'server.example/settings?scope=account&section=profile');
    assert.equal([...listeners][0]!(), true);
    tree.props.onAccessibilityEscape();
    assert.deepEqual(calls, ['back', 'back']);
  } finally {await host.unmount();}
  assert.equal(listeners.size, 0, 'the departed management view retains no hardware Back handler');
});

test('unsigned Forgot PIN has the profile why, public QR, one initially focused Back and no setup or credential action', async () => {
  const callbacks: string[] = [], entries: unknown[][] = [], listeners = new Set<() => boolean>();
  const {TVForgotPINHandoff} = await handoffModule({...React, default: React}, {
    addEventListener: (_name: string, listener: () => boolean) => {listeners.add(listener); return {remove: () => listeners.delete(listener)};},
  }, (...args) => entries.push(args));
  const props = {profileName: 'Actual profile', url: 'https://server.example/settings/profile', body: 'account.tvHandoffTitle', onBack: () => callbacks.push('back')};
  let tree: any;
  const host = createFabricHookHost(() => {tree = scope(TVForgotPINHandoff, props);});
  await act(() => host.mount());
  try {
    assert.deepEqual(callbacks, [], 'displaying this variant has no session-retirement or automatic-continue callback');
    assert.equal(tree.props.eyebrow, props.profileName);
    assert.equal(tree.props.portico, false, 'there is no terms line');
    assert.equal(tree.props.heading, 'Forgot PIN?');
    assert.equal(tree.props.body, defaultI18n.t('account.tvHandoffTitle'));
    const card = nodes(tree.props.aside);
    assert.equal(card.find(node => node.type === 'QR').props.value, props.url);
    assert.equal(card.filter(node => node.type === 'Text').length, 2, 'no device sign-in code is present');
    assert.ok(card.some(node => node.props.children === 'server.example/settings/profile'));
    assert.ok(card.some(node => node.props.children === defaultI18n.t('next.auth.tv.handoffHint')));
    assert.ok(!nodes([tree, tree.props.aside]).some(node => node.type === 'TextInput'));
    const buttons = nodes(tree).filter(node => node.type === 'Button');
    assert.equal(buttons.length, 1);
    assert.equal(buttons[0].props.variant, undefined, 'Back uses the primary recipe');
    assert.equal(buttons[0].props.children, defaultI18n.t('action.back'));
    assert.deepEqual(entries[0]![1], {regionId: 'tv.auth.profile.forgot-pin', childId: 'back'});
    buttons[0].props.onPress(); assert.deepEqual(callbacks, ['back']);
    assert.equal([...listeners][0]!(), true); assert.deepEqual(callbacks, ['back', 'back']);
  } finally {await host.unmount();}
  assert.equal(listeners.size, 0);
  assert.deepEqual(callbacks, ['back', 'back'], 'unmount is not a sign-out or cancellation action');
});

test('unsigned Forgot PIN rejects code/credential/query hand-offs instead of drawing them at picker level', async () => {
  const h = hooks(), {TVForgotPINHandoff} = await handoffModule(h.react, {});
  const props = {profileName: 'Actual profile', body: 'account.tvHandoffTitle', onBack() {}};
  for (const url of ['https://server.example/account', 'https://server.example/settings?scope=account&section=profile',
    'https://server.example/settings/profile?token=private', 'https://server.example/settings/profile#grant',
    'https://username:password@server.example/settings/profile']) {
    assert.throws(() => h.render(() => scope(TVForgotPINHandoff, {...props, url})));
  }
});

for (const requirement of ['password', 'two-step'] as const) test(`required ${requirement} stays a public-only hand-off with explicit retry and no sign-in code`, async () => {
  const h = hooks(), calls: string[] = [], entries: unknown[][] = [];
  const {TVRequiredSetupHandoff} = await handoffModule(h.react, {}, (...args) => entries.push(args));
  const props = {serverName: 'Actual server', requirement, url: 'https://server.example/settings/profile', busy: false,
    copy: {body: 'auth.direct.lead', reference: 'activation.codeRefreshes'}, onSignInAgain: () => calls.push('retry'), onBack: () => calls.push('back')};
  const tree = h.render(() => scope(TVRequiredSetupHandoff, props));
  assert.deepEqual(calls, [], 'a blocked state has no automatic continue or polling callback');
  assert.equal(tree.props.eyebrow, 'Actual server');
  assert.equal(tree.props.portico, false);
  assert.equal(tree.props.heading, defaultI18n.t(requirement === 'password' ? 'web.direct.newPasswordTitle' : 'web.twoStep.turnOnTitle'));
  const card = nodes(tree.props.aside);
  assert.equal(card.find(node => node.type === 'QR').props.value, props.url);
  assert.equal(card.filter(node => node.type === 'Text').length, 2, 'the card has only its actual address and reference, never a device sign-in code');
  assert.ok(!nodes([tree, tree.props.aside]).some(node => node.type === 'TextInput'));
  assert.equal((entries[0]![1] as any).childId, 'sign-in-again');
  const buttons = nodes(tree).filter(node => node.type === 'Button');
  assert.equal(buttons.length, 2);
  buttons[0].props.onPress(); buttons[1].props.onPress();
  assert.deepEqual(calls, ['retry', 'back']);
  props.busy = true;
  const busyTree = h.render(() => scope(TVRequiredSetupHandoff, props));
  nodes(busyTree).filter(node => node.type === 'Button').forEach(node => node.props.onPress());
  assert.deepEqual(calls, ['retry', 'back']);
  assert.ok(nodes(busyTree).filter(node => node.type === 'Button').every(node => node.props.disabled));
});

test('required setup uses its ruled path and Portico-only terms, not the unrelated management query', async () => {
  const h = hooks(), {TVRequiredSetupHandoff} = await handoffModule(h.react, {});
  const props = {serverName: 'Actual server', requirement: 'two-step', url: 'https://server.example/settings/profile', busy: false,
    copy: {body: 'auth.direct.lead', reference: 'activation.codeRefreshes'}, onSignInAgain() {}, onBack() {}};
  assert.throws(() => h.render(() => scope(TVRequiredSetupHandoff, {...props, url: 'https://server.example/settings?scope=account&section=profile'})));
  assert.throws(() => h.render(() => scope(TVRequiredSetupHandoff, {...props, url: 'https://server.example/account'})));
  const tree = h.render(() => scope(TVRequiredSetupHandoff, {...props, portico: true, url: 'https://web.getportico.tv/account'}));
  assert.equal(tree.props.portico, true);
  assert.equal(tree.props.eyebrow, defaultI18n.t('account.portico'));
  assert.equal(nodes(tree.props.aside).find(node => node.type === 'QR').props.value, 'https://web.getportico.tv/account');
});
