import test from 'node:test';
import assert from 'node:assert/strict';
import {activeTextScale, createTheme, fontFamilies, nativeType, textScaleFor, tvTextScale} from '../src-next/kit/theme.ts';
import {shellType} from '../src-next/kit/shell/visual.ts';
import {typeScale} from '@portico/design';

// Ruling R46-TV-TEXT-SIZE: appearance.textSize Standard is 1.0 and Large is
// 1.2 on Android TV and tvOS; the OS accessibility scale stays independent.

test('the confirmed TV text size maps to one published scale', () => {
  assert.deepEqual(tvTextScale, {standard: 1, large: 1.2});
  assert.equal(textScaleFor('standard', true), 1);
  assert.equal(textScaleFor('large', true), 1.2);
  assert.equal(textScaleFor(undefined, true), 1);
  assert.equal(textScaleFor('large', false), 1, 'handhelds keep the OS scale, not the TV preference');
  assert.equal(activeTextScale(), 1, 'no provider has published a preference');
});

test('Large scales every semantic ten-foot role once, font size and line height together', () => {
  const standard = createTheme({isTV: true, width: 1920});
  const large = createTheme({isTV: true, width: 1920, textScale: textScaleFor('large', true)});
  // tv.html and tv-more.html draw every TV page heading at 44/800 with -.03em;
  // theme.ts applies that one drawn correction to the published ten-foot title
  // (44/700 at -.027). It is drawn geometry, so it scales once with the rest.
  const drawnTitle = (scale: number) => ({...nativeType(typeScale.tenFoot.title, scale),
    fontFamily: fontFamilies[800], fontWeight: '800', letterSpacing: -0.03 * 44 * scale});
  for (const [role, base] of Object.entries(typeScale.tenFoot)) {
    const scaled = large.type[role as keyof typeof large.type];
    const unscaled = standard.type[role as keyof typeof standard.type];
    assert.equal(scaled.fontSize, base.size * 1.2, `${role} size`);
    assert.equal(scaled.fontSize, unscaled.fontSize! * 1.2, `${role} size scales once`);
    assert.equal(scaled.letterSpacing, unscaled.letterSpacing! * 1.2, `${role} tracking scales once`);
    if (base.lineHeight !== 'normal') {
      assert.equal(scaled.lineHeight, base.lineHeight * 1.2, `${role} line height`);
      assert.equal(scaled.lineHeight, unscaled.lineHeight! * 1.2, `${role} line height scales once`);
    } else assert.equal('lineHeight' in scaled, false, `${role} keeps normal metrics`);
    assert.deepEqual(scaled, role === 'title' ? drawnTitle(1.2) : nativeType(base, 1.2), `${role} is scaled exactly once`);
    assert.deepEqual(unscaled, role === 'title' ? drawnTitle(1) : nativeType(base), `${role} standard geometry`);
  }
});

test('the preference never scales focus, spacing, card or window geometry', () => {
  const standard = createTheme({isTV: true, width: 1920});
  const large = createTheme({isTV: true, width: 1920, textScale: 1.2});
  assert.deepEqual(large.spec, standard.spec);
  assert.deepEqual(large.layout, standard.layout);
  assert.deepEqual(large.radii, standard.radii);
  assert.deepEqual(large.space, standard.space);
  assert.equal(large.gutter, standard.gutter);
  assert.equal(large.width, standard.width);
  assert.equal(large.platform, 'tv');
});

test('omitted shellType callers read the one published scale while explicit values win', () => {
  assert.equal(shellType(22, 700).fontSize, 22);
  assert.equal(shellType(22, 700, 'normal', 0, 1.2).fontSize, 26.4);
  assert.equal(shellType(22, 700, 'normal', 0, 1).fontSize, 22);
});
