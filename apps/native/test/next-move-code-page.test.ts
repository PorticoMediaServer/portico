// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

const react = {...React, default: React};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.trailing)] : [];

async function mount(isCurrent: () => boolean) {
  const calls: string[] = [];
  let snapshot: {phase: string; busy: boolean; username?: string; error?: unknown} = {phase: 'code', busy: false};
  const listeners = new Set<() => void>();
  const controller = {
    subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    getSnapshot: () => snapshot, isCurrentOpening: () => true,
    readCodeInput: (raw: string) => raw.startsWith('MOVE-') ? raw : undefined,
    previewCode: async (code: string) => {calls.push(`preview:${code}`);},
    redeemLocal: async (username: string, password: string) => {calls.push(`redeem:${username}:${password.length}`);},
    cancel: () => calls.push('cancel'),
  };
  const hook = await screenModule(new URL('../src-next/pages/shared/accounts/useMoveCode.ts', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/password-strength': {passwordStrength: (value: string) => ({acceptable: value.length >= 8, label: value.length >= 8 ? 'Strong' : 'Too short', level: value.length >= 8 ? 4 : 0})},
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'refused'})},
    '../../../platform/qr-scanner': {qrScannerAvailable: () => false, scanQRCode: async () => undefined},
    '../../../platform/app-session-storage': {subscribeNativeAppSessions: () => () => {}}});
  const {MoveCodePage} = await screenModule(new URL('../src-next/pages/touch/accounts/MoveCodePage.tsx', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/controls': {Button: 'Button', FormError: 'FormError', TextField: 'Field'},
    '../../../kit/accounts/AccountParts': {AccountFields: 'Fields', AccountLinks: 'Links'}, '../../../kit/states/StateActionButton': {StateActionButton: 'Action'},
    '../../../kit/frames': {SignInFrame: 'Frame'}, '../../../kit/accounts/PasswordStrengthMeter': {PasswordStrengthMeter: 'Meter'},
    '../../shared/accounts/useMoveCode': hook});
  let tree: any;
  const host = createFabricHookHost(() => {tree = MoveCodePage({controller: controller as any, isCurrent, onBack: () => calls.push('back')});});
  await host.mount(); await host.flush();
  const render = async () => {await host.rerender(); await host.flush();};
  const publish = async (next: typeof snapshot) => {snapshot = next; listeners.forEach(listener => listener()); await render();};
  const field = (id: string) => nodes(tree).find(node => node.props.testID === `accounts-move-${id}`);
  const action = () => nodes(tree).find(node => node.type === 'Action').props.action;
  return {calls, host, render, publish, field, action, tree: () => tree};
}

test('move code: a code the controller cannot read is refused in place; a readable one is previewed once and cleared', async () => {
  const page = await mount(() => true);
  page.field('code-field').props.onChangeText('nonsense'); await page.render();
  page.action().onPress(); await page.render();
  assert.equal(page.field('code-field').props.error, defaultI18n.t('accounts.move.codeInvalid'));
  assert.deepEqual(page.calls, []);
  page.field('code-field').props.onChangeText('MOVE-1234'); await page.render();
  assert.equal(page.field('code-field').props.error, undefined);
  page.action().onPress(); await page.render();
  assert.deepEqual(page.calls, ['preview:MOVE-1234']);
  assert.equal(page.field('code-field').props.value, '');
  await page.host.unmount();
});

test('move code: the new local sign-in needs a strong matching password, hands it over once and clears it; Back cancels', async () => {
  const page = await mount(() => true);
  await page.publish({phase: 'local', busy: false, username: 'old-name'});
  assert.equal(page.field('username').props.value, 'old-name');
  page.field('new-password').props.onChangeText('long-enough'); await page.render();
  page.field('confirm-password').props.onChangeText('long-enougH'); await page.render();
  assert.equal(page.field('confirm-password').props.error, defaultI18n.t('accounts.signIn.passwordsDiffer'));
  assert.equal(page.action().disabled, true);
  page.field('confirm-password').props.onChangeText('long-enough'); await page.render();
  page.action().onPress(); await page.render();
  assert.deepEqual(page.calls, ['redeem:old-name:11']);
  assert.equal(page.field('new-password').props.value, '');
  assert.equal(page.field('confirm-password').props.value, '');
  await page.publish({phase: 'local', busy: true, username: 'old-name'});
  nodes(page.tree()).find(node => node.type === 'Frame').props.back.onPress();
  assert.deepEqual(page.calls, ['redeem:old-name:11'], 'Back does nothing while busy');
  await page.publish({phase: 'local', busy: false, username: 'old-name'});
  nodes(page.tree()).find(node => node.type === 'Frame').props.back.onPress();
  assert.deepEqual(page.calls, ['redeem:old-name:11', 'cancel', 'back']);
  await page.publish({phase: 'done', busy: false});
  assert.equal(page.tree(), null);
  await page.host.unmount();
});

test('move code: once the captured controller is no longer current, edits, submit and Back do nothing', async () => {
  let current = true;
  const page = await mount(() => current);
  page.field('code-field').props.onChangeText('MOVE-1'); await page.render();
  current = false; await page.render();
  page.field('code-field').props.onChangeText('MOVE-2'); page.action().onPress();
  nodes(page.tree()).find(node => node.type === 'Frame').props.back.onPress(); await page.render();
  assert.deepEqual(page.calls, []);
  await page.host.unmount();
});
