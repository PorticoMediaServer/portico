import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const css = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

// E-web-20 (390px phone web): a typed settings field takes the row's width under its label, as the
// phone forms draw it; the A–Z rail keeps its own gutter and never widens the page.
test('E-web-20: phone settings rows stack a typed field under its label', () => {
  const rows = css('../src/kit/shell/settings.module.css');
  assert.match(rows, /\.row:is\(\[data-stacked\], \[data-path\], :has\(> \.rowControl :is\(code, pre, \[role='radiogroup'\], input\[type='text'\].*\) > \* \{ flex-basis: 100%; margin-left: 0; \}/);
  const controls = css('../src/kit/settings/settings.module.css');
  assert.match(controls, /\.control > div:has\(> div > :is\(input\[type='text'\][^)]*\)\) \{ width: 100%; \}/);
});

test('E-web-20: the browse page clips the rail hit areas and gives the phone rail its own gutter', () => {
  const browse = css('../src/screens/browse/browse.module.css');
  assert.match(css('../src/kit/frames/browse.module.css'), /\.content \{[^}]*overflow-x: clip;/);
  assert.match(browse, /@media \(max-width: 599px\) \{ \.results:has\(> \.rail\) \{ margin-right: calc\(30px - var\(--shell-gutter\) - var\(--grid-overflow, 0px\)\); \} \.rail \{ right: calc\(-26px \+ var\(--grid-overflow, 0px\)\); \} \}/);
  assert.match(css('../src/screens/browse/BrowseResults.tsx'), /<AlphabetRail [^\n]*platform=\{phone \? 'phone' : 'desktop'\}/);
});
