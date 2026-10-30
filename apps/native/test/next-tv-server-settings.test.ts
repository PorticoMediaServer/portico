import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {maintenance} from './helpers/owner-server-settings.mjs';
import {settingsSpec, settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec';
import {settingsBinding, settingsBindingPatch, applySettingsBindingPatch} from '../../../packages/client-core/src/presentation/settings-bindings';
import {tvCustomChoiceValue, tvServerSettingGroups, tvServerSettingsSources, type TVServerSettingsState} from '../src-next/screens/settings-live/server/tv-server-settings-model';
import {settingNumber} from '../src-next/screens/settings-live/server/settings-values';

const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children), ...nodes(node.props.footer)] : [];
const feed = 'https://updates.example/releases.json';
const field = (id: string, value: unknown, extra: Record<string, unknown> = {}) => ({id, key: id, current: value, effective: value, default: value, source: 'default', configOnly: false, applicationMode: 'live', ...extra});
const configuration = (...fields: ReturnType<typeof field>[]) => ({revision: 1, registryRevision: 'r', path: '/etc/portico/config.toml', fields: [field('general.updateFeed', feed), ...fields]}) as any;
const runtime = {name: 'Den', 'updates.feedUrl': feed, perAccountCap: null, playHistoryDays: 0, 'library.missingTitles': 'after_days', 'library.missingTitlesDays': 46, 'people.invitation_days': 7};
const alerts = {storageWarningPercent: 10, storageCriticalPercent: 5, certificateWarningDays: 14, noticeAudience: {scan: 'both', storage: 'both', certificate: 'both', recording: 'both', feedback: 'owner'}};
const section = (id: string) => settingsSpec.find(value => value.scope === 'server' && value.id === id)!;
const state = (page: TVServerSettingsState['page'], drafts: TVServerSettingsState['drafts'], extra: Partial<TVServerSettingsState> = {}): TVServerSettingsState =>
  ({page, configuration: configuration(), drafts, custom: new Set(), ...extra});
const items = (value: TVServerSettingsState, advanced = false) => tvServerSettingGroups(value, advanced ? section(value.page).advanced?.groups : section(value.page).groups).flatMap(group => group.items);
const ids = (value: TVServerSettingsState, advanced = false) => items(value, advanced).map(item => item.id);

test('General on TV edits its bound rows, the alert thresholds and notice routing, and keeps config-file and closed custom rows out', () => {
  const general = state('general', {runtime, maintenance: maintenance(), alerts});
  const main = items(general);
  assert.deepEqual(main.map(item => [item.id, item.kind]), [['general.name', 'text'], ['general.updateChecks', 'choice'], ['general.heavyWork', 'choice'], ['general.backgroundSpeed', 'choice']]);
  assert.equal((main[0] as any).value, 'Den');
  assert.equal((main[1] as any).value, 'auto');
  assert.equal((main[2] as any).value, 'overnight');
  // Advanced holds the alert thresholds and who sees each notice; the heavy window opens only for Custom.
  assert.deepEqual(ids(general, true), ['general.alertStorageWarn', 'general.alertStorageCritical', 'general.alertCertificateDays', 'general.noticeAudience']);
  const audience = items(general, true).find(item => item.id === 'general.noticeAudience') as any;
  assert.equal(audience.kind, 'matrix');
  assert.equal(audience.values.feedback, 'owner');
  const custom = state('general', {runtime, maintenance: maintenance(), alerts}, {custom: new Set(['general.heavyWork', 'general.updateChecks'])});
  assert.deepEqual(ids(custom, true), ['general.updateFeed', 'general.heavyStart', 'general.heavyLength', 'general.heavyDays', 'general.alertStorageWarn', 'general.alertStorageCritical', 'general.alertCertificateDays', 'general.noticeAudience']);
  const days = items(custom, true).find(item => item.id === 'general.heavyDays') as any;
  assert.equal(days.kind, 'matrix');
  assert.equal(days.items.length, 7);
  assert.deepEqual(days.columns.map((column: any) => column.value), ['runs', 'skips']);
  // A value only the config file sets is not offered for change.
  const pinned = state('general', {runtime, maintenance: maintenance(), alerts}, {configuration: configuration(field('general.name', 'Den', {configOnly: true}))});
  assert.equal(ids(pinned).includes('general.name'), false);
});

