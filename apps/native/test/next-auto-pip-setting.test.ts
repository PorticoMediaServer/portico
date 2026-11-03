import assert from 'node:assert/strict';
import test from 'node:test';
import {nativePlaybackSettings} from '../src-next/shell/playback-settings.ts';
import {NativeDeviceSettings} from '../src-next/platform/device-settings.ts';
import {settingsRowId, settingsStructure} from '../../../packages/client-core/src/presentation/settings-structure.ts';

// Settings › Device › Start Picture in Picture automatically: kept by this device, on by default, phones and tablets only.
const values = {'playback.defaultSpeed': 1, 'audiobooks.defaultSpeed': 1, 'playback.introSkip': 'ask', 'playback.recapSkip': 'ask', 'playback.creditsSkip': 'ask',
  'playback.subtitleSize': 'medium', 'playback.subtitleColor': 'white', 'playback.subtitleBackground': 'shadow', 'playback.subtitlePosition': 'bottom', 'playback.subtitleFont': 'default',
  'playback.keepSubtitleStyling': false, 'playback.useSystemCaptionStyle': false};

test('the playback runtime is told the device\'s choice: on unless this device turned it off', () => {
  assert.equal(nativePlaybackSettings(values as never, {}).autoPictureInPicture, true);
  assert.equal(nativePlaybackSettings(values as never, {autoPictureInPicture: true}).autoPictureInPicture, true);
  assert.equal(nativePlaybackSettings(values as never, {autoPictureInPicture: false}).autoPictureInPicture, false);
});

test('the device store reads it on by default and keeps a boolean only', async () => {
  let record: unknown = {};
  const owner = {current: () => true, subscribePreferences: () => () => {}};
  const store = new NativeDeviceSettings(owner, {readRecord: () => record, acceptRecord: raw => {record = raw;},
    storage: {change: async (_key: string, apply: (raw: unknown) => {record: unknown; value: unknown}) => {const next = apply(record); record = next.record; return next.value;}} as never});
  assert.equal(store.supports('autoPictureInPicture'), true);
  assert.equal(store.read('autoPictureInPicture' as never), true);
  await store.write('autoPictureInPicture' as never, false);
  assert.equal(store.read('autoPictureInPicture' as never), false);
  await assert.rejects(store.write('autoPictureInPicture' as never, 'off'), /value_invalid/);
});

test('the row is under Settings › Device on phones and tablets, and not on TV or the web', () => {
  const rows = (platform: 'phone' | 'tv' | 'web') => settingsStructure({owner: false, platform, capabilities: {}}).headings[0]!.pages
    .find(page => page.id === 'device')?.sections.flatMap(section => section.rows.map(settingsRowId)) ?? [];
  assert.ok(rows('phone').includes('autoPictureInPicture'));
  assert.ok(!rows('tv').includes('autoPictureInPicture'));
  assert.ok(!rows('web').includes('autoPictureInPicture'));
});
