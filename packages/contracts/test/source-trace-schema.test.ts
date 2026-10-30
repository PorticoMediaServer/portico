import test from 'node:test';
import assert from 'node:assert/strict';
import {schemas} from '../src/schemas.ts';

test('existing playback offers retain sparse source trace facts on their delivery plan', () => {
  const plan = schemas.GetItemsIdPlaybackOffersResponse.properties.deliveryPlan;
  const video = plan.properties.trace.properties.video;
  const audio = plan.properties.trace.properties.audio;
  for (const key of ['interlaced', 'dolbyVisionRpu', 'dolbyVisionBaseLayer'] as const) {
    assert.equal(video.properties[key].type, 'boolean');
    assert.equal(new Set<string>(video.required).has(key), false);
  }
  for (const key of ['frameRateNumerator', 'frameRateDenominator'] as const) {
    assert.equal(video.properties[key].type, 'integer');
  }
  for (const key of ['chromaSubsampling', 'frameRateMode', 'dolbyVisionEnhancementType', 'dolbyVisionCmVersion', 'masterDisplay', 'maxCll'] as const) {
    assert.equal(video.properties[key].type, 'string');
  }
  for (const key of ['profile', 'variant', 'layout'] as const) assert.equal(audio.properties[key].type, 'string');
  for (const key of ['sampleRate', 'bitDepth'] as const) assert.equal(audio.properties[key].type, 'integer');
});