test('an exact server value is drawn as itself, never rounded to a named choice', () => {
  const storage = state('storage', {runtime, maintenance: {...maintenance(), backupKeepCount: 9}});
  const keep = items(storage).find(item => item.id === 'storage.backupKeep') as any;
  assert.ok(keep, 'backups to keep is editable on TV');
  const exact = keep.options.find((option: any) => option.value === keep.value);
  assert.ok(exact);
  if (!section('storage').groups!.flatMap(group => group.rows).find(row => row.id === 'storage.backupKeep')!.control.options!.some(option => String(option.value) === keep.value)) assert.equal(exact.disabled, true);
  // The folder is chosen with the phone's explorer and is not drawn as a TV row.
  assert.equal(ids(storage).includes('storage.backupFolder'), false);
});

test('Custom stores a window mode or the current removal days; other customs only open their exact rows', () => {
  const drafts = {runtime, maintenance: maintenance()};
  assert.equal(tvCustomChoiceValue(settingsRowIndex.get('general.heavyWork')!.row, drafts), 'custom');
  assert.deepEqual(tvCustomChoiceValue(settingsRowIndex.get('storage.missingRule')!.row, drafts), {days: 46});
  assert.deepEqual(tvCustomChoiceValue(settingsRowIndex.get('storage.missingRule')!.row, {runtime: {...runtime, 'library.missingTitlesDays': 0}}), {days: 30});
  assert.equal(tvCustomChoiceValue(settingsRowIndex.get('general.updateChecks')!.row, drafts), undefined);
  // The stored value round-trips through the same binding the phone saves with.
  const libraries = state('libraries', {runtime: {...runtime, 'library.missingTitles': 'never'}});
  const row = settingsRowIndex.get('storage.missingRule')!.row;
  const patch = settingsBindingPatch(settingsBinding(row.id)!, tvCustomChoiceValue(row, libraries.drafts), tvServerSettingsSources(libraries));
  assert.deepEqual(applySettingsBindingPatch(libraries.drafts.runtime!, patch), {...runtime, 'library.missingTitles': 'after_days', 'library.missingTitlesDays': 46});
  const opened = state('libraries', {runtime: applySettingsBindingPatch(libraries.drafts.runtime!, patch)}, {custom: new Set(['storage.missingRule'])});
  assert.deepEqual(items(opened, true).map(item => [item.id, item.kind, (item as any).value]), [['storage.missingDaysExact', 'number', '46']]);
});

test('Remote access offers the Portico Services switch only for a linked server and opens own-address rows on demand', () => {
  const connectivity = {remoteSignInPolicy: 'allow', lanDiscoveryEnabled: true, dlnaEnabled: false, ownAddressEnabled: false, customCertificateDomain: '', customCertificatePath: '', customCertificateKeyPath: '', advertisedInterface: '', connectionLimit: 0, lanNetworks: [], accessUrls: [], trustedProxies: []};
  const local = state('remote', {connectivity});
  assert.equal(ids(local).includes('remote.secure'), false);
  assert.equal(ids(local).includes('remote.externalAddress'), false);
  assert.ok(ids(local).includes('remote.who'));
  assert.ok(ids(local).includes('remote.dlna'));
  // Lists of networks and proxies stay with the phone and web.
  assert.equal(ids(local, true).includes('remote.trustedProxies'), false);
  const linked = state('remote', {connectivity: {...connectivity, ownAddressEnabled: true, customCertificateDomain: 'media.example'}}, {secure: true});
  const shown = items(linked);
  assert.deepEqual(shown.find(item => item.id === 'remote.secure'), {id: 'remote.secure', row: settingsRowIndex.get('remote.secure')!.row, dependent: false, kind: 'switch', checked: true});
  assert.equal((shown.find(item => item.id === 'remote.externalAddress') as any).value, 'https://media.example');
});

