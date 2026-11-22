// @fabric-react
/** Settings › This device › Advanced › Test connection: what it asks the rebuilt server. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children), ...nodes(value.props.control)] : [];

test('Test connection reads GET /v1/system over the route in use and says the server answered', async () => {
  const sent: {path: string; method?: string}[] = [];
  const {NativeTestConnectionRow} = await screenModule(new URL('../src-next/pages/shared/settings/ConnectionRows.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {View: 'View'},
    '../../../../../../packages/client-core/src/connection-policy': {}, '../../../../../../packages/client-core/src/route-connection': {},
    '../../../../../../packages/client-core/src/query/server-api': await import('../../../packages/client-core/src/query/server-api.ts'),
    '../../../kit/controls': {Button: 'Button', Select: 'Select'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../kit/settings/DeviceRows': {DeviceRowBlock: 'DeviceRowBlock'},
    '../../../platform/connection-preference': {}, '../../../platform/device-settings': {}, '../../../shell/runtime': {}, '../../../screens/settings-live/settings-panel-deps': {},
  });
  const context = {serverName: 'Home', api: {request: async (path: string, method?: string) => {sent.push({path, method}); return {id: 'srv'};}}};
  const lifetime = {request: <T,>(work: (signal: AbortSignal) => Promise<T>) => work(new AbortController().signal)};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeTestConnectionRow({context, lifetime, t: (key: string) => key, runtime: {error: () => ({})}});});
  try {
    await act(() => host.mount());
    await act(() => nodes(tree).find(node => node.props.testID === 'settings-test-connection-button').props.onPress());
    assert.deepEqual(sent, [{path: '/v1/system', method: 'GET'}]);
    assert.equal(nodes(tree).find(node => node.props.testID === 'settings-test-connection').props.description, 'next.settingsSpec.account.device.advanced.groups.4.rows.2.control.result');
    const block = nodes(tree).find(node => node.type === 'DeviceRowBlock');
    assert.equal(block.props.error, undefined, 'no error'); assert.equal(block.props.errorContext, 'settings');
  } finally {await host.unmount();}
});
