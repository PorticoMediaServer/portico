import test from 'node:test';
import assert from 'node:assert/strict';
import {newlyRecorded} from '../src/screens/search/recorded.ts';

const answer = (generation: number, recorded: boolean, phase = 'ready') => ({phase, generation, data: {query: {recorded}}});

test('Recent searches reads again once for each answer the server recorded, and never for others (SR-8)', () => {
  assert.equal(newlyRecorded(undefined, answer(3, true)), 3, 'Enter on "dune": the recorded answer');
  assert.equal(newlyRecorded(3, answer(3, true)), undefined, 'the same answer re-rendered reads nothing more');
  assert.equal(newlyRecorded(3, answer(4, false)), undefined, 'typeahead and continuations are never recorded');
  assert.equal(newlyRecorded(3, answer(5, true, 'loading')), undefined, 'only an arrived answer counts');
  assert.equal(newlyRecorded(3, {phase: 'ready', generation: 6, data: null}), undefined);
  // The server declined to record (history turned off on another device): no read on the client's say-so.
  assert.equal(newlyRecorded(3, answer(7, false)), undefined);
  assert.equal(newlyRecorded(3, answer(8, true)), 8);
});
