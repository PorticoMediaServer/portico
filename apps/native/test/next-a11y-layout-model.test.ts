import test from 'node:test';
import assert from 'node:assert/strict';
import {captionItemHeight, captureGridAnchor, gridAnchorOffset, gridWindow} from '../src-next/kit/layout/model.ts';

test('caption growth retains authored default dimensions and never scales the artwork', () => {
  for (const [art, caption] of [[150, 43], [116, 54], [300, 72]]) {
    const original = art! + caption!;
    assert.equal(captionItemHeight(original, caption!, 1), original);
    assert.equal(captionItemHeight(original, caption!, .8), original);
    assert.equal(captionItemHeight(original, caption!, 2), art! + 2 * caption!);
    assert.equal(captionItemHeight(original, caption!, 3.5), art! + 3.5 * caption!);
    assert.equal(captionItemHeight(original, caption!, NaN), original);
    assert.equal(captionItemHeight(original, caption!, Infinity), original);
  }
  assert.equal(captionItemHeight(225, 0, 3), 225, 'a wide card with no caption keeps its existing art height');
  assert.equal(captionItemHeight(70, 70, 2), 140, 'text list rows share the same scaled stride as their renderer');
});

test('a live font-scale change updates sparse-grid stride without moving the anchored item', () => {
  const total = 1_000_000, columns = 3, art = 180, caption = 48, gap = 22;
  const before = captionItemHeight(art + caption, caption, 1) + gap;
  const after = captionItemHeight(art + caption, caption, 2.5) + gap;
  const header = 230, sticky = 52, oldScroll = header + 12 * before + 17 - sticky;
  const anchor = captureGridAnchor(oldScroll, columns, before, header, sticky, total)!;
  assert.deepEqual(anchor, {index: 36, offset: -17});
  const extent = header + Math.ceil(total / columns) * after;
  const next = gridAnchorOffset(anchor, columns, after, header, sticky, extent, 640);
  assert.deepEqual(captureGridAnchor(next, columns, after, header, sticky, total), anchor);
  const visible = gridWindow(total, columns, after, next + sticky - header, 640 - sticky);
  assert.equal(visible.first, 36);
  assert.ok(visible.last - visible.first < 15, 'large libraries remain bounded to the visible window');
  assert.ok(after > before);
});
