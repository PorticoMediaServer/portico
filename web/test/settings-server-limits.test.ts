import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {readSettingsBinding, settingsBinding} from '@core/presentation/settings-bindings.ts';
import {presetEnginesAvailable} from '../src/screens/settings/server/engines.ts';
import {pendingServerRows, sectionLinkRows, sectionTarget, storageRowState, backupRows, factRows, fileRestoreRows} from '../src/screens/settings/server/rows.ts';

const row = (id: string) => settingsRowIndex.get(id)!.row;
test('Storage keeps the supported notices setting and omits the retired runtime limits', () => {
  const binding = settingsBinding('storage.keepNotifications')!;
  assert.equal(readSettingsBinding(binding, {forms: {notices: {keepDays: 30}}})?.current, '30');
  for (const id of ['storage.analysisSpace', 'storage.analysisStage', 'storage.subtitleTracks', 'storage.subtitleFile',
    'troubleshooting.keepLogs', 'troubleshooting.keepLogsByKind']) {
    assert.equal(settingsBinding(id), undefined, id);
  }
});

test('a preset is offered only when every row it sets has a real engine', () => {
  assert.equal(presetEnginesAvailable(row('quality.safeMode'), () => false), false);
  assert.equal(presetEnginesAvailable(row('storage.analysisSpace'), () => true), undefined);
});

test('the retention link opens the page that owns the value, and never a mock page', () => {
  assert.deepEqual([...sectionLinkRows].map(id => sectionTarget(row(id).control.to)), [
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
    // What's using space draws its own rows (owner-server 8), so it is bare; the others are ordinary rows.
    assert.deepEqual(storageRowState(row(id), {live: true, backups: false}), id === 'storage.usage' ? {present: true, bare: true} : {present: true}, `${id} draws as soon as its own read is live`);
    assert.deepEqual(storageRowState(row(id), {live: false, backups: true}), {present: false}, `${id} is not drawn outside a live session`);
  }
  for (const id of [...backupRows]) {
    assert.deepEqual(storageRowState(row(id), {live: true, backups: false}), {present: false}, `${id} waits for the server's backup document`);
    assert.deepEqual(storageRowState(row(id), live), {present: true}, `${id} draws once the server has answered`);
  }
  for (const id of pendingServerRows) assert.deepEqual(storageRowState(row(id), live), {present: false}, `${id} has no drawn control yet`);
  for (const id of ['storage.analysisSpace', 'storage.keepNotifications']) assert.equal(storageRowState(row(id), live), undefined, id);
  // A link whose drawn target is a mock page would be a dead control, so it stays absent.
  const keepLogs = row('storage.keepLogsLink');
  assert.deepEqual(storageRowState({...keepLogs, control: {...keepLogs.control, to: 'owner-server.html#f9'}}, live), {present: false});
});
