// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';

const react = {...React, default: React};

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];

test('offline servers: a row retries only while its opening is current, nothing is busy and the row is still listed', async () => {
  const calls: string[] = [];
  const guard = await screenModule(new URL('../src-next/pages/shared/accounts/useOfflineServersGuard.ts', import.meta.url), {react});
  const hook = await screenModule(new URL('../src-next/pages/shared/accounts/useOfflineServers.ts', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n}, './useOfflineServersGuard': guard});
  const {OfflineServersPage} = await screenModule(new URL('../src-next/pages/touch/accounts/OfflineServersPage.tsx', import.meta.url), {react,
    '../../../../../../packages/i18n/src': {defaultI18n}, '../../../kit/controls': {Button: 'Button'},
    '../../../kit/accounts/OfflineServersParts': {OfflineServersFrame: 'Frame', OfflineServerRow: 'Row', OfflineServersCountdown: 'Countdown', OfflineServersNote: 'Note'},
    '../../shared/accounts/useOfflineServers': hook});
  let current = true, busy = false, rows = [{id: 'home', name: 'Home server'}];
  const scope = {};
  const props = () => ({rows, scope, revision: 1, isCurrent: () => current, busy, onRetry: (id: string) => calls.push(`retry:${id}`),
    onBack: () => calls.push('back'), onRefresh: () => calls.push('refresh'), onFind: () => calls.push('find')});
  let tree: any;
  const host = createFabricHookHost(() => {tree = OfflineServersPage(props());});
  await host.mount(); await host.flush();
  const render = async () => {await host.rerender(); await host.flush();};
  const row = () => nodes(tree).find(node => node.type === 'Row');
  const button = (label: string) => nodes(tree).find(node => node.type === 'Button' && node.props.children === label);
  row().props.onRetry();
  button(defaultI18n.t('connection.discovery.find')).props.onPress();
  assert.deepEqual(calls, ['retry:home', 'find']);
  busy = true; await render(); row().props.onRetry(); button(defaultI18n.t('accounts.servers.refresh')).props.onPress();
  assert.deepEqual(calls, ['retry:home', 'find'], 'nothing runs while busy');
  busy = false; current = false; await render(); row().props.onRetry(); button(defaultI18n.t('action.back')).props.onPress();
  assert.deepEqual(calls, ['retry:home', 'find'], 'nothing runs once the directory is no longer current');
  current = true; await render(); const stale = row().props.onRetry; rows = [{id: 'other', name: 'Other'}]; await render(); stale();
  assert.deepEqual(calls, ['retry:home', 'find'], 'a row the opening no longer lists does not retry');
  await host.unmount();
});
