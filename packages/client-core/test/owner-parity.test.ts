import assert from 'node:assert/strict';
import test from 'node:test';
import {clampToLength, offersScrubber, playedFraction, seekTarget, sendSeekTo, END_MARGIN_MS} from '../src/server-admin/now-playing.ts';
import {parseSessionControls, type SessionControls} from '../src/playback-controls.ts';
import {groupPlaybackFailures, parsePlaybackFailureMemory} from '../src/playback-failure-memory.ts';
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

test('moving the position bar is sent again quietly when the session moved, to the same point', async () => {
  let revision = 1;
  const sent: number[] = [];
  const now = (): SessionControls => ({sessionId: 's', revision: String(revision), state: 'playing', positionMs: 1000, durationMs: 600000, actions: ['pause', 'resume', 'seek', 'stop']});
  const service = {
    readAdmin: async () => now(),
    controlAdmin: async (current: SessionControls, _action: string, positionMs?: number) => {
      if (current.revision !== String(revision)) throw Object.assign(new Error('revision_mismatch'), {code: 'revision_mismatch', status: 412});
      sent.push(positionMs!);
      return {...now(), positionMs: positionMs!};
    },
  };
  revision = 2;
  const out = await sendSeekTo({readAdmin: async () => ({...now(), revision: '1'}), controlAdmin: service.controlAdmin}, 's', 900000).catch(() => undefined);
  assert.equal(out, undefined, 'a stale record that cannot be read fresh is not forced');
  const done = await sendSeekTo(service, 's', 900000);
  assert.equal(done.positionMs, 600000 - END_MARGIN_MS);
  assert.deepEqual(sent, [600000 - END_MARGIN_MS]);
});

test('a remembered playback problem names its title, and a bad title never drops the row', () => {
  const memory = parsePlaybackFailureMemory({more: false, items: [
    {sourceId: 'a1', engine: 'native', route: 'original', code: 'decode_error', detail: '', reportedAtMs: 2, expiresAtMs: 9, itemId: 'm1', title: 'Arrival', kind: 'movie'},
    {sourceId: 'a1', engine: 'mpv', route: 'copy_remux', code: 'decode_error', detail: '', reportedAtMs: 1, expiresAtMs: 8},
    {sourceId: 'b2', engine: 'native', route: 'original', code: 'x', detail: '', reportedAtMs: 3, expiresAtMs: 9, itemId: 7, title: 'Bad'},
  ]});
  const groups = groupPlaybackFailures(memory.items);
  assert.deepEqual(groups.map(g => [g.sourceId, g.item?.title]), [['b2', undefined], ['a1', 'Arrival']]);
});

test('a list hint is a dependency on its own event', () => {
  assert.equal(dependsOn.list('apiKeys'), 'event:list.changed:apiKeys');
  assert.equal(dependsOn.list('apiKeys'), dependsOn.event('list.changed', 'apiKeys'));
});
