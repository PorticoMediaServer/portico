import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {bufferingPresets, matchingPreset, paddingPresets, retentionPresets, spacePresets, layoutPresets} from '../src/screens/live/owner/settings-presets.ts';

test('buffering and padding presets set both engine values in milliseconds', () => {
  assert.deepEqual(bufferingPresets.balanced, {streamBufferMs: 8000, retryWindowMs: 60000});
  assert.deepEqual(bufferingPresets.smooth, {streamBufferMs: 20000, retryWindowMs: 180000});
  assert.deepEqual(bufferingPresets.live, {streamBufferMs: 3000, retryWindowMs: 30000});
  assert.deepEqual(paddingPresets.sport, {prePaddingMs: 60000, postPaddingMs: 900000});
  assert.equal(matchingPreset(bufferingPresets, {streamBufferMs: 8000, retryWindowMs: 60001}), 'custom');
  assert.equal(matchingPreset(paddingPresets, {prePaddingMs: 60000, postPaddingMs: 900000, unrelated: true}), 'sport');
});

test('retention and space presets preserve exact custom combinations', () => {
  assert.equal(matchingPreset(retentionPresets, {retentionDays: 30, episodeLimit: 5}), 'custom');
  assert.deepEqual(retentionPresets.n5, {retentionDays: 0, episodeLimit: 5});
  assert.equal(matchingPreset(spacePresets, {floorBytes: 0, capBytes: 250e9}), 'custom');
  assert.deepEqual(spacePresets.up250, {floorBytes: 10e9, capBytes: 250e9});
  assert.equal(matchingPreset(spacePresets, {floorBytes: 10e9, capBytes: 1e12}), 'up1000');
});

test('a folder template must match both templates to be a named layout', () => {
  assert.equal(matchingPreset(layoutPresets, {...layoutPresets.season}), 'season');
  assert.equal(matchingPreset(layoutPresets, {...layoutPresets.season, movieFolderTemplate: ''}), 'custom');
});
