import test from 'node:test';
import assert from 'node:assert/strict';
import {nameInitials} from '../src/presentation/kit-model/index.ts';

// One helper for web and the apps: an artist or person with no picture shows these letters, never an empty circle.
test('initials are the first letters of the first and last names, one letter for a single name', () => {
  assert.equal(nameInitials('H. G. Wells'), 'HW');
  assert.equal(nameInitials('Maximiliana Konstantinopoulou-Abernathy and the Northern Island Ensemble'), 'ME');
  assert.equal(nameInitials('  Björk '), 'B');
  assert.equal(nameInitials('"The Astronomers" of the Pale Blue Morning'), 'TM');
  assert.equal(nameInitials(''), '');
});
