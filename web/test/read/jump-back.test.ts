import assert from 'node:assert/strict';
import {test} from 'node:test';
import {isImplicitJump, jumpBackPlace, nextJumpBack, sameReaderPlace} from '../../src/screens/read/jump-back.ts';

test('sameReaderPlace distinguishes epub CFIs and fixed-page ordinals', () => {
  assert.equal(sameReaderPlace({format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', progression: 0.1}, {format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', progression: 0.12}), true);
  assert.equal(sameReaderPlace({format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', progression: 0.1}, {format: 'epub', cfi: 'epubcfi(/6/4!/4/2)', progression: 0.4}), false);
  assert.equal(sameReaderPlace({format: 'pdf', page: 3, progression: 0.3}, {format: 'pdf', page: 3, progression: 0.3}), true);
  assert.equal(sameReaderPlace({format: 'pdf', page: 3, progression: 0.3}, {format: 'pdf', page: 4, progression: 0.4}), false);
  assert.equal(sameReaderPlace({format: 'cb7', page: 3, progression: 0.3}, {format: 'cbz', page: 3, progression: 0.3}), false);
});

test('isImplicitJump detects unrequested EPUB section or large progression jumps without false reflow triggers', () => {
  const start = {format: 'epub' as const, cfi: 'epubcfi(/6/2!/4/2)', section: 'ch1.xhtml', progression: 0.1};
  assert.equal(isImplicitJump(start, {...start, cfi: 'epubcfi(/6/2!/4/4)', progression: 0.105}), false);
  assert.equal(isImplicitJump(start, {...start, cfi: 'epubcfi(/6/2!/4/90)', progression: 0.14}), true);
  assert.equal(isImplicitJump(start, {format: 'epub', cfi: 'epubcfi(/6/8!/4/2)', section: 'notes.xhtml', progression: 0.95}), true);
  assert.equal(isImplicitJump({format: 'pdf', page: 1, progression: 0.1}, {format: 'pdf', page: 8, progression: 0.8}), false);
});

test('nextJumpBack preserves the original pre-scrub position during continuous slider drags', () => {
  const origin = {format: 'pdf' as const, page: 2, progression: 0.2};
  const mid = {format: 'pdf' as const, page: 5, progression: 0.5};
  const chapter = {format: 'pdf' as const, page: 8, progression: 0.8};
  const first = nextJumpBack(null, origin, 'scrub', 1);
  assert.equal(first.location, origin);
  assert.equal(nextJumpBack(first, mid, 'scrub', 2), first);
  const explicit = nextJumpBack(first, chapter, 'jump', 3);
  assert.equal(explicit.location, chapter);
  assert.equal(explicit.id, 3);
});

test('jumpBackPlace formats observed page labels, chapters and progression truthfully', () => {
  const page = (value: string) => `Page ${value}`;
  const pct = (value: number) => `${Math.round(value * 100)}%`;
  const num = (value: number) => String(value);
  assert.equal(jumpBackPlace({format: 'pdf', page: 4, progression: 0.4}, page, pct, num), 'Page 5');
  assert.equal(jumpBackPlace({format: 'pdf', page: 4, pageLabel: 'iv', progression: 0.4}, page, pct, num), 'Page iv');
  assert.equal(jumpBackPlace({format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', chapter: 'Chapter 3', progression: 0.3}, page, pct, num), 'Chapter 3');
  assert.equal(jumpBackPlace({format: 'epub', cfi: 'epubcfi(/6/2!/4/2)', progression: 0.42}, page, pct, num), '42%');
});
