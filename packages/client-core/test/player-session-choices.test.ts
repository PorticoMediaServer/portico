import test from 'node:test';
import assert from 'node:assert/strict';
import {PlayerSessionChoices} from '../src/player-session-choices.ts';

test('a choice made in the player lasts its playback; a new playback reads the Settings again', () => {
  const choices = new PlayerSessionChoices();
  assert.equal(choices.get('forcedOnly', 1, false), false, 'nothing chosen: the Settings');
  assert.equal(choices.has('forcedOnly', 1), false);
  choices.set('forcedOnly', 1, true);
  assert.equal(choices.get('forcedOnly', 1, false), true, 'the same playback keeps it (the next episode too)');
  assert.equal(choices.has('forcedOnly', 1), true);
  assert.equal(choices.get('forcedOnly', 2, false), false, 'a fresh start goes back to the Settings');
  assert.equal(choices.get('forcedOnly', 2, true), true, 'whatever the Settings say');
  assert.equal(choices.get('raise', 1, 0), 0, 'each choice is its own');
  choices.set('raise', 2, 5);
  assert.equal(choices.get('raise', 1, 0), 0, 'a choice belongs to the playback it was made in');
  let told = 0;
  const stop = choices.subscribe(() => told++);
  const before = choices.getVersion();
  choices.set('raise', 2, 6);
  assert.equal(told, 1);
  assert.notEqual(choices.getVersion(), before);
  stop();
});
