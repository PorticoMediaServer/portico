import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

// J-29: at phone width the Discover tab's "Nothing in this library yet" sat at x=0, with the title
// running off the right edge. The toolbar, the rows and the browse tab's own empty state all start
// at the page's standard gutter (20px on a phone, the shell gutter above that).
test('J-29: a Discover empty state takes the page gutter, on every library kind', () => {
  assert.match(source('../src/screens/browse/Discovery.tsx'), /const emptyBlock = <div className=\{styles\.emptyState\}>\{empty\}<\/div>;/);
  assert.match(source('../src/screens/browse/Discovery.tsx'), /empty=\{snapshot\.appended\.length === 0 \? emptyBlock : undefined\}/,
    'the music mixes empty state takes the same block');
  assert.match(source('../src/screens/browse/recent-tiles.module.css'), /\.emptyState \{ padding: 20px var\(--shell-gutter\) 0; \}/);
});

test('J-29: a Reading library\'s empty Discover state takes the same gutter', () => {
  assert.match(source('../src/screens/read/ReadLibraryEntry.tsx'), /return <div className=\{styles\.emptyLibrary\}><StateView title=\{t\('library\.emptyLibrary'\)\}/);
  const css = source('../src/screens/read/entry.module.css');
  assert.match(css, /\.emptyLibrary \{ margin: 0 var\(--shell-gutter\); padding-top: 20px; \}/);
  assert.doesNotMatch(css, /\.emptyLibrary \{ margin-inline: 20px; \}/, 'the shell\'s gutter is the phone\'s and the rail\'s too, not a pinned 20px');
});
