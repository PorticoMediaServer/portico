// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

const react = {...React, default: React};
const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.footer)] : [];

test('manage unlock: a new refusal reports the lock and asks for the PIN; only four digits are tried; a refusal clears the PIN; success closes', async () => {
  const reports: unknown[] = [], unlocks: string[] = [];
  let refusals = 0; const gateListeners = new Set<() => void>();
  const gate = {subscribe: (listener: () => void) => {gateListeners.add(listener); return () => gateListeners.delete(listener);}, getSnapshot: () => refusals};
  const ready = {kind: 'ready', manageLocked: true, viewer: {familyId: 'family', profileId: 'p1'}, profiles: [{id: 'p1', name: 'Ada'}]};
  const lifecycle = {subscribe: () => () => {}, getSnapshot: () => ({state: ready}), report: (event: unknown) => reports.push(event)};
  let refuse = true;
  const hook = await screenModule(new URL('../src-next/pages/shared/accounts/useManageUnlock.ts', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/errors': {presentError: () => ({body: 'refused'})},
    '../../../platform/native-session-lifecycle': {nativeSessionLifecycle: () => ({lifecycle}),
      unlockNativeManagement: async (_family: string, pin: string) => {unlocks.push(pin); if (refuse) throw new Error('wrong');}},
    '../../../platform/manage-gate': {nativeManageGate: gate},
    '../../../screens/accounts/profile-pin-failure': {profilePinFailureText: () => 'Wrong PIN'}});
  const {ManageUnlockPage} = await screenModule(new URL('../src-next/pages/touch/accounts/ManageUnlockPage.tsx', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/overlays': {Dialog: 'Dialog'}, '../../../kit/controls': {Button: 'Button', FormError: 'FormError'},
    '../../../kit/accounts/PhonePINEntry': {PhonePINEntry: 'PIN'}, '../../shared/accounts/useManageUnlock': hook});
  let tree: any;
  const host = createFabricHookHost(() => {tree = ManageUnlockPage();});
  await host.mount(); await host.flush();
  const render = async () => {await host.rerender(); await host.flush();};
  const dialog = () => nodes(tree).find(node => node.type === 'Dialog');
  const pin = () => nodes(tree).find(node => node.type === 'PIN');
  assert.equal(dialog().props.open, false);
  refusals = 1; gateListeners.forEach(listener => listener()); await render();
  assert.deepEqual(reports, [{type: 'manageRefused'}]);
  assert.equal(dialog().props.open, true);
  assert.equal(dialog().props.title, defaultI18n.t('profile.pinTitle', {name: 'Ada'}));
  pin().props.onComplete('12'); await render();
  assert.deepEqual(unlocks, [], 'only four digits are tried');
  pin().props.onChange('1234'); await render();
  pin().props.onComplete('1234'); await render(); await render();
  assert.deepEqual(unlocks, ['1234']);
  assert.equal(pin().props.value, '');
  assert.equal(nodes(tree).find(node => node.type === 'FormError').props.children, 'Wrong PIN');
  refuse = false; pin().props.onComplete('5678'); await render(); await render();
  assert.equal(dialog().props.open, false);
  await host.unmount();
});
