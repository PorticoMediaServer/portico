/** The download menu says when a title's original is a file this device cannot open by itself. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {devicePlayerOpens, originalMayNotPlay} from '../src-next/screens/downloads/original-opens.ts';

test('a container is read however the server names it', () => {
  assert.equal(devicePlayerOpens('matroska', 'apple'), false);
  assert.equal(devicePlayerOpens('matroska,webm', 'android'), true);
  assert.equal(devicePlayerOpens('mov,mp4,m4a,3gp,3g2,mj2', 'apple'), true);
  assert.equal(devicePlayerOpens('MP4', 'apple'), true);
  assert.equal(devicePlayerOpens('mpegts', 'apple'), false);
  assert.equal(devicePlayerOpens('mpegts', 'android'), true);
  assert.equal(devicePlayerOpens('avi', 'android'), false);
  assert.equal(devicePlayerOpens('', 'apple'), undefined, 'nothing is said about a file the server did not describe');
});

test('the note appears only where nothing on this device would play the file', () => {
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: false, player: 'automatic'}), true);
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: true, player: 'automatic'}), false, 'mpv plays it');
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: true, player: 'mpv'}), false);
  assert.equal(originalMayNotPlay('matroska', {platform: 'apple', mpv: true, player: 'native'}), true, 'the viewer chose the device’s player alone');
  assert.equal(originalMayNotPlay('mp4', {platform: 'apple', mpv: false, player: 'automatic'}), false);
  assert.equal(originalMayNotPlay('', {platform: 'apple', mpv: false, player: 'automatic'}), false);
});
