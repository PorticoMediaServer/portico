import test from 'node:test';
import assert from 'node:assert/strict';
import {COVER_MAX_LINES, coverTitleLines} from '../src/kit/artwork/cover-fit.ts';

// The cover's own geometry in cqw-derived px for a 200-wide frame: 12cqw/11cqw padding, the
// 1.2cqw rule plus its 6.5cqw margin, a 12.6cqw title at line-height 1.03.
const w = 200, cq = (value: number) => value * w / 100;
const base = {paddingTop: cq(12), paddingBottom: cq(11), rule: cq(1.2 + 6.5), lineHeight: cq(12.6) * 1.03};

test('a poster keeps the authored five lines; a square cover with an author holds fewer, never cut mid-line (FX-P3)', () => {
  assert.equal(coverTitleLines({...base, copyHeight: w * 1.5, author: 0}), COVER_MAX_LINES);
  const square = coverTitleLines({...base, copyHeight: w, author: 17});
  assert.equal(square, 4);
  // What is left after those lines and the author still fits inside the padding.
  const used = base.paddingTop + base.rule + square * base.lineHeight + 17;
  assert.ok(used <= w - base.paddingBottom, `${used} > ${w - base.paddingBottom}`);
});
test('a wide or tiny frame draws at least one whole line, and an unmeasured one keeps the default', () => {
  assert.equal(coverTitleLines({...base, copyHeight: w * 9 / 16, author: 17}), 1);
  assert.equal(coverTitleLines({...base, copyHeight: 40, author: 0}), 1);
  assert.equal(coverTitleLines({...base, copyHeight: 0, author: 0}), COVER_MAX_LINES);
  assert.equal(coverTitleLines({...base, copyHeight: 300, author: 0, lineHeight: 0}), COVER_MAX_LINES);
});
