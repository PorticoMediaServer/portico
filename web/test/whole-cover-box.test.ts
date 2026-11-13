import test from 'node:test';
import assert from 'node:assert/strict';
import {wholeCoverBox} from '../src/kit/artwork/whole-cover.ts';

// Resolve the box against a slot `width` wide, as the browser does: the size and percent margins are of the slot's width.
const px = (value: unknown, width: number) => {
  const expression = String(value ?? '0').replaceAll('var(--slot)', String(width)).replaceAll('100%', String(width)).replaceAll('calc(', '(');
  assert.match(expression, /^[\d.()*\s+e-]+$/);
  return Function(`return ${expression}`)() as number;
};
const drawn = (aspect: number, size: string, width: number, options?: Parameters<typeof wholeCoverBox>[2]) => {
  const box = wholeCoverBox(aspect, size, options)!;
  const w = px(box.width, width), h = w / Number(box.aspectRatio);
  const inline = px(box.marginInline, width), top = px(box.marginTop ?? box.marginBlock, width), bottom = px(box.marginBlock, width);
  return {w: Math.round(w), h: Math.round(h), outerW: Math.round(w + 2 * inline), outerH: Math.round(h + top + bottom), top: Math.round(top)};
};

test('a cover in its own slot shape draws as before, and nothing is drawn until its shape is known', () => {
  assert.equal(wholeCoverBox(undefined, '100%'), undefined);
  assert.equal(wholeCoverBox(1, '100%'), undefined);
  assert.equal(wholeCoverBox(2 / 3, '112px', {slot: 2 / 3}), undefined);
});

test('a 2:3 cover in a square card keeps its shape, centred, and the slot keeps its size', () => {
  assert.deepEqual(drawn(2 / 3, '100%', 180, {align: 'end'}), {w: 120, h: 180, outerW: 180, outerH: 180, top: 0});
});

test('a landscape cover in a square card sits whole at the slot foot, so a grid stays even', () => {
  assert.deepEqual(drawn(1.5, '100%', 180, {align: 'end'}), {w: 180, h: 120, outerW: 180, outerH: 180, top: 60});
});

test('the dock and Continue listening centre a landscape cover in their square slot', () => {
  assert.deepEqual(drawn(1.5, 'var(--slot)', 60), {w: 60, h: 40, outerW: 60, outerH: 60, top: 10});
});

test('a reading slot (2:3 book, 3:4 issue) holds a landscape cover and a narrower one whole', () => {
  assert.deepEqual(drawn(1.5, 'var(--slot)', 112, {slot: 2 / 3}), {w: 112, h: 75, outerW: 112, outerH: 168, top: 47});
  assert.deepEqual(drawn(.5, 'var(--slot)', 126, {slot: 3 / 4}), {w: 84, h: 168, outerW: 126, outerH: 168, top: 0});
});
