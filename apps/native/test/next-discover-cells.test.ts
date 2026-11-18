import test from 'node:test';
import assert from 'node:assert/strict';
import {discoverCell} from '../src-next/screens/library/discover-cells.ts';

// The caption is R5's reserved slot from the form scale (kit cardCaptionHeight).
const theme = (isTV: boolean) => ({isTV, fontScale: 1,
  scale: {card: {caption: isTV ? {gap: 15, titleLine: 32, subtitleGap: 4, subtitleLine: 29} : {gap: 8, titleLine: 18, subtitleGap: 2, subtitleLine: 17}}}}) as any;
const entry = (kind: string, extra: object = {}) => ({id: kind, kind, title: kind, ...extra}) as any;

test('a Discover shelf cell is fixed from the shelf’s own card kinds (LR05)', () => {
  // A music genre shelf is the drawn tile alone.
  assert.deepEqual(discoverCell(theme(true), 320, [entry('category', {navigation: {browse: {pivot: 'albums'}}})]), {art: 180, caption: 0});
  assert.deepEqual(discoverCell(theme(false), 112, [entry('category', {navigation: {browse: {pivot: 'albums'}}})]), {art: 84, caption: 0});
  // Square kinds keep a square; any poster kind makes the shelf a poster shelf. Two title lines plus metadata.
  assert.deepEqual(discoverCell(theme(true), 200, [entry('album'), entry('artist')]), {art: 200, caption: 15 + 2 * 32 + 29 + 4});
  assert.deepEqual(discoverCell(theme(false), 112, [entry('album'), entry('movie')]), {art: 168, caption: 8 + 2 * 18 + 17 + 2});
});
