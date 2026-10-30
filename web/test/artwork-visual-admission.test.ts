import test from 'node:test';
import assert from 'node:assert/strict';
import {createPaintAdmission} from '../src/kit/artwork/visual-admission.ts';

// Source vectors UNRUN. The 40ms rule is a ceiling, never a minimum wait.
const scopeA = {}, scopeB = {};

test('out-of-order decoded arrivals drain the actual contiguous reading frontier without another wait', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const c = paint.mount({scope: scopeA, group: 'row', order: 3});
  const a = paint.mount({scope: scopeA, group: 'row', order: 1});
  const b = paint.mount({scope: scopeA, group: 'row', order: 2});
  c.ready(() => seen.push('c')); assert.equal(seen.length, 0);
  a.ready(() => seen.push('a')); assert.deepEqual(seen, ['a']);
  b.ready(() => seen.push('b'));
  assert.deepEqual(seen, ['a', 'b', 'c'], 'already decoded C needs no minimum40ms timer after B');
  assert.equal(paint.size(), 0);
});

test('the retained88632 readiness pattern adds no40ms floor before native paint publication', () => {
  const paint = createPaintAdmission(), seen: number[] = []; let now = 7228.3;
  const slots = [1, 2, 3].map(order => paint.mount({scope: scopeA, group: 'home-thumbnails', order}));
  slots[0].ready(() => seen.push(now)); now = 7241.4;
  slots[1].ready(() => seen.push(now)); now = 7242.1;
  slots[2].ready(() => seen.push(now));
  assert.deepEqual(seen, [7228.3, 7241.4, 7242.1], 'these are callback times, not fabricated browser opacity-start proof');
});

test('retired failed or offscreen predecessors release an already decoded successor immediately', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const first = paint.mount({scope: scopeA, group: 'row', order: 0});
  const next = paint.mount({scope: scopeA, group: 'row', order: 1});
  next.ready(() => seen.push('next')); first.retire();
  first.ready(() => seen.push('old')); assert.deepEqual(seen, ['next']);
  assert.equal(paint.size(), 0);
});

test('absent scope or group and non-finite reading positions stay independent', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const pending = paint.mount({scope: scopeA, group: 'row', order: 0});
  for (const target of [{order: 1}, {group: 'row', order: 1}, {scope: scopeA, group: 'row'}, {scope: scopeA, group: 'row', order: NaN}])
    paint.mount(target).ready(() => seen.push('independent'));
  assert.equal(seen.length, 4); assert.equal(paint.size(), 1); pending.retire();
});

test('a same-name group never borrows another original scheduler or equal-order registration', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const pending = paint.mount({scope: scopeA, group: 'row', order: 0});
  const first = paint.mount({scope: scopeB, group: 'row', order: 0});
  const next = paint.mount({scope: scopeB, group: 'row', order: 0});
  next.ready(() => seen.push('next')); assert.equal(seen.length, 0);
  first.ready(() => seen.push('first')); assert.deepEqual(seen, ['first', 'next']);
  assert.equal(paint.size(), 1); pending.retire();
});

test('the first ready closure is immutable while its original predecessor is pending', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const head = paint.mount({scope: scopeA, group: 'row', order: 0});
  const next = paint.mount({scope: scopeA, group: 'row', order: 1});
  next.ready(() => seen.push('original')); next.ready(() => seen.push('borrowed'));
  head.retire(); assert.deepEqual(seen, ['original']);
});

test('reentrant retirement cannot paint an outgoing sibling or strand the remaining frontier', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const a = paint.mount({scope: scopeA, group: 'row', order: 0});
  const b = paint.mount({scope: scopeA, group: 'row', order: 1});
  const c = paint.mount({scope: scopeA, group: 'row', order: 2});
  b.ready(() => seen.push('b')); c.ready(() => seen.push('c'));
  a.ready(() => {seen.push('a'); b.retire();});
  assert.deepEqual(seen, ['a', 'c']); assert.equal(paint.size(), 0);
});

test('a replacement mounted during original paint is a genuine pending predecessor', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const first = paint.mount({scope: scopeA, group: 'row', order: 0});
  const next = paint.mount({scope: scopeA, group: 'row', order: 2});
  let replacement!: ReturnType<typeof paint.mount>;
  next.ready(() => seen.push('next'));
  first.ready(() => {seen.push('first'); replacement = paint.mount({scope: scopeA, group: 'row', order: 1});});
  assert.deepEqual(seen, ['first']);
  replacement.ready(() => seen.push('replacement')); assert.deepEqual(seen, ['first', 'replacement', 'next']);
});

test('a thrown original paint cannot block the ready frontier', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const first = paint.mount({scope: scopeA, group: 'row', order: 0});
  const next = paint.mount({scope: scopeA, group: 'row', order: 1});
  next.ready(() => seen.push('next')); first.ready(() => {throw new Error('paint refused');});
  assert.deepEqual(seen, ['next']); assert.equal(paint.size(), 0);
});

test('retired groups and slots never adopt stale ready closures into a recreated group', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const old = paint.mount({scope: scopeA, group: 'row', order: 0}); old.retire();
  const next = paint.mount({scope: scopeA, group: 'row', order: 0});
  old.ready(() => seen.push('old')); next.ready(() => seen.push('next'));
  old.retire(); assert.deepEqual(seen, ['next']); assert.equal(paint.size(), 0);
});

test('dispose during paint invalidates queued and independent callbacks synchronously', () => {
  const paint = createPaintAdmission(), seen: string[] = [];
  const first = paint.mount({scope: scopeA, group: 'row', order: 0});
  const next = paint.mount({scope: scopeA, group: 'row', order: 1});
  const independent = paint.mount({}); next.ready(() => seen.push('next'));
  first.ready(() => {seen.push('first'); paint.dispose();}); independent.ready(() => seen.push('late'));
  assert.deepEqual(seen, ['first']); assert.equal(paint.size(), 0);
});
