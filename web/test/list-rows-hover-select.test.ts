import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the select circle exists only in selection mode and takes no space otherwise, on any device (lead ruling, #676)', () => {
  const browse = read('../src/screens/browse/browse.module.css'), search = read('../src/screens/search/search.module.css');
  assert.match(browse, /^\.listRow \[data-variant\]\[aria-pressed\]:not\(\[data-selected='true'\], \[data-selecting='true'\]\) \{ display: none; \}/m);
  assert.match(search, /^\.songIndex > \[aria-pressed\]:not\(\[data-selected='true'\], \[data-selecting='true'\]\) \{ display: none; \}/m);
  for (const css of [browse, search]) {
    assert.doesNotMatch(css, /\[aria-pressed\][^{]*\{ opacity: 0; \}/);
    assert.doesNotMatch(css, /@media \(hover: hover\) \{\n  \.(musicNumber|songIndexNumber)/);
  }
});

test('a song row swaps its number for ▶ on hover (desktop-listen #songs, desktop-search #s-songs); a title row raises Play and ⋯', () => {
  const browse = read('../src/screens/browse/browse.module.css'), rows = read('../src/screens/browse/BrowseResults.tsx');
  assert.match(rows, /<span className=\{styles\.musicNumber\}>\{select\}<span className=\{styles\.trackNumber\}>\{index \+ 1\}<\/span>\n\s+\{action\.onPlay && <button type="button" className=\{styles\.trackPlay\}/);
  assert.match(rows, /<span className=\{styles\.listActions\}>\{!music && action\.onPlay && !action\.selection\?\.active && <Button/);
  assert.match(browse, /\.listRow:is\(:hover, :focus-within\) \.musicNumber > \.trackNumber \{ display: none; \}/);
  assert.match(browse, /\.listRow:is\(:hover, :focus-within\) \.musicNumber > \.trackPlay \{ display: grid; \}/);
  const search = read('../src/screens/search/search.module.css'), songs = read('../src/screens/search/Search.tsx');
  assert.match(songs, /<button type="button" className=\{styles\.songIndexPlay\} onClick=\{activate\} aria-label=\{t\('next\.browse\.play', \{title\}\)\}><span>\{defaultI18n\.number\(position \+ 1\)\}<\/span><Icon name="i-play" size=\{16\}\/><\/button><\/span>/);
  assert.doesNotMatch(songs, /songIndexNumber/);
  assert.match(search, /\.songTableRow:is\(:hover, :focus-within\) \.songIndexPlay > svg \{ display: block; \}/);
});

test('a title with no progress draws no empty progress bar; the music text headers read left', () => {
  assert.match(read('../src/screens/browse/BrowseResults.tsx'), /progress !== undefined && progress > 0 && <span className=\{styles\.miniProgress\}>/);
  assert.match(read('../src/screens/browse/browse.module.css'), /\.results\[data-music\] \.listHead > :nth-child\(-n\+4\):not\(:first-child\) \{ text-align: left; \}/);
});
