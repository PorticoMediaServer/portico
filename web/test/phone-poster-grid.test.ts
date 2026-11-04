import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {gridColumns} from '../src/kit/layout/geometry.ts';

test('A4: a phone poster grid is three across at 360–390 (phone-app 5); square grids stay two across', () => {
  const route = readFileSync(new URL('../src/screens/browse/Library.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /const phoneCard = library\.kind === 'music' \|\| library\.kind === 'audiobook' \? 112 : 88;/);
  assert.match(route, /cardWidth: \(width < 600 \? phoneCard : 170\)/);
  // The grid's 20px gap (BrowseResults gridGap).
  // Measured grid widths: 340 at a 390 viewport, 310 at 360 (the alphabet rail takes its column).
  assert.equal(gridColumns(340, 88, 20), 3);
  assert.equal(gridColumns(310, 88, 20), 3);
  assert.equal(gridColumns(350, 112, 20), 2);
});
