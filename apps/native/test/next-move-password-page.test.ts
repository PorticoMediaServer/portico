// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

const react = {...React, default: React};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.trailing)] : [];

test('move password: proves to the move client only, clears the typed password on hand-over and Back, and draws nothing off its step', async () => {
  const calls: string[] = [];
  let snapshot: {phase: string; busy: boolean; error?: unknown} = {phase: 'password', busy: false};
  const listeners = new Set<() => void>();
  const controller = {
    subscribe: (listener: () => void) => {listeners.add(listener); return () => listeners.delete(listener);},
    getSnapshot: () => snapshot,
    proveOldPassword: async (username: string, password: string) => {calls.push(`prove:${username}:${password.length}`);},
    cancel: () => calls.push('cancel'),
  };
  const hook = await screenModule(new URL('../src-next/pages/shared/accounts/useMovePassword.ts', import.meta.url), {react,
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'refused'})}});
  const {MovePasswordPage} = await screenModule(new URL('../src-next/pages/touch/accounts/MovePasswordPage.tsx', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/controls': {Button: 'Button', FormError: 'FormError', TextField: 'Field'},
    '../../../kit/accounts/AccountParts': {AccountFields: 'Fields', AccountHint: 'Hint'}, '../../../kit/states/StateActionButton': {StateActionButton: 'Action'},
    '../../../kit/frames': {SignInFrame: 'Frame'}, '../../shared/accounts/useMovePassword': hook});
  let tree: any;
  const host = createFabricHookHost(() => {tree = MovePasswordPage({controller: controller as any, serverName: 'Den', onBack: () => calls.push('back')});});
  await host.mount(); await host.flush();
  const render = async () => {await host.rerender(); await host.flush();};
  const field = (id: string) => nodes(tree).find(node => node.props.testID === `accounts-move-password-${id}`);
  const action = () => nodes(tree).find(node => node.type === 'Action').props.action;
  assert.equal(action().disabled, true);
  field('username').props.onChangeText('old-name'); field('password').props.onChangeText('secret'); await render();
  assert.equal(action().disabled, false);
  action().onPress(); await render();
  assert.deepEqual(calls, ['prove:old-name:6']);
  assert.equal(field('password').props.value, '', 'the password is cleared once handed over');
  field('password').props.onChangeText('again'); await render();
  snapshot = {phase: 'password', busy: true}; listeners.forEach(listener => listener()); await render();
  nodes(tree).find(node => node.type === 'Frame').props.back.onPress(); action().onPress();
  assert.deepEqual(calls, ['prove:old-name:6'], 'nothing runs while the move is busy');
  snapshot = {phase: 'password', busy: false, error: new Error('x')}; listeners.forEach(listener => listener()); await render();
  assert.equal(nodes(tree).find(node => node.type === 'FormError').props.children, 'refused');
  nodes(tree).find(node => node.type === 'Frame').props.back.onPress(); await render();
  assert.deepEqual(calls, ['prove:old-name:6', 'cancel', 'back']);
  assert.equal(field('password').props.value, '', 'Back clears the password');
  snapshot = {phase: 'done', busy: false}; listeners.forEach(listener => listener()); await render();
  assert.equal(tree, null);
  await host.unmount();
});
