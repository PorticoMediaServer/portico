import test from 'node:test';
import assert from 'node:assert/strict';
import {visibleGuideRows} from '../src-next/screens/settings-live/guide-pan-model.ts';

// E-tv-38: the guide's visible range is half-open everywhere. Viewability gave an inclusive end,
// so the bottom visible row looked off screen and focusing it re-scrolled the guide.
test('E-tv-38: viewable guide rows are a half-open range', () => {
  assert.deepEqual(visibleGuideRows([3, 4, 5, 6]), {first: 3, last: 7});
  assert.deepEqual(visibleGuideRows([0]), {first: 0, last: 1});
  assert.equal(visibleGuideRows([]), undefined);
  const rows = visibleGuideRows([3, 4, 5, 6])!, bottom = 6;
  assert.equal(bottom < rows.first || bottom >= rows.last, false, 'the bottom visible row is on screen');
});
