import assert from 'node:assert/strict';
import test from 'node:test';
import {nativePlaybackSettings} from '../src-next/shell/playback-settings';
import type {PreferenceValues} from '../../../packages/client-core/src/preferences';

const effective: PreferenceValues = {
  'playback.defaultSpeed': 1.25,
  'audiobooks.defaultSpeed': 1.5,
  'playback.introSkip': 'ask',
  'playback.recapSkip': 'auto',
  'playback.creditsSkip': 'off',
  'playback.subtitleSize': 'large',
  'playback.subtitleBackground': 'translucent',
  'playback.subtitleColor': 'white',
  'playback.subtitlePosition': 'bottom',
  'playback.subtitleFont': 'default',
  'playback.keepSubtitleStyling': true,
  'playback.useSystemCaptionStyle': false,
};

test('the native engine receives the server effective settings and the actual device overrides', () => {
  const result = nativePlaybackSettings(effective, {playerEngine: 'native', forceStereo: true, disableHdr: false});
  assert.deepEqual(result, {engine: 'native', overrides: {forceStereo: true, disableHdr: false}, passthroughFormats: {},
    matchFrameRate: 'auto', matchDynamicRange: 'auto', videoRate: 1.25, audiobookRate: 1.5,
    skips: {intro: 'ask', recap: 'auto', credits: 'off'}, subtitles: {size: 'large', color: 'white', background: 'translucent', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false}});
});

test('the engine receives the device\'s own passthrough and display-matching choices, and only those', () => {
  const result = nativePlaybackSettings(effective, {matchFrameRate: 'off', matchDynamicRange: 'off', passthroughFormats: {dd: 'off'}});
  assert.deepEqual(result.passthroughFormats, {dd: 'off'});
  assert.equal(result.matchFrameRate, 'off');
  assert.equal(result.matchDynamicRange, 'off');
});

test('absent or invalid playback facts are rejected instead of replaced with invented defaults', () => {
  for (const field of Object.keys(effective)) {
    const copy = {...effective}; delete copy[field];
    assert.throws(() => nativePlaybackSettings(copy, null), /invalid_playback_preference/, field);
  }
  assert.throws(() => nativePlaybackSettings({...effective, 'playback.defaultSpeed': 0}, null), /invalid_playback_preference/);
  assert.throws(() => nativePlaybackSettings({...effective, 'playback.recapSkip': 'always'}, null), /invalid_playback_preference/);
  assert.throws(() => nativePlaybackSettings({...effective, 'playback.subtitleSize': 'huge'}, null), /invalid_playback_preference/);
});

test('malformed installation overrides cannot quietly enable a device capability workaround', () => {
  const result = nativePlaybackSettings(effective, {forceStereo: 'true', disableHdr: 1, disablePassthrough: null});
  assert.equal(result.overrides, undefined);
  assert.equal(result.videoRate, 1.25);
  assert.equal(result.audiobookRate, 1.5);
});