test('a page never draws another page’s document', () => {
  const people = state('people', {runtime, maintenance: maintenance()});
  assert.ok(ids(people, true).includes('people.maxStreamsEach'));
  assert.equal(ids(people, true).some(id => id.startsWith('general.')), false);
  assert.deepEqual(items(state('history', {}), true), []);
});

/** Two components render through one module, so each gets its own hook slots. */
async function entryRow() {
  const row = hooks(), sheet = hooks(), saved: unknown[] = [];
  let current = row;
  const react: any = new Proxy({}, {get: (_, key: string) => key === 'default' ? react : key === 'useId' ? () => 'id' : key === 'Fragment' ? 'Fragment' : (...args: unknown[]) => {const hook = (current.react as any)[key]; if (typeof hook !== 'function') throw new Error(`no hook ${key}`); return hook(...args);}});
  const host = {render: (fn: () => any) => {current = row; return row.render(fn);}, sheet: (fn: () => any) => {current = sheet; return sheet.render(fn);}};
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/TVSettingRows.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../kit/controls': {Button: 'Button', Focusable: 'Focusable', Switch: 'Switch'},
    '../../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => ({focus() {}})},
    '../../../kit/overlays': {Banner: 'Banner', Menu: 'Menu', SidePanel: 'SidePanel'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/shell/visual': {shellType: () => ({})},
    '../../../kit/theme': {useTheme: () => ({colors: {text: 'text', textSecondary: 'secondary'}})},
    '../../search/TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'}, './settings-values': {settingNumber},
  });
  return {host, module, saved};
}

test('a secret row shows only whether a value is kept, and its sheet starts empty and masks what is typed', async () => {
  const {host, module, saved} = await entryRow();
  const t = (id: string, values?: {count?: number}) => values?.count === undefined ? id : `${id}:${values.count}`;
  const render = () => host.render(() => module.TVEntryRow({testID: 'metadata.tmdb', label: 'TMDB', kind: 'secret', configured: true, t, onSave: (value: unknown) => saved.push(value)}));
  let tree = render();
  const row = nodes(tree).find(node => node.type === 'SettingsRow');
  assert.equal(row.props.value, 'next.tvServer.secretSet');
  row.props.onPress(); tree = render();
  const sheet = nodes(tree).find(node => typeof node.type === 'function');
  const panel = () => host.sheet(() => sheet.type(sheet.props));
  let drawn = panel();
  const keyboard = () => nodes(drawn).find(node => node.type === 'NativeTVKeyboard');
  assert.equal(keyboard().props.value, '');
  keyboard().props.onChangeText('s3cret'); drawn = panel();
  const text = nodes(drawn).filter(node => node.type === 'Text').flatMap(node => node.props.children).join('');
  assert.ok(text.includes('••••••'));
  assert.equal(text.includes('s3cret'), false);
  assert.equal(JSON.stringify(nodes(drawn).map(node => node.props.accessibilityLabel)).includes('s3cret'), false);
  const save = nodes(drawn).find(node => node.type === 'Button' && node.props.focusId === 'save');
  assert.deepEqual(save.props.children, ['next.tvServer.replace']);
  save.props.onPress();
  assert.deepEqual(saved, ['s3cret']);
});

