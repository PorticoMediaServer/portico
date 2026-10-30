import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence, sameScope} from '../../../packages/player-core/src/identity.ts';
import {PlayerSessionChoices} from '../../../packages/player-core/src/session-choices.ts';
import {NativeVideoPlaybackPolicy, nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay} from '../src-next/platform/playback/video-policy.ts';
import type {PolicyCommand} from '../../../packages/player-core/src/playback-policy.ts';

// This boundary test calls the real runtime methods and real full-fence helpers.
// Its native transport and service snapshots are explicit ports; the constructor's
// unrelated network, engine and platform lifetimes are not started here.
const runtimeModule = screenModule(new URL('../src-next/platform/playback/runtime.ts', import.meta.url), {
  react: {default: {}}, 'react-native': {}, './start-failure': {}, './capacity-retry': {}, './listening-controls': {},
  '../../../../../packages/client-core/src/index': {}, '../../../../../packages/client-core/src/detail': {},
  '../../../../../packages/client-core/src/media-item': {}, '../../../../../packages/contracts/src/routes': {},
  '../../../../../packages/client-core/src/playback-v1/sessions': {}, '../../../../../packages/client-core/src/playback-v1/local-http': {},
  '../../../../../packages/client-core/src/playback-v1/http': {}, '../../../../../packages/client-core/src/playback-v1/options': {},
  '../../../../../packages/client-core/src/playback-v1/v1-queue-player': {}, '../../../../../packages/client-core/src/playback-v1/queue': {},
  '../../../../../packages/client-core/src/playback-v1/channel-control': {}, '../../../../../packages/client-core/src/playback/linear-protocol': {},
  '../../../../../packages/client-core/src/guide/directory': {}, '../../../../../packages/client-core/src/guide/legacy-source': {},
  '../../../../../packages/client-core/src/playback-v1/types': {}, '../../../../../packages/client-core/src/playback-v1/quality': {},
  '../../../../../packages/client-core/src/subtitles': {}, '../../../../../packages/client-core/src/subtitle-originals': {}, './forced-image': {},
  '../../../../../packages/client-core/src/presentation/service-text': {},
  '../../../../../packages/player-core/src': {playbackFence, sameFence, sameScope, PlayerSessionChoices},
  './NativePlaybackCoordinator': {}, './external-presentation': {}, './native-playback': {}, './scope-http': {},
  './preparing-owner': {}, './start-presentation': {}, './offline-playback': {}, './caption-style': {},
  './subtitle-style-choices': {}, './start-intent': {}, './video-policy': {NativeVideoPlaybackPolicy, nativeVideoPolicyMarkers, nativeVideoPolicyPostPlay},
  './subtitle-controls': {}, './subtitle-file-upload': {}, './engine-media': {}, './playback-failure': {}, './playback-recovery': {},
  './audio-track-selection': {}, './offline-tracks': {},
  './native-listening-kinds': {}, './selection': {}, './video-exit-permission': {},
});

