import test from 'node:test';
import assert from 'node:assert/strict';
import {listeningSleepChoices, sleepChoiceFrom, sleepChoiceLabel, sleepChoiceValue} from '../src/screens/watch/listening/listening-owner.ts';

test('MU-6: a book offers the end of its chapter and of the book; music the end of the title', () => {
  assert.deepEqual(listeningSleepChoices('audiobook'), [0, 15, 30, 45, 60, 90, 'chapter', 'book']);
  assert.deepEqual(listeningSleepChoices('music'), [0, 15, 30, 45, 60, 90, 'item']);
  for (const choice of [0, 45, 'item', 'chapter', 'book'] as const) assert.equal(sleepChoiceFrom(sleepChoiceValue(choice)), choice);
  assert.equal(sleepChoiceLabel('item'), 'End of this title');
  assert.equal(sleepChoiceLabel('chapter'), 'End of this chapter');
  assert.equal(sleepChoiceLabel('book'), 'End of this book');
  assert.equal(sleepChoiceLabel(0), 'Off');
});
