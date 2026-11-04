import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {publicationTooLarge} from '../../src/screens/read/open-failure.ts';

// RD-5 (#684): opening an EPUB can answer 422 `reading_publication_too_large` (retry never).
test('only the server\'s too-large refusal takes the plain message; any other open failure keeps the generic page', () => {
  assert.equal(publicationTooLarge(Object.assign(new Error('reading_publication_too_large'), {code: 'reading_publication_too_large', status: 422})), true);
  for (const other of [Object.assign(new Error('x'), {code: 'reading_sessions_busy', status: 503}), Object.assign(new Error('x'), {code: 'forbidden', status: 403}), new Error('network'), null, undefined])
    assert.equal(publicationTooLarge(other), false);
});
test('the refusal\'s words are in the en-US catalogue, in house style', () => {
  assert.equal(defaultI18n.t('next.reader.publicationTooLargeTitle'), 'This book is too large to open');
  assert.equal(defaultI18n.t('next.reader.publicationTooLargeBody'), 'The reader can’t open a book this size. It’s still in your library.');
});
