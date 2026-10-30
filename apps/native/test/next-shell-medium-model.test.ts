import test from 'node:test';
import assert from 'node:assert/strict';
import {mediumLibraryIcon, mediumRailMetrics, mediumRailPadding, shellLayout} from '../src-next/shell/model.ts';

test('shellLayout moves 600-1023 to the drawn medium rail and keeps phone and desktop unchanged', () => {
  const phone = shellLayout(390, 844, false);
  assert.deepEqual({kind: phone.kind, tabs: phone.tabs, rail: phone.rail, panel: phone.panel}, {kind: 'phone', tabs: true, rail: false, panel: 'bottom'});
  for (const [width, height] of [[600, 960], [840, 880], [890, 626], [1023, 768]] as const) {
    const medium = shellLayout(width, height, false);
    assert.deepEqual({kind: medium.kind, tabs: medium.tabs, rail: medium.rail, panel: medium.panel}, {kind: 'tablet', tabs: false, rail: true, panel: 'side'}, `${width}x${height}`);
  }
  const landscapePhone = shellLayout(914, 411, false);
  assert.deepEqual({kind: landscapePhone.kind, tabs: landscapePhone.tabs, rail: landscapePhone.rail, panel: landscapePhone.panel},
    {kind: 'phone', tabs: true, rail: false, panel: 'bottom'}, 'phone-guide 2 keeps the phone chrome in landscape');
  const desktop = shellLayout(1024, 700, false);
  assert.equal(shellLayout(1400, 500, false).kind, 'desktop', 'a short desktop window keeps the desktop shell');
  assert.deepEqual({kind: desktop.kind, tabs: desktop.tabs, rail: desktop.rail}, {kind: 'desktop', tabs: false, rail: false});
  assert.deepEqual({tabs: shellLayout(1920, 1080, true).tabs, rail: shellLayout(1920, 1080, true).rail}, {tabs: false, rail: false});
});

test('medium rail keeps the drawn 84 pt item geometry and clears real insets', () => {
  assert.equal(mediumRailMetrics.width, 84);
  assert.equal(mediumRailMetrics.itemWidth, 64);
  assert.equal(mediumRailMetrics.itemRadius, 14);
  assert.equal(mediumRailMetrics.iconSize, 22);
  assert.deepEqual(mediumRailPadding(0, 0), {top: 40, bottom: 16});
  assert.deepEqual(mediumRailPadding(47, 34), {top: 63, bottom: 42});
  assert.deepEqual(mediumRailPadding(-3, -3), {top: 40, bottom: 16});
});

test('medium rail takes the foldable glyph table without changing the TV rail icons', () => {
  assert.equal(mediumLibraryIcon('audiobook'), 'i-headphones');
  assert.equal(mediumLibraryIcon('comic'), 'i-panels');
  assert.equal(mediumLibraryIcon('book'), 'i-book');
  assert.equal(mediumLibraryIcon('movie'), 'i-film');
  assert.equal(mediumLibraryIcon('tv'), 'i-tvlib');
  assert.equal(mediumLibraryIcon('music'), 'i-music');
  assert.equal(mediumLibraryIcon('anime'), 'i-category');
});
