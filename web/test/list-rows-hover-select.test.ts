import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('list rows draw the select circle on hover, focus, while selecting or when selected; at rest a song shows its number', () => {
  const browse = read('../src/screens/browse/browse.module.css'), search = read('../src/screens/search/search.module.css');
  for (const [css, row] of [[browse, 'listRow'], [search, 'songTableRow']] as const) {
    assert.match(css, new RegExp(`\\.${row} \\[data-variant\\]\\[aria-pressed\\]:not\\(\\[data-selected='true'\\]\\):not\\(\\[data-selecting='true'\\]\\) \\{ opacity: 0; \\}`), row);
    assert.match(css, new RegExp(`\\.${row}:is\\(:hover, :focus-within\\) \\[data-variant\\]\\[aria-pressed\\]:not\\(\\[data-selected='true'\\]\\):not\\(\\[data-selecting='true'\\]\\) \\{ opacity: 1; \\}`), row);
  }
  const rows = read('../src/screens/browse/BrowseResults.tsx');
  assert.match(rows, /<span className=\{styles\.musicNumber\}>\{select\}<span className=\{styles\.trackNumber\}>\{index \+ 1\}<\/span><\/span>/);
  assert.match(read('../src/screens/search/Search.tsx'), /\{picked && <span className=\{styles\.songIndexNumber\}>/);
});

test('a title with no progress draws no empty progress bar; the music text headers read left', () => {
  assert.match(read('../src/screens/browse/BrowseResults.tsx'), /progress !== undefined && progress > 0 && <span className=\{styles\.miniProgress\}>/);
  assert.match(read('../src/screens/browse/browse.module.css'), /\.results\[data-music\] \.listHead > :nth-child\(-n\+4\):not\(:first-child\) \{ text-align: left; \}/);
});
