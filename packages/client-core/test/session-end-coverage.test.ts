import test from 'node:test';
import assert from 'node:assert/strict';
import {sessionEndMessage} from '../src/session-end.ts';

test('terminated shows the administrator message within bounds', () => {
  assert.equal(sessionEndMessage({reason: 'terminated'}), 'The server owner stopped this playback.');
  assert.equal(sessionEndMessage({reason: 'terminated', message: '  Maintenance at 9  '}), 'Maintenance at 9');
  assert.equal(sessionEndMessage({reason: 'terminated', message: ''}), 'The server owner stopped this playback.');
  assert.equal(sessionEndMessage({reason: 'terminated', message: '   '}), 'The server owner stopped this playback.');
});

test('terminated strips control characters, collapses spaces and caps at 500', () => {
  assert.equal(sessionEndMessage({reason: 'terminated', message: 'a\u0000b\u001fc  d'}), 'a b c d');
  const long = 'x'.repeat(600);
  const out = sessionEndMessage({reason: 'terminated', message: long});
  assert.equal(out.length, 500);
  assert.equal(out, 'x'.repeat(500));
});

test('fixed text for the other reasons and unknown values', () => {
  assert.equal(sessionEndMessage({reason: 'transferred'}), 'Playback continued on another device.');
  assert.equal(sessionEndMessage({reason: 'lease_expired'}), 'The stream disconnected. Play again to continue.');
  assert.equal(sessionEndMessage({reason: 'replaced'}), 'This playback was replaced by a newer one.');
  assert.equal(sessionEndMessage({reason: 'restored'}), 'The server was restored from a backup. Play again to continue.');
  assert.equal(sessionEndMessage({reason: 'bogus'}), 'Playback was stopped on another device.');
  assert.equal(sessionEndMessage({reason: ''}), 'Playback was stopped on another device.');
});
