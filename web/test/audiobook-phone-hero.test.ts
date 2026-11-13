import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const css = readFileSync(new URL('../src/screens/detail/audiobooks/audiobooks.module.css', import.meta.url), 'utf8');
const hero = readFileSync(new URL('../src/kit/title/cover-hero.module.css', import.meta.url), 'utf8');

// FX-P1: phone-app 13 (the phone album page) and phone-read 3 (a book) draw 20px between the cover
// and the title. The audiobook page now draws its hero with the kit's CoverHero, which keeps that gap
// on phones; the page draws no hero of its own.
test('FX-P1: a phone audiobook page keeps 20px between the cover and the title', () => {
  assert.match(hero, /@media \(max-width: 599px\) \{[^@]*\.hero \{ flex-direction: column; align-items: center; gap: 20px;/);
  assert.doesNotMatch(css, /^\.hero\b|^\.copy\b|^\.eyebrow\b/m, 'the audiobook page draws no hero of its own');
});
