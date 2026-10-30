import test from 'node:test';
import assert from 'node:assert/strict';
import {captionAppearance, captionStyle, type CaptionAppearance} from '../src/kit/media/caption-style.ts';

const sizes: readonly CaptionAppearance['size'][] = ['small', 'medium', 'large', 'extra-large'];
const colors: readonly CaptionAppearance['color'][] = ['white', 'yellow', 'cyan', 'green', 'pink'];
const backgrounds: readonly CaptionAppearance['background'][] = ['none', 'outline', 'shadow', 'translucent', 'opaque'];
const fonts: readonly CaptionAppearance['font'][] = ['default', 'serif', 'mono', 'rounded'];

test('captionStyle is exactly the delegated captionAppearance paint for every preference combination', () => {
  for (const size of sizes) for (const color of colors) for (const background of backgrounds) for (const font of fonts) {
    const preferences: CaptionAppearance = {size, color, background, font};
    const paint = captionAppearance(preferences);
    assert.equal(captionStyle('k', preferences),
      `[data-portico-caption-style="k"]::cue {font-size:${paint.fontSize};color:${paint.color};background-color:${paint.backgroundColor};font-family:${paint.fontFamily};text-shadow:${paint.textShadow};}`,
      JSON.stringify(preferences));
  }
});

test('the paint carries only the five cue properties and the drawn palette values', () => {
  assert.deepEqual(captionAppearance({size: 'medium', color: 'white', background: 'translucent', font: 'default'}),
    {fontSize: '100%', color: '#fff', backgroundColor: 'rgba(0,0,0,0.65)', textShadow: 'none', fontFamily: 'sans-serif'});
  assert.equal(captionAppearance({size: 'extra-large', color: 'pink', background: 'outline', font: 'rounded'}).fontSize, '150%');
  assert.equal(captionAppearance({size: 'small', color: 'yellow', background: 'shadow', font: 'serif'}).textShadow, '0 2px 3px #000');
});
