import test from 'node:test';
import assert from 'node:assert/strict';
import {REMOTE_SKIP_MS, positionClock, seekTarget, sessionFacts, transportOffers} from '../src/server-admin/now-playing.ts';
import {createI18n} from '../../i18n/src/index.ts';
import {parseSessionControls, type SessionControls} from '../src/playback-controls.ts';
import {presentError} from '../src/presentation/index.ts';

const controls = (patch: Partial<SessionControls> = {}): SessionControls => ({sessionId: 's1', revision: '4', state: 'playing', positionMs: 600_000, actions: ['pause', 'resume', 'seek', 'stop'], ...patch});

test('a menu offers Pause or Resume, and moving only where there is a timeline', () => {
  assert.deepEqual(transportOffers({kind: 'vod', state: 'playing'}), ['pause', 'back', 'forward', 'startOver']);
  assert.deepEqual(transportOffers({kind: 'audio', state: 'paused'}), ['resume', 'back', 'forward', 'startOver']);
  // Something still starting can be paused before its first frame; a channel has no position to move.
  assert.deepEqual(transportOffers({kind: 'vod', state: 'preparing'}), ['pause', 'back', 'forward', 'startOver']);
  assert.deepEqual(transportOffers({kind: 'channel', state: 'playing'}), ['pause']);
  assert.deepEqual(transportOffers({kind: 'live', state: 'paused'}), ['resume']);
  // Nothing the server left out is offered.
  assert.deepEqual(transportOffers({kind: 'vod', state: 'playing'}, ['pause', 'resume', 'stop']), ['pause']);
  assert.deepEqual(transportOffers({kind: 'vod', state: 'playing'}, []), []);
});

test('Back and Forward move thirty seconds from where playback is, never before the start', () => {
  assert.equal(REMOTE_SKIP_MS, 30_000);
  assert.equal(seekTarget('back', 600_000), 570_000);
  assert.equal(seekTarget('forward', 600_000.4), 630_000);
  assert.equal(seekTarget('back', 12_000), 0);
  assert.equal(seekTarget('startOver', 600_000), 0);
  assert.equal(seekTarget('pause', 600_000), undefined);
});

test('a controls document keeps only the actions this client knows, and a position reads as a clock', () => {
  const parsed = parseSessionControls({sessionId: 's', revision: '2', state: 'playing', positionMs: 5, actions: ['pause', 'teleport', 'seek']});
  assert.deepEqual(parsed.actions, ['pause', 'seek']);
  assert.equal(positionClock(0), '0:00');
  assert.equal(positionClock(125_000), '2:05');
  assert.equal(positionClock(3_723_000), '1:02:03');
});

test('a row says where playback is when there is a timeline to be somewhere on', () => {
  const {t} = createI18n();
  const session = {id: 's', user: 'Ada', profile: 'Ada', device: 'Living room TV', title: 'Film', kind: 'vod', state: 'paused', positionMs: 754_000, decision: {}, location: 'local'} as never;
  assert.equal(sessionFacts(session, t), 'Ada  on Living room TV  Paused  At 12:34  Home network');
  assert.equal(sessionFacts({...(session as object), kind: 'channel'} as never, t), 'Ada  on Living room TV  Paused  Home network');
  assert.ok(sessionFacts({...(session as object),user:'sam_k',profile:'Kids'} as never,t).startsWith('sam_k Kids  on Living room TV'));
});
