import test from 'node:test';
import assert from 'node:assert/strict';
import {colorDifference, deltaE2000, srgbToLab} from './color.mjs';

test('CIEDE2000 matches published neutral and hue-wrap verification pairs', () => {
  const pairs = [
    [[50, 2.6772, -79.7751], [50, 0, -82.7485], 2.0425],
    [[50, 3.1571, -77.2803], [50, 0, -82.7485], 2.8615],
    [[50, 0, 0], [50, -1, 2], 2.3669],
    [[50, 2.49, -.001], [50, -2.49, .0009], 7.1792],
    [[50, 2.49, -.001], [50, -2.49, .0011], 7.2195],
    [[50, -.001, 2.49], [50, .0011, -2.49], 4.7461],
  ];
  for (const [left, right, expected] of pairs) {
    assert.ok(Math.abs(deltaE2000(left, right) - expected) < .00005);
    assert.ok(Math.abs(deltaE2000(right, left) - expected) < .00005);
  }
});
test('CSS alpha and sRGB D65 conversion are measured separately from perceptual colour', () => {
  assert.equal(srgbToLab([0, 0, 0])[0], 0);
  assert.ok(Math.abs(srgbToLab([255, 255, 255])[0] - 100) < .0001);
  assert.deepEqual(colorDifference('rgb(46, 63, 73)', 'rgba(46, 63, 73, 0.5)'), {deltaE2000: 0, alpha: .5});
  assert.deepEqual(colorDifference('rgba(255, 0, 0, 0)', 'rgba(0, 0, 255, 0)'), {deltaE2000: 0, alpha: 0});
  assert.equal(colorDifference('not a colour', 'rgb(0, 0, 0)'), null);
});
