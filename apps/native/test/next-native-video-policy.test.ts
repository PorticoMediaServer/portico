import test from 'node:test';
import assert from 'node:assert/strict';
import {playbackFence, sameFence} from '../../../packages/player-core/src/identity.ts';
import type {PolicyCommand, PolicyFrame} from '../../../packages/player-core/src/playback-policy.ts';
import type {SegmentMarker} from '../../../packages/player-core/src/segment-markers.ts';
import {NativeVideoPlaybackPolicy, nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay} from '../src-next/platform/playback/video-policy.ts';

const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'episode', sourceId: 'version', entryId: 'entry', generation: 1});
const credits: SegmentMarker = {id: 'credits', kind: 'credits', startSeconds: 900, endSeconds: 1000, automaticSafe: true};
const intro: SegmentMarker = {id: 'intro', kind: 'intro', startSeconds: 10, endSeconds: 40, automaticSafe: true};
function fixture(change: Partial<PolicyFrame> = {}, markers: readonly SegmentMarker[] = [credits]) {
  let now = 0, serial = 0, live = true, notifications = 0;
  let frame: PolicyFrame = {fence, nowMs: 0, positionSeconds: 910, durationSeconds: 1000, rate: 1, playing: true, expanded: true, ended: false, kind: 'episode',
    modes: {intro: 'ask', recap: 'ask', credits: 'ask'}, postPlay: {nextEntryId: 'next', available: true, reason: 'ready', countdownSeconds: 5, autoplay: true, passoutCheckDue: false, automaticAdvances: 0, startDuringCredits: true}, ...change};
  const commands: PolicyCommand[] = [], errors: unknown[] = [], timers = new Map<number, {run: () => void; due: number}>();
  const policy = new NativeVideoPlaybackPolicy({read: () => live ? {frame, markers} : undefined,
    current: expected => live && sameFence(frame.fence, expected), execute: async command => {commands.push(command);},
    changed: () => {notifications++;}, failed: (_fence, error) => {errors.push(error);}, now: () => now,
    setTimer: (run, ms) => {const id = ++serial; timers.set(id, {run, due: now + ms}); return id;}, clearTimer: id => {timers.delete(id as number);}});
  const update = (change: Partial<PolicyFrame>) => {frame = {...frame, ...change}; policy.update();};
  const time = (ms: number) => {
    const end = now + ms;
    for (;;) {
      const next = [...timers].filter(([, timer]) => timer.due <= end).sort((a, b) => a[1].due - b[1].due)[0];
      if (!next) break;
      timers.delete(next[0]); now = next[1].due; next[1].run();
    }
    now = end;
  };
  policy.update();
  return {policy, commands, errors, timers, update, time, frame: () => frame, notifications: () => notifications,
    retire: () => {live = false; policy.update();}};
}

test('credits advance once on shared-policy expiry and hold through pause', () => {
  const d = fixture();
  try {
    d.time(2000); const before = d.policy.getSnapshot()!.prompt;
    assert.equal(before?.kind === 'up-next' && before.countdown?.seconds, 3);
    d.update({playing: false}); d.time(20_000);
    assert.equal(d.commands.length, 0); assert.equal(d.timers.size, 0);
    d.update({playing: true}); d.time(2000); assert.equal(d.commands.length, 0);
    d.time(1000); assert.deepEqual(d.commands.map(command => command.kind), ['advance']);
    assert.equal(d.commands[0]!.kind === 'advance' && d.commands[0]!.mode, 'automatic');
    d.time(20_000); d.update({positionSeconds: 920}); assert.equal(d.commands.length, 1);
  } finally {d.policy.dispose();}
});

test('credits defaults, post-credit scenes and Watch credits do not advance early', () => {
  for (const scenario of ['default', 'scene', 'dismiss'] as const) {
    const d = fixture({}, scenario === 'scene' ? [{...credits, endSeconds: 960}] : [credits]);
    try {
      if (scenario === 'default') d.update({postPlay: {...d.frame().postPlay!, startDuringCredits: false}});
      if (scenario === 'dismiss') assert.equal(d.policy.dismiss(fence), true);
      d.time(20_000); d.update({positionSeconds: 930}); assert.equal(d.commands.length, 0, scenario);
    } finally {d.policy.dispose();}
  }
});

test('automatic skips own Undo; an unrelated manual seek never invents an Undo note', async () => {
  const d = fixture({positionSeconds: 15, modes: {intro: 'auto', credits: 'ask'}}, [intro]);
  try {
    assert.equal(d.commands[0]?.kind, 'seek'); assert.equal(d.policy.getSnapshot()?.undo?.marker.id, 'intro');
    d.update({positionSeconds: 40}); await d.policy.undo(fence);
    const undo = d.commands.at(-1)!;
    assert.equal(undo.kind === 'seek' && undo.reason, 'undo'); assert.equal(undo.kind === 'seek' && undo.positionSeconds, 15);
    assert.equal(d.policy.getSnapshot()?.undo, null);
    d.update({positionSeconds: 15}); assert.equal(d.commands.length, 2, 'Undo does not immediately skip the same marker again');
  } finally {d.policy.dispose();}
  const manual = fixture({positionSeconds: 15}, [intro]);
  try {manual.update({positionSeconds: 40}); assert.equal(manual.policy.getSnapshot()?.undo, null); assert.equal(manual.commands.length, 0);}
  finally {manual.policy.dispose();}
});

