import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {bindingModule, lifecycleHooks, nodes, content, t, i18n, theme} from './helpers/owner-server-settings.mjs';

async function fixture() {
  const bindings = await bindingModule(), host = lifecycleHooks();
  const person = {accountId: 'member-fixture', username: 'member', role: 'member', revision: 2, limitsRevision: 3, disabled: false, notes: '', allowedLibraries: [], limits: {allowRemote: true, maxStreamsMode: 'limit', maxStreams: 5, maxBitrateKbps: 8000}};
  const device = {id: 'device-fixture', accountId: person.accountId, name: 'Tablet', lastSeenAt: '2026-10-28T12:00:00Z'};
  const state = {devices: {person, items: [device], cursor: ''},tab: 'people', query: {search: '', role: 'all', status: 'all', sort: 'person', direction: 'asc'}, invitationStatus: '', cursor: '', loading: false, busy: false, opening: false, selected: person, person, people: {items: [person], matchedCount: 1, totalCount: 1, nextCursor: ''}};
  const context = {account: {account: {id: 'owner-fixture'}}, authority: () => ({accountId: 'owner-fixture'}), serverName: 'Test server'}, writes: any[] = [];
  const source = {context, ownerControls: () => true, current: () => true, subscribe: () => () => {}, getSnapshot: () => state, load: async () => {}, dispose() {}, loadLibraries() {}, loadDevices(captured: unknown) {writes.push({readDevices: captured});}, signOutDevices: async (captured: unknown, capturedDevice: unknown) => {writes.push({signOut: captured, device: capturedDevice});}, limits: async (captured: unknown, limits: unknown) => {writes.push({captured, limits});}};
  const runtime = {text: {t}, reportError: assert.fail, error: () => ({title: 'Error'})};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/PeoplePage.tsx', import.meta.url), {
    react: host.react, 'react-native': {FlatList: 'FlatList', ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    '../../../../../../packages/client-core/src/server-admin/people-words': {roleLabel: (_: unknown, role: string) => role},
    '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsSpec: []},
    '../../../../../../packages/i18n/src': {defaultI18n: {...i18n, date: (value: string) => value}},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Select: 'Select', Switch: 'Switch', Tabs: 'Tabs', TextField: 'TextField'},
    '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'}, './page-layout': {settingsPageInset: () => ({}), settingsPageHeading: () => ({})}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/states': {StateView: 'StateView'}, '../../../kit/theme': {useTheme: () => theme},
    './AdvancedSection': {NativeAdvancedSection: 'Advanced'}, './SettingControl': {NativeSettingControl: 'Setting'},
    './storage/forms': {}, './people-source': {NativePeopleSource: class {constructor() {return source;}}}, './InvitationReadySheet': {InvitationReadySheet: 'InvitationReadySheet'}, './PeopleMemberProfiles': {PeopleMemberProfiles: 'PeopleMemberProfiles'},
  });
  const ports = {context, requestId: () => 'operation-fixture'};
  // Render the private details component reached through the public page.
  const frames = () => host.render(() => {
    const element = module.NativeServerPeople({ports, runtime}), page = element ? element.type(element.props) : null;
    const details = nodes(page).find((node: any) => typeof node.type === 'function' && node.props.person === person);
    return {page, details: details ? details.type(details.props) : null};
  });
  const render = () => frames().details;
  render();host.flush();assert.ok(render());host.flush();
  return {render, page: () => frames().page, host, writes, person, device, source, state};
}

test('People keeps a saved non-preset stream limit visible and unchanged when another limit is edited', async () => {
  const {render, host, writes, person} = await fixture();
  nodes(render()).find((node: any) => node.props.testID === 'people-person-tabs').props.onValueChange('access');
  const streamSelect = () => nodes(render()).find((node: any) => node.type === 'Select' && node.props.label === t('web.access.maxStreams'));
  assert.equal(streamSelect().props.value, '5');
  assert.deepEqual(streamSelect().props.options.find((option: any) => option.value === '5'), {value: '5', label: '5', disabled: true});
  nodes(render()).find((node: any) => node.type === 'Switch').props.onCheckedChange(false);
  assert.equal(streamSelect().props.value, '5');
  nodes(render()).find((node: any) => node.props.testID === 'people-save-limits' && !node.props.disabled).props.onPress();
  assert.deepEqual(writes, [{captured: person, limits: {...person.limits, allowRemote: false}}]);
  streamSelect().props.onValueChange('6');
  assert.equal(streamSelect().props.value, '6');
  assert.equal(streamSelect().props.options.some((option: any) => option.value === '5'), false);
  host.dispose();
});


test('Person Devices is windowed and both sign-out actions wait for confirmation', async () => {
  const f = await fixture();
  try {
    const tabs = nodes(f.render()).find((node: any) => node.props.testID === 'people-person-tabs');
    assert.deepEqual(tabs.props.items.map((item: any) => item.value), ['account', 'access', 'profiles', 'devices', 'advanced']);
    tabs.props.onValueChange('devices');
    assert.deepEqual(f.writes, [{readDevices: f.person}]); f.writes.length = 0;
    const deviceList = nodes(f.render()).find((node: any) => node.props.testID === 'people-devices');
    assert.deepEqual(deviceList.props.data, [f.device]);
    assert.equal(nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.open && node.props.title === f.person.username).props.scrollable, false);
    const item = deviceList.props.renderItem({item: f.device});
    nodes(item).find((node: any) => node.props.testID === `people-device-signout-${f.device.id}`).props.onPress();
    assert.equal(f.writes.length, 0);
    let confirmation = nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.showClose === false);
    assert.equal(confirmation.props.open, true);
    await nodes(confirmation.props.footer).find((node: any) => node.type === 'Button' && content(node) === 'action.signOut').props.onPress();
    assert.deepEqual(f.writes, [{signOut: f.person, device: f.device}]); f.writes.length = 0;
    nodes(f.render()).find((node: any) => node.props.testID === 'people-devices-signout-all').props.onPress();
    assert.equal(f.writes.length, 0);
    confirmation = nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.showClose === false);
    await nodes(confirmation.props.footer).find((node: any) => node.type === 'Button' && content(node) === 'action.signOut').props.onPress();
    assert.deepEqual(f.writes, [{signOut: f.person, device: undefined}]);
  } finally {f.host.dispose();}
});


test('Person Profiles mounts the real read/editor surface with the exact selected person', async () => {
  const f = await fixture();
  try {
    nodes(f.render()).find((node: any) => node.props.testID === 'people-person-tabs').props.onValueChange('profiles');
    const profiles = nodes(f.render()).find((node: any) => node.type === 'PeopleMemberProfiles');
    assert.equal(profiles.props.person, f.person); assert.equal(profiles.props.people, f.source);
  } finally {f.host.dispose();}
});
