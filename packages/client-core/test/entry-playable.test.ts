import test from 'node:test';
import assert from 'node:assert/strict';
import {entryPlayable} from '../src/library-content.ts';

// W1 F3 and the lead's follow-up (16 Nov): list rows never carry `available` (only an item's own read does),
// and cards that asked for `available === true` (Search, rows on a title's page, episode cards) never offered Play.
test('a card offers Play when it names its playback and is not known to be unavailable', () => {
  assert.equal(entryPlayable({playback: {itemId: 'a'}}), true, 'a list row with no availability');
  assert.equal(entryPlayable({playback: {itemId: 'a'}, available: true}), true);
  assert.equal(entryPlayable({playback: {itemId: 'a'}, available: false}), false);
  assert.equal(entryPlayable({playback: {itemId: 'a'}, missing: true}), false);
  assert.equal(entryPlayable({}), false, 'nothing to play');
});
