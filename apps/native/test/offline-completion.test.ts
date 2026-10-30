import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineCompletion} from '../platform/offline-completion.ts';

test('a long audiobook stopped at 93% keeps its resume and does not trigger completion rules', () => {
  const duration = 10 * 60 * 60, position = duration * 0.93;
  assert.deepEqual(offlineCompletion('audiobook_file', position, duration, false), {finished: false, resumeSeconds: position});
});

test('audio completion uses the smaller of 30 seconds and 5% of its duration', () => {
  for (const kind of ['song', 'audiobook_file']) {
    for (const duration of [20, 240, 600, 36000]) {
      const boundary = duration - Math.min(30, duration / 20);
      assert.equal(offlineCompletion(kind, boundary - 0.01, duration, false).finished, false, `${kind}, ${duration}s before boundary`);
      assert.deepEqual(offlineCompletion(kind, boundary, duration, false), {finished: true, resumeSeconds: boundary});
    }
  }
});

test('video, and any kind that is not audio, keeps the 92% completion rule without resetting an unfinished resume point', () => {
  for (const kind of ['movie', 'episode', 'extra', 'disc', undefined]) {
    assert.deepEqual(offlineCompletion(kind, 919, 1000, false), {finished: false, resumeSeconds: 919});
    assert.deepEqual(offlineCompletion(kind, 920, 1000, false), {finished: true, resumeSeconds: 920});
    assert.deepEqual(offlineCompletion(kind, 1000, 1000, false), {finished: true, resumeSeconds: 1000});
  }
});

test('only an actual end resets resume, and an unknown duration is never assumed finished', () => {
  for (const kind of ['song', 'audiobook_file', 'movie', 'unknown', undefined]) {
    for (const duration of [0, -1, Number.NaN, Number.POSITIVE_INFINITY, 1000]) {
      assert.deepEqual(offlineCompletion(kind, 930, duration, true), {finished: true, resumeSeconds: 0});
      if (!Number.isFinite(duration) || duration <= 0) {
        assert.deepEqual(offlineCompletion(kind, 930, duration, false), {finished: false, resumeSeconds: 930});
      }
    }
  }
});
