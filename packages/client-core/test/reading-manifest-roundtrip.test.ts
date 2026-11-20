import test from 'node:test';
import assert from 'node:assert/strict';
import {parseReadingManifest} from '../src/reading.ts';
import {rebuiltBookFacts} from '../src/reading-lists-rebuilt.ts';

// The app builds a book's manifest from the rebuilt server's answers and hands it to the Reader, which parses it again
// with the strict contract. A manifest the app built must survive that parse, or no book opens on a phone.
const id = 'A'.repeat(43);
const manifest = (publicationType: unknown) => ({sessionId: id, itemId: id, sourceId: id, revision: 'r1', format: 'epub', size: 1000,
  metadata: {wordCount: 1000, estimatedSeconds: 240, title: 'Pride and Prejudice',
    ...rebuiltBookFacts({libraryId: 'books', summary: 'A novel.', credits: [{role: 'author', name: 'Jane Austen', personId: 'p1'}], ...(publicationType === undefined ? {} : {publicationType})})},
  resources: [{path: 'a.xhtml', mime: 'application/xhtml+xml', size: 10}], spine: ['a.xhtml'], canAnnotate: true});

test('a manifest the app built from the rebuilt server parses again in the Reader, its kind of publication kept without a source', () => {
  const built = manifest('book');
  const parsed = parseReadingManifest(JSON.parse(JSON.stringify(built)));
  assert.deepEqual(parsed.metadata.publicationType, {value: 'book'});
  assert.equal(parsed.metadata.title, 'Pride and Prejudice');
  assert.equal(parsed.metadata.description, undefined, 'the contract keeps only its own facts');
});

test('a kind with its source is kept whole; a malformed kind is left out, never failing the book', () => {
  const sourced = manifest(undefined);
  const withSource = parseReadingManifest({...sourced, metadata: {...sourced.metadata, publicationType: {value: 'magazine', source: 'file'}}});
  assert.deepEqual(withSource.metadata.publicationType, {value: 'magazine', source: 'file'});
  const malformed = parseReadingManifest({...sourced, metadata: {...sourced.metadata, publicationType: {value: 7}}});
  assert.equal(malformed.metadata.publicationType, undefined);
});
