import test from 'node:test';
import assert from 'node:assert/strict';
import {guideRowChannel} from '../src/guide/row-state.ts';

const channel = {id: 'quiet', guide: 'full' as const};
test('a channel whose complete window holds no programmes is the "No guide information" row (LT-7)', () => {
  assert.equal(guideRowChannel(channel, {programs: [], complete: true, failed: false}).guide, 'none');
});
test('a window still loading, a failed one, or a row with programmes is never labelled "no guide"', () => {
  assert.equal(guideRowChannel(channel, {programs: [], complete: false, failed: false}), channel);
  assert.equal(guideRowChannel(channel, {programs: [], complete: true, failed: true}), channel);
  assert.equal(guideRowChannel(channel, {programs: [{}], complete: true, failed: false}), channel, 'a hole inside a row stays "No information"');
});
