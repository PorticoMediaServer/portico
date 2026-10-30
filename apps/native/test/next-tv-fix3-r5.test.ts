import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createDestinationEntryFocus} from '../src-next/kit/focus/destination-entry.ts';

const source = (path: string) => readFileSync(new URL(`../src-next/${path}`, import.meta.url), 'utf8');

test('E-tv-27: a failure after Try again is a new entry that focuses Try again again', () => {
  const focused: string[] = [];
  const request = createDestinationEntryFocus({focus: (address: {childId: string}) => {focused.push(address.childId); return true;}} as any);
  const address = {regionId: 'library.error', childId: 'retry'};
  assert.equal(request({visible: true, intent: 'enter', revision: 4}, address), true);
  assert.equal(request({visible: true, intent: 'enter', revision: 4}, address), false);
  const afterRetry = createDestinationEntryFocus({focus: (address: {childId: string}) => {focused.push(address.childId); return true;}} as any);
  assert.equal(afterRetry({visible: true, intent: 'enter', revision: 4}, address), true, 'a fresh request focuses Try again again');
  assert.deepEqual(focused, ['retry', 'retry']);
  const library = source('screens/library/LibraryScreen.tsx');
  assert.match(library, /retryPressed\.current = true; destination\.retry\(\)/);
  assert.match(library, /runtime\.error\(state\.error, retryPage, 'library'\)/);
  assert.match(library, /createDestinationEntryFocus\(registry\), \[registry, retryEpoch\]/);
});

test('E-tv-25: every anchor move is observable to the scroll reaction', () => {
  const anchor = source('kit/layout/FocusAnchor.ts');
  assert.match(anchor, /requests: requests\.value/);
  assert.match(anchor, /requests\.value \+= 1;/);
});

test('E-tv-26: related albums and artists open as a fresh page, scrolled to the top', () => {
  const music = source('screens/destinations/music/register.tsx');
  assert.match(music, /<TVAlbumScreen key=\{page\.entry\.id\}/);
  assert.match(music, /<TVArtistScreen key=\{page\.entry\.id\}/);
});

test('E-tv-28: the TV hero title is clamped to two lines', () => {
  assert.match(source('kit/layout/Hero.tsx'), /lines=\{phone \|\| tv \? 2 : undefined\}/);
});
