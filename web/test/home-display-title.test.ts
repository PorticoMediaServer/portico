import test from 'node:test';
import assert from 'node:assert/strict';
import {homeCardSubject} from '../src/screens/home/model.ts';
test('Home and browse card subjects keep genuine display titles, editions and suffixes verbatim', () => {
  const title = 'The Hunger Games: Special Edition';
  assert.equal(homeCardSubject({id: 'book', kind: 'book', title}).title, title);
  assert.equal(homeCardSubject({id: 'book', kind: 'book', title: 'stored fallback', displayTitle: title}).title, title);
  assert.equal(homeCardSubject({id: 'book', kind: 'book', title: 'Project Hail Mary (Unabridged)'}).title, 'Project Hail Mary (Unabridged)');
});
