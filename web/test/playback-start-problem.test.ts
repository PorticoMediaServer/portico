import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFailure} from '../../packages/client-core/src/presentation/playback-failure.ts';
import {markPresentedInPlayer, presentedInPlayer} from '../src/platform/playback/presented.ts';
import {startsInPlayer} from '../src/screens/watch/StartingPlayer.tsx';

test('a start refused before its session says what failed in plain words, with Try again', () => {
  for (const code of ['internal', 'playback_start_failed', 'playback_metadata_failed']) {
    const failure = playbackFailure(code);
    assert.equal(failure.body, 'web.player.error.notReady', code);
    assert.equal(failure.permanent, false, `${code} keeps Try again`);
  }
  assert.equal(playbackFailure('playback_version_unavailable').body, 'web.player.error.versionGone');
});

test('a failure the player shows is marked once so the toast stays quiet', () => {
  const error = new Error('internal');
  assert.equal(presentedInPlayer(error), false);
  assert.equal(markPresentedInPlayer(error), error);
  assert.equal(presentedInPlayer(error), true);
  assert.equal(presentedInPlayer(new Error('internal')), false);
  assert.equal(presentedInPlayer('internal'), false);
});

test('only video starts open the full player; music, audiobooks and channels keep the dock', () => {
  const item = {id: 'x', kind: 'movie', title: 'X'} as never;
  assert.equal(startsInPlayer({subject: {kind: 'video', item}}), true);
  assert.equal(startsInPlayer({subject: {kind: 'music', item}}), false);
  assert.equal(startsInPlayer({subject: {kind: 'audiobook', item}}), false);
  assert.equal(startsInPlayer({subject: {kind: 'channel', id: 'c', sourceId: 's', channelName: 'C'}}), false);
  assert.equal(startsInPlayer(undefined), false);
});
