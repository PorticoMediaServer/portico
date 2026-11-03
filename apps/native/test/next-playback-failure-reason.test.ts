import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFailureReason} from '../src-next/platform/playback/playback-failure.ts';
import {failureBody, failureTitle, failureWhatHappened} from '../src-next/screens/watch/failure-words.ts';

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

test('a channel a recording took reads as Live TV\'s handover on native, in the same words as every client (W2-A9)', () => {
  const tuner = {reason: 'tuner_reserved'};
  assert.equal(failureTitle(tuner), 'Live TV stopped');
  assert.equal(failureBody(undefined, '0:00', tuner), 'A recording needed this tuner. Watch this channel again when a tuner is free.');
  assert.equal(failureTitle(undefined), 'Playback stopped');
  assert.equal(failureTitle({reason: 'terminated'}), 'Playback stopped');
  assert.equal(failureBody('connection', '1:00', {reason: 'terminated', message: 'Maintenance at 9'}), 'Maintenance at 9', 'the server\'s own end wins over a transport guess');
});

test('the TV and handheld failed states and their capture carry the server\'s end', async () => {
  const {readFileSync} = await import('node:fs');
  const read = (name: string) => readFileSync(new URL(`../src-next/screens/watch/${name}`, import.meta.url), 'utf8');
  for (const name of ['PlaybackFailed.tsx', 'HandheldPlaybackFailed.tsx']) {
    assert.match(read(name), /failureTitle\(failure\.ended\)/, name);
    assert.match(read(name), /failureBody\(failure\.reason, .*, failure\.ended\)/, name);
  }
  assert.match(read('WatchHost.tsx'), /\.\.\.\(source\.playback\.ended \? \{ended: source\.playback\.ended\} : \{\}\)/);
});
