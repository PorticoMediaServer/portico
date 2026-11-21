import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const css = readFileSync(new URL('../src/screens/detail/audiobooks/audiobooks.module.css', import.meta.url), 'utf8');

// FX-P1: phone-app 13 (the phone album page) and phone-read 3 (a book) draw the cover over the title. The audiobook page
// is drawn by the kit's title frame, which owns that hero; the page draws no hero of its own.
test('FX-P1: a phone audiobook page draws no hero of its own', () => {
  assert.doesNotMatch(css, /^\.hero\b|^\.copy\b|^\.eyebrow\b/m, 'the audiobook page draws no hero of its own');
});
