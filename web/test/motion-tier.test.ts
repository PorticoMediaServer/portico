import test from 'node:test';
import assert from 'node:assert/strict';
import {liteEffectsRequested, motionTier} from '../src/platform/motion-tier.ts';

test('the motion tier matches native: Reduce motion wins, lite only by explicit opt-in, otherwise full', () => {
  assert.equal(motionTier(false, false), 'full');
  assert.equal(motionTier(false, true), 'lite');
  assert.equal(motionTier(true, true), 'reduced');
  assert.equal(liteEffectsRequested({}), false);
  assert.equal(liteEffectsRequested({__PORTICO_EFFECTS_TIER__: 'lite'}), true);
  assert.equal(liteEffectsRequested({__PORTICO_EFFECTS_TIER__: 'full'}), false);
});
