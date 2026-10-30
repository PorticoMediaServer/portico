import test from 'node:test';
import assert from 'node:assert/strict';
import {sessionWindow, COMPACT_SESSION_HEIGHT} from '../src/screens/dashboard/session-window.ts';
import {SUPPORT_WINDOWS, DEFAULT_SUPPORT_COMPONENTS} from '../src/screens/settings/server/troubleshooting/support-options.ts';

test('busy phone window stays bounded for large lists and includes distant interaction owners', () => {
  const middle = sessionWindow(100_000, COMPACT_SESSION_HEIGHT * 50_000, 660, [2, 99999, 2]);
  assert.equal(middle.length, 16);assert.equal(middle[0], 2);assert.equal(middle.at(-1), 99999);
  assert.deepEqual(middle.slice(1, -1), Array.from({length: 14}, (_, i) => 49998 + i));
});
test('busy phone window clamps a stale scroll position when the list shrinks', () => {
  assert.deepEqual(sessionWindow(3, 999999, 660, [-1, 3, NaN]), [0, 1, 2]);
  assert.deepEqual(sessionWindow(0, 0, 0), []);
  assert.deepEqual(sessionWindow(5, NaN, Infinity), [0, 1, 2]);
  assert.throws(() => sessionWindow(-1, 0, 1));assert.throws(() => sessionWindow(1.5, 0, 1));
});
test('support offers all five approved periods and app logs start off', () => {
  assert.deepEqual(SUPPORT_WINDOWS.map(window => window.ms), [3600000, 21600000, 86400000, 259200000, 604800000]);
  assert.deepEqual(DEFAULT_SUPPORT_COMPONENTS, ['runtime', 'audit', 'snapshot']);
});
