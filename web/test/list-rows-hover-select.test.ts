import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('the select circle sits over the art\'s top-left corner, never taking space; hover shows it with a pointer, selection mode always (select.html 1, 11)', () => {
  const browse = read('../src/screens/browse/browse.module.css'), search = read('../src/screens/search/search.module.css');
  assert.match(browse, /\.listTitle > \[data-variant\]\[aria-pressed\] \{ position: absolute; left: -8px; top: calc\(50% - var\(--list-art-half\) - 8px\); \}/);
  assert.match(browse, /^\.listRow \[data-variant\]\[aria-pressed\]:not\(\[data-selected='true'\], \[data-selecting='true'\]\) \{ display: none; \}/m);
  assert.match(browse, /@media \(hover: hover\) \{ \.listRow:is\(:hover, :focus-within\) \[data-variant\]\[aria-pressed\] \{ display: inline-grid; \} \}/);
  assert.match(search, /\.songTitle > \[aria-pressed\] \{ position: absolute;/);
  assert.match(search, /@media \(hover: hover\) \{ \.songTableRow:is\(:hover, :focus-within\) \.songTitle > \[aria-pressed\] \{ display: inline-grid; \} \}/);
  for (const css of [browse, search]) assert.doesNotMatch(css, /\[aria-pressed\][^{]*\{ opacity: 0; \}/);
  const rows = read('../src/screens/browse/BrowseResults.tsx');
  assert.match(rows, /<div className=\{styles\.listTitle\}>\{select\}/);
  assert.doesNotMatch(rows, /musicNumber\}>\{select\}/);
});

test('a song row swaps its number for ▶ on hover; a title row raises Play and ⋯ (desktop-listen #songs, desktop-browse #list)', () => {
  const browse = read('../src/screens/browse/browse.module.css'), rows = read('../src/screens/browse/BrowseResults.tsx');
  assert.match(rows, /<span className=\{styles\.musicNumber\}><span className=\{styles\.trackNumber\}>\{index \+ 1\}<\/span>\n\s+\{action\.onPlay && <button type="button" className=\{styles\.trackPlay\}/);
  assert.match(rows, /<span className=\{styles\.listActions\}>\{!music && action\.onPlay && !action\.selection\?\.active && <Button/);
  assert.match(browse, /\.listRow:is\(:hover, :focus-within\) \.musicNumber > \.trackPlay \{ display: grid; \}/);
  const songs = read('../src/screens/search/Search.tsx');
  assert.match(songs, /<span className=\{styles\.songIndex\}><button type="button" className=\{styles\.songIndexPlay\}/);
});

test('on touch, press and hold on a list row opens the title menu, whose Select starts selection (select.html 16, 17)', () => {
  const rows = read('../src/screens/browse/BrowseResults.tsx'), songs = read('../src/screens/search/Search.tsx');
  assert.match(rows, /const hold = useCardHold\(\{selection, disabled: retained, row: member\}\);/);
  assert.match(rows, /data-layout-primary \{\.\.\.hold\.handlers\} onBlur=\{\(\) => hold\.cancel\(\)\} onClick=\{event => \{if \(hold\.consumeClick\(\)\)/);
  // The library's wrapped actions route a long-press toggle to the title menu (use-card-menu), which has Select.
  assert.match(read('../src/screens/browse/use-card-menu.tsx'), /if \(intent\.source === 'long-press' && intent\.anchor && descriptor\.active === false\)/);
  assert.match(songs, /if \(intent\.source === 'long-press' && intent\.anchor && !picked\.active\) \{more\(intent\.anchor\); return;\}/);
  assert.match(songs, /className=\{styles\.songTitleCell\} \{\.\.\.hold\.handlers\}/);
  assert.match(songs, /\.\.\.\(picked \? \{select: \(\) => picked\.onToggle/);
});

test('a title with no progress draws no empty progress bar; the music text headers read left', () => {
  assert.match(read('../src/screens/browse/BrowseResults.tsx'), /progress !== undefined && progress > 0 && <span className=\{styles\.miniProgress\}>/);
  assert.match(read('../src/screens/browse/browse.module.css'), /\.results\[data-music\] \.listHead > :nth-child\(-n\+4\):not\(:first-child\) \{ text-align: left; \}/);
});
