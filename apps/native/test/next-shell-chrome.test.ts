import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeScreenChromeHidden, setNativeScreenChromeHidden, subscribeNativeScreenChrome} from '../src-next/shell/chrome.ts';

test('a pushed destination hides the shell chrome by owner and cannot strand it', () => {
  let changes = 0; const off = subscribeNativeScreenChrome(() => {changes++;});
  try {
    assert.equal(nativeScreenChromeHidden(), false);
    setNativeScreenChromeHidden('settings-page', true);
    setNativeScreenChromeHidden('settings-page', true);
    assert.equal(nativeScreenChromeHidden(), true);
    assert.equal(changes, 1, 'a repeated hide is not a change');
    setNativeScreenChromeHidden('other', true);
    setNativeScreenChromeHidden('settings-page', false);
    assert.equal(nativeScreenChromeHidden(), true, 'one owner leaving keeps another owner hidden');
    setNativeScreenChromeHidden('other', false);
    assert.equal(nativeScreenChromeHidden(), false);
    assert.equal(changes, 4);
  } finally {off(); setNativeScreenChromeHidden('settings-page', false); setNativeScreenChromeHidden('other', false);}
});
