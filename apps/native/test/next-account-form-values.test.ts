import test from 'node:test';
import assert from 'node:assert/strict';
import {addHiddenLabel, completeTVLinkCode, formatTVLinkCode} from '../src-next/screens/accounts/account-form-values.ts';

test('TV link codes normalize typing and pasted code, and require exactly eight characters', () => {
  assert.equal(formatTVLinkCode('ab cd-12_34'), 'ABCD-1234');
  assert.equal(formatTVLinkCode('abcd'), 'ABCD');
  assert.equal(formatTVLinkCode('abcde'), 'ABCD-E');
  assert.equal(formatTVLinkCode('ab😀cd-é1234ignored'), 'ABCD-1234');
  for (const value of ['', 'ABCD', 'ABCD-EFG', 'abcd-efgh', 'ABCD-EFGHI', 'ABCD EFGH']) assert.equal(completeTVLinkCode(value), false, value);
  assert.equal(completeTVLinkCode(formatTVLinkCode('abcd-efgh')), true);
});

test('hidden labels preserve the original draft and enforce server shape limits', () => {
  const labels = Object.freeze(['Horror']);
  assert.deepEqual(addHiddenLabel(labels, ' Reality '), ['Horror', 'Reality']);
  assert.deepEqual(labels, ['Horror']);
  assert.equal(addHiddenLabel(labels, 'hORRoR'), undefined);
  assert.equal(addHiddenLabel(labels, '  '), undefined);
  assert.equal(addHiddenLabel(labels, 'bad\nlabel'), undefined);
  assert.equal(addHiddenLabel(labels, 'x'.repeat(121)), undefined);
  assert.equal(addHiddenLabel(Array.from({length: 64}, (_, i) => `label-${i}`), 'new'), undefined);
  assert.equal(addHiddenLabel(labels, 'x'.repeat(120))?.length, 2);
});
