import assert from 'node:assert/strict';
import {test} from 'node:test';
import {rightEndMenuRect} from '../src-next/kit/overlays/anchored-menu-geometry.ts';

const insets = {left: 0, right: 0, top: 0, bottom: 0};
test('a genuine right-end opener determines the TV flyout edge and bottom alignment', () => {
  const rect = rightEndMenuRect({x: 72, y: 948, width: 320, height: 76}, {width: 1920, height: 1080}, {width: 640, height: 525}, insets, 168, -8);
  assert.deepEqual(rect, {x: 560, y: 491, width: 640, height: 525});
});
test('window measurements and logical flyout dimensions agree on a scaled letterboxed TV canvas', () => {
  // A 960x540 canvas sits40pt below the top of this960x620 native window.
  const rect = rightEndMenuRect({x: 36, y: 514, width: 160, height: 38}, {width: 960, height: 620}, {width: 640, height: 525}, insets, 168, -8, .5);
  assert.deepEqual(rect, {x: 280, y: 285.5, width: 320, height: 262.5});
});
test('right-end geometry follows the measured opener and stays inside the actual safe area', () => {
  const rect = rightEndMenuRect({x: 700, y: 40, width: 100, height: 50}, {width: 820, height: 600}, {width: 640, height: 525}, {left: 12, right: 16, top: 24, bottom: 20}, 168, -8);
  assert.deepEqual(rect, {x: 156, y: 32, width: 640, height: 525});
  const moved = rightEndMenuRect({x: 50, y: 300, width: 100, height: 50}, {width: 820, height: 600}, {width: 300, height: 200}, insets, 12, -8);
  assert.deepEqual(moved, {x: 162, y: 142, width: 300, height: 200});
});
