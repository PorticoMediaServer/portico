import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFailureReason} from '../src-next/platform/playback/playback-failure.ts';
import {failureBody, failureWhatHappened} from '../src-next/screens/watch/failure-words.ts';

test('N1-7: a failure is classified once from the facts the player has', () => {
  assert.equal(playbackFailureReason({networkError: true}), 'connection');
  assert.equal(playbackFailureReason({httpStatus: 503}), 'connection');
  assert.equal(playbackFailureReason({httpStatus: 410}), 'stream-ended');
  assert.equal(playbackFailureReason({deliveryError: true}), 'stream-ended');
  assert.equal(playbackFailureReason({engineCode: 'decode_error'}), 'unsupported');
  assert.equal(playbackFailureReason({hasPlayed: false}), 'unsupported');
  assert.equal(playbackFailureReason({hasPlayed: true}), undefined, 'unknown keeps the neutral sentence');
});

test('the failure body says what happened and where the place is saved, as states 10 and 11 draw it', () => {
  assert.equal(failureBody('connection', '12:04'), 'The connection to the server dropped. Your place is saved at 12:04.');
  assert.equal(failureBody('stream-ended', '18:20'), 'The server ended this stream. Your place is saved at 18:20.');
  assert.match(failureBody('unsupported', '0:10'), /can't play this title.*0:10/);
  assert.doesNotMatch(failureBody(undefined, '5:00'), /connection|server ended/);
  const generic = 'Playback could not continue. Please try again.';
  assert.notEqual(failureWhatHappened('connection', generic), failureWhatHappened('stream-ended', generic), 'Details tells the classes apart');
  assert.equal(failureWhatHappened(undefined, generic), generic);
});
