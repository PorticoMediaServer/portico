import test from 'node:test';
import assert from 'node:assert/strict';
import {fontReadiness} from './readiness.mjs';

test('used font family and weight must actually load; computed family alone is insufficient', () => {
  const screen = {strings: [{style: {fontFamily: 'Example, sans-serif', fontWeight: '700'}}], fontFaces: []};
  assert.equal(fontReadiness(screen, 'reference').length, 1);
  screen.fontFaces = [{family: 'Example', weight: '400 800', status: 'loaded'}];
  assert.deepEqual(fontReadiness(screen, 'reference'), []);
  screen.fontFaces[0].status = 'error';
  assert.equal(fontReadiness(screen, 'reference').length, 1);
});
test('CSS weight matching retains requested 900 while requiring its actual available face to load', () => {
  const screen = {strings: [{style: {fontFamily: 'Example', fontWeight: '900'}}], fontFaces: [{family: 'Example', weight: '800', status: 'loaded'}]};
  assert.deepEqual(fontReadiness(screen, 'reference'), []); assert.equal(screen.strings[0].style.fontWeight, '900');
  screen.fontFaces.push({family: 'Example', weight: '900', status: 'error'});
  assert.equal(fontReadiness(screen, 'reference').length, 1, 'an existing but failed exact face cannot be masked by another weight');
});
test('CSS matching searches 400..500 upward first and higher weights upward before lower weights', () => {
  const screen = {strings: [{style: {fontFamily: 'Example', fontWeight: '450'}}], fontFaces: [{family: 'Example', weight: '400', status: 'loaded'}, {family: 'Example', weight: '500', status: 'error'}]};
  assert.equal(fontReadiness(screen, 'reference').length, 1);
  screen.strings[0].style.fontWeight = '600'; screen.fontFaces = [{family: 'Example', weight: '500', status: 'loaded'}, {family: 'Example', weight: '700', status: 'error'}];
  assert.equal(fontReadiness(screen, 'reference').length, 1);
});
