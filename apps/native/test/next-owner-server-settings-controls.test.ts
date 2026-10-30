import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {bindingModule, formsFixture, maintenance, nodes, content, specification, t, i18n, theme} from './helpers/owner-server-settings.mjs';
import {settingNumber, settingChoice, settingEnabled, settingsSummary, missingRemovalTime, downloadPerson} from '../src-next/screens/settings-live/server/settings-values.ts';

const bindings = await bindingModule(), {settingsRowIndex} = specification;
const row = (id: string) => settingsRowIndex.get(id)!.row;

test('number fields accept supported fractional Mbps and reject invalid edits without rounding', () => {
  const control = row('streaming.awayExact').control;
  assert.deepEqual(settingNumber('', row('streaming.uploadSpeed').control), {valid: true, value: null});
  assert.deepEqual(settingNumber('0.5', control), {valid: true, value: 0.5});
  for (const value of ['-1', '0.25', 'Infinity', '1e3', 'words']) assert.equal(settingNumber(value, control).valid, false, value);
  assert.equal(settingNumber('31', row('segments.shortestEpisode').control).valid, false);
  assert.equal(settingNumber('12', row('segments.shortestFilm').control).valid, false);
});

test('exact non-preset values stay Custom and dependent controls use actual bound settings', () => {
  assert.equal(settingChoice(row('storage.analysisSpace'), 40), 'custom');
  assert.equal(settingChoice(row('storage.analysisSpace'), 64), '64');
  assert.equal(settingEnabled(row('streaming.toneMappingMethod'), () => false), false);
  assert.equal(settingEnabled(row('streaming.toneMappingMethod'), () => true), true);
  assert.equal(settingEnabled(row('streaming.toneMappingMethod'), () => undefined), false);
});

test('Advanced summaries compare actual values and matrix defaults regardless of property order', () => {
  const matrix = row('segments.libraryModes');
  assert.equal(settingsSummary([matrix], () => ({current: {movies: 'server', anime: 'server', tv: 'server'}, default: {tv: undefined, anime: undefined, movies: undefined}}), t), t('next.settings.allDefaults'));
  assert.equal(settingsSummary([matrix], () => ({current: {tv: 'server', anime: 'off', movies: 'server'}}), t), `${t(matrix.label)}: ${t('server.channels.custom')}`);
  assert.equal(settingsSummary([row('storage.analysisSpace')], () => ({current: '32'}), t), t('next.settings.allDefaults'));
  assert.match(settingsSummary([row('storage.analysisSpace')], () => ({current: '40'}), t), /40/);
});

test('missing-title presets roundtrip and dates use committed rules or the explicit server date', () => {
  const binding = bindings.settingsBinding('storage.missingRule'), sources = {forms: {runtime: {'library.missingTitles': 'after_days', 'library.missingTitlesDays': 30}}};
  assert.ok(binding);
  const value = bindings.readSettingsBinding(binding, sources); assert.ok(value);
  assert.equal(value.current, '30d');
  for (const [preset, days] of [['7d', 7], ['30d', 30], ['90d', 90]] as const) assert.deepEqual(bindings.settingsBindingPatch(binding, preset, sources).values, {'library.missingTitles': 'after_days', 'library.missingTitlesDays': days});
  assert.deepEqual(bindings.settingsBindingPatch(binding, {days: 46}, sources).values, {'library.missingTitles': 'after_days', 'library.missingTitlesDays': 46});
  assert.throws(() => bindings.settingsBindingPatch(binding, {days: 0}, sources));
  const missing = {missingSince: '2026-10-01T12:00:00Z'};
  assert.equal(missingRemovalTime(missing, 'after_days', 7), Date.parse('2026-10-08T12:00:00Z'));
  assert.equal(missingRemovalTime(missing, 'immediately', 30), Date.parse(missing.missingSince));
  assert.equal(missingRemovalTime(missing, 'never', 30), undefined);
  assert.equal(missingRemovalTime({...missing, removedAt: '2026-10-05T00:00:00Z'}, 'after_days', 30), Date.parse('2026-10-05T00:00:00Z'));
  assert.equal(missingRemovalTime({missingSince: 'unreadable'}, 'after_days', 7), undefined);
});

