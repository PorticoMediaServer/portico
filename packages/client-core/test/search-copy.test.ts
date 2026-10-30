import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';

test('the approved phone Library overview hint is available through the actual catalogue', () => {
  assert.equal(defaultI18n.t('next.browse.native.libraryOrderHint'), 'Live TV has its own tab. Edit pins changes this order on all your devices.');
});

test('approved Search frame copy is present in the actual runtime catalogue', () => {
  for (const [key, drawn] of [
    ['next.search.browseByGenre', 'Browse by genre'],
    ['next.search.browseInstead', 'Browse instead'],
    ['next.search.preferToTalk', 'Prefer to talk?'],
    ['next.search.remoteMicrophoneHelp', "Hold your remote's microphone button and say what you want."],
  ] as const) assert.equal(defaultI18n.t(key), drawn);
});
