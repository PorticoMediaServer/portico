import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {SettingsConfiguration} from '@core/settings-configuration.ts';
import {exactPresetValue, presetPatch, readSettingsBinding, settingsBinding} from '@core/presentation/settings-bindings.ts';
import {presetEnginesAvailable} from '../src/screens/settings/server/engines.ts';
import {pendingServerRows, sectionLinkRows, sectionTarget, storageRowState, backupRows, factRows, fileRestoreRows} from '../src/screens/settings/server/rows.ts';

const row = (id: string) => settingsRowIndex.get(id)!.row;
type Forms = Record<string, Readonly<Record<string, unknown>>>;
/** The runtime document the server writes; these are its own field names. */
const runtime = {analysisStorageGiB: 64, analysisStageStorageMiB: 256, subtitleTrackLimit: 512, subtitleFileLimitMiB: 128, notificationDays: 30, playHistoryDays: 90,
  diagnosticDays: 7, jobDays: 30, logRetention: [{category: 'server', days: 14}]};
/** The maintenance document: the five generated-file retention values. */
const retention = {conversions: 7, prepared: 90, downloads: 30, subtitles: 365, trickplay: 365};
const maintenance = {retention, backupKeepCount: 14};
/** The same path the Settings reader takes: no binding means no value, never a guessed one. */
const read = (id: string, forms: Forms = {runtime}) => {
  const binding = settingsBinding(id);
  return binding ? readSettingsBinding(binding, {forms}, id) : undefined;
};

test('the four Limits read the runtime document fields the server validates', () => {
  assert.deepEqual(['storage.analysisSpace', 'storage.analysisStage', 'storage.subtitleTracks', 'storage.subtitleFile'].map(id => read(id)?.current),
    ['64', '256', '512', '128']);
  // The same reader supplies the retention figures the page keeps beside them.
  assert.equal(read('history.keepHistory')?.current, '90d', 'the stored 90 days reads as the drawn 90d choice');
  assert.equal(read('storage.keepNotifications')?.current, '30');
  assert.equal(read('troubleshooting.keepLogs')?.current, '7');
  assert.deepEqual(read('troubleshooting.keepLogsByKind')?.current, {server: '14', playback: 'default', scan: 'default', network: 'default', client: 'default'});
});

test('a row with no producer never becomes a value, so it stays absent', () => {
  assert.equal(read('storage.downloadSpace'), undefined);
  assert.equal(read('storage.analysisSpace', {})?.current, undefined);
});

test('defaults and config-file state come from the server configuration registry, not from the client', () => {
  const configuration: SettingsConfiguration = {revision: 9, registryRevision: 'registry', path: '/etc/portico.toml', fields: [
    {id: 'storage.analysisSpace', key: 'storage.analysis_space_gb', current: 64, effective: 64, default: 16, source: 'file', configOnly: false, applicationMode: 'immediate'},
    {id: 'storage.subtitleTracks', key: 'storage.subtitle_tracks_per_file', current: 512, effective: 512, default: 256, source: 'file', configOnly: true, applicationMode: 'restart'},
  ]};
  const space = readSettingsBinding(settingsBinding('storage.analysisSpace')!, {configuration, forms: {runtime}}, 'storage.analysisSpace');
  assert.deepEqual({current: space?.current, default: space?.default, configOnly: space?.configOnly, source: space?.source}, {current: '64', default: '16', configOnly: false, source: 'file'});
  // A row the owner sets in the config file is not drawn as a control (ruling 3).
  assert.equal(readSettingsBinding(settingsBinding('storage.subtitleTracks')!, {configuration, forms: {runtime}}, 'storage.subtitleTracks')?.configOnly, true);
});

test('Keep generated files resolves from the five real retention values and writes exactly them', () => {
  const options = row('storage.keepFiles').control.options!;
  const readRetention = (id: string) => read(id, {maintenance})?.current;
  assert.equal(exactPresetValue(options, readRetention), 'balanced');
  assert.deepEqual(presetPatch(options, 'short'), {'storage.keepConversions': '1', 'storage.keepPrepared': '30', 'storage.keepDownloads': '14', 'storage.keepSubtitles': '90', 'storage.keepTrickplay': '90'});
  // A value that is nobody's preset is shown as Custom, never as a preset it is not.
  assert.equal(exactPresetValue(options, id => id === 'storage.keepPrepared' ? '180' : readRetention(id)), 'custom');
});

test('a preset is offered only when every row it sets has a real engine', () => {
  const keepFiles = row('storage.keepFiles');
  assert.equal(presetEnginesAvailable(keepFiles, () => true), true);
  assert.equal(presetEnginesAvailable(keepFiles, () => false), false);
  // The account presets still answer to the same engines as before.
  assert.equal(presetEnginesAvailable(row('quality.safeMode'), () => false), false);
  assert.equal(presetEnginesAvailable(row('storage.analysisSpace'), () => true), undefined);
});

test('the two retention links open the page that owns the value, and never a mock page', () => {
  assert.deepEqual([...sectionLinkRows].map(id => sectionTarget(row(id).control.to)), [
    {scope: 'server', section: 'history', row: 'history.keepHistory'},
    {scope: 'server', section: 'troubleshooting', row: 'troubleshooting.keepLogs'},
  ]);
  for (const id of [...sectionLinkRows]) {
    const location = settingsRowIndex.get(id)!;
    assert.equal(location.section.id, 'storage');
    assert.equal(location.advanced, true);
    assert.ok(settingsRowIndex.get(location.row.control.to!.split('/')[2])?.advanced, `${id} opens an Advanced row`);
  }
  // A mock page is design artwork, not a page of the app.
  assert.equal(sectionTarget('owner-server.html#f1'), undefined);
  assert.deepEqual(sectionTarget('server/libraries'), {scope: 'server', section: 'libraries'});
  assert.equal(sectionTarget('elsewhere/libraries'), undefined);
});

test('Storage answers for its own rows and leaves every other row to the shared reader', () => {
  const live = {live: true, backups: true};
  for (const id of [...sectionLinkRows]) assert.deepEqual(storageRowState(row(id), live), {present: true});
  for (const id of [...factRows, ...fileRestoreRows]) {
    assert.deepEqual(storageRowState(row(id), {live: true, backups: false}), {present: true}, `${id} draws as soon as its own read is live`);
    assert.deepEqual(storageRowState(row(id), {live: false, backups: true}), {present: false}, `${id} is not drawn outside a live session`);
  }
  for (const id of [...backupRows]) {
    assert.deepEqual(storageRowState(row(id), {live: true, backups: false}), {present: false}, `${id} waits for the server's backup document`);
    assert.deepEqual(storageRowState(row(id), live), {present: true}, `${id} draws once the server has answered`);
  }
  for (const id of pendingServerRows) assert.deepEqual(storageRowState(row(id), live), {present: false}, `${id} has no drawn control yet`);
  for (const id of ['storage.analysisSpace', 'storage.keepFiles', 'storage.keepNotifications']) assert.equal(storageRowState(row(id), live), undefined, id);
  // A link whose drawn target is a mock page would be a dead control, so it stays absent.
  const keepHistory = row('storage.keepHistory');
  assert.deepEqual(storageRowState({...keepHistory, control: {...keepHistory.control, to: 'owner-server.html#f9'}}, live), {present: false});
});
