import assert from 'node:assert/strict';
import test from 'node:test';
import {clampToLength, offersScrubber, playedFraction, seekTarget, END_MARGIN_MS} from '../src/server-admin/now-playing.ts';
import {parseSessionControls, type SessionControls} from '../src/playback-controls.ts';
import {dependsOn} from '../src/freshness-hub.ts';

test('a session with a length has a position bar and Forward stops short of the end', () => {
  assert.equal(parseSessionControls({sessionId: 's', revision: '3', state: 'playing', positionMs: 10, durationMs: 600000, actions: ['seek']}).durationMs, 600000);
  assert.equal(parseSessionControls({sessionId: 's', revision: '3', state: 'playing', positionMs: 10, actions: ['seek']}).durationMs, undefined);
  assert.equal(seekTarget('forward', 590000, 600000), 600000 - END_MARGIN_MS);
  assert.equal(seekTarget('forward', 590000), 620000);
  assert.equal(clampToLength(-5, 1000), 0);
  assert.equal(playedFraction({positionMs: 300000, durationMs: 600000}), 0.5);
  assert.equal(playedFraction({positionMs: 300000}), undefined);
  assert.equal(offersScrubber({kind: 'vod', durationMs: 1}), true);
  assert.equal(offersScrubber({kind: 'channel', durationMs: 1}), false);
  assert.equal(offersScrubber({kind: 'vod', durationMs: 1}, ['pause']), false);
});

test('a list hint is a dependency on its own event', () => {
  assert.equal(dependsOn.list('apiKeys'), 'event:list.changed:apiKeys');
  assert.equal(dependsOn.list('apiKeys'), dependsOn.event('list.changed', 'apiKeys'));
});
