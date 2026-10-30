import test from 'node:test';
import assert from 'node:assert/strict';
import {REMOTE_SKIP_MS, positionClock, seekTarget, sendTransport, sessionFacts, transportOffers} from '../src/server-admin/now-playing.ts';
import {createI18n} from '../../i18n/src/index.ts';
import {parseSessionControls, type SessionControls} from '../src/playback-controls.ts';
import {presentError} from '../src/presentation/index.ts';

const controls = (patch: Partial<SessionControls> = {}): SessionControls => ({sessionId: 's1', revision: '4', state: 'playing', positionMs: 600_000, actions: ['pause', 'resume', 'seek', 'stop'], ...patch});

/** A server that holds one session: a command must carry the current revision. */
function server(initial: SessionControls) {
  let now = initial;
  const sent: {action: string; revision: string; positionMs?: number}[] = [];
  return {
    sent,
    set(next: SessionControls) { now = next; },
    service: {
      readAdmin: async () => now,
      controlAdmin: async (current: SessionControls, action: 'pause' | 'resume' | 'seek', positionMs?: number) => {
        sent.push({action, revision: current.revision, ...(positionMs === undefined ? {} : {positionMs})});
        if (!now.actions.length) throw Object.assign(new Error('ended'), {status: 410, code: 'session_ended'});
        if (current.revision !== now.revision) throw Object.assign(new Error('moved'), {status: 412, code: 'revision_mismatch'});
        now = {...now, revision: String(Number(now.revision) + 1), state: action === 'pause' ? 'paused' : action === 'resume' ? 'playing' : now.state, positionMs: positionMs ?? now.positionMs};
        return now;
      },
    },
  };
}

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

test('the owner\'s command is sent against the session as it is now, and again when it moved in between', async () => {
  const s = server(controls());
  const paused = await sendTransport(s.service, 's1', 'pause');
  assert.equal(paused.state, 'paused');
  assert.deepEqual(s.sent, [{action: 'pause', revision: '4'}]);
  // The player reports between the read and the command: the revision moves, and so does the position a seek is measured from.
  s.sent.length = 0;
  const read = s.service.readAdmin;
  let first = true;
  s.service.readAdmin = async () => { const out = await read(); if (first) { first = false; s.set({...out, revision: '9', positionMs: 660_000}); } return out; };
  const moved = await sendTransport(s.service, 's1', 'forward');
  assert.deepEqual(s.sent, [{action: 'seek', revision: '5', positionMs: 630_000}, {action: 'seek', revision: '9', positionMs: 690_000}]);
  assert.equal(moved.positionMs, 690_000);
});

test('a command that is already true, or for playback that has ended, sends nothing more', async () => {
  const s = server(controls({state: 'paused'}));
  assert.equal((await sendTransport(s.service, 's1', 'pause')).state, 'paused');
  assert.equal(s.sent.length, 0, 'already paused');
  s.set(controls({actions: []}));
  await assert.rejects(sendTransport(s.service, 's1', 'resume'), {code: 'session_ended'});
  assert.equal(s.sent.length, 0);
  // It ends while the command is on its way.
  const racing = server(controls());
  const read = racing.service.readAdmin;
  let reads = 0;
  racing.service.readAdmin = async () => { const out = await read(); if (reads++ === 0) racing.set({...out, revision: '5'}); else racing.set({...out, actions: []}); return reads === 1 ? out : {...out, actions: []}; };
  await assert.rejects(sendTransport(racing.service, 's1', 'pause'), {code: 'session_ended'});
  assert.equal(presentError(Object.assign(new Error('x'), {code: 'session_ended', status: 410}), 'server-console', {operation: 'action'}).body, 'That playback has already ended.');
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
