import assert from 'node:assert/strict';
import test from 'node:test';
import {defaultI18n} from '../../../packages/i18n/src';
import {validateContentEntry} from '../../../packages/client-core/src/library-content';
import {homeCardText, homeSubject} from '../src-next/screens/home/model';
const env = {t: defaultI18n.t, locale: 'en-US'};

test('Home uses the actual supplied display title and keeps the raw title and edition untouched', () => {
  const entry = validateContentEntry({id: 'book-1', libraryId: 'books-1', kind: 'book', title: 'Project Hail Mary (Unabridged)', displayTitle: 'Project Hail Mary', edition: 'Unabridged'});
  assert.equal(homeSubject(entry).title, 'Project Hail Mary');
  assert.equal(homeCardText(entry, false, env).title, 'Project Hail Mary');
  assert.equal(entry.title, 'Project Hail Mary (Unabridged)');
  assert.equal(entry.edition, 'Unabridged');
});
test('Home keeps an unsupplied display title intact instead of parsing parentheses or edition text', () => {
  const entry = validateContentEntry({id: 'book-2', libraryId: 'books-1', kind: 'book', title: 'An actual (parenthesized) title', edition: 'Edition'});
  assert.equal(homeCardText(entry, false, env).title, 'An actual (parenthesized) title');
});
