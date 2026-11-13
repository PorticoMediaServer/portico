import test from 'node:test';
import assert from 'node:assert/strict';
import {PlayOnController, type PlayOnPorticoRemote, type PlayOnReceiversPort} from '../src/platform/playback/play-on.ts';

/** The dock's bar for a Portico device this browser moved playback to or controls (rules 27, 28):
 * it is drawn from the receivers port, and its buttons go to that port, never to the Cast sender. */
function fixture() {
  const castCalls: string[] = [], portCalls: string[] = [], played: unknown[] = [];
  let remote: PlayOnPorticoRemote | undefined;
  const listeners = new Set<() => void>();
  const cast = {phase: 'off', available: false, audioTracks: [], textTracks: []} as never;
  const sender = {getSnapshot: () => cast, subscribe: () => () => {}, prepareOnIdle: () => () => {}, prepareForPlayer: async () => {},
    start: async () => false, cancelConnecting: () => castCalls.push('cancel'), play: () => castCalls.push('play'), pause: () => castCalls.push('pause'),
    toggle: () => castCalls.push('toggle'), seek: () => castCalls.push('seek'), setAudioTrack: () => {}, setSubtitleTrack: () => {}, answerTakeover: () => {},
    stop: () => castCalls.push('stop'), dismiss: () => castCalls.push('dismiss'), dispose: () => {}} as never;
  const runtime = {getSnapshot: () => ({disposed: false}), subscribe: () => () => {}, stop: async () => {}, play: async (...args: unknown[]) => {played.push(args);},
    bindRemoteRetention: () => () => {}} as never;
  const receivers: PlayOnReceiversPort = {
    getSnapshot: () => ({phase: 'ready', items: [], ...(remote ? {remote} : {})}), subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    refresh: () => {}, send: async () => false, control: async () => true,
    toggle: () => {portCalls.push('toggle');}, seek: seconds => {portCalls.push('seek ' + seconds);}, stop: () => {portCalls.push('stop');},
    next: () => {portCalls.push('next');}, handBack: async () => {portCalls.push('hand back'); remote = undefined; for (const l of [...listeners]) l();}, audio: id => {portCalls.push('audio ' + id);}, subtitles: id => {portCalls.push('subtitles ' + id);}, dismiss: () => {portCalls.push('dismiss'); remote = undefined; for (const l of [...listeners]) l();},
  };
  let timer: (() => void) | undefined;
  const controller = new PlayOnController({sender, runtime, receivers, isCurrent: () => true, reportError: error => {throw error;},
    setTimer: (callback: () => void) => {timer = callback; return 1;}, clearTimer: () => {timer = undefined;}});
  const set = (value: PlayOnPorticoRemote | undefined) => {remote = value; for (const l of [...listeners]) l();};
  return {controller, set, castCalls, portCalls, played, fireTimer: () => timer?.()};
}
const tv: PlayOnPorticoRemote = {sessionId: 'pbs_1', deviceName: 'Living room TV', itemId: 'itm_1', title: 'Walk Film', positionKnown: true, positionSeconds: 90, durationSeconds: 180, paused: false, stopped: false};

test('the bar shows "Playing on" the Portico device, and its buttons control that device', () => {
  const f = fixture();
  f.set(tv);
  const remote = f.controller.getSnapshot().remote!;
  assert.deepEqual({kind: remote.kind, phase: remote.phase, deviceName: remote.deviceName, title: remote.title, position: remote.positionSeconds}, {kind: 'portico', phase: 'playing', deviceName: 'Living room TV', title: 'Walk Film', position: 90});
  f.controller.toggle(remote); f.controller.seek(remote, 500); f.controller.openRemote(remote);
  assert.deepEqual(f.portCalls, ['toggle', 'seek 180'], 'a seek past the end lands at the end');
  assert.equal(f.controller.getSnapshot().panel, 'remote', 'the phone-player 7b remote opens for a Portico device too');
  f.controller.closePanel();
  f.controller.stop(f.controller.getSnapshot().remote!);
  assert.deepEqual(f.portCalls.at(-1), 'stop');
  assert.deepEqual(f.castCalls, [], 'the Cast sender is never asked');
});

test('once the device stopped, Continue here plays it here at its place and the bar goes', async () => {
  const f = fixture();
  f.set({...tv, stopped: true, paused: true, positionSeconds: 120});
  const remote = f.controller.getSnapshot().remote!;
  assert.equal(remote.phase, 'stopped');
  await f.controller.continueHere(remote);
  assert.deepEqual(f.played, [['itm_1', 120, undefined]]);
  assert.equal(f.portCalls.at(-1), 'dismiss');
  assert.equal(f.controller.getSnapshot().remote, undefined);
});

test('a stopped bar goes by itself after a while; an old button cannot act on a newer bar', () => {
  const f = fixture();
  f.set(tv);
  const old = f.controller.getSnapshot().remote!;
  f.set({...tv, positionSeconds: 95});
  f.controller.toggle(old);
  assert.deepEqual(f.portCalls, [], 'a button drawn for an older read does nothing');
  f.set({...tv, stopped: true});
  f.fireTimer();
  assert.equal(f.portCalls.at(-1), 'dismiss');
  assert.equal(f.controller.getSnapshot().remote, undefined);
});

test('rule 27: a Portico device\'s bar has Next while its queue has one, and its own audio and subtitles', () => {
  const f = fixture();
  f.set({...tv, next: {title: 'Next Film'}, tracks: {audio: [{id: 'a1', name: 'English'}, {id: 'a2', name: 'French'}], subtitles: [{id: 's1', name: 'English'}], audioId: 'a1', subtitleId: null}});
  const remote = f.controller.getSnapshot().remote!;
  assert.deepEqual([remote.next?.title, remote.tracks?.audio.length], ['Next Film', 2]);
  f.controller.next(remote); f.controller.audio(remote, 'a2'); f.controller.subtitles(remote, 's1'); f.controller.subtitles(remote, null);
  f.controller.audio(remote, 'a9'); f.controller.audio(remote, 3);
  assert.deepEqual(f.portCalls, ['next', 'audio a2', 'subtitles s1', 'subtitles null'], 'only its own tracks, by their ids; nothing else is sent');
  f.controller.openTracks(remote);
  assert.equal(f.controller.getSnapshot().panel, 'tracks', 'Subtitles & audio opens its tracks');
  f.set({...tv});
  const plain = f.controller.getSnapshot().remote!;
  f.controller.next(plain); f.controller.closePanel(); f.controller.openTracks(plain);
  assert.equal(f.portCalls.at(-1), 'subtitles null', 'no Next without a next title');
  assert.equal(f.controller.getSnapshot().panel, null, 'no tracks panel without tracks');
  assert.deepEqual(f.castCalls, []);
});

test('rule 27: Play here while the device still plays starts it here from the device\'s place, and only then stops it there', async () => {
  const f = fixture();
  f.set({...tv, positionSeconds: 754});
  await f.controller.playHere(f.controller.getSnapshot().remote!);
  assert.deepEqual(f.played, [['itm_1', 754]], 'here, from where the device was');
  assert.deepEqual(f.portCalls, ['hand back'], 'then the device is stopped and let go');
  assert.equal(f.controller.getSnapshot().remote, undefined, 'the bar goes');
  const g = fixture();
  g.set({...tv, positionKnown: false});
  await g.controller.playHere(g.controller.getSnapshot().remote!);
  assert.deepEqual([g.played, g.portCalls], [[], []], 'nothing without a known place');
});

