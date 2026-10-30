import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence} from '../../../packages/player-core/src/identity.ts';
import {NativeSubtitleStyleChoices, nativeSubtitleStyleValue} from '../src-next/platform/playback/subtitle-style-choices.ts';
import {captionStyle} from '../src-next/platform/playback/caption-style.ts';

const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});
const defaults = Object.freeze({size: 'medium', color: 'white', background: 'outline', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false} as const);

test('player appearance changes all five rendered fields without changing saved defaults', () => {
  const choices = new NativeSubtitleStyleChoices(); choices.bind(fence);
  for (const [key, value] of [['playback.subtitleSize', 'extra-large'], ['playback.subtitleColor', 'pink'], ['playback.subtitleBackground', 'shadow'], ['playback.subtitlePosition', 'raised'], ['playback.subtitleFont', 'mono']] as const) {
    assert.equal(choices.set(fence, key, value), true);
    assert.equal(nativeSubtitleStyleValue(key, choices.read(fence, defaults)), value);
  }
  assert.deepEqual(captionStyle(choices.read(fence, defaults)), {size: 'extra-large', scale: 1.6, color: 'pink', background: 'shadow', position: 'raised', font: 'mono', keepStyling: true, useSystemCaptionStyle: false});
  assert.equal(defaults.size, 'medium'); assert.equal(defaults.color, 'white');
});

test('source replans retain run choices but reject callbacks from the old source', () => {
  const choices = new NativeSubtitleStyleChoices(); choices.bind(fence);
  choices.set(fence, 'playback.subtitleColor', 'cyan');
  const replanned = playbackFence({...fence, generation: 2}); choices.bind(replanned);
  assert.equal(choices.read(replanned, defaults).color, 'cyan');
  assert.equal(choices.set(fence, 'playback.subtitleColor', 'yellow'), false);
  assert.equal(choices.read(replanned, defaults).color, 'cyan');
});

test('a new run, a stopped run and a different authority never inherit player appearance', () => {
  for (const next of [playbackFence({...fence, playback: {...fence.playback, run: 2}}), playbackFence({...fence, playback: {...fence.playback, scope: {...fence.playback.scope, profileId: 'other'}}})]) {
    const choices = new NativeSubtitleStyleChoices(); choices.bind(fence); choices.set(fence, 'playback.subtitleColor', 'yellow'); choices.bind(next);
    assert.deepEqual(choices.read(next, defaults), defaults);
    assert.equal(choices.set(fence, 'playback.subtitleFont', 'serif'), false);
  }
  const choices = new NativeSubtitleStyleChoices(); choices.bind(fence); choices.set(fence, 'playback.subtitleSize', 'large'); choices.clear();
  assert.equal(choices.set(fence, 'playback.subtitleSize', 'small'), false);
  choices.bind(fence); assert.deepEqual(choices.read(fence, defaults), defaults);
});

test('new Settings flow through fields without an override; invalid player values are refused', () => {
  const choices = new NativeSubtitleStyleChoices(); choices.bind(fence); choices.set(fence, 'playback.subtitleSize', 'large');
  assert.deepEqual(choices.read(fence, {...defaults, size: 'small', color: 'green'}), {...defaults, size: 'large', color: 'green'});
  for (const [key, value] of [['playback.subtitleSize', 'huge'], ['playback.subtitlePosition', '50'], ['playback.keepSubtitleStyling', 'false'], ['__proto__', 'mono']]) {
    assert.throws(() => choices.set(fence, key!, value!), /invalid_playback_preference/);
  }
  assert.equal(choices.read(fence, defaults).size, 'large');
});
