import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {bindingModule, lifecycleHooks, nodes, content, t, i18n, specification} from './helpers/owner-server-settings.mjs';
import {createStorageForms} from '../src-next/screens/settings-live/server/storage/forms.ts';

async function fixture(forms = {}) {
  const bindings = await bindingModule(), host = lifecycleHooks();
  const person = {accountId: 'member-fixture', username: 'member', role: 'member', revision: 2, limitsRevision: 3, disabled: false, notes: '', allowedLibraries: [], limits: {allowRemote: true, maxStreamsMode: 'limit', maxStreams: 5, maxBitrateKbps: 8000}, accountLimits: {homeOnly: false}};
  const device = {id: 'device-fixture', accountId: person.accountId, name: 'Tablet', lastSeenAt: '2026-10-28T12:00:00Z'};
  const state = {devices: {person, items: [device], cursor: ''},tab: 'people', query: {search: '', role: 'all', status: 'all', sort: 'person', direction: 'asc'}, invitationStatus: '', cursor: '', loading: false, busy: false, opening: false, selected: person, person, people: {items: [person], matchedCount: 1, totalCount: 1, nextCursor: ''}};
  const context = {account: {account: {id: 'owner-fixture'}}, authority: () => ({accountId: 'owner-fixture'}), serverName: 'Test server'}, writes: any[] = [];
  const source = {context, ownerControls: () => true, current: () => true, subscribe: () => () => {}, getSnapshot: () => state, load: async () => {}, dispose() {}, loadLibraries() {}, loadDevices(captured: unknown) {writes.push({readDevices: captured});}, signOutDevices: async (captured: unknown, capturedDevice: unknown) => {writes.push({signOut: captured, device: capturedDevice});}, limits: async (captured: unknown, limits: unknown) => {writes.push({captured, limits});}};
  const runtime = {text: {t}, reportError: assert.fail, error: () => ({title: 'Error'})};
  const model = await screenModule(new URL('../src-next/pages/shared/settings/useServerPeople.ts', import.meta.url), {
    react: host.react, '../../../../../../packages/client-core/src/server-admin/people-words': {roleLabel: (_: unknown, role: string) => role},
    '../../../screens/settings-live/server/people-source': {NativePeopleSource: class {constructor() {return source;}}},
  });
  const detailModel = await screenModule(new URL('../src-next/pages/shared/settings/useServerPeopleDetails.ts', import.meta.url), {
    react: host.react, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../screens/settings-live/server/storage/forms': forms,
  });
  const list = await screenModule(new URL('../src-next/kit/settings/SettingsDataList.tsx', import.meta.url), {
    react: host.react, 'react-native': {FlatList: 'FlatList', View: 'View'}, '../frames/metrics': {useFrameMetrics: () => ({})},
  });
  const chrome = {
    react: host.react, '../../../../../../packages/i18n/src': {defaultI18n: {...i18n, date: (value: string) => value}},
    '../../../kit/controls': {Button: 'Button', Segmented: 'Segmented', Select: 'Select', Switch: 'Switch', Tabs: 'Tabs', TextField: 'TextField'},
    '../../../kit/frames': {PanelBlock: 'PanelBlock', SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame', StateRow: 'StateRow'},
    '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'},
    '../../../kit/settings/SettingsDataList': list, '../../../kit/settings/SettingsParts': {SettingsHelp: 'Text'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/states': {StateView: 'StateView'}, './useServerPeople': model,
  };
  const detailsModule = await screenModule(new URL('../src-next/pages/shared/settings/ServerPeopleDetails.tsx', import.meta.url), {
    ...chrome, './useServerPeopleDetails': detailModel, './ServerMemberProfiles': {ServerMemberProfiles: 'PeopleMemberProfiles'},
    '../../../screens/settings-live/server/AdvancedSection': {NativeAdvancedSection: 'Advanced'},
    '../../../screens/settings-live/server/SettingControl': {NativeSettingControl: 'Setting'},
  });
  const {ServerPeoplePage} = await screenModule(new URL('../src-next/pages/touch/settings/ServerPeoplePage.tsx', import.meta.url), {
    ...chrome, '../../shared/settings/useServerPeople': model, '../../shared/settings/ServerPeopleDetails': detailsModule, '../../shared/settings/ServerInvitationReady': {InvitationReadySheet: 'InvitationReadySheet'},
  });
  const ports = {context, requestId: () => 'operation-fixture'};
  const frames = () => host.render(() => {
    const panel = ServerPeoplePage({ports, runtime}).props.panel;
    const page = panel ? panel.type(panel.props) : null;
    const details = nodes(page).find((node: any) => node.type === detailsModule.ServerPersonDetails);
    let body = details ? details.type(details.props) : null;
    if (body?.type?.name === 'PeopleLibraryAccess') body = body.type(body.props);
    if (body?.type === list.SettingsDataList) body = body.type(body.props);
    return {page, details: body};
  });
  const render = () => frames().details;
  render();host.flush();assert.ok(render());host.flush();
  return {render, page: () => frames().page, host, writes, person, device, source, state};
}

test('People Advanced reads the default stream limit from the rebuilt server', async () => {
  const authority = {serverId: 'server', accountId: 'owner', profileId: 'main', epoch: '1', viewerFence: 'fence'};
  const reads: unknown[] = [];
  const context = {current: () => true, authority: () => authority, subscribeAuthority: () => () => {},
    account: {serverId: 'server', account: {id: 'owner', role: 'owner', disabled: false, primaryProfileId: 'main'}, capabilities: {manageServer: true}},
    api: {request: async (path: string, method: string) => {reads.push([method, path]);return {perPerson: 3};}},
  };
  let group: ReturnType<typeof createStorageForms> | undefined;
  const f = await fixture({
    useStorageForms: (_ports: unknown, ids: Parameters<typeof createStorageForms>[1]) => group ??= createStorageForms(context as never, ids, error => {throw error;}),
    useFormState: (form: ReturnType<typeof createStorageForms>['forms']['streamLimits']) => form.getSnapshot(),
    useStorageSet: (value: ReturnType<typeof createStorageForms>) => value.set.getSnapshot(),
  });
  try {
    const advanced = nodes(f.page()).find((node: any) => node.type?.name === 'SettingsDataList');
    assert.ok(advanced);
    const panel = nodes(advanced.props.footer).find((node: any) => node.type?.name === 'ServerPeopleAdvanced');
    f.host.render(() => panel.type(panel.props));
    assert.deepEqual(Object.keys(group!.forms), ['streamLimits']);
    await group!.forms.streamLimits.load();
    const tree = f.host.render(() => {const body = panel.type(panel.props);return body.type(body.props);});
    assert.equal(nodes(tree).find((node: any) => node.props.row?.id === 'people.maxStreamsEach')?.props.value, '3');
    assert.deepEqual(reads, [['GET', '/v1/admin/playback/stream-limits']]);
  } finally {group?.dispose();f.host.dispose();}
});

test('People keeps a saved non-preset stream limit visible and unchanged when another limit is edited', async () => {
  const {render, host, writes, person} = await fixture();
  nodes(render()).find((node: any) => node.props.testID === 'people-person-tabs').props.onValueChange('access');
  const listFooter = () => render().props.ListFooterComponent;
  const streamSelect = () => nodes(listFooter()).find((node: any) => node.props.control?.type === 'Select' && node.props.label === t('web.access.maxStreams')).props.control;
  assert.equal(streamSelect().props.value, '5');
  assert.deepEqual(streamSelect().props.options.find((option: any) => option.value === '5'), {value: '5', label: '5', disabled: true});
  nodes(listFooter()).find((node: any) => node.props.control?.type === 'Switch').props.control.props.onCheckedChange(false);
  assert.equal(streamSelect().props.value, '5');
  nodes(listFooter()).find((node: any) => node.props.testID === 'people-save-limits' && !node.props.disabled).props.onPress();
  assert.deepEqual(writes, [{captured: person, limits: {...person.limits, allowRemote: false}}]);
  streamSelect().props.onValueChange('6');
  assert.equal(streamSelect().props.value, '6');
  assert.equal(streamSelect().props.options.some((option: any) => option.value === '5'), false);
  host.dispose();
});


test('Person Devices is windowed and both sign-out actions wait for confirmation', async () => {
  const f = await fixture();
  try {
    const tabs = nodes(f.render().props.tabs).find((node: any) => node.props.testID === 'people-person-tabs');
    assert.deepEqual(tabs.props.items.map((item: any) => item.value), ['account', 'access', 'profiles', 'devices', 'advanced']);
    tabs.props.onValueChange('devices');
    assert.deepEqual(f.writes, [{readDevices: f.person}]); f.writes.length = 0;
    const deviceList = f.render();
    assert.equal(deviceList.props.testID, 'people-devices');
    assert.deepEqual(deviceList.props.data, [f.device]);
    assert.equal(nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.open && node.props.title === f.person.username).props.scrollable, false);
    const item = deviceList.props.renderItem({item: f.device});
    nodes(item).find((node: any) => node.props.testID === `people-device-signout-${f.device.id}`).props.onPress();
    assert.equal(f.writes.length, 0);
    let confirmation = nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.showClose === false);
    assert.equal(confirmation.props.open, true);
    await nodes(confirmation.props.footer).find((node: any) => node.type === 'Button' && content(node) === 'action.signOut').props.onPress();
    assert.deepEqual(f.writes, [{signOut: f.person, device: f.device}]); f.writes.length = 0;
    nodes(f.render().props.ListFooterComponent).find((node: any) => node.props.testID === 'people-devices-signout-all').props.onPress();
    assert.equal(f.writes.length, 0);
    confirmation = nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.showClose === false);
    await nodes(confirmation.props.footer).find((node: any) => node.type === 'Button' && content(node) === 'action.signOut').props.onPress();
    assert.deepEqual(f.writes, [{signOut: f.person, device: undefined}]);
  } finally {f.host.dispose();}
});


