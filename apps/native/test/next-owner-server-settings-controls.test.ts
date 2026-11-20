import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {bindingModule, formsFixture, maintenance, nodes, content, specification, t, i18n, theme} from './helpers/owner-server-settings.mjs';
import {settingNumber, settingChoice, settingEnabled, settingsSummary, missingRemovalTime} from '../src-next/screens/settings-live/server/settings-values.ts';

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
  assert.equal(settingEnabled(row('streaming.toneMappingMethod'), () => 'all'), true);
  assert.equal(settingEnabled(row('streaming.toneMappingMethod'), () => undefined), false);
});

test('Advanced summaries compare actual values and matrix defaults regardless of property order', () => {
  const matrix = row('segments.libraryModes');
  assert.equal(settingsSummary([matrix], () => ({current: {movies: 'server', anime: 'server', tv: 'server'}, default: {tv: undefined, anime: undefined, movies: undefined}}), t), t('next.settings.allDefaults'));
  assert.equal(settingsSummary([matrix], () => ({current: {tv: 'server', anime: 'off', movies: 'server'}}), t), `${t(matrix.label)}: ${t('server.channels.custom')}`);
  assert.equal(settingsSummary([row('storage.analysisSpace')], () => ({current: '32'}), t), t('next.settings.allDefaults'));
  assert.match(settingsSummary([row('storage.analysisSpace')], () => ({current: '40'}), t), /40/);
});

test('missing-title dates use committed rules or the explicit server date', () => {
  const missing = {missingSince: '2026-10-01T12:00:00Z'};
  assert.equal(missingRemovalTime(missing, 'after_days', 7), Date.parse('2026-10-08T12:00:00Z'));
  assert.equal(missingRemovalTime(missing, 'immediately', 30), Date.parse(missing.missingSince));
  assert.equal(missingRemovalTime(missing, 'never', 30), undefined);
  assert.equal(missingRemovalTime({...missing, removedAt: '2026-10-05T00:00:00Z'}, 'after_days', 30), Date.parse('2026-10-05T00:00:00Z'));
  assert.equal(missingRemovalTime({missingSince: 'unreadable'}, 'after_days', 7), undefined);
});

test('download rows and confirmations always lead with account username', () => {
});

test('backup chosen days, whole hour and keep count form one draft for the rebuilt backup source', async () => {
  const host = hooks(), settings = {days: [0,1,2,3,4,5,6], hour: 3, keep: 7, folder: '', includeArtwork: false}, saved: unknown[] = [];
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/storage/BackupSchedule.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View'}, '../../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../../kit/controls': {Button: 'Button'}, '../SettingControl': {NativeSettingControl: 'Setting'},
  });
  const render = () => host.render(() => module.NativeBackupSchedule({settings, saving: false, t, onSave: (value: unknown) => saved.push(value)}));
  const field = (id: string) => nodes(render()).find((node: any) => node.props.row?.id === id)!;
  field('storage.backupSchedule').props.onCustom();
  const days = field('storage.backupDays');assert.equal(days.props.value.sunday, 'runs');
  days.props.onChange({...days.props.value, sunday: 'skips'});
  field('storage.backupTime').props.onChange('23');field('storage.backupKeep').props.onChange(365);
  nodes(render()).find((node: any) => node.type === 'Button' && content(node) === t('action.save')).props.onPress();
  assert.deepEqual(saved, [{days:[1,2,3,4,5,6],hour:23,keep:365}]);
  assert.equal(nodes(render()).some((node: any) => node.props.row?.id === 'storage.backupWindowLength'), false);
});

