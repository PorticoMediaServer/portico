import test from 'node:test';
import assert from 'node:assert/strict';
import {React, fabricHost} from './helpers/fabric-host.mjs';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';
import type {NativeShellBootstrapContext} from '../src-next/shell/bootstrap';

/**
 * The phone's Server › General page on the rebuilt server: it reads the general, storage, notices and
 * remote-access settings documents (shapes as the demo server answers them, 16 Nov) and saves a changed row
 * with the revision it read. Without this an owner's General page fails to load or loses an edit.
 */
const documents: Record<string, {etag: string; values: Record<string, unknown>}> = {
  general: {etag: '"3"', values: {name: 'Portico Demo', backgroundSpeed: 'normal', updateCheck: 'automatic', heavyWork: {hours: 'any'}}},
  storage: {etag: '"0"', values: {warnPercent: 15, majorPercent: 8}},
  notices: {etag: '"0"', values: {serverNoticesTo: 'owner-and-admins', keepDays: 180}},
};
const remoteAccess = {advertisedInterface: '', remoteAccess: 'on', certificateWarningDays: 30, ownAddress: {enabled: false, domain: '', certificateFile: '', keyFile: ''}};

const textOf = (value: unknown): string => Array.isArray(value) ? value.map(textOf).join('')
  : typeof value === 'string' ? value
  : value && typeof value === 'object' && 'props' in value ? textOf((value as {props?: {children?: unknown}}).props?.children) : '';

test('General shows the rebuilt documents and saves a changed name with the revision it read', async t => {
  const saves: any[] = [];
  let restartRequests = 0;
  let refuse = false;
  const app = await standardHomeApp({
    accountRole: 'owner', mountUI: false,
    nativeResponse: async (request: any) => {
      const named = /^\/v1\/admin\/settings\/(general|storage|notices)$/.exec(request.path);
      if (named && request.method === 'GET') {
        const document = documents[named[1]!]!;
        return {status: 200, headers: {ETag: document.etag}, body: {name: named[1], values: document.values, defaults: document.values, etag: document.etag}};
      }
      if (named && request.method === 'PUT') {
        saves.push(request);
        if (refuse) return {status: 412, body: {error: {code: 'changed_elsewhere', message: 'These settings changed elsewhere.'}}};
        const document = documents[named[1]!]!;
        documents[named[1]!] = {etag: `"${Number(document.etag.slice(1, -1)) + 1}"`, values: {...document.values, ...request.body}};
        return {status: 200, body: {}};
      }
      if (request.path === '/v1/admin/settings/remote-access' && request.method === 'GET') return {status: 200, headers: {ETag: '"4"'}, body: remoteAccess};
      if (request.path === '/v1/admin/background/pause') return {status: 200, body: {}};
      if (request.path === '/v1/admin/updates/check') return {status: 500, body: {error: {code: 'internal', message: 'The update feed could not be checked.'}}};
      if (request.path === '/v1/admin/restart') {
        restartRequests++;
        return {status: 500, body: {error: {code: 'internal', message: 'The server did not restart.'}}};
      }
      if (request.path === '/v1/admin/updates') return {status: 200, body: {version: 'b4b19fd13dca', newer: false, install: 'systemd'}};
      return undefined;
    },
  });
  const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
  const {createNativeShellBindings} = await import('../src-next/shell/assembly');
  const {NativeScreenLifetime} = await import('../src-next/screens/settings-live/lifetime');
  const {createNativeSettingsPorts} = await import('../src-next/screens/settings-live/settings-ports');
  const {ThemeProvider} = await import('../src-next/kit/theme');
  const {SettingsListFrame} = await import('../src-next/kit/frames');
  const {ServerGeneralPage} = await import('../src-next/pages/shared/settings/ServerGeneralPage');
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
  await renderer.render(React.createElement(ThemeProvider, {width: 390, children: React.createElement(ServerGeneralPage, {ports, runtime})}));
  await settle();

  assert.equal(byTestId('general.name')?.props.value, 'Portico Demo', 'the name comes from the general document');
  assert.match(hosts().map(host => textOf(host.children)).join('\n'), /b4b19fd13dca/, 'the version comes from the updates answer');
  byTestId('server-general-advanced').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  assert.equal(byTestId('general.alertStorageWarn')?.props.accessibilityValue?.text, '15%');
  assert.equal(byTestId('general.alertStorageCritical')?.props.accessibilityValue?.text, '8%');
  assert.equal(byTestId('general.alertCertificateDays')?.props.accessibilityValue?.text, '30 days');
  assert.equal(byTestId('general.serverNoticesTo')?.props.accessibilityValue?.text, 'The owner and administrators');
  byTestId('general.name').props.onChangeText('Living room');
  await settle();
  pressable('Save').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  assert.equal(saves.length, 1);
  assert.equal(saves[0].path, '/v1/admin/settings/general');
  assert.equal(saves[0].headers['if-match'], '"3"', 'the save carries the revision the page read');
  assert.deepEqual(saves[0].body, {name: 'Living room'});
  assert.equal(pressable('Save'), undefined, 'a stored change leaves nothing to save');
  assert.equal(byTestId('general.name')?.props.value, 'Living room');

  // A save the server refuses (changed elsewhere) keeps the owner's words and says so.
  refuse = true;
  byTestId('general.name').props.onChangeText('Den');
  await settle();
  pressable('Save').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  assert.equal(saves.length, 2);
  assert.equal(saves[1].headers['if-match'], '"4"');
  assert.equal(byTestId('general.name')?.props.value, 'Den', 'the refused draft is kept');
  assert.ok(pressable('Save'), 'and can be saved again');

  byTestId('server-general-check').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  assert.match(hosts().map(host => textOf(host.children)).join('\n'), /b4b19fd13dca/, 'a failed check keeps the installed version visible');
  assert.ok(pressable('Try again'), 'a failed check shows a retry while a version is known');
  byTestId('server-general-restart-open').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  byTestId('server-general-restart-confirm').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();
  assert.equal(restartRequests, 1);
  assert.ok(byTestId('server-general-restart-error'), 'a failed restart is shown inside its confirmation');
  pressable('Cancel').props.onPress({nativeEvent: {}, stopPropagation() {}});
  await settle();

  const children = React.createElement(SettingsListFrame, {
    bar: {title: 'Settings'}, list: React.createElement(React.Fragment, null, 'Server index'),
    page: React.createElement(ServerGeneralPage, {ports, runtime}), testID: 'tablet-settings',
  });
  await renderer.render(React.createElement(ThemeProvider, {width: 900, children}));
  await settle();
  const scrolls = hosts().filter(host => host.name === 'ScrollView');
  assert.equal(scrolls.length, 2, 'the tablet has a scroll host for the index and another for the real General form');
  assert.ok(byTestId('general.name'), 'the General form still loads beside the index');
});
