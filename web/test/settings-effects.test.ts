import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';

test('actual MAIN appearance consumers can be edited through their canonical row bindings', () => {
  for (const id of ['appearance.backdrops', 'appearance.cardSizeExact', 'appearance.libraryView', 'appearance.episodesLayout', 'appearance.motion', 'appearance.notificationDot']) {
    assert.equal(hasWebPreferenceEffect(settingsRowIndex.get(id)!.row), true, id);
  }
});
test('typed composite skip/still-watching controls require all of their real effects', () => {
  for (const id of ['playback.skipping', 'playback.stillWatching']) assert.equal(hasWebPreferenceEffect(settingsRowIndex.get(id)!.row), true, id);
  const row = settingsRowIndex.get('appearance.cardSizeExact')!.row;
  assert.equal(hasWebPreferenceEffect({...row, id: 'unpublished.setting'}), false);
});
test('native display controls and Library engine eligibility are never inferred from a facade getter', () => {
  for (const id of ['appearance.textSize', 'appearance.layout', 'device.audioOutput', 'libraries.episodeThumbs']) {
    assert.equal(hasWebPreferenceEffect(settingsRowIndex.get(id)!.row), false, id);
  }
});
