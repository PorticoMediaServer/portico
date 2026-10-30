import test from 'node:test';
import assert from 'node:assert/strict';
import {compareTextMetrics} from './text-metrics.mjs';

test('repeated exact strings match separate nearest occurrences and expose measured type/position changes', () => {
  const text = (id, x, style = {}) => ({id, kind: 'text', text: 'Public label', rects: [{x, y: 20, width: 40, height: 20}], style});
  const reference = {strings: [text('r1', 10, {fontSize: '16px'}), text('r2', 100, {fontSize: '16px'})]};
  const actual = {strings: [text('a2', 100, {fontSize: '16px'}), text('a1', 11, {fontSize: '17px'})]};
  const report = compareTextMetrics(reference, actual);
  assert.equal(report.matchedOccurrences, 2); assert.equal(report.diagnostics.length, 2);
  assert.equal(report.diagnostics[0].actualElement, 'a1'); assert.equal(report.diagnostics[0].fact, 'text-rectangles');
  assert.equal(report.diagnostics[1].fact, 'fontSize'); assert.deepEqual(report.unassessed, []);
});
test('native input host rectangles are never presented as measured glyph rectangles', () => {
  const reference = {strings: [{id: 'r', kind: 'text', text: 'public-user', rects: [{x: 5, y: 5, width: 90, height: 20}]}]};
  const actual = {strings: [{id: 'a', kind: 'value', text: 'public-user', box: {x: 0, y: 0, width: 300, height: 50}}]};
  const report = compareTextMetrics(reference, actual);
  assert.equal(report.unassessed.length, 1); assert.equal(report.unassessed[0].fact, 'native-input-glyph-bounds');
  assert.deepEqual(report.diagnostics, []);
});