const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local' as const};
const authority = {serverId: scope.serverId, accountId: scope.accountId, profileId: scope.profileId, epoch: 'authority', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', itemId: 'episode', sourceId: 'version', entryId: 'entry', generation: 1});
const intro = {id: 'intro', type: 'intro', startMs: 10000, endMs: 40000, automaticSafe: true};

async function fixture() {
  const {NativePlaybackRuntime} = await runtimeModule;
  const runtime = Object.create(NativePlaybackRuntime.prototype);
  let state: any = {phase: 'ready', intent: 'playing', intentId: 7, playbackRun: 1, itemId: 'episode', duration: 1000, positionSeconds: 15,
    session: {id: 'session', generation: 1, mode: 'direct'}};
  let session: any = {id: 'session', kind: 'vod', role: 'local', itemId: 'episode', versionId: 'version', presentation: {generation: 1}, queue: {queueId: 'queue', entryId: 'entry'}};
  let queue: any = {view: {queue: {id: 'queue', revision: 'revision', scope, currentEntryId: 'entry'}, items: [{entryId: 'entry', itemId: 'episode', kind: 'episode'}],
    next: {entryId: 'next', available: true, reason: 'ready'},
    postPlay: {nextEntryId: 'next', available: true, reason: 'ready', countdownSeconds: 5, autoplay: true, passoutCheckDue: false, automaticAdvances: 0, startDuringCredits: true}}};
  let liveAuthority = authority;
  const reports: unknown[][] = [], seeks: number[] = [], advances: unknown[][] = [], expansions: unknown[] = [];
  const choices = new PlayerSessionChoices(); choices.begin(fence.playback); choices.bindSource(fence);
  Object.assign(runtime, {scope, bound: authority, sessionFamilyId: 'family', choices, retired: false, preparing: false, expanded: true, effectiveRate: 0,
    snapshot: {fence, playback: {...state}}, options: {itemId: 'episode', markers: [intro,
      {id: 'preview', type: 'preview', startMs: 50000, endMs: 60000, automaticSafe: true},
      {id: 'credits', type: 'credits', startMs: 900000, endMs: 1000000, automaticSafe: false}]},
    entry: {id: 'episode', kind: 'movie'},
    o: {authority: () => liveAuthority, session: () => ({sessionFamilyId: 'family', viewer: scope}),
      settings: () => ({videoRate: 1.25, skips: {intro: 'ask', recap: 'off', credits: 'ask'}})},
    playback: {getSnapshot: () => state, markerSkipped: (...args: unknown[]) => {reports.push(args);}, seek: (position: number) => {seeks.push(position);}},
    path: {session: (id: string) => id === session.id ? session : undefined},
    queue: {getSnapshot: () => queue, currentSession: () => session,
      continueCompletion: async (mode: string) => {advances.push(['completion', mode]);},
      advanceFromCredits: async (...args: unknown[]) => {advances.push(['credits', ...args]);},
      next: async (...args: unknown[]) => {advances.push(['next', ...args]);}},
    dock: {expand: (value: unknown) => {expansions.push(value);}}, applyOptions() {}, publish() {},
  });
  return {runtime, reports, seeks, advances, expansions, state: () => state, session: () => session, queue: () => queue,
    changeState: (patch: any) => {state = {...state, ...patch};}, changeSession: (patch: any) => {session = {...session, ...patch};},
    changeQueue: (patch: any) => {queue = {...queue, ...patch};}, changeAuthority: () => {liveAuthority = {...authority, epoch: 'replacement'};}};
}

test('native policy frames use the actual queue, current source markers, rate and held completion', async () => {
  const f = await fixture(), input = f.runtime.videoPolicyFrame();
  assert.equal(sameFence(input.frame.fence, fence), true);
  assert.deepEqual(input.markers, [{id: 'intro', kind: 'intro', startSeconds: 10, endSeconds: 40, automaticSafe: true},
    {id: 'credits', kind: 'credits', startSeconds: 900, endSeconds: 1000, automaticSafe: false}]);
  assert.equal(input.frame.postPlay, f.queue().view.postPlay);
  assert.equal(input.frame.kind, 'episode', 'the current queue item wins over retained title display data');
  assert.equal(input.frame.rate, 1.25); assert.equal(input.frame.playing, true); assert.equal(input.frame.ended, false);
  assert.deepEqual(input.frame.modes, {intro: 'ask', recap: 'off', credits: 'ask'});
  f.runtime.choices.set('speed', fence.playback, 2); assert.equal(f.runtime.videoPolicyFrame().frame.rate, 2);
  f.runtime.effectiveRate = 1.5; assert.equal(f.runtime.videoPolicyFrame().frame.rate, 1.5);
  f.changeState({positionSeconds: 2000}); assert.equal(f.runtime.videoPolicyFrame().frame.positionSeconds, 1000);
  f.runtime.options = {...f.runtime.options, itemId: 'other'}; assert.deepEqual(f.runtime.videoPolicyFrame().markers, []);
  f.changeState({phase: 'ended'}); assert.equal(f.runtime.videoPolicyFrame().frame.ended, false);
  f.changeQueue({completion: {sessionId: 'old'}}); assert.equal(f.runtime.videoPolicyFrame().frame.ended, false);
  f.changeQueue({completion: {sessionId: 'session'}}); assert.equal(f.runtime.videoPolicyFrame().frame.ended, true);
  f.changeState({phase: 'error'}); assert.equal(f.runtime.videoPolicyFrame().frame.attention, 'playback-error');
});

test('pause, buffering and unsettled seeks do not run the native policy playing clock', async () => {
  for (const patch of [{intent: 'paused'}, {recovering: true}, {pendingSeek: {revision: 1}}, {failedSeek: {}}, {phase: 'starting'}]) {
    const f = await fixture(); f.changeState(patch);
    assert.equal(f.runtime.videoPolicyFrame().frame.playing, false, JSON.stringify(patch));
  }
});

test('policy frames exclude preparing, foreign queues, nonlocal VOD and clocks outside the core range', async () => {
  for (const scenario of ['retired', 'preparing', 'transition', 'remote', 'audio', 'queue', 'entry', 'generation', 'duration', 'position'] as const) {
    const f = await fixture();
    if (scenario === 'retired' || scenario === 'preparing') f.runtime[scenario] = true;
    else if (scenario === 'transition') f.runtime.transitionEpoch = 1;
    else if (scenario === 'remote') f.changeSession({role: 'remote'});
    else if (scenario === 'audio') f.changeSession({kind: 'audio'});
    else if (scenario === 'queue') f.changeSession({queue: {...f.session().queue, queueId: 'different'}});
    else if (scenario === 'entry') f.changeQueue({view: {...f.queue().view, queue: {...f.queue().view.queue, currentEntryId: 'different'}}});
    else if (scenario === 'generation') f.changeSession({presentation: {generation: 2}});
    else f.changeState(scenario === 'duration' ? {duration: 1e9 + 1} : {positionSeconds: 1e9 + 1});
    assert.equal(f.runtime.videoPolicyFrame(), undefined, scenario);
  }
});

test('skip commands report their actual mode, Undo only seeks, and buffering blocks seek effects', async () => {
  const f = await fixture();
  for (const reason of ['manual-skip', 'automatic-skip', 'undo'] as const) await f.runtime.executeVideoPolicy({kind: 'seek', fence, markerId: 'intro', positionSeconds: 40, reason});
  assert.deepEqual(f.reports, [['intro', 'manual', 15], ['intro', 'automatic', 15]]);
  assert.deepEqual(f.seeks, [40, 40, 40]);
  for (const patch of [{recovering: true}, {recovering: false, pendingSeek: {}}, {pendingSeek: undefined, failedSeek: {}}, {failedSeek: undefined, phase: 'starting'}]) {
    f.changeState(patch); await f.runtime.executeVideoPolicy({kind: 'seek', fence, markerId: 'intro', positionSeconds: 70, reason: 'manual-skip'});
  }
  assert.equal(f.reports.length, 2); assert.deepEqual(f.seeks, [40, 40, 40]);
});

test('policy commands reject an old actual generation and authority even when the published snapshot is retained', async () => {
  for (const changed of ['generation', 'authority', 'retirement'] as const) {
    const f = await fixture();
    if (changed === 'generation') {f.changeState({session: {...f.state().session, generation: 2}}); f.changeSession({presentation: {generation: 2}});}
    else if (changed === 'authority') f.changeAuthority();
    else f.runtime.retired = true;
    assert.equal(f.runtime.snapshot.fence, fence, 'the old UI publication is deliberately retained');
    assert.equal(f.runtime.videoPolicyCurrent(fence), false, changed);
    const commands: PolicyCommand[] = [
      {kind: 'seek', fence, markerId: 'intro', positionSeconds: 40, reason: 'automatic-skip'},
      {kind: 'advance', fence, mode: 'automatic', nextEntryId: 'next'}, {kind: 'expand', fence, reason: 'still-watching'},
    ];
    for (const command of commands) await f.runtime.executeVideoPolicy(command);
    assert.deepEqual([f.reports, f.seeks, f.advances, f.expansions], [[], [], [], []], changed);
  }
});

test('policy respects an explicitly collapsed playback error while still-watching can request attention', async () => {
  const f = await fixture(); f.runtime.expanded = false; f.changeState({phase: 'error'});
  await f.runtime.executeVideoPolicy({kind: 'expand', fence, reason: 'playback-error'});
  assert.equal(f.runtime.expanded, false, 'runtime.changed owns one initial error expansion; policy cannot undo Back');
  assert.deepEqual(f.expansions, []);
  f.changeState({phase: 'ended'}); f.changeQueue({completion: {sessionId: 'session'}});
  await f.runtime.executeVideoPolicy({kind: 'expand', fence, reason: 'still-watching'});
  assert.equal(f.runtime.expanded, true); assert.deepEqual(f.expansions, [fence]);
});

test('advance commands use completion, credits and manual routes with the actual next-entry guard', async () => {
  const f = await fixture();
  await f.runtime.executeVideoPolicy({kind: 'advance', fence, mode: 'automatic', nextEntryId: 'next'});
  await f.runtime.executeVideoPolicy({kind: 'advance', fence, mode: 'manual', nextEntryId: 'next'});
  f.changeQueue({completion: {sessionId: 'session'}});
  for (const mode of ['automatic', 'manual', 'still-watching'] as const) await f.runtime.executeVideoPolicy({kind: 'advance', fence, mode, nextEntryId: 'next'});
  assert.deepEqual(f.advances, [['credits', 'queue', 'revision'], ['next', 'queue', 'revision'], ['completion', 'automatic'], ['completion', 'manual'], ['completion', 'still-watching']]);
  await f.runtime.executeVideoPolicy({kind: 'advance', fence, mode: 'manual', nextEntryId: 'old-next'});
  f.changeQueue({view: {...f.queue().view, next: {...f.queue().view.next, available: false}}});
  await f.runtime.executeVideoPolicy({kind: 'advance', fence, mode: 'manual', nextEntryId: 'next'});
  f.changeQueue({view: {...f.queue().view, next: {...f.queue().view.next, available: true}, queue: {...f.queue().view.queue, currentEntryId: 'different'}}});
  await f.runtime.executeVideoPolicy({kind: 'advance', fence, mode: 'manual', nextEntryId: 'next'});
  assert.equal(f.advances.length, 5);
});

test('a real buffering frame prevents automatic skips until progress resumes', async () => {
  const f = await fixture(); f.changeState({recovering: true});
  f.runtime.o.settings = () => ({videoRate: 1, skips: {intro: 'auto', recap: 'ask', credits: 'ask'}});
  const timers = new Set<() => void>();
  const policy = new NativeVideoPlaybackPolicy({read: () => f.runtime.videoPolicyFrame(), current: value => f.runtime.videoPolicyCurrent(value),
    execute: value => f.runtime.executeVideoPolicy(value), changed() {}, failed: (_fence, error) => {assert.fail(String(error));}, now: () => 0,
    setTimer: run => {timers.add(run); return run;}, clearTimer: timer => {timers.delete(timer as () => void);}});
  try {
    policy.update(); assert.deepEqual(f.seeks, []); assert.equal(timers.size, 0);
    f.changeState({recovering: false}); policy.update();
    assert.deepEqual(f.seeks, [39], 'the shared skip target retains its one-second landing cushion'); assert.deepEqual(f.reports, [['intro', 'automatic', 15]]);
    assert.equal(policy.getSnapshot()?.undo?.marker.id, 'intro');
    policy.update(); assert.equal(f.seeks.length, 1, 'the shared policy owns skip deduplication');
  } finally {policy.dispose();}
});

test('blocked manual Skip retains its shared-policy prompt until a seek can be accepted', async () => {
  const f = await fixture();
  const policy = new NativeVideoPlaybackPolicy({read: () => f.runtime.videoPolicyFrame(), current: value => f.runtime.videoPolicyCurrent(value),
    execute: value => f.runtime.executeVideoPolicy(value), changed() {}, failed: (_fence, error) => {assert.fail(String(error));}, now: () => 0,
    setTimer: () => 1, clearTimer() {}});
  f.runtime.videoPolicy = policy;
  const ready = {phase: 'ready', recovering: false, pendingSeek: undefined, failedSeek: undefined};
  try {
    policy.update(); assert.equal(policy.getSnapshot()?.prompt?.kind, 'skip');
    for (const blocked of [{recovering: true}, {pendingSeek: {revision: 1}}, {failedSeek: {}}, {phase: 'starting'}]) {
      f.changeState({...ready, ...blocked}); policy.update();
      await f.runtime.applyPolicySeek(fence, {kind: 'skip', markerId: 'intro'});
      const prompt = policy.getSnapshot()?.prompt;
      assert.equal(prompt?.kind === 'skip' && prompt.marker.id, 'intro', JSON.stringify(blocked));
      assert.deepEqual(f.seeks, []); assert.deepEqual(f.reports, []);
    }
    f.changeState(ready); policy.update();
    await f.runtime.applyPolicySeek(fence, {kind: 'skip', markerId: 'intro'});
    assert.deepEqual(f.seeks, [39]); assert.deepEqual(f.reports, [['intro', 'manual', 15]]);
    assert.equal(policy.getSnapshot()?.prompt, null);
    await f.runtime.applyPolicySeek(fence, {kind: 'skip', markerId: 'intro'});
    assert.deepEqual(f.seeks, [39], 'the accepted prompt is consumed exactly once');
  } finally {policy.dispose();}
});

test('blocked Undo retains its shared-policy return point until a seek can be accepted', async () => {
  const f = await fixture();
  f.runtime.o.settings = () => ({videoRate: 1, skips: {intro: 'auto', recap: 'ask', credits: 'ask'}});
  const policy = new NativeVideoPlaybackPolicy({read: () => f.runtime.videoPolicyFrame(), current: value => f.runtime.videoPolicyCurrent(value),
    execute: value => f.runtime.executeVideoPolicy(value), changed() {}, failed: (_fence, error) => {assert.fail(String(error));}, now: () => 0,
    setTimer: () => 1, clearTimer() {}});
  f.runtime.videoPolicy = policy;
  const ready = {phase: 'ready', recovering: false, pendingSeek: undefined, failedSeek: undefined};
  try {
    policy.update(); assert.deepEqual(f.seeks, [39]); assert.equal(policy.getSnapshot()?.undo?.marker.id, 'intro');
    f.changeState({positionSeconds: 39}); policy.update();
    for (const blocked of [{recovering: true}, {pendingSeek: {revision: 1}}, {failedSeek: {}}, {phase: 'starting'}]) {
      f.changeState({...ready, ...blocked}); policy.update();
      await f.runtime.applyPolicySeek(fence, {kind: 'undo'});
      assert.equal(policy.getSnapshot()?.undo?.marker.id, 'intro', JSON.stringify(blocked));
      assert.deepEqual(f.seeks, [39]); assert.deepEqual(f.reports, [['intro', 'automatic', 15]]);
    }
    f.changeState(ready); policy.update();
    await f.runtime.applyPolicySeek(fence, {kind: 'undo'});
    assert.deepEqual(f.seeks, [39, 15], 'Undo returns to the original position, not its blocked tap position');
    assert.deepEqual(f.reports, [['intro', 'automatic', 15]]); assert.equal(policy.getSnapshot()?.undo, null);
    await f.runtime.applyPolicySeek(fence, {kind: 'undo'});
    assert.deepEqual(f.seeks, [39, 15], 'the accepted Undo is consumed exactly once');
  } finally {policy.dispose();}
});

test('invalid optional markers cannot interrupt a valid policy frame or mask a valid intro', async () => {
  const f = await fixture();
  f.runtime.options.markers = [
    {...intro, id: 'later', startMs: 70000, endMs: 100000}, {...intro, id: 'negative', startMs: -1},
    {...intro, id: 'backwards', endMs: 1000}, {...intro, id: 'beyond-source', endMs: 2000000},
    {...intro, id: 'bad.id'}, {...intro, id: 'duplicate'}, {...intro, id: 'duplicate'},
    {...intro, id: 'not-finite', endMs: Infinity}, {...intro, id: 'not-a-flag', automaticSafe: 'true'},
    intro, {...intro, type: 'preview'},
  ];
  const policy = new NativeVideoPlaybackPolicy({read: () => f.runtime.videoPolicyFrame(), current: value => f.runtime.videoPolicyCurrent(value),
    execute: value => f.runtime.executeVideoPolicy(value), changed() {}, failed: (_fence, error) => {assert.fail(String(error));}, now: () => 0,
    setTimer: () => 1, clearTimer() {}});
  try {
    assert.doesNotThrow(() => policy.update());
    assert.deepEqual(policy.getSnapshot()?.markers.map(marker => marker.id), ['intro', 'later']);
    assert.equal(policy.getSnapshot()?.prompt?.kind, 'skip');
    f.runtime.options.markers = Array.from({length: 513}, (_, index) => ({...intro, id: `marker-${index}`}));
    assert.doesNotThrow(() => policy.update()); assert.deepEqual(policy.getSnapshot()?.markers, []);
    f.changeState({duration: 0}); f.runtime.options.markers = [{...intro, endMs: 1e13}];
    assert.doesNotThrow(() => policy.update()); assert.deepEqual(policy.getSnapshot()?.markers, []);
  } finally {policy.dispose();}
});

test('invalid optional postplay offers neither an advance nor a timer', async () => {
  const patches = [{countdownSeconds: 2}, {autoplay: 'yes'}, {startDuringCredits: undefined}, {automaticAdvances: -1}, {nextEntryId: null}, {reason: 'unknown'}];
  for (const patch of patches) {
    const f = await fixture(); f.changeState({phase: 'ended', positionSeconds: 1000});
    f.changeQueue({completion: {sessionId: 'session'}, view: {...f.queue().view, postPlay: {...f.queue().view.postPlay, ...patch}}});
    let timers = 0;
    const policy = new NativeVideoPlaybackPolicy({read: () => f.runtime.videoPolicyFrame(), current: value => f.runtime.videoPolicyCurrent(value),
      execute: value => f.runtime.executeVideoPolicy(value), changed() {}, failed: (_fence, error) => {assert.fail(String(error));}, now: () => 0,
      setTimer: () => {timers++; return 1;}, clearTimer() {}});
    try {
      assert.equal(f.runtime.videoPolicyFrame().frame.postPlay, null, JSON.stringify(patch));
      assert.doesNotThrow(() => policy.update()); assert.deepEqual(f.advances, []); assert.equal(timers, 0);
      assert.equal(policy.getSnapshot()?.endCountdown, undefined);
    } finally {policy.dispose();}
  }
});
