import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {ReadingError} from '@portico/client-core';
import {readingServerBusy} from '../../src/screens/read/open-failure.ts';

// RD-8 (#693 review): web's failed page says the server is busy, as native does, and keeps Try again.
test('only a busy server takes the busy line on the reader\'s failed page; any other failure keeps the generic page', () => {
  assert.equal(readingServerBusy(new ReadingError('reading_sessions_busy', 503, 2)), true);
  for (const other of [new ReadingError('forbidden', 403), new ReadingError('reading_session_expired', 410), new Error('network'), null, undefined]) assert.equal(readingServerBusy(other), false);
  assert.equal(defaultI18n.t('error.busy'), 'Your server is busy right now. Try again in a moment.');
});