test('collapsed playback keeps automatic actions quiet and never replays a silent Undo', () => {
  const d = fixture({expanded: false, positionSeconds: 15, modes: {intro: 'auto', credits: 'ask'}}, [intro]);
  try {
    assert.equal(d.commands[0]?.kind, 'seek'); assert.equal(d.policy.getSnapshot()?.undo, null); assert.equal(d.policy.getSnapshot()?.prompt, null);
    d.update({positionSeconds: 40, expanded: true}); assert.equal(d.policy.getSnapshot()?.undo, null);
  } finally {d.policy.dispose();}
  const creditsDock = fixture({expanded: false});
  try {creditsDock.time(5000); assert.equal(creditsDock.commands[0]?.kind, 'advance'); assert.equal(creditsDock.policy.getSnapshot()?.prompt, null);}
  finally {creditsDock.policy.dispose();}
});

test('ended countdown, cancellation and still-watching follow the shared policy', async () => {
  const d = fixture({ended: true, playing: false, positionSeconds: 1000});
  try {assert.equal(d.policy.getSnapshot()?.endCountdown?.seconds, 5); d.time(5000); assert.equal(d.commands[0]?.kind, 'advance');}
  finally {d.policy.dispose();}
  const cancelled = fixture({ended: true, playing: false, positionSeconds: 1000});
  try {cancelled.policy.cancelAutoplay(fence); cancelled.time(20_000); assert.equal(cancelled.commands.length, 0); assert.equal(cancelled.policy.getSnapshot()?.replay, true);}
  finally {cancelled.policy.dispose();}
  const still = fixture({ended: true, playing: false, expanded: false, positionSeconds: 1000,
    postPlay: {nextEntryId: 'next', available: true, reason: 'ready', countdownSeconds: 5, autoplay: true, passoutCheckDue: true, automaticAdvances: 3, startDuringCredits: true}});
  try {
    assert.equal(still.commands[0]?.kind, 'expand'); still.update({expanded: true}); still.time(20_000);
    await still.policy.advance(fence, 'manual'); assert.equal(still.commands.filter(command => command.kind === 'advance').length, 0);
    await still.policy.advance(fence, 'still-watching');
    assert.equal(still.commands.at(-1)?.kind === 'advance' && (still.commands.at(-1) as Extract<PolicyCommand, {kind: 'advance'}>).mode, 'still-watching');
  } finally {still.policy.dispose();}
});

test('retirement, disposal and a replacement source revoke retained timers and actions', async () => {
  for (const retire of ['retire', 'dispose', 'replace'] as const) {
    const d = fixture();
    try {
      const retained = [...d.timers.values()][0]!.run;
      if (retire === 'retire') d.retire();
      else if (retire === 'dispose') d.policy.dispose();
      else d.update({fence: playbackFence({...fence, generation: 2}), playing: false});
      retained(); await d.policy.advance(fence, 'manual'); await d.policy.skip(fence, 'credits'); await d.policy.undo(fence);
      assert.equal(d.commands.length, 0, retire);
    } finally {d.policy.dispose();}
    assert.equal(d.timers.size, 0);
  }
});

test('wire marker hints are bounded, ordered and validated without masking usable markers', () => {
  const marker = (id: string, type: 'intro' | 'credits' | 'other', startMs: number, endMs: number) => ({id, type, startMs, endMs, confidence: 'manual' as const, automaticSafe: true});
  const accepted = nativeVideoPolicyMarkers([
    marker('credits', 'credits', 900000, 1000000), marker('unsupported', 'other', 0, 1000000), marker('intro', 'intro', 10000, 40000),
    marker('too-long', 'intro', 10, 1000001.1), marker('bad.id', 'intro', 10, 100), marker('infinite', 'intro', 10, Infinity),
    marker('negative', 'intro', -10, 100), marker('duplicate', 'intro', 10, 100), marker('duplicate', 'intro', 20, 100),
  ], 1000);
  assert.deepEqual(accepted, [intro, credits]);
  assert.deepEqual(nativeVideoPolicyMarkers(Array.from({length: 513}, (_, i) => marker(`marker-${i}`, 'intro', i, i + 1)), 1000), []);
  const d = fixture({positionSeconds: 15, modes: {intro: 'auto', credits: 'ask'}}, accepted);
  try {assert.equal(d.commands[0]?.kind, 'seek');}
  finally {d.policy.dispose();}
});

test('unsupported finish-policy facts never become automatic playback defaults', () => {
  const d = fixture();
  try {
    const valid = d.frame().postPlay!;
    assert.equal(nativeVideoPolicyPostPlay(valid), valid);
    assert.equal(nativeVideoPolicyPostPlay(undefined), null);
    assert.equal(nativeVideoPolicyPostPlay({...valid, countdownSeconds: 7 as 5}), null);
    assert.equal(nativeVideoPolicyPostPlay({...valid, automaticAdvances: -1}), null);
    assert.equal(nativeVideoPolicyPostPlay({...valid, nextEntryId: null}), null);
  } finally {d.policy.dispose();}
});
