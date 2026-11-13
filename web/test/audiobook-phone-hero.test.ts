import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const css = readFileSync(new URL('../src/screens/detail/audiobooks/audiobooks.module.css', import.meta.url), 'utf8');

// FX-P1: phone-app 13 (the phone album page) draws 20px between the square cover and the title.
// The audiobook phone hero put the copy straight under the cover, so a standalone book's title
// touched the artwork. The gap is on the copy, so a book with a series line and one without
// start in the same place; the eyebrow no longer adds a top margin of its own.
test('FX-P1: a phone audiobook page keeps 20px between the cover and the title', () => {
  assert.match(css, /\.page\[data-layout='phone'\] \.copy \{ margin-top:20px; \}/);
  assert.match(css, /\.eyebrow \{ display:block; margin:0 auto 4px;/);
  assert.match(css, /\.eyebrowLine \{ margin:0 0 4px; \}/);
  assert.doesNotMatch(css, /\.eyebrow \{ display:block; margin:18px auto 4px;/, 'the eyebrow no longer doubles the gap');
});

test('FX-P1: the phone gap leaves the desktop hero untouched', () => {
  assert.doesNotMatch(css, /^\.copy \{/m, 'no unscoped copy margin that would move the desktop hero');
});
