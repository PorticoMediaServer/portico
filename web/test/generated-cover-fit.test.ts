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

test('every generated cover shares one ResizeObserver, and one style read serves a size (FX-P3 follow-up)', async () => {
  const {measureCoverTitleLines, observeCoverSize} = await import('../src/kit/artwork/cover-fit.ts');
  let observers = 0, styleReads = 0;
  const observed = new Set<unknown>();
  let deliver: ((entries: {target: unknown}[]) => void) | undefined;
  const view: any = {innerWidth: 1440,
    ResizeObserver: class {constructor(callback: (entries: {target: unknown}[]) => void) {observers++; deliver = callback;} observe(target: unknown) {observed.add(target);} unobserve(target: unknown) {observed.delete(target);}},
    getComputedStyle: (_element: unknown, pseudo?: string) => {styleReads++;
      return pseudo ? {display: 'block', content: '""', height: '2.4px', marginBottom: '13px'} : {paddingTop: '24px', paddingBottom: '22px', lineHeight: '25.956px', fontSize: '25.2px'};}};
  const cover = (author: boolean) => {
    const title = {}, small = author ? {offsetParent: {}, getBoundingClientRect: () => ({height: 17})} : null;
    const copy: any = {ownerDocument: {defaultView: view}, clientWidth: 200, clientHeight: 200, querySelector: (selector: string) => selector === 'b' ? title : small};
    return {copy, element: {ownerDocument: {defaultView: view}}};
  };
  const covers = Array.from({length: 40}, (_, index) => cover(index % 2 === 0));
  const lines = covers.map(({copy}) => measureCoverTitleLines(copy, 'scale-a'));
  assert.equal(styleReads, 3, 'forty same-size covers make one set of style reads');
  assert.deepEqual([...new Set(lines)].sort(), [4, 5], 'the author is still read per cover');
  const fired: number[] = [];
  const stops = covers.map(({element}, index) => observeCoverSize(element, () => fired.push(index)));
  assert.equal(observers, 1);
  assert.equal(observed.size, 40);
  deliver!([{target: covers[3]!.element}, {target: covers[7]!.element}]);
  assert.deepEqual(fired, [3, 7]);
  for (const stop of stops) stop();
  assert.equal(observed.size, 0, 'each cover stops observing on cleanup');
  deliver!([{target: covers[3]!.element}]);
  assert.deepEqual(fired, [3, 7]);
});