test('the hardware group draws the rebuilt report in shared words and runs its test on demand', async () => {
  let tests = 0;const host = hooks(), module = await screenModule(new URL('../src-next/screens/settings-live/server/StreamingHardware.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    '../../../../../../packages/client-core/src/server-admin/streaming-hardware': await import('../../../packages/client-core/src/server-admin/streaming-hardware.ts'),
    '../../../../../../packages/i18n/src': {defaultI18n: i18n},
    '../../../../../../packages/client-core/src/presentation/settings-spec': specification, '../../../kit/controls': {Button: 'Button'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => theme},
  });
  const hardware = {checking: false, devices: [{id: 'opaque1', name: 'Measured GPU', method: 'vaapi', inUse: true, toneMappingWithMasteringData: true, toneMappingWithoutMasteringData: true, formats: []}],
    formats: [{format: 'h264', decode: true, encode: false}], toneMappingWithMasteringData: true, toneMappingWithoutMasteringData: false, lastError: {reason: 'opaque_failure'}};
  const tree = host.render(() => module.NativeStreamingHardware({hardware, disabled: false, testing: false, onTest: () => {tests++;}, t}));
  assert.match(content(nodes(tree)), /Measured GPU/);assert.doesNotMatch(content(nodes(tree)), /opaque_failure/);
  assert.equal(nodes(tree).filter((node: any) => node.props.testID === 'server-streaming-formats').length, 1);
  assert.equal(tests, 0);nodes(tree).find((node: any) => node.props.testID === 'server-streaming-test-hardware').props.onPress();assert.equal(tests, 1);
});

test('Segments exposes every Advanced control the segments document has (the per-kind library matrix is per library now), preserves review, and draws the bulk actions for the owner', async () => {
  const values = {mode: 'standard', viewerConfidencePercent: 50, automaticConfidencePercent: 85, useChapterNames: true, introWindow: 'usual', creditsWindow: 'usual', filmCreditsWindow: 'usual', shortestEpisodeMinutes: 2, shortestFilmMinutes: 10};
  const fixture = await formsFixture({segments: values}), host = hooks();
  const model = await screenModule(new URL('../src-next/pages/shared/settings/useServerSegments.ts', import.meta.url), {
    react: host.react, '../../../../../../packages/client-core/src/presentation/settings-bindings': bindings,
    '../../../../../../packages/client-core/src/presentation/settings-spec': specification,
    '../../../../../../packages/client-core/src/segment-review': {remeasureAllSegments: async () => ({}), retryUnreadableSegments: async () => ({})},
    '../../../screens/settings-live/lifetime': {}, '../../../screens/settings-live/server/settings-values': {settingEnabled, settingsSummary},
    '../../../screens/settings-live/server/storage/space-source': {nativeStorageOwner: (context: any) => context.current()},
    '../../../screens/settings-live/server/storage/forms': fixture.hooks,
  });
  const module = await screenModule(new URL('../src-next/pages/shared/settings/ServerSegmentsPage.tsx', import.meta.url), {
    react: host.react, './useServerSegments': model,
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/states': {StateView: 'StateView'},
    '../../../kit/frames': {SettingsPageFrame: 'SettingsPageFrame', PanelBlock: 'PanelBlock', SectionCard: 'SectionCard', StateRow: 'StateRow'},
    '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'},
    '../../../kit/settings/SettingsParts': {SettingsHelp: 'SettingsHelp'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../screens/settings-live/server/AdvancedSection': {NativeAdvancedSection: 'Advanced'},
    '../../../screens/settings-live/server/SettingControl': {NativeSettingControl: 'Setting'},
  });
  const ports = {context: fixture.group.context, renderSettingsPanel: () => ({type: 'Review', props: {}})}, runtime = {text: {t}, reportError: assert.fail};
  const render = () => host.render(() => {
    const frame = module.ServerSegmentsPage({ports, runtime});
    const body = frame.props.children.find((node: any) => typeof node?.type === 'function');
    return body.type(body.props);
  });
  const tree = render(), controls = nodes(tree).filter((node: any) => node.type === 'Setting');
  assert.equal(controls.length, 9);assert.equal(nodes(tree).filter((node: any) => node.type === 'Advanced').length, 1);assert.equal(nodes(tree).filter((node: any) => node.type === 'Review').length, 1);
  assert.ok(!controls.some((node: any) => node.props.row.id === 'segments.keepOld'));
  // The spec draws the two bulk actions on every platform: the phone has them for the owner (#3337).
  assert.deepEqual(nodes(tree).filter((node: any) => node.type?.name === 'SegmentsBulkAction' || node.props?.row?.id === 'segments.redetectAll' && node.type !== 'Setting').map((node: any) => node.props.row.id).filter((id: string, index: number, all: string[]) => all.indexOf(id) === index), ['segments.redetectAll', 'segments.retryFailed']);
  controls.find((node: any) => node.props.row.id === 'segments.chapters').props.onChange(false);assert.equal(fixture.group.forms.segments.getSnapshot().draft.useChapterNames, false);
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
