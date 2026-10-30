import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';
import {playbackUserMessage} from '../src/playback-messages.ts';

const copy = 'This queue is too long. Remove some of it or start a new one.';

test('queue_too_large reads the same wherever queue errors are presented', () => {
  assert.equal(presentError({status: 422, code: 'queue_too_large'}, 'library').body, copy);
  assert.equal(playbackUserMessage({code: 'queue_too_large'}), copy);
});
