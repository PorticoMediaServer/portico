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

test('a guide title takes two lines only where the part on screen fits a whole word per line; a sliver is one line cut at its end (W3 F16)', async () => {
  const {nativeGuideTitleLines} = await import('../src-next/screens/settings-live/guide-pan-model.ts');
  assert.equal(nativeGuideTitleLines('full', 0, 220, 330, false, false), 2, 'a whole wide programme wraps to two lines');
  assert.equal(nativeGuideTitleLines('full', -190, 220, 330, false, false), 1, 'a wide programme that began before the first column shows 30 points: one line');
  assert.equal(nativeGuideTitleLines('title2', 290, 120, 330, false, false), 1, 'a programme running off the right edge shows 40 points: one line');
  assert.equal(nativeGuideTitleLines('title', 0, 220, 330, true, false), 1, 'the compact tiers keep their own one line');
  assert.equal(nativeGuideTitleLines('title2', -190, 220, 330, false, true), 2, 'TV keeps its tiers');
});
