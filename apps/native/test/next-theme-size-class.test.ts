import assert from 'node:assert/strict';
import test from 'node:test';
import {createTheme} from '../src-next/kit/theme.ts';
import {shellLayout} from '../src-next/shell/model.ts';

// N2-4: one size class for the whole app. A phone held sideways is the shell's phone, so the theme's too:
// the phone Settings sheet, Saved page, title page and Home, never the tablet compositions in a 390 pt-high window.
const cases = [[390, 844, 'phone'], [844, 390, 'phone'], [932, 430, 'phone'], [820, 1180, 'tablet'], [1180, 820, 'web'], [1024, 700, 'web'], [600, 960, 'tablet']] as const;
for (const [width, height, platform] of cases) test(`N2-4: a ${width}×${height} window is a ${platform} to the theme and the shell alike`, () => {
  assert.equal(createTheme({isTV: false, width, height}).platform, platform);
  const shell = shellLayout(width, height, false).kind;
  assert.equal(shell === 'desktop' ? 'web' : shell, platform);
});

test('N2-4: a surface drawn at a given width alone (a specimen) keeps the width rule, and TV is TV', () => {
  assert.equal(createTheme({isTV: false, width: 844}).platform, 'tablet');
  assert.equal(createTheme({isTV: true, width: 1920, height: 1080}).platform, 'tv');
});
