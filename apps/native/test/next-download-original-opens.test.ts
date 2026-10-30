import test from 'node:test';
import assert from 'node:assert/strict';
import {devicePlayerOpens, originalMayNotPlay} from '../src-next/screens/downloads/original-opens.ts';

test('the download sheet warns only when the device player cannot open the original container', () => {
  assert.equal(devicePlayerOpens('mp4', 'apple'), true);
  assert.equal(devicePlayerOpens('matroska', 'apple'), false, 'Apple AVFoundation does not read mkv');
  assert.equal(devicePlayerOpens('', 'android'), undefined, 'no container fact means no warning');
});

test('mpv and the chosen engine decide whether the original warning is drawn', () => {
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: false, player: 'automatic'}), true);
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: true, player: 'automatic'}), false, 'mpv plays the original');
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: true, player: 'native'}), true, 'the viewer chose the device player alone');
  assert.equal(originalMayNotPlay('mp4', {platform: 'apple', mpv: false, player: 'automatic'}), false);
  assert.equal(originalMayNotPlay('', {platform: 'android', mpv: false, player: 'automatic'}), false);
});
