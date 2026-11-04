import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {settingsBinding, settingsStorableChoices} from '@core/presentation/settings-bindings.ts';
import {browserSettingsEnvironment, settingsRowVisible} from '@core/presentation/settings-visibility.ts';
import {hasWebPreferenceEffect} from '../src/screens/settings/effects.ts';

const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};

test('the saved quality defaults are offered because the server applies them', () => {
  for (const id of ['quality.home', 'quality.remote']) {
    assert.equal(hasWebPreferenceEffect(row(id)), true, id);
    const binding = settingsBinding(id);
    assert.equal(binding?.form, 'preferences');
    assert.equal(binding?.key, id);
  }
});

test('mobile data uses the independent cellular preference rather than a reach lane', () => {
  assert.equal(hasWebPreferenceEffect(row('quality.mobileData')), true);
  const binding = settingsBinding('quality.mobileData');
  assert.equal(binding?.form, 'preferences');
  assert.equal(binding?.key, 'quality.mobile');
});

test('Download quality saves the key the server reads, only where downloads are made', () => {
  // FB-10: the row writes `quality.downloads` (downloads/music.go videoDownloadQuality), never the
  // `downloadQuality` spec label the server doesn't know. A browser makes no downloads, so the row is
  // drawn for the desktop app only (the spec's phone/desktop platforms); Automatic is no download quality.
  const download = row('quality.downloadQuality'), binding = settingsBinding('quality.downloadQuality')!;
  assert.deepEqual(binding, {form: 'preferences', key: 'quality.downloads', scope: 'profile-device-class', codec: 'video-quality-choice'});
  assert.equal(hasWebPreferenceEffect(download), true);
  assert.equal(settingsRowVisible(download, browserSettingsEnvironment), false, 'not in a browser');
  assert.equal(settingsRowVisible(download, {platform: 'desktop', os: 'macos'}), true, 'in the desktop app');
});

test('a select offers only the values its registry can store: Ask each time waits for the server', () => {
  const binding = settingsBinding('quality.downloadQuality')!, options = row('quality.downloadQuality').control.options!;
  const registry = (allowedValues: readonly string[]) => ({preferences: {deviceClass: 'web', deviceBound: true, registry: {revision: '1', fields: [{key: 'quality.downloads', type: 'string', default: 'original', allowedValues, scopes: ['profile-device-class'], group: 'quality', labelKey: 'x'}]},
    documents: [{scope: 'profile-device-class', revision: 1, digest: 'd', activeRevision: 1, values: {}}], effective: {'quality.downloads': 'original'}, effectiveSource: {}, clampedFields: [], registryRevision: '1'}}) as never;
  const ladder = ['original', '2160p', '1080p-high', '1080p-low', '720p-high', '720p-low', '480p', '240p', 'automatic'];
  const today = settingsStorableChoices(binding, options, registry(ladder)).map(option => option.value);
  assert.equal(today.includes('ask'), false, 'the server doesn\'t store ask yet, so it isn\'t offered');
  assert.ok(today.includes('original') && today.includes('4k'));
  assert.ok(settingsStorableChoices(binding, options, registry([...ladder, 'ask'])).some(option => option.value === 'ask'), 'it appears once the registry publishes it');
  assert.deepEqual(settingsStorableChoices(binding, options, {}), options, 'with nothing storable here the drawn list stays');
});

test('quality rows without a producer stay hidden rather than pretending to work', () => {
  // TV screen/sound facts and the safe-mode device switches are device settings; the web
  // Settings device source is read-only, so the web player cannot write them yet.
  assert.equal(hasWebPreferenceEffect(row('quality.tvSupports')), false, 'quality.tvSupports');
  assert.equal(hasWebPreferenceEffect(row('quality.safeMode')), false, 'quality.safeMode');
});
