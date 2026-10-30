import test from 'node:test';
import assert from 'node:assert/strict';
import {readBookTitleStyle} from '../../src/kit/reader/book-title.ts';

test('the approved examples choose the standard, sparse and long title treatments', () => {
  assert.equal(readBookTitleStyle('Dune', true), 'standard');
  assert.equal(readBookTitleStyle('The Time Machine', true), 'sparse');
  assert.equal(readBookTitleStyle('Amazing Stories'), 'standard');
  assert.equal(readBookTitleStyle('Little Nemo in Slumberland', true), 'long');
  assert.equal(readBookTitleStyle('Frieren: Beyond Journey’s End'), 'long');
});

test('layout classification ignores surrounding whitespace and counts Unicode code points', () => {
  assert.equal(readBookTitleStyle('  Dune  ', true), 'standard');
  assert.equal(readBookTitleStyle('𠮷'.repeat(12), true), 'standard');
  assert.equal(readBookTitleStyle('𠮷'.repeat(23), true), 'long');
});
