import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';

/**
 * The phone's Server › Remote access page on the rebuilt server (shapes as the demo server answered them, 16 Nov):
 * it shows the server's own status and saves a changed row to the remote-access document at the revision it read,
 * without the read-only facts. Without this an owner's Remote access page fails to load or loses an edit.
 */
const remoteAccess = {advertisedInterface: '', remoteAccess: 'on', remoteSecurity: 'standard', secureConnections: 'preferred', signInFromOutside: 'anyone', portMapping: 'off', publicPort: 32500, ipv6: false,
  ownAddress: {enabled: true, domain: 'demo.getportico.tv', certificateFile: '', keyFile: ''}, allowedAddresses: [], findAtHome: true, enhancedWithoutAddress: false, enhancedWithoutHome: false, dlna: false, dlnaNames: [], certificateWarningDays: 14,
  interfaces: [{name: 'eth0', addresses: ['192.168.1.5']}]};
const status = {thisDevice: {atHome: false, address: '142.166.194.47'}, refused: [], remoteSecurity: {chosen: 'standard', active: 'standard'},
  warnings: [{code: 'plain_http_outside', message: 'Connections from outside your home networks may use plain HTTP.'}], certificate: {state: 'none'}, managedCertificate: {state: 'off', ready: false},
  listener: {address: '::', port: 32500}, router: {state: 'off'}, routerIPv6: {state: 'off'}, identityCode: 'C3HX-J9VK-QVV4', addresses: [{address: '66.94.102.39', port: 32500, home: false}]};
const network = {homeNetworks: ['127.0.0.0/8'], homeNetworksDefault: true, trustedProxies: ['127.0.0.1/32'], webOrigins: [], builtInWebOrigins: [], warnings: []};

const textOf = (value: unknown): string => Array.isArray(value) ? value.map(textOf).join('')
  : typeof value === 'string' ? value
  : value && typeof value === 'object' && 'props' in value ? textOf((value as {props?: {children?: unknown}}).props?.children) : '';

test('Remote access shows the server status and saves a changed row at the revision it read', async t => {
  const saves: any[] = [];
  const app = await standardHomeApp({
    accountRole: 'owner', mountUI: false,
    nativeResponse: async (request: any) => {
      if (request.path === '/v1/admin/settings/remote-access' && request.method === 'GET') return {status: 200, headers: {ETag: '"4"'}, body: remoteAccess};
      if (request.path === '/v1/admin/settings/remote-access' && request.method === 'PUT') {saves.push(request); return {status: 200, body: {}};}
      if (request.path === '/v1/admin/settings/network' && request.method === 'GET') return {status: 200, headers: {ETag: '"2"'}, body: network};
      if (request.path === '/v1/admin/remote-access') return {status: 200, body: status};
      if (request.path === '/v1/admin/blocked-networks') return {status: 200, body: {items: []}};
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime');
  const {createNativeSettingsPorts} = await import('../src-next/screens/settings-live/settings-ports');
  const {ThemeProvider} = await import('../src-next/kit/theme');
  const {NativeRemoteAccessPage} = await import('../src-next/screens/settings-live/server/RemoteAccessPage');
  const renderer = await fabricHost();
  let captured: NativeShellBootstrapContext | undefined;
  const runtime = await restoreNativeShellRuntime(context => {captured = context; return createNativeShellBindings(context);}, {onAuthorityEnded() {}});
  assert.ok(runtime && captured);
  const lifetime = new NativeScreenLifetime(captured);
  const ports = createNativeSettingsPorts(captured, lifetime, () => {});
  t.after(async () => {try {await renderer.unmount();} finally {try {await app.unmount();} finally {app.disposeLoader(); lifetime.dispose(); runtime.dispose();}}});
  const settle = async () => {await renderer.settle(); await app.settle();};
  const hosts = () => app.hosts() as any[];
  const byTestId = (id: string) => hosts().find(host => host.props?.testID === id);
  const pressable = (label: string) => hosts().find(host => host.name === 'Pressable' && app.nativeLabel(host) === label);
  await renderer.render(React.createElement(ThemeProvider, {width: 390, children: React.createElement(NativeRemoteAccessPage, {ports, runtime})}));
  await settle();

  const text = hosts().map(host => textOf(host.children)).join('\n');
  assert.match(text, /may use plain HTTP/, 'the server\'s own warning is shown');
  assert.match(text, /C3HX-J9VK-QVV4/, 'the identity code is the server\'s');
  const discovery = byTestId('remote.discovery');
  assert.ok(discovery, 'Find this server at home is a row the rebuilt document stores');
  discovery.props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  pressable('Save').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  assert.equal(saves.length, 1);
  assert.equal(saves[0].headers['if-match'], '"4"', 'the save carries the revision the page read');
  assert.equal(saves[0].body.findAtHome, false);
  assert.equal('interfaces' in saves[0].body, false, 'read-only facts are never sent back');
  assert.equal(pressable('Save'), undefined, 'a stored change leaves nothing to save');
});
