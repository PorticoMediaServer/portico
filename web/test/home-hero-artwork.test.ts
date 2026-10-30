import test from 'node:test';
import assert from 'node:assert/strict';
import {heroArtworkTreatment} from '../src/screens/home/model.ts';

test('a title with a backdrop uses the drawn backdrop hero', () => {
  assert.equal(heroArtworkTreatment({backdropUrl: 'backdrop.jpg'}, true), 'backdrop');
  assert.equal(heroArtworkTreatment({backdropUrl: 'backdrop.jpg', posterUrl: 'poster.jpg'}, true), 'backdrop');
});

test('the extras panel crops the backdrop instead of drawing the full hero', () => {
  assert.equal(heroArtworkTreatment({backdropUrl: 'backdrop.jpg', posterUrl: 'poster.jpg'}, true, 'panel'), 'panel');
});

test('a poster-only title keeps the drawn poster treatment, never a stretched backdrop', () => {
  assert.equal(heroArtworkTreatment({posterUrl: 'poster.jpg'}, true), 'poster');
  assert.equal(heroArtworkTreatment({posterUrl: 'poster.jpg'}, true, 'panel'), 'poster');
});

test('a title with no artwork uses the generated title letter', () => {
  assert.equal(heroArtworkTreatment({}, true), 'generated');
  assert.equal(heroArtworkTreatment({backdropUrl: '', posterUrl: ''}, true), 'generated');
});

test('the colour preference withdraws every image before the artwork facts are read', () => {
  for (const artwork of [{}, {posterUrl: 'poster.jpg'}, {backdropUrl: 'backdrop.jpg'}, {backdropUrl: 'backdrop.jpg', posterUrl: 'poster.jpg'}])
    for (const variant of ['hero', 'panel'] as const) assert.equal(heroArtworkTreatment(artwork, false, variant), 'colour');
});
