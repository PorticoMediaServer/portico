import test from 'node:test';
import assert from 'node:assert/strict';
import {presentError} from '../src/presentation/errors.ts';
import {playbackUserMessage} from '../src/playback-messages.ts';

// Online-only actions disabled by either library or global lookup policy answer
// 409 local_metadata_only. Every client says why, the server's reason, and offers no Try again.
test('local_metadata_only names the reason and offers nothing to retry', () => {
  for (const context of ['library', 'server-console', 'generic'] as const) {
    const p = presentError({status: 409, code: 'local_metadata_only', message: 'ignored server text'}, context, {operation: 'action'});
    assert.equal(p.body, 'Online metadata lookup is turned off for this library or server.');
    assert.equal(p.action, 'none');
    assert.equal(p.code, 'local_metadata_only');
  }
});

test('lyric and subtitle searches (the player\'s own messages) say the same', () => {
  assert.equal(playbackUserMessage({status: 409, code: 'local_metadata_only'}), 'Online metadata lookup is turned off for this library or server.');
  assert.equal(presentError({status: 409, code: 'local_metadata_only'}, 'playback').body, 'Online metadata lookup is turned off for this library or server.');
});
