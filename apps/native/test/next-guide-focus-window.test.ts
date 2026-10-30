import assert from 'node:assert/strict';
import test from 'node:test';
import {layoutRow} from '../../../packages/client-core/src/guide';
import {tvGuideNavigationCells, tvGuideFocusStart} from '../src-next/tv/layout/guide-focus.ts';

const programme = {id: 'real', channelId: 'channel', title: 'Real server programme', start: 20, end: 60};
test('guide frontier placeholders expose only the next offered block within genuine coverage', () => {
  const cells = layoutRow([programme], {viewStart: 30, viewEnd: 50, pxPerMs: 10, now: 40});
  const result = tvGuideNavigationCells(cells, {first: 0, last: 80}, 30, 10, 10);
  assert.deepEqual(result.map(cell => [cell.start, cell.end, cell.kind, cell.program?.id, cell.pending]), [
    [10, 20, 'gap', undefined, true], [20, 60, 'program', 'real', undefined], [60, 70, 'gap', undefined, true],
  ]);
  assert.equal(result[0]!.x, -200); assert.equal(result[2]!.x, 300);
  assert.equal(tvGuideNavigationCells(cells, undefined, 30, 10, 10), cells, 'no fabricated coverage when the source publishes none');
});
test('the true coverage end has no additional focusable block', () => {
  const cells = layoutRow([programme], {viewStart: 20, viewEnd: 60, pxPerMs: 10, now: 40});
  const result = tvGuideNavigationCells(cells, {first: 20, last: 60}, 20, 10, 10);
  assert.equal(result.length, 1); assert.equal(result[0]!.program?.id, 'real');
});
test('a wide vertical destination keeps the time window and a lateral target reveals its anchor', () => {
  assert.equal(tvGuideFocusStart(45, 30, 55, 5), 30);
  assert.equal(tvGuideFocusStart(20, 30, 55, 5), 20);
  assert.equal(tvGuideFocusStart(60, 30, 55, 5), 40);
});
