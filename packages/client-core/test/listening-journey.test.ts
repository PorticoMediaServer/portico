import test from 'node:test';
import assert from 'node:assert/strict';
import {parseListeningJourney} from '../src/listening.ts';

test('a way of playing this app does not know is not offered; the ones it knows still are', () => {
  const journey = parseListeningJourney({target: {libraryId: 'music', kind: 'album', id: 'a1'}, actions: ['play', 'radio', 'shuffle', 'play'], resumeUnavailable: false, future: 1}, 'music');
  assert.deepEqual(journey.actions, ['play', 'shuffle']);
  assert.throws(() => parseListeningJourney({target: {libraryId: 'other', kind: 'album', id: 'a1'}, actions: ['play'], resumeUnavailable: false}, 'music'));
  assert.throws(() => parseListeningJourney({target: {libraryId: 'music', kind: 'album', id: 'a1'}, actions: Array(17).fill('play'), resumeUnavailable: false}, 'music'));
});
