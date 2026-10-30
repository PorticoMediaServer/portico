import test from 'node:test';
import assert from 'node:assert/strict';
import {applyNativeTVKeyboardKey, nativeTVKeyboardRows} from '../src-next/screens/search/keyboard.ts';

test('the drawn six-column keyboard edits the actual field without changing its order', () => {
  assert.deepEqual(nativeTVKeyboardRows.map(row => row.join('')), ['abcdef', 'ghijkl', 'mnopqr', 'stuvwx', 'yz1234', '567890']);
  let value = '';
  for (const key of ['a', 'space', '9']) value = applyNativeTVKeyboardKey(value, key);
  assert.equal(value, 'a 9'); assert.equal(applyNativeTVKeyboardKey(value, 'delete'), 'a ');
  assert.equal(applyNativeTVKeyboardKey('title😀', 'delete'), 'title');
  assert.equal(applyNativeTVKeyboardKey(value, 'clear'), '');
  assert.throws(() => applyNativeTVKeyboardKey(value, 'not-a-key'));
});
