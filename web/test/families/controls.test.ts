import test from 'node:test';
import assert from 'node:assert/strict';
import {progressFraction, rangeValue, moveOption, matchingOption} from '../../src/kit/controls/model.ts';
import {controlsMessages} from '../../../packages/i18n/src/catalog/en-US/next/controls.ts';

test('resume progress treats missing/nonfinite presentation values as no progress', () => {
  for (const value of [NaN, Infinity, -Infinity]) assert.equal(progressFraction(value), 0);
});
test('resume progress never paints outside its button or loses valid fractions', () => {
  assert.equal(progressFraction(-1), 0);
  assert.equal(progressFraction(2), 1);
  for (const value of [0, .09, .67, 1]) assert.equal(progressFraction(value), value);
});
test('volume clamps and aligns values to the actual caller range', () => {
  assert.equal(rangeValue(-2, 0, 100, 1), 0);
  assert.equal(rangeValue(120, 0, 100, 1), 100);
  assert.equal(rangeValue(57, 0, 100, 5), 55);
  assert.equal(rangeValue(8.4, 2, 20, 2), 8);
});
test('fractional volume steps stay precise and bounded', () => {
  assert.equal(rangeValue(.30000000000000004, 0, 1, .1), .3);
  assert.equal(rangeValue(.99, 0, 1, .3), .9);
  assert.equal(rangeValue(100, 0, 1, .3), .9);
});
test('volume handles nonfinite inputs and degenerate ranges without NaN', () => {
  assert.equal(rangeValue(NaN, 0, 100, 1), 0);
  assert.equal(rangeValue(10, 20, 2, 1), 20);
  assert.equal(rangeValue(40, NaN, Infinity, 0), 40);
  assert.equal(rangeValue(1, 0, 0, 1), 0);
});
test('owned messages stay in the registered flat controls namespace', () => {
  assert.ok(Object.keys(controlsMessages).length > 0);
  assert.ok(Object.keys(controlsMessages).every(key => key.startsWith('next.controls.')));
  assert.equal(controlsMessages['next.controls.default'], 'Default');
  assert.equal(controlsMessages['next.controls.myList'], 'My List');
  assert.ok(Object.values(controlsMessages).every(text => !text.includes('·')));
});
test('phone listbox navigation wraps while skipping every disabled choice', () => {
  const options = [{label: 'Original'}, {label: 'Automatic'}, {label: '4K', disabled: true}, {label: '1440p', disabled: true}, {label: '1080p'}];
  assert.equal(moveOption(options, 1, 1), 4);
  assert.equal(moveOption(options, 4, 1), 0);
  assert.equal(moveOption(options, 0, -1), 4);
  assert.equal(moveOption(options, -1, 1), 0);
  assert.equal(moveOption([], 0, 1), -1);
  assert.equal(moveOption([{label: '4K', disabled: true}], 0, 1), -1);
});
test('phone listbox typeahead accumulates prefixes and cycles repeated keys', () => {
  const options = [{label: 'Original'}, {label: 'Automatic'}, {label: 'Audio', disabled: true}, {label: 'After 3 episodes'}];
  assert.equal(matchingOption(options, 'a', 0), 1);
  assert.equal(matchingOption(options, 'aa', 1), 3);
  assert.equal(matchingOption(options, 'au', 1), 1);
  assert.equal(matchingOption(options, 'o', 3), 0);
  assert.equal(matchingOption(options, 'missing', 1), 1);
  assert.equal(matchingOption([], 'a', -1), -1);
});
