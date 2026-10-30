import test from 'node:test';
import assert from 'node:assert/strict';
import {liveStabilityPatch, liveStabilityValue, liveRewindRow} from '../src-next/screens/settings-live/live-defaults-presentation';

test('live stability presets round-trip both server values in milliseconds', () => {
  for (const [choice, buffer, retry] of [['balanced', 8000, 60000], ['smooth', 20000, 180000], ['live', 3000, 30000]] as const) {
    const patch = liveStabilityPatch(choice);
    assert.deepEqual(patch, {streamBufferMs: buffer, retryWindowMs: retry});
    assert.equal(liveStabilityValue(patch!), choice);
  }
});

test('custom tuning is exact and selecting Custom does not overwrite it', () => {
  assert.equal(liveStabilityValue({streamBufferMs: 8000, retryWindowMs: 59000}), 'custom');
  assert.equal(liveStabilityValue({streamBufferMs: 7999, retryWindowMs: 60000}), 'custom');
  assert.equal(liveStabilityPatch('custom'), undefined);
  assert.equal(liveStabilityPatch('unrecognized'), undefined);
  assert.deepEqual(liveRewindRow.control.options?.map(option => Number(option.value)), [10, 30, 60, 120]);
});
