// @fabric-react
/** Integrations: the Jellyfin-compatible routes switch reads and saves the rebuilt server's Integrations settings. */
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {IntegrationSwitchesSource} from '../../../packages/client-core/src/server-admin/integration-switches.ts';

type Node = {type: any; props: Record<string, any>};
const nodes = (value: any): Node[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];

async function fixture(refuse = false) {
  const sent: unknown[] = [];
  let kept = {assistants: false, compatibleRoutes: false};
  const context = {api: {request: async (path: string, method?: string, body?: Record<string, boolean>) => {
    sent.push(body === undefined ? [method, path] : [method, path, body]);
    if (refuse) throw Object.assign(new Error('refused'), {status: 403, code: 'forbidden'});
    if (method === 'PUT') kept = {...kept, ...body};
    return kept;
  }}};
  class Lifetime {live = true; private abort = new AbortController(); signal = this.abort.signal; current = () => this.live; dispose() {this.live = false; this.abort.abort();}}
  const {NativeIntegrationAdvanced} = await screenModule(new URL('../src-next/screens/settings-live/server/integrations/Advanced.tsx', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../../packages/client-core/src/server-admin/integration-switches': {IntegrationSwitchesSource},
    '../../../../kit/controls': {Switch: 'Switch'}, '../../../../kit/theme': {useTheme: () => ({colors: {textSecondary: '#ccc'}})},
    '../../../../kit/states': {StateView: 'StateView'}, '../../lifetime': {NativeScreenLifetime: Lifetime},
  });
  let tree: any;
  const runtime = {text: {t: (key: string) => key}, error: (_error: unknown, retry: () => void) => ({retry})};
  const host = createFabricHookHost(() => {tree = NativeIntegrationAdvanced({ports: {context}, runtime});});
  return {sent, host, tree: () => tree};
}

test('the compatible routes switch reads GET /v1/admin/settings/integrations and saves one change at once with PUT', async () => {
  const f = await fixture();
  try {
    await act(() => f.host.mount());
    const control = () => nodes(f.tree()).find(node => node.type === 'Switch')!;
    assert.equal(control().props.checked, false);
    await act(() => control().props.onCheckedChange(true));
    assert.equal(control().props.checked, true);
    assert.deepEqual(f.sent, [['GET', '/v1/admin/settings/integrations'], ['PUT', '/v1/admin/settings/integrations', {compatibleRoutes: true}]]);
    assert.equal(nodes(f.tree()).some(node => node.type === 'TextField'), false, 'no requests-per-minute field: the rebuilt server keeps no such setting');
  } finally {await f.host.unmount();}
});

test('a refused read draws no switch and offers Try again', async () => {
  const f = await fixture(true);
  try {
    await act(() => f.host.mount());
    assert.equal(nodes(f.tree()).some(node => node.type === 'Switch'), false);
    assert.ok(nodes(f.tree()).some(node => node.type === 'StateView'));
  } finally {await f.host.unmount();}
});
