/** A title's own end screen shows only for a real end, not while Up next hands over to the last episode. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {showsOwnEnd} from '../src-next/screens/watch/post-play-gate.ts';

const view = (currentEntryId: string | null, next: boolean) => ({
  queue: {currentEntryId},
  next: {entryId: next ? 'e4' : null, available: next, reason: next ? 'ready' : 'end'},
  items: [{entryId: 'e3', itemId: 'whack-job'}, {entryId: 'e4', itemId: 'rip'}],
}) as never;

test('the last episode ending shows its end screen', () => {
  assert.equal(showsOwnEnd('ended', 'rip', view('e4', false)), true);
});

test('a hand-over into the last episode is not an end: the queue names the next episode, the player still holds the one that ended', () => {
  assert.equal(showsOwnEnd('ended', 'whack-job', view('e4', false)), false);
});

test('an episode with another one queued after it does not show its own end screen', () => {
  assert.equal(showsOwnEnd('ended', 'whack-job', view('e3', true)), false);
});

test('a single title with no queue shows its end screen when it ends', () => {
  assert.equal(showsOwnEnd('ended', 'film', null), true);
  assert.equal(showsOwnEnd('ended', 'film', undefined), true);
});

test('nothing shows before the end', () => {
  assert.equal(showsOwnEnd('ready', 'rip', view('e4', false)), false);
});
