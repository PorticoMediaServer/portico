import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {queueEntrySubject} from '../src/platform/playback/subject.ts';

test('P2-74 E-web-14: a queue "track" entry names its music dock; video waits for its own facts', () => {
  assert.deepEqual(queueEntrySubject({itemId: 'i1', kind: 'track', title: 'Long tone', album: 'Long', artist: 'Smoke'}),
    {kind: 'music', item: {id: 'i1', kind: 'song', title: 'Long tone', album: 'Long', artist: 'Smoke'}});
  assert.equal(queueEntrySubject({itemId: 'i1', kind: 'audiobook_file', title: 'Chapter 1'})?.kind, 'audiobook');
  assert.equal(queueEntrySubject({itemId: 'i1', kind: 'movie', title: 'Film'}), undefined);
  assert.equal(queueEntrySubject({itemId: 'i1', kind: 'track', title: ''}), undefined, 'no invented title');
});

test('P2-74 E-web-16: a library whose directory did not answer is the library retry state, never "not here"', () => {
  const route = readFileSync(new URL('../src/screens/browse/Library.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /facts\?\.identity\.librariesUnavailable \? <StateView variant="page" title=\{t\('next\.states\.pageFailed'\)\}/);
  assert.match(route, /retryLibraries\?\.\(\)/);
  assert.match(route, /: <StateView variant="page" title=\{t\('next\.states\.notFound'\)\}/, 'a real missing library is laid out as a page state too');
});
