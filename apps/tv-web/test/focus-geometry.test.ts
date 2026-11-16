import assert from 'node:assert/strict';
import {test} from 'node:test';
import {distance, type Rect} from '../src/shims/focus-geometry.ts';

const box = (left: number, top: number, width = 100, height = 100): Rect => ({left, top, right: left + width, bottom: top + height});
const nearest = (from: Rect, direction: Parameters<typeof distance>[2], candidates: Rect[]) =>
  candidates.map((rect, index) => ({index, score: distance(from, rect, direction)})).filter(entry => Number.isFinite(entry.score)).sort((a, b) => a.score - b.score)[0]?.index;

test('Down in a grid keeps the column', () => {
  const grid = [box(0, 150), box(150, 150), box(300, 150)];
  assert.equal(nearest(box(150, 0), 'down', grid), 1);
});

test('Right in a row takes the next card, not one in another row', () => {
  const cards = [box(150, 0), box(300, 0), box(150, 150)];
  assert.equal(nearest(box(0, 0), 'right', cards), 0);
});

test('nothing behind the key counts', () => {
  assert.equal(distance(box(150, 0), box(0, 0), 'right'), Infinity);
  assert.equal(distance(box(0, 150), box(0, 0), 'down'), Infinity);
});

test('a candidate overlapping on the axis counts when its centre lies that way', () => {
  // A wide hero button above a narrower row still reaches the row below it.
  assert.ok(Number.isFinite(distance(box(0, 0, 400, 100), box(50, 90, 100, 100), 'down')));
});

test('from a wide control, the nearer of two rows wins', () => {
  assert.equal(nearest(box(0, 0, 600, 80), 'down', [box(0, 400), box(0, 120)]), 1);
});
