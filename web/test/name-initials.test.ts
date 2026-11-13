import test from 'node:test';
import assert from 'node:assert/strict';
import {nameInitials} from '../src/kit/artwork/CircleArt.tsx';

// One person's circle initials everywhere (desktop-read 3: "H. G. Wells" is HW).
test('initials are the first letters of the first and last names', () => {
  assert.equal(nameInitials('H. G. Wells'), 'HW');
  assert.equal(nameInitials('Frank Herbert'), 'FH');
  assert.equal(nameInitials('  ursula k. le guin '), 'UG');
  assert.equal(nameInitials('Madonna'), 'M');
  assert.equal(nameInitials('"Weird Al" Yankovic'), 'WY');
  assert.equal(nameInitials('Émile Zola'), 'ÉZ');
  assert.equal(nameInitials(''), '');
});