test('a number sheet steps within the row’s range and refuses a value off its step', async () => {
  const {host, module, saved} = await entryRow();
  const control = {type: 'number', min: 5, max: 3600, step: 5} as const;
  const sheet = () => {
    const tree = host.render(() => module.TVEntryRow({testID: 'live.retry', label: 'Retry', kind: 'number', value: '60', control, t: (id: string) => id, onSave: (value: unknown) => saved.push(value)}));
    nodes(tree).find(node => node.type === 'SettingsRow').props.onPress();
    const element = nodes(host.render(() => module.TVEntryRow({testID: 'live.retry', label: 'Retry', kind: 'number', value: '60', control, t: (id: string) => id, onSave: (value: unknown) => saved.push(value)}))).find(node => typeof node.type === 'function');
    return () => host.sheet(() => element.type(element.props));
  };
  const panel = sheet();
  let drawn = panel();
  const button = (id: string) => nodes(drawn).find(node => node.type === 'Button' && node.props.focusId === id);
  button('increase').props.onPress(); drawn = panel();
  assert.equal(nodes(drawn).find(node => node.type === 'NativeTVKeyboard').props.value, '65');
  nodes(drawn).find(node => node.type === 'NativeTVKeyboard').props.onChangeText('62'); drawn = panel();
  assert.equal(button('save').props.disabled, true);
  assert.ok(nodes(drawn).some(node => node.type === 'Banner'));
  nodes(drawn).find(node => node.type === 'NativeTVKeyboard').props.onChangeText('120'); drawn = panel();
  button('save').props.onPress();
  assert.deepEqual(saved, [120]);
});

for (const memoryAvailable of [true, false]) test(`Streaming on TV uses an explicit preference and always keeps its disk folder (${memoryAvailable})`, () => {
  const capacity = {hardware: {devices: [{id: 'drm-0a1b', name: 'Intel UHD 770', inUse: true}]}, temporaryDirectory: {path: '/var/portico/transcode', fallback: false, memoryPath: '/dev/shm', memoryAvailable, ready: true}} as any;
  const allIds = [...section('streaming').groups!, ...section('streaming').advanced!.groups!].flatMap(group => group.rows.flatMap(row => [row.id, ...(row.children ?? []).map(child => child.id)]));
  const configured = configuration(...allIds.map(id => field(id, null)));
  for (const transcodeLocation of ['memory', 'disk']) {
    const drafts = {runtime: {conversionMode: 'all', hardwareSelection: 'drm-0a1b', hardwareDecode: true, transcodeLocation, memoryCap: 'half', temporaryDirectory: '/mnt/fast/convert'}, connectivity: {}};
    const streaming = state('streaming', drafts, {capacity, configuration: configured});
    const main = items(streaming);
    const location = main.find(item => item.id === 'streaming.transcodeLocation') as any;
    assert.equal(location.value, transcodeLocation);
    assert.deepEqual(location.options.map((option: any) => option.value), ['memory', 'disk']);
    assert.ok(location.options.every((option: any) => !option.disabled), 'memory preference remains valid on a disk-only host');
    assert.equal(location.row.help, memoryAvailable ? settingsRowIndex.get('streaming.transcodeLocation')!.row.help : 'next.settings.streaming.memoryUnavailable');
    assert.equal((items(streaming, true).find(item => item.id === 'streaming.memoryCap') as any)?.value, transcodeLocation === 'memory' ? 'half' : undefined);
    const folder = main.find(item => item.id === 'streaming.transcodeFolder') as any;
    assert.deepEqual([folder.kind, folder.value], ['folder', '/mnt/fast/convert']);
    const device = items(streaming, true).find(item => item.id === 'streaming.hardware') as any;
    assert.deepEqual(device.options.map((option: any) => [option.value, !!option.plain]), [['auto', false], ['software', false], ['drm-0a1b', true]]);
    assert.equal(device.value, 'drm-0a1b');
    assert.ok(items(streaming, true).some(item => item.id === 'streaming.hardwareDecode'));
    const noCapacity = items(state('streaming', drafts, {configuration: configured})).find(item => item.id === 'streaming.transcodeLocation') as any;
    assert.equal(noCapacity.value, transcodeLocation);
    assert.equal(noCapacity.row.help, 'next.settings.streaming.memoryUnavailable');
  }
});
