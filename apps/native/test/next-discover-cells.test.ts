import test from 'node:test';
import assert from 'node:assert/strict';
import {discoverCell} from '../src-next/screens/library/discover-cells.ts';

const theme = (isTV: boolean) => ({isTV, platform: isTV ? 'tv' : 'phone', textScale: 1,
  spec: {card: {titleMargin: {tv: 12, phone: 6}, subtitleMargin: {tv: 4, phone: 2}}},
  type: {cardTitle: {lineHeight: isTV ? 30 : 18}, caption: {lineHeight: isTV ? 26 : 16}}}) as any;
const entry = (kind: string, extra: object = {}) => ({id: kind, kind, title: kind, ...extra}) as any;

test('a Discover shelf cell is fixed from the shelf’s own card kinds (LR05)', () => {
  // A music genre shelf is the drawn tile alone.
  assert.deepEqual(discoverCell(theme(true), 320, [entry('category', {navigation: {browse: {pivot: 'albums'}}})]), {art: 180, caption: 0});
  assert.deepEqual(discoverCell(theme(false), 112, [entry('category', {navigation: {browse: {pivot: 'albums'}}})]), {art: 84, caption: 0});
  // Square kinds keep a square; any poster kind makes the shelf a poster shelf. Two title lines plus metadata.
  assert.deepEqual(discoverCell(theme(true), 200, [entry('album'), entry('artist')]), {art: 200, caption: 12 + 60 + 4 + 26});
  assert.deepEqual(discoverCell(theme(false), 112, [entry('album'), entry('movie')]), {art: 168, caption: 6 + 36 + 2 + 16});
});
