import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';

// The Account/Playback frame owner returned exactly these five rows: the three
// Next episode rows and the two watched/started Advanced rows. Each row is
// backed by an actual current-MAIN engine, so the web support filter must show
// it instead of hiding a working setting.
const backedRows = {
  'playback.autoplayNext': 'playback.autoplayNext',
  'playback.upNextCountdownMs': 'playback.upNextCountdownMs',
  'playback.startNextDuringCredits': 'playback.startNextDuringCredits',
  'playback.playedThresholdPercent': 'playback.playedThresholdPercent',
  'playback.startedThresholdPercent': 'playback.startedThresholdPercent',
};

function playbackRows(): SettingsRow[] {
  const section = settingsSpec.find(candidate => candidate.scope === 'account' && candidate.id === 'playback');
  assert.ok(section, 'account/playback section');
  const flatten = (rows: readonly SettingsRow[]): SettingsRow[] => rows.flatMap(row => [row, ...flatten(row.children ?? [])]);
  return [...(section.groups ?? []), ...(section.advanced?.groups ?? [])].flatMap(group => flatten(group.rows));
}

test('the five engine-backed playback rows are exposed through the current registry bindings', () => {
  for (const [rowId, key] of Object.entries(backedRows)) {
    const binding = settingsBinding(rowId);
    assert.equal(binding?.form, 'preferences', rowId);
    assert.equal(binding?.key, key, rowId);
    assert.equal(binding?.scope, 'profile-server', rowId);
    assert.equal(hasWebPreferenceEffect(settingsRowIndex.get(rowId)!.row), true, rowId);
  }
});

test('account-playback shows exactly its backed rows, including the Advanced bar rows', () => {
  const visible = playbackRows().filter(row => hasWebPreferenceEffect(row)).map(row => row.id);
  assert.deepEqual(visible, [
    'playback.autoplayNext',
    'playback.upNextCountdownMs',
    'playback.startNextDuringCredits',
    'playback.stillWatching',
    'playback.skipping',
    'playback.skipBackMs',
    'playback.skipForwardMs',
    'playback.playedThresholdPercent',
    'playback.startedThresholdPercent',
  ]);
  for (const id of visible) assert.equal(settingsBinding(id)?.form, 'preferences', id);
});

test('Resuming stays hidden because neither drawn row has an engine or binding', () => {
  for (const id of ['playback.resumeRewindMs', 'playback.stopAtSeasonEnd']) {
    assert.equal(settingsBinding(id), undefined, id);
    assert.equal(hasWebPreferenceEffect(settingsRowIndex.get(id)!.row), false, id);
  }
  const row = settingsRowIndex.get('playback.autoplayNext')!.row;
  assert.equal(hasWebPreferenceEffect({...row, id: 'unpublished.effects-five'}), false);
});
