import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '@i18n';
import {serverTextLabel} from '@core/presentation/server-text.ts';

test('an audiobook or music library\'s Discover heads its continuation "Continue listening" (desktop-listen #books, phone-app music and audiobooks)', () => {
  // The server names the shelf with this code and a fallback; the client's words win.
  assert.equal(serverTextLabel(defaultI18n, {code: 'home.row.continueListening', fallback: 'Continue Listening'}), 'Continue listening');
});

test('Home keeps its row name in title case, as its frames draw it beside Continue Watching (motion, collections)', () => {
  assert.equal(defaultI18n.t('next.home.continueListening'), 'Continue Listening');
  assert.equal(defaultI18n.t('next.home.continueWatching'), 'Continue Watching');
});
