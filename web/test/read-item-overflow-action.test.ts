import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

// W-4: desktop-read 3c and phone-read 3 put Read, My List, Mark as read and More on one row. The book,
// comic and magazine page drew More in the hero's `under` slot, so it fell to a second line even when
// the row had room. It now rides the actions row through the hero's `overflow` slot.
test('W-4: a book, comic or magazine page keeps its More control on the actions row', () => {
  const entry = source('../src/screens/read/ReadItemEntry.tsx');
  assert.match(entry, /overflow=\{<><Button ref=\{editorAnchor\} variant="glass" icon="i-more"/);
  assert.doesNotMatch(entry, /\bunder=\{/, 'More is never handed to the hero\'s second-line slot');
  assert.match(source('../src/kit/reader/ReadBookHero.tsx'), /<div className=\{styles\.actions\}>\{actions\}\{overflow\}<\/div>/,
    'the overflow control renders inside the one actions row');
});

test('W-4: the overflow control takes the same round glass size as My List and Mark as read', () => {
  const entry = source('../src/screens/read/ReadItemEntry.tsx');
  assert.doesNotMatch(entry, /icon="i-more"[^>]*size="small"/, 'a small More sat shorter than the buttons beside it');
});
