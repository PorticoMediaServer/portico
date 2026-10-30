import test from 'node:test';
import assert from 'node:assert/strict';
import {planMix, type MixPacket} from '../src/platform/playback/audio/mix-frontier.ts';

const buffer = (length: number) => ({length} as AudioBuffer);
const packet = (start: number, end: number): MixPacket => ({start, end, buffer: buffer(end - start), offset: 0});

test('an incoming voice limits the shared frontier at its first missing sample', () => {
  const a = {}, b = {};
  const result = planMix([
    {id: a, start: 0, end: 20, packets: [packet(0, 20)]},
    {id: b, start: 10, end: 30, packets: [packet(10, 13)]},
  ], 0, 20);
  assert.equal(result.frontier, 13);
  assert.deepEqual(result.slices.map(s => [s.voice === a ? 'a' : 'b', s.start, s.frames, s.offset]), [
    ['a', 0, 10, 0], ['a', 10, 3, 10], ['b', 10, 3, 0],
  ]);
});

test('a retained buffer crossing the held frontier uses its original suffix offset', () => {
  const voice = {}, result = planMix([{id: voice, start: 0, end: 20, packets: [packet(0, 20)]}], 7, 12);
  assert.equal(result.frontier, 12);
  assert.deepEqual(result.slices.map(s => [s.start, s.offset, s.frames]), [[7, 7, 5]]);
});

test('missing required PCM prevents every other active voice from advancing', () => {
  const a = {}, b = {};
  const result = planMix([{id: a, start: 0, end: 20, packets: [packet(0, 20)]}, {id: b, start: 0, end: 20, packets: []}], 0, 20);
  assert.equal(result.frontier, 0);
  assert.deepEqual(result.slices, []);
});
