import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';

// final3-mobileweb-02: on a phone, Search's All view lists a group's first results as rows (picture, title, facts, ⋯).
const {ResultRow} = await import('../src/kit/search/ResultRow.tsx');

test('a result row is its picture, its title and facts, and its ⋯', () => {
  const html = renderToStaticMarkup(createElement(ResultRow, {art: createElement('img', {alt: ''}), shape: 'wide', title: 'It’s Like a Game',
    facts: [createElement('span', {key: 'a'}, 'Solo Leveling'), createElement('span', {key: 'b'}, 'S1 E3')], onPress() {}, more: createElement('button', {'aria-label': 'More'})}));
  assert.match(html, /data-shape="wide"/);
  assert.match(html, /<b>It’s Like a Game<\/b><small><span>Solo Leveling<\/span><span>S1 E3<\/span><\/small>/);
  assert.match(html, /aria-label="More"/);
});

test('on a known phone width the All view lists up to four rows a group; people, artists and channels keep their shelves', () => {
  const row = readFileSync(new URL('../src/screens/search/AllResultsRow.tsx', import.meta.url), 'utf8');
  assert.match(row, /if \(viewport > 0 && viewport < 600 && !\['people', 'artists', 'live-tv'\]\.includes\(result\.id\)\) \{/);
  assert.match(row, /const shown = entries\.slice\(0, Math\.min\(cap, 4\)\);/);
  assert.match(row, /renderEntry\(entry, index, true, true\)/);
  const search = readFileSync(new URL('../src/screens/search/Search.tsx', import.meta.url), 'utf8');
  assert.match(search, /if \(row && entry\.kind !== 'song' && entry\.kind !== 'artist'\) \{/);
});
