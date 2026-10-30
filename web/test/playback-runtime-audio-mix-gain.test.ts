import test from 'node:test';
import assert from 'node:assert/strict';
import {rebuildGain} from '../src/platform/playback/audio/mix-gain.ts';

test('a held fade resumes at the interpolated value and preserves each remaining knot', () => {
  const events: unknown[][] = [];
  const param = {cancelScheduledValues: (t: number) => events.push(['cancel', t]), setValueAtTime: (v: number, t: number) => events.push(['set', v, t]), linearRampToValueAtTime: (v: number, t: number) => events.push(['ramp', v, t])} as unknown as AudioParam;
  rebuildGain(param, 15, 0, [{frame: 10, value: 0}, {frame: 20, value: 0.5}, {frame: 30, value: 1}], frame => (frame - 15) / 10 + 8);
  assert.deepEqual(events, [['cancel', 0], ['set', 0.25, 8], ['ramp', 0.5, 8.5], ['ramp', 1, 9.5]]);
});

test('a future ramp holds its initial value until its original starting knot', () => {
  const events: unknown[][] = [];
  const param = {cancelScheduledValues: (t: number) => events.push(['cancel', t]), setValueAtTime: (v: number, t: number) => events.push(['set', v, t]), linearRampToValueAtTime: (v: number, t: number) => events.push(['ramp', v, t])} as unknown as AudioParam;
  rebuildGain(param, 0, 1, [{frame: 10, value: 1}, {frame: 12, value: 0.5}], frame => frame / 10);
  assert.deepEqual(events, [['cancel', 0], ['set', 1, 0], ['set', 1, 1], ['ramp', 0.5, 1.2]]);
});