test('download rows and confirmations always lead with account username', () => {
  assert.deepEqual(downloadPerson({accountName: 'sam_k', profileName: 'Kids'}), {name: 'sam_k', profile: 'Kids'});
  assert.deepEqual(downloadPerson({accountName: 'sam_k', profileName: 'sam_k'}), {name: 'sam_k'});
  assert.deepEqual(downloadPerson({profileName: 'Kids'}), {name: '—', profile: 'Kids'});
});

test('backup Custom days use the matrix binding and preserve the heavy-work window', async () => {
  const fixture = await formsFixture({maintenance: maintenance()}), host = hooks();let customDraft: unknown, opened = false;
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/storage/BackupSchedule.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View', Text: 'Text'}, '../../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../../kit/controls': {Select: 'Select'}, '../../../../kit/shell/visual': {shellType: () => ({})}, '../../../../kit/theme': {useTheme: () => theme}, './space-source': {nativeStorageOwner: (context: any) => context.current()},
  });
  const render = (placement: string) => host.render(() => module.NativeBackupSchedule({group: fixture.group, draft: fixture.group.forms.maintenance.getSnapshot().draft, saving: false, t, placement, customDraft, onCustomDraftChange: (value: unknown) => {customDraft = value;}, onAdvanced: () => {opened = true;}}));
  nodes(render('main')).find((node: any) => node.props.testID === 'storage.backupSchedule').props.onValueChange('custom');assert.equal(opened, true);
  const days = nodes(render('advanced')).filter((node: any) => String(node.props.testID).startsWith('storage.backupDays.'));
  assert.equal(days.length, 7);assert.equal(days[0].props.value, 'runs');
  days.find((node: any) => node.props.testID === 'storage.backupDays.sunday').props.onValueChange('skips');
  assert.ok(!fixture.group.forms.maintenance.getSnapshot().draft.windows[0].days.includes('sunday'));
  assert.deepEqual(fixture.group.forms.maintenance.getSnapshot().draft.windows[1], maintenance().windows[1]);fixture.stop();
});

test('generated-file presets change exactly their canonical fields and preserve exact custom values', () => {
  const draft = maintenance(), sources = {forms: {maintenance: draft}};
  const requiredBinding = (id: string) => {const value = bindings.settingsBinding(id); assert.ok(value); return value;};
  const read = (id: string) => {const value = bindings.readSettingsBinding(requiredBinding(id), sources); assert.ok(value); return value.current;};
  const options = row('storage.keepFiles').control.options!;assert.equal(bindings.exactPresetValue(options, read), 'balanced');
  const patch = bindings.presetPatch(options, 'short', read);
  const changed = Object.entries(patch).reduce((current: any, [id, value]) => bindings.applySettingsBindingPatch(current, bindings.settingsBindingPatch(requiredBinding(id), value, {forms: {maintenance: current}})), draft);
  assert.deepEqual(changed.retention, {conversions: 1, prepared: 30, downloads: 14, subtitles: 90, trickplay: 90});assert.deepEqual(changed.windows, draft.windows);
  assert.equal(bindings.exactPresetValue(options, id => id === 'storage.keepConversions' ? '12' : read(id)), 'custom');
});

test('hardware view uses only supplied devices/formats and invokes its test callback on demand', async () => {
  let tests = 0;const host = hooks(), module = await screenModule(new URL('../src-next/screens/settings-live/server/StreamingHardware.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../../../../packages/client-core/src/server-admin/console-words': {when: (value: number) => String(value)},
    '../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../kit/controls': {Button: 'Button'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme},
  });
  const capacity = {hardware: {effective: 'vaapi', device: 'actual-device', devices: [{id: 'opaque1', name: 'Measured GPU', method: 'VA-API', inUse: true}], formats: [{format: 'h264', decode: true, encode: false}], lastTest: 123, lastError: {reason: 'opaque_failure'}}, hdrToneMapping: {status: 'limited', detail: 'Measured limitation'}};
  const tree = host.render(() => module.NativeStreamingHardware({capacity, disabled: false, testing: false, onTest: () => {tests++;}, t}));
  assert.match(content(nodes(tree)), /Measured GPU/);assert.match(content(nodes(tree)), /Measured limitation/);assert.doesNotMatch(content(nodes(tree)), /opaque_failure/);
  assert.equal(nodes(tree).filter((node: any) => node.props.testID === 'server-streaming-formats').length, 1);
  assert.equal(tests, 0);nodes(tree).find((node: any) => node.props.testID === 'server-streaming-test-hardware').props.onPress();assert.equal(tests, 1);
});

