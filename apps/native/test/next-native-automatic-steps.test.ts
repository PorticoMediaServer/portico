import test from 'node:test';
import assert from 'node:assert/strict';
import {automaticSteps} from '../platform/automatic-video-quality.ts';
import {automaticSteps as nextSteps} from '../src-next/platform/playback/native-playback.ts';
import type {QualityOption} from '../../../packages/client-core/src/playback-v1/quality.ts';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';

test('both native paths use the same exact offered canonical targets in ladder order', () => {
  const offered: readonly QualityOption[] = [{mode: 'fixed', id: '720p-low'}, {mode: 'original'}, {mode: 'automatic'}, {mode: 'fixed', id: '1080p-low'}, {mode: 'fixed', id: '720p-low'}];
  assert.equal(nextSteps, automaticSteps);
  assert.deepEqual(automaticSteps(offered).map(value => value.id), ['1080p-low', '720p-low']);
});

test('explicitly inapplicable/original-equivalent or missing targets cannot grant an Automatic step', () => {
  const offered: readonly QualityOption[] = [{mode: 'fixed', id: '1080p-low', applicable: false}, {mode: 'fixed', id: '720p-high', sameAsOriginal: true}, {mode: 'fixed'}, {mode: 'fixed', id: '720p-low', applicable: true}];
  assert.deepEqual(automaticSteps(offered).map(value => value.id), ['720p-low']);
  const legacy = parseOptions({itemId: 'item', versions: [{id: 'version', parts: [], video: [], audio: [], subtitles: []}], qualities: [{mode: 'fixed', id: '1080p'}, {mode: 'fixed', id: '720p'}]});
  assert.deepEqual(automaticSteps(legacy.qualities), [], 'no suffix translation or old alias reinstatement');
});
