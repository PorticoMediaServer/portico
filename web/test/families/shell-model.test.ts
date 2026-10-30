import assert from 'node:assert/strict';
import test from 'node:test';
import {counterProgress, differingProfileName, readSegmentClock, segmentClock, zoomedSegmentRange} from '../../src/kit/shell/model.ts';

test('profile labels retain their actual value only when they differ from the username', () => {
  assert.equal(differingProfileName('Demo', 'demo'), undefined);
  assert.equal(differingProfileName(' Demo ', 'demo'), undefined);
  assert.equal(differingProfileName('Family room', 'sam'), 'Family room');
  assert.equal(differingProfileName('Sam', undefined), 'Sam');
  assert.equal(differingProfileName(' ', undefined), undefined);
});

test('activity progress never guesses a denominator while discovery is incomplete', () => {
  assert.equal(counterProgress(412, undefined), undefined);
  assert.equal(counterProgress(undefined, 500), undefined);
  assert.equal(counterProgress(0, 0), undefined);
  assert.equal(counterProgress(2_000_000, 100), undefined);
  assert.equal(counterProgress(-1, 80), undefined);
  assert.equal(counterProgress(Number.NaN, 80), undefined);
  assert.equal(counterProgress(51, 80), .6375);
  assert.equal(counterProgress(0, 80), 0);
  assert.equal(counterProgress(80, 80), 1);
});

test('segment windows leave two minutes of context around actual markers', () => {
  assert.deepEqual(zoomedSegmentRange([{start: 130, end: 201.5}, {start: 0, end: 38}], 2832, false), {start: 0, end: 360});
  assert.deepEqual(zoomedSegmentRange([{start: 2720, end: 2832}], 2832, true), {start: 2580, end: 2832});
  assert.equal(zoomedSegmentRange([], 2832, false), undefined);
  assert.equal(zoomedSegmentRange([{start: 2, end: 1}], 2832, false), undefined);
  assert.equal(zoomedSegmentRange([{start: 1, end: 3000}], 2832, false), undefined);
  assert.equal(zoomedSegmentRange([{start: Number.NaN, end: 2}], 2832, false), undefined);
});

test('time entry rejects malformed or impossible seconds and preserves tenths', () => {
  assert.equal(readSegmentClock('2:10.5'), 130.5);
  assert.equal(readSegmentClock('47:12'), 2832);
  assert.equal(readSegmentClock('2:60'), undefined);
  assert.equal(readSegmentClock('2:1'), undefined);
  assert.equal(readSegmentClock('2:10.55'), undefined);
  assert.equal(readSegmentClock('bad'), undefined);
  assert.equal(segmentClock(201.5, true), '3:21.5');
  assert.equal(segmentClock(59.96, true), '1:00.0');
  assert.equal(segmentClock(59.5), '1:00');
});
