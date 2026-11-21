import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const source = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

// W-4: desktop-read 3c and phone-read 3 put Read, My List, Mark as read and More on one row. The page is drawn by
// the title frame, whose one actions row ends with More: the frame draws the button and the page wraps it in its menu.
test('W-4: a book, comic or magazine page keeps its More control on the actions row', () => {
  const entry = source('../src/screens/read/ReadItemEntry.tsx');
  assert.match(entry, /\{icon: 'i-more', label: t\('next\.controls\.more'\), onPress: \(\) => \{\}, wrap:/);
  assert.match(entry, /<TitleFrame [^]*actions=\{actions\}/, 'the actions, More last, are the frame\'s one row');
  assert.doesNotMatch(entry, /\bunder=\{/, 'More is never handed to a second-line slot');
});

test('W-4: the overflow control takes the same round glass size as My List and Mark as read', () => {
  const entry = source('../src/screens/read/ReadItemEntry.tsx');
  assert.doesNotMatch(entry, /icon="i-more"[^>]*size="small"/, 'a small More sat shorter than the buttons beside it');
});
