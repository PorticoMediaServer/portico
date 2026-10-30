import test from 'node:test';
import assert from 'node:assert/strict';
import {captionStyle, nativeCaptionPreferences} from '../src-next/platform/playback/caption-style.ts';
import type {PreferenceValues} from '../../../packages/client-core/src/preferences.ts';

const effective: PreferenceValues = {
  'playback.subtitleSize': 'extra-large', 'playback.subtitleColor': 'pink',
  'playback.subtitleBackground': 'outline', 'playback.subtitlePosition': 'raised',
  'playback.subtitleFont': 'mono', 'playback.keepSubtitleStyling': false,
  'playback.useSystemCaptionStyle': true,
};

test('all seven actual effective preferences reach detached platform command data', () => {
  const preferences = nativeCaptionPreferences(effective);
  const style = captionStyle(preferences);
  assert.deepEqual(style, {size: 'extra-large', scale: 1.6, color: 'pink', background: 'outline', position: 'raised', font: 'mono', keepStyling: false, useSystemCaptionStyle: true});
  assert.notEqual(style, preferences);
  assert.ok(Object.isFrozen(preferences)); assert.ok(Object.isFrozen(style));
  assert.deepEqual(Object.keys(style).sort(), ['background', 'color', 'font', 'keepStyling', 'position', 'scale', 'size', 'useSystemCaptionStyle'].sort());
});

test('canonical size, background, color, position and font choices retain their exact meaning', () => {
  for (const [size, scale] of [['small', 0.8], ['medium', 1], ['large', 1.3], ['extra-large', 1.6]] as const) {
    assert.equal(captionStyle(nativeCaptionPreferences({...effective, 'playback.subtitleSize': size})).scale, scale);
  }
  for (const [key, field, values] of [
    ['playback.subtitleBackground', 'background', ['none', 'outline', 'shadow', 'translucent', 'opaque']],
    ['playback.subtitleColor', 'color', ['white', 'yellow', 'cyan', 'green', 'pink']],
    ['playback.subtitlePosition', 'position', ['bottom', 'raised', 'top']],
    ['playback.subtitleFont', 'font', ['default', 'serif', 'mono', 'rounded']],
  ] as const) {
    for (const value of values) assert.equal(captionStyle(nativeCaptionPreferences({...effective, [key]: value}))[field], value);
  }
  const original = captionStyle(nativeCaptionPreferences({...effective, 'playback.keepSubtitleStyling': true, 'playback.useSystemCaptionStyle': false}));
  assert.equal(original.keepStyling, true); assert.equal(original.useSystemCaptionStyle, false);
});

test('missing and malformed facts reject without defaults or unrelated preference leakage', () => {
  for (const key of Object.keys(effective)) {
    const absent = {...effective}; delete absent[key];
    assert.throws(() => nativeCaptionPreferences(absent), /invalid_playback_preference/, key);
    for (const value of [null, undefined, {}, 1, 'unknown']) {
      assert.throws(() => nativeCaptionPreferences({...effective, [key]: value} as PreferenceValues), /invalid_playback_preference/, key);
    }
  }
  const style = captionStyle(nativeCaptionPreferences({...effective, 'account.unrelated': 'not-a-command'}));
  assert.equal('account.unrelated' in style, false);
});
