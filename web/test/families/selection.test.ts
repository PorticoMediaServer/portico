import test from 'node:test';
import assert from 'node:assert/strict';
import {circleIntent, holdProgress} from '../../src/kit/selection/interaction.ts';
import {selectionMessages} from '../../../packages/i18n/src/catalog/en-US/next/selection.ts';
import {parseMessage} from '../../../packages/i18n/src/messageformat.ts';

test('circle entry, subsequent toggle and Shift range remain distinct semantic intents', () => {
  assert.deepEqual(circleIntent(false, false), {intent: 'enter', shiftKey: false});
  assert.deepEqual(circleIntent(true, false), {intent: 'toggle', shiftKey: false});
  assert.deepEqual(circleIntent(false, true), {intent: 'range', shiftKey: true});
  assert.deepEqual(circleIntent(true, true), {intent: 'range', shiftKey: true});
});

test('hold feedback uses supplied progress without manufacturing unknown progress', () => {
  assert.equal(holdProgress(null), null);
  assert.equal(holdProgress(NaN), null);
  assert.equal(holdProgress(Infinity), null);
  assert.equal(holdProgress(-Infinity), null);
  assert.equal(holdProgress(-.5), 0);
  assert.equal(holdProgress(.31), .31);
  assert.equal(holdProgress(2), 1);
});

test('hold feedback copy is the exact catalogued frame copy', () => {
  assert.equal(selectionMessages['next.selection.keepHolding'], 'Keep holding to select');
});

test('all owned product messages have valid ICU syntax and keep fixture domain copy outside production', () => {
  for (const [key, message] of Object.entries(selectionMessages)) {
    assert.doesNotThrow(() => parseMessage(message), key);
    assert.doesNotMatch(message, /Toy Story|The Last of Us|The Weeknd|Family night|Portico Cloud|·/, key);
  }
});