test('Segments exposes every supported Advanced control, preserves review, and omits unsupported bulk actions', async () => {
  const values = {'library.segmentDetection': 'standard', 'segments.viewerConfidencePercent': 50, 'segments.automaticConfidencePercent': 85, 'segments.useChapterNames': true, 'segments.introWindow': 'usual', 'segments.creditsWindow': 'usual', 'segments.filmCreditsWindow': 'usual', 'segments.shortestEpisode': 2, 'segments.shortestFilm': 10, 'segments.tvMode': 'server', 'segments.animeMode': 'server', 'segments.movieMode': 'server'};
  const fixture = await formsFixture({runtime: values}), host = hooks();
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/SegmentsPage.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView'}, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings, '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../packages/i18n/src': {defaultI18n: i18n}, '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme},
    './AdvancedSection': {NativeAdvancedSection: 'Advanced'}, './SettingControl': {NativeSettingControl: 'Setting'}, './settings-values': {settingEnabled, settingsSummary}, './storage/space-source': {nativeStorageOwner: (context: any) => context.current()}, './storage/forms': fixture.hooks,
  });
  const ports = {context: fixture.group.context, renderSettingsPanel: () => ({type: 'Review', props: {}})}, runtime = {text: {t}, reportError: assert.fail};
  const render = () => host.render(() => {const outer = module.NativeServerSegments({ports, runtime});return outer.type(outer.props);});
  const tree = render(), controls = nodes(tree).filter((node: any) => node.type === 'Setting');
  assert.equal(controls.length, 10);assert.equal(nodes(tree).filter((node: any) => node.type === 'Advanced').length, 1);assert.equal(nodes(tree).filter((node: any) => node.type === 'Review').length, 1);
  assert.ok(!controls.some((node: any) => ['segments.keepOld', 'segments.redetectAll', 'segments.retryFailed'].includes(node.props.row.id)));
  controls.find((node: any) => node.props.row.id === 'segments.chapters').props.onChange(false);assert.equal(fixture.group.forms.runtime.getSnapshot().draft['segments.useChapterNames'], false);
  controls.find((node: any) => node.props.row.id === 'segments.libraryModes').props.onChange({tv: 'server', anime: 'off', movies: 'thorough'});assert.equal(fixture.group.forms.runtime.getSnapshot().draft['segments.animeMode'], 'off');fixture.stop();
});

test('a folder row names the bound directory and picks one through the shared explorer', async () => {
  const values = await import('../src-next/screens/settings-live/server/settings-values.ts'), host = hooks();
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/SettingControl.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'}, '../../../kit/controls': {Button: 'Button', Select: 'Select', Switch: 'Switch', TextField: 'TextField'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme},
    '../owner-libraries/FolderExplorer': {FolderExplorer: 'FolderExplorer'}, './settings-values': values,
  });
  let changed: unknown, opened = false;
  const api = {request: async () => ({})};
  const element = host.render(() => module.NativeSettingControl({row: row('streaming.transcodeFolder'), value: '/transcode', t, api, onChange: (next: unknown) => {changed = next;}}));
  const render = () => host.render(() => element.type(element.props)), explorer = () => nodes(render()).find((node: any) => node.type === 'FolderExplorer');
  const tree = render();
  assert.ok(nodes(tree).some((node: any) => node.type === 'Text' && content(node) === '/transcode'));
  assert.equal(explorer().props.open, false);assert.equal(explorer().props.multiple, false);
  nodes(tree).find((node: any) => node.props.testID === 'streaming.transcodeFolder:choose').props.onPress();
  opened = explorer().props.open;assert.equal(opened, true);assert.deepEqual(explorer().props.chosen.map((folder: any) => folder.path), ['/transcode']);
  assert.equal(changed, undefined);explorer().props.onUse([{path: '/fast-disk/transcode', name: 'transcode'}]);assert.equal(changed, '/fast-disk/transcode');
});
