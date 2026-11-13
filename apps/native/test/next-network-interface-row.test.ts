import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {lifecycleHooks, nodes} from './helpers/owner-server-settings.mjs';
import {NativeScreenLifetime, screenScopeEnded} from '../src-next/screens/settings-live/lifetime.ts';
import * as network from '../../../packages/client-core/src/server-admin/network-interface.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

const settle = () => new Promise(resolve => setImmediate(resolve));
test('native interface row shows automatic fallback, saves with its revision and refuses retired presses', async () => {
 const host = lifecycleHooks(), calls: any[] = [];
 let current = true, selected = 'gone';
 const authority = {serverId: 'server', profileId: 'profile'};
 const context: any = {current: () => current, authority: () => authority, subscribeAuthority: () => () => {}, api: {
  requestRaw: async (path: string, method: string, options: any) => {
   calls.push({path, method, ...options});
   if (method === 'PUT') {assert.equal(options.headers['If-Match'], '"3"');assert.equal(options.body.remoteAccess, 'off');selected = options.body.advertisedInterface;}
   return {status: 200, headers: {etag: '"3"'}, body: {remoteAccess: 'off', advertisedInterface: selected, interfaces: [{name: 'eth0', addresses: ['192.168.1.2']}], ...(selected ? {interfaceNote: 'The selected interface is unavailable. Using Automatic until it returns.'} : {})}};
  },
 }};
 const runtime: any = {text: {t: defaultI18n.t}, error: () => ({title: 'Error'})};
 const module = await screenModule(new URL('../src-next/screens/settings-live/server/NetworkInterfaceRow.tsx', import.meta.url), {
  react: host.react, 'react-native': {View: 'View'}, '../../../../../../packages/client-core/src/server-admin/network-interface': network,
  '../../../kit/controls': {Select: 'Select'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/states': {StateView: 'StateView'},
  '../lifetime': {NativeScreenLifetime, screenScopeEnded}, './storage/space-source': {nativeStorageOwner: () => current},
 });
 const render = () => host.render(() => module.NativeNetworkInterfaceRow({context, runtime}));
 render();host.flush();await settle();
 const row = nodes(render()).find((node: any) => node.props.testID === 'remote-network-interface');
 assert.match(row.props.description, /Using Automatic/);
 const select = nodes(render()).find((node: any) => node.props.testID === 'remote-network-interface-select');
 assert.equal(select.props.value, 'gone');
 assert.ok(select.props.options.some((option: any) => option.value === '' && option.label === 'Automatic'));
 select.props.onValueChange('');await settle();
 assert.equal(calls.filter(call => call.method === 'PUT').length, 1);
 const retained = nodes(render()).find((node: any) => node.props.testID === 'remote-network-interface-select').props.onValueChange;
 assert.equal(nodes(render()).find((node: any) => node.props.testID === 'remote-network-interface-select').props.value, '');
 current = false;host.dispose();retained('eth0');await settle();
 assert.equal(calls.filter(call => call.method === 'PUT').length, 1);
});