test('Person Profiles mounts the real read/editor surface with the exact selected person', async () => {
  const f = await fixture();
  try {
    nodes(f.render().props.tabs).find((node: any) => node.props.testID === 'people-person-tabs').props.onValueChange('profiles');
    const profiles = nodes(f.render()).find((node: any) => node.type === 'PeopleMemberProfiles');
    assert.equal(profiles.props.person, f.person); assert.equal(profiles.props.people, f.source);
  } finally {f.host.dispose();}
});

test('the owner\'s Let back in sits on a Left row only and asks once before it is sent (area 2 rule 39)', async () => {
  const f = await fixture();
  try {
    const left = {...f.person, accountId: 'left-fixture', username: 'left', status: 'left', allowedLibraries: [], libraryNames: [], profileNames: [], effectiveMaxStreams: null, liveState: 'idle'};
    const active = {...left, accountId: 'active-fixture', username: 'active', status: 'active'};
    (f.source as any).letBackIn = async (captured: unknown) => {f.writes.push({letBackIn: captured});};
    const list = () => nodes(f.page()).find((node: any) => node.props.testID === 'server-people');
    const button = (item: unknown, id: string) => nodes(list().props.row(item, 0)).find((node: any) => node.props.testID === `people-let-back-in-${id}`);
    assert.equal(button(active, 'active-fixture'), undefined, 'no button for someone who has not left');
    button(left, 'left-fixture').props.onPress();
    assert.equal(f.writes.length, 0, 'nothing is sent before it is confirmed');
    const confirmation = nodes(f.page()).find((node: any) => node.type === 'BottomSheet' && node.props.showClose === false);
    assert.equal(confirmation.props.open, true);
    // The page names the person in the title; this helper's text is the key itself.
    assert.equal(confirmation.props.title, (t as (key: string, values?: unknown) => string)('next.settings.letBackInTitle', {name: 'left'}));
    assert.ok(nodes(confirmation).some((node: any) => node.type === 'Text' && content(node) === t('next.settings.letBackInBody')));
    await nodes(confirmation.props.footer).find((node: any) => node.type === 'Button' && content(node) === t('next.settings.letBackIn')).props.onPress();
    assert.deepEqual(f.writes, [{letBackIn: left}]);
    (f.source as any).ownerControls = () => false;
    assert.equal(button(left, 'left-fixture'), undefined, 'only the owner sees it');
  } finally {f.host.dispose();}
});
