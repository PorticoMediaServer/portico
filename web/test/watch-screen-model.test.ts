import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';
import {dispatchWatch, watchClock, watchIsPortrait, watchSeek, watchSeekBounds, watchStepIntent} from '../src/screens/watch/model.ts';
import {parseSession} from '@core/playback-v1/types.ts';
import type {PlaybackOptions, MediaVersion} from '@core/playback-v1/types.ts';
import type {WebPlaybackAction, WebPlaybackRuntimeSnapshot} from '../src/platform/playback/types.ts';
import type {QueuePlaybackFence} from '../../packages/player-core/src/index.ts';
import {PlaybackDock, PlaybackPolicy} from '../../packages/player-core/src/index.ts';
import {parseTrickplay, trickplayFrameAt} from '@core/trickplay.ts';
import {createArtworkRuntime} from '../src/kit/artwork/source.ts';
import {scrubImageStyle, watchPreviewFrame} from '../src/screens/watch/preview.ts';
import {dispatchWatchMarker, watchUndoOffer} from '../src/screens/watch/policy.ts';
import {dispatchWatchVersion, watchVersionRows} from '../src/screens/watch/versions.ts';
import {WatchPictureInput} from '../src/screens/watch/picture.ts';

function previewSet() {
  const id = 'a'.repeat(64);
  return parseTrickplay({scope: {serverId: 'server', libraryId: 'library', itemId: 'item', viewerFence: 'viewer', sessionId: '', sessionGeneration: 0}, sets: [{
    id, revision: 'b'.repeat(64), sourceId: 'source', sourceRevision: 'revision', width: 640, height: 360, tileWidth: 320, tileHeight: 180,
    columns: 2, rows: 2, intervalMs: 10000, durationMs: 60000, sourceOffsetMs: 0, tileCount: 2, frameCount: 6, stale: false,
    tilesUrl: `/v1/items/item/trickplay/${id}/tiles`, thumbnailsUrl: `/v1/items/item/trickplay/${id}/thumbnails.vtt`,
  }]}, 'server', {libraryId: 'library', itemId: 'item'}).sets[0]!;
}

const authority = Object.freeze({serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', epoch: 1, viewerFence: 'viewer-fence'});
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
function snapshot(change: Partial<WebPlaybackRuntimeSnapshot> = {}): WebPlaybackRuntimeSnapshot {
  return {authority, sequence: 1, disposed: false, fence,
    presentation: {fence, subject: {kind: 'video', item: {id: 'item', kind: 'movie', title: 'Title'}}, kind: 'video', durationSeconds: 7200, positionSeconds: 30, playing: true, expanded: true, finished: false, controls: ['pause', 'seek'], volume: .7, muted: false, rate: 1, policy: null},
    playback: {} as WebPlaybackRuntimeSnapshot['playback'], dock: {} as WebPlaybackRuntimeSnapshot['dock'], presentations: [], queueView: null, options: null, offers: null, ...change};
}

test('speed and frame shortcuts use offered current engine commands and never synthesize a seek', async () => {
  const shown = snapshot(); let live = shown, frames = false, speeds: readonly number[] = [.5, 1, 1.5];
  const calls: WebPlaybackAction[] = [];
  const runtime = {getSnapshot: () => live, speedChoices: () => speeds, canStepFrame: () => frames,
    onAction: async (action: WebPlaybackAction) => {calls.push(action);}};
  for (const [key, direction] of [['<', -1], ['>', 1]] as const) {
    const intent = watchStepIntent(runtime, shown, key)!;
    await dispatchWatch(runtime, shown, intent);
    assert.deepEqual(calls.at(-1), {kind: 'step-speed', direction, fence});
  }
  assert.equal(watchStepIntent(runtime, shown, '.'), null);
  const paused = snapshot({presentation: {...shown.presentation!, playing: false}}); live = paused;
  assert.equal(watchStepIntent(runtime, paused, '.'), null); // Ordinary browser has no frame primitive.
  frames = true;
  for (const [key, direction] of [[',', -1], ['.', 1]] as const) {
    await dispatchWatch(runtime, paused, watchStepIntent(runtime, paused, key)!);
    assert.deepEqual(calls.at(-1), {kind: 'step-frame', direction, fence});
  }
  speeds = []; assert.equal(watchStepIntent(runtime, paused, '>'), null);
  live = snapshot({fence: {...fence, sessionId: 'new-session'}});
  assert.equal(watchStepIntent(runtime, paused, '.'), null);
  assert.equal(watchStepIntent(runtime, paused, '>'), null);
  assert.ok(calls.every(action => action.kind !== 'seek'));
});

test('a picture double-click enters fullscreen without a transient pause; assistive activation is immediate', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const input = new WatchPictureInput(), shown = snapshot(), calls: WebPlaybackAction[] = [];
  let fullscreen = 0;
  const target = {runtime: {getSnapshot: () => shown, onAction: async (action: WebPlaybackAction) => { calls.push(action); }}, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }};
  input.mouse(target, 1); context.mock.timers.tick(100);
  input.mouse(target, 2); input.doubleClick(target, () => fullscreen++);
  context.mock.timers.tick(500);
  assert.equal(fullscreen, 1); assert.deepEqual(calls, []);
  input.mouse(target, 0);
  assert.deepEqual(calls, [{kind: 'pause', fence}]);
  input.cancel();
});

test('a pending picture click cannot pause a replacement source, viewer, remote, collapsed player or disposed screen', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const shown = snapshot();
  for (const replacement of [{fence: {...fence, generation: 2}}, {authority: {...authority}}, {dock: {...shown.dock, remote: {phase: 'playing'}}}, {disposed: true}] as Partial<WebPlaybackRuntimeSnapshot>[]) {
    const input = new WatchPictureInput(); let live = shown, calls = 0;
    const target = {runtime: {getSnapshot: () => live, onAction: async () => { calls++; }}, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }};
    input.mouse(target, 1); live = snapshot(replacement); context.mock.timers.tick(221);
    assert.equal(calls, 0);
  }
  for (const cancel of [false, true]) {
    const input = new WatchPictureInput(); let active = true, calls = 0;
    const target = {runtime: {getSnapshot: () => shown, onAction: async () => { calls++; }}, snapshot: shown, isActive: () => active, reportError: (error: unknown) => { throw error; }};
    input.mouse(target, 1); if (cancel) input.cancel(); else active = false;
    context.mock.timers.tick(221); assert.equal(calls, 0);
  }
});

test('one mouse click pauses only after the double-click window and retains the shown complete fence', context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const input = new WatchPictureInput(), shown = snapshot(), calls: WebPlaybackAction[] = [];
  const target = {runtime: {getSnapshot: () => snapshot({sequence: 4}), onAction: async (action: WebPlaybackAction) => { calls.push(action); }}, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }};
  input.mouse(target, 1); context.mock.timers.tick(219); assert.equal(calls.length, 0);
  context.mock.timers.tick(1); assert.deepEqual(calls, [{kind: 'pause', fence}]);
});

test('the second click cannot fullscreen a replacement playback even if that new target is current', context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const input = new WatchPictureInput(); let shown = snapshot(), fullscreen = 0, effects = 0;
  const runtime = {getSnapshot: () => shown, onAction: async () => { effects++; }};
  const target = () => ({runtime, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }});
  input.mouse(target(), 1);
  const replacement = {...fence, generation: 2}; shown = snapshot({fence: replacement, presentation: {...shown.presentation!, fence: replacement}});
  input.mouse(target(), 2); input.doubleClick(target(), () => fullscreen++); context.mock.timers.tick(500);
  assert.equal(effects, 0); assert.equal(fullscreen, 0);
});

test('touch double-taps seek by actual Settings amounts without toggling hidden controls; tablet centre is inert', async context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const input = new WatchPictureInput(), calls: WebPlaybackAction[] = [], feedback: number[] = [];
  let shown = snapshot(), singles = 0, fullscreen = 0;
  const runtime = {getSnapshot: () => shown, onAction: async (action: WebPlaybackAction) => {
    calls.push(action);
    if (action.seconds !== undefined) shown = {...shown, presentation: {...shown.presentation!, positionSeconds: action.seconds}};
  }};
  const target = () => ({runtime, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }});
  const touch = {zone: 'right' as const, tablet: true, back: 10, forward: 30, single: () => singles++, fullscreen: () => fullscreen++, feedback: (_direction: string, seconds: number) => feedback.push(seconds)};
  for (let i = 0; i < 2; i++) { input.tap(target(), touch); context.mock.timers.tick(100); input.tap(target(), touch); await new Promise(resolve => setImmediate(resolve)); }
  context.mock.timers.tick(500);
  assert.equal(singles, 0); assert.deepEqual(calls.map(a => a.seconds), [60, 90]); assert.deepEqual(feedback, [30, 30]);
  const middle = {...touch, zone: 'middle' as const};
  input.tap(target(), middle); input.tap(target(), middle); context.mock.timers.tick(500); assert.equal(fullscreen, 0); assert.equal(singles, 0);
  input.tap(target(), {...middle, tablet: false}); input.tap(target(), {...middle, tablet: false}); assert.equal(fullscreen, 1);
  input.tap(target(), touch); context.mock.timers.tick(320); assert.equal(singles, 1);
});

test('a retired touch read drops its pending single and cannot combine with a new playback double-tap', context => {
  context.mock.timers.enable({apis: ['setTimeout']});
  const input = new WatchPictureInput(); let shown = snapshot(), singles = 0, effects = 0;
  const runtime = {getSnapshot: () => shown, onAction: async () => { effects++; }};
  const target = () => ({runtime, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }});
  const touch = {zone: 'left' as const, tablet: true, back: 10, forward: 30, single: () => singles++, fullscreen: () => effects++, feedback: () => effects++};
  input.tap(target(), touch);
  const replacement = {...fence, sessionId: 'replacement'};
  shown = snapshot({fence: replacement, presentation: {...shown.presentation!, fence: replacement}});
  input.tap(target(), touch); context.mock.timers.tick(320);
  assert.equal(effects, 0); assert.equal(singles, 1);
});

test('picture wheel volume requires a current offered volume command and stays within actual bounds', () => {
  const input = new WatchPictureInput(), base = snapshot(), calls: WebPlaybackAction[] = [];
  let shown = {...base, presentation: {...base.presentation!, volume: .98, controls: [...base.presentation!.controls, 'volume' as const]}};
  const runtime = {getSnapshot: () => shown, onAction: async (action: WebPlaybackAction) => { calls.push(action); }};
  const target = () => ({runtime, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }});
  assert.equal(input.wheel(target(), -1), true); assert.equal(calls[0]!.volume, 1);
  shown = {...shown, presentation: {...shown.presentation, volume: .02}};
  assert.equal(input.wheel(target(), 1), true); assert.equal(calls[1]!.volume, 0);
  assert.equal(input.wheel(target(), NaN), false); assert.equal(input.wheel(target(), 0), false);
  shown = {...shown, presentation: {...shown.presentation, controls: []}};
  assert.equal(input.wheel(target(), 1), false); assert.equal(calls.length, 2);
});

test('an accepted touch seek cannot restore its ripple after a panel or source retires that gesture', async () => {
  const input = new WatchPictureInput(), shown = snapshot();
  let finish!: () => void, feedback = 0;
  const wait = new Promise<void>(resolve => { finish = resolve; });
  const target = {runtime: {getSnapshot: () => shown, onAction: async () => wait}, snapshot: shown, isActive: () => true, reportError: (error: unknown) => { throw error; }};
  const touch = {zone: 'left' as const, tablet: true, back: 10, forward: 30, single: () => assert.fail(), fullscreen: () => assert.fail(), feedback: () => feedback++};
  input.tap(target, touch); input.tap(target, touch); input.cancel(); finish();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(feedback, 0);
});

test('the screen sends the displayed complete fence through the existing runtime command', async () => {
  const shown = snapshot(), calls: WebPlaybackAction[] = [];
  await dispatchWatch({getSnapshot: () => snapshot({sequence: 2}), onAction: async action => { calls.push(action); }}, shown, {kind: 'pause'});
  assert.deepEqual(calls, [{kind: 'pause', fence}]);
});
test('retained screen and keyboard callbacks reject every changed fence and authority before an effect', async () => {
  const shown = snapshot(), cases: Partial<WebPlaybackRuntimeSnapshot>[] = [
    {disposed: true}, {fence: undefined}, {authority: {...authority, viewerFence: 'new-viewer'}},
    {fence: {...fence, sessionId: 'replacement'}}, {fence: {...fence, sourceId: 'replacement'}},
    {fence: {...fence, generation: 2}}, {fence: {...fence, entryId: 'another-occurrence'}},
    {fence: {...fence, playback: {...fence.playback, run: 2}}},
    {fence: {...fence, playback: {...fence.playback, scope: {...fence.playback.scope, profileId: 'other'}}}},
  ];
  for (const replacement of cases) {
    let count = 0;
    await assert.rejects(dispatchWatch({getSnapshot: () => snapshot(replacement), onAction: async () => { count++; }}, shown, {kind: 'seek', seconds: 50}), /stale_watch_command/);
    assert.equal(count, 0);
  }
});
test('a retained local callback cannot act after an actual confirmed remote dock takeover', async () => {
  const shown = snapshot(), dock = new PlaybackDock(fence.playback.scope);
  const playback = {kind: 'video' as const, fence, queue: null, positionSeconds: 30, playing: true, expanded: true};
  dock.start(playback);
  dock.beginRemote({id: 'cast', kind: 'cast', phase: 'connecting', sequence: '1', playback, positionSeconds: 0}, 0);
  assert.equal(dock.getSnapshot().local?.playing, true);
  let effects = 0;
  await dispatchWatch({getSnapshot: () => snapshot({dock: dock.getSnapshot()}), onAction: async () => { effects++; }}, shown, {kind: 'pause'});
  assert.equal(effects, 1);
  dock.remote({id: 'cast', kind: 'cast', phase: 'playing', sequence: '2', playback, positionSeconds: 35}, 1000);
  assert.equal(dock.getSnapshot().local?.playing, false);
  await assert.rejects(dispatchWatch({getSnapshot: () => snapshot({dock: dock.getSnapshot()}), onAction: async () => { effects++; }}, shown, {kind: 'seek', seconds: 60}), /stale_watch_command/);
  assert.equal(effects, 1);
});
test('seek bounds respect the actual retained live window and never invent a clock', () => {
  const live = snapshot();
  const withWindow = {...live, presentation: {...live.presentation!, seekWindow: {startSeconds: 1800, endSeconds: 2100}}};
  assert.deepEqual(watchSeekBounds(withWindow), {start: 1800, end: 2100});
  assert.equal(watchSeek(withWindow, 1700), 1800);
  assert.equal(watchSeek(withWindow, 2200), 2100);
  assert.equal(watchSeek(withWindow, NaN), null);
  for (const presentation of [undefined, {...live.presentation!, controls: []}, {...live.presentation!, durationSeconds: NaN}, {...live.presentation!, durationSeconds: 0}]) assert.equal(watchSeekBounds({...live, presentation}), null);
});
test('clock lengths remain exact while window shape changes presentation only', () => {
  assert.equal(watchClock(3186), '53:06');
  assert.equal(watchClock(3601), '1:00:01');
  assert.equal(watchClock(59.9), '0:59');
  assert.equal(watchClock(NaN), '');
  assert.equal(watchIsPortrait(390, 844), true);
  assert.equal(watchIsPortrait(834, 1194), true);
  assert.equal(watchIsPortrait(890, 626), false);
  assert.equal(watchIsPortrait(1440, 810), false);
});

test('preview selection never requests an obsolete or retired resource', () => {
  const set = previewSet(), frame = trickplayFrameAt(set, 35)!;
  let calls = 0, current = true;
  const artwork = createArtworkRuntime({scopeKey: 'scope', load: async () => { throw new Error('preview_selection_must_not_load'); }, classify: () => ({retryable: false})});
  const source = artwork.source({url: frame.tileUrl, cacheKey: 'tile'});
  const port = {fence, isCurrent: () => current, frameAt: (seconds: number) => { calls++; const frame = trickplayFrameAt(set, seconds); return frame ? {frame, source} : null; }};
  assert.equal(watchPreviewFrame(snapshot(), port, 35)?.frame.index, 3);
  assert.equal(calls, 1);
  for (const shown of [snapshot({disposed: true}), snapshot({fence: {...fence, sourceId: 'other'}}), snapshot({fence: {...fence, generation: 2}})]) assert.equal(watchPreviewFrame(shown, port, 35), null);
  for (const seconds of [NaN, Infinity, -1, 7201, null]) assert.equal(watchPreviewFrame(snapshot(), port, seconds), null);
  current = false;
  assert.equal(watchPreviewFrame(snapshot(), port, 35), null);
  assert.equal(calls, 1);
  artwork.dispose();
});

test('preview crops use canonical sprite cells and reject missing or inconsistent image dimensions', () => {
  const set = previewSet(), frame = trickplayFrameAt(set, 35)!;
  assert.deepEqual(scrubImageStyle(frame, {width: 640, height: 360}, false), {position: 'absolute', width: 512, height: 288, left: -256, top: -144});
  const phone = scrubImageStyle(frame, {width: 640, height: 360}, true)!;
  assert.equal(phone.width, 356);
  assert.equal(phone.left, -178);
  for (const image of [{}, {width: 320, height: 180}, {width: NaN, height: 360}]) assert.equal(scrubImageStyle(frame, image, false), null);
});

test('drawn skip actions carry the current policy marker identity, never a UI-made seek', async () => {
  const policy = new PlaybackPolicy();
  policy.begin(fence, [{id: 'intro', kind: 'intro', startSeconds: 60, endSeconds: 120, automaticSafe: true}], 600);
  const view = policy.update({fence, nowMs: 0, positionSeconds: 80, durationSeconds: 600, rate: 1, playing: true, expanded: true, ended: false, kind: 'episode', modes: {intro: 'ask'}, postPlay: null})!.view;
  const shown = snapshot({presentation: {...snapshot().presentation!, policy: view}});
  let live = shown, called = 0;
  const binding = {getSnapshot: () => live, isCurrent: () => true, reportError: () => {}, policyAction: async (captured: QueuePlaybackFence, action: {kind: 'skip' | 'dismiss' | 'undo'; markerId: string}) => {
    called++; assert.deepEqual(action, {kind: 'skip', markerId: 'intro'}); assert.equal(captured, fence);
    assert.equal(policy.skip(captured, action.markerId)?.positionSeconds, 119);
  }};
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'other'}), false);
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'intro'}), true);
  live = snapshot({presentation: {...snapshot().presentation!, policy: {...view, attention: 'owner-stopped'}}});
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'intro'}), false);
  live = snapshot({fence: {...fence, generation: 2}});
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'intro'}), false);
  assert.equal(called, 1);
});

test('a retained Undo action cannot outlive the real current policy offer', async () => {
  const marker = {id: 'intro', kind: 'intro' as const, startSeconds: 60, endSeconds: 120, automaticSafe: true};
  const policy = new PlaybackPolicy(), frame = {fence, nowMs: 0, positionSeconds: 80, durationSeconds: 600, rate: 1, playing: true, expanded: true, ended: false, kind: 'episode', modes: {intro: 'auto' as const}, postPlay: null};
  policy.begin(fence, [marker], 600); policy.update(frame);
  const view = policy.update({...frame, nowMs: 4000, positionSeconds: 119})!.view;
  assert.equal(view.undo?.remainingSeconds, 2);
  const shown = snapshot({presentation: {...snapshot().presentation!, policy: view}});
  let live = shown, called = 0;
  const binding = {getSnapshot: () => live, isCurrent: () => true, reportError: () => {}, policyAction: async () => { called++; assert.equal(policy.undoSkip(fence, 4000)?.positionSeconds, 80); }};
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'undo', markerId: 'intro'}), true);
  live = snapshot({presentation: {...shown.presentation!, policy: policy.update({...frame, nowMs: 6000, positionSeconds: 120})!.view}});
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'undo', markerId: 'intro'}), false);
  assert.equal(called, 1);
});

test('Undo progress comes from the live policy, expires while paused and stays out of a collapsed or retired source', () => {
  const policy = new PlaybackPolicy();
  policy.begin(fence, [{id: 'intro', kind: 'intro', startSeconds: 60, endSeconds: 120, automaticSafe: true}], 600);
  const frame = {fence, nowMs: 0, positionSeconds: 80, durationSeconds: 600, rate: 1, playing: true, expanded: true, ended: false, kind: 'episode', modes: {intro: 'auto' as const}, postPlay: null};
  const view = policy.update(frame)!.view;
  const shown = snapshot({presentation: {...snapshot().presentation!, policy: view}});
  let live = shown;
  const binding = {getSnapshot: () => live, isCurrent: () => true, policyAction: async () => {}, reportError: (error: unknown) => { throw error; }};
  assert.equal(watchUndoOffer(binding, shown)?.fraction, 1);
  const elapsed = policy.update({...frame, nowMs: 4000, positionSeconds: 119, playing: false})!.view;
  live = {...shown, presentation: {...shown.presentation!, policy: elapsed}};
  assert.equal(watchUndoOffer(binding, shown)?.remainingSeconds, 2);
  assert.equal(watchUndoOffer(binding, shown)?.fraction, 1 / 3);
  const expired = policy.update({...frame, nowMs: 6001, positionSeconds: 119, playing: false})!.view;
  live = {...shown, presentation: {...shown.presentation!, policy: expired}};
  assert.equal(watchUndoOffer(binding, shown), null);
  live = {...shown, presentation: {...shown.presentation!, expanded: false}};
  assert.equal(watchUndoOffer(binding, shown), null);
  live = {...shown, fence: {...fence, generation: 2}};
  assert.equal(watchUndoOffer(binding, shown), null);
});

test('the credits card sends only its current closing marker to skip and never reuses dismiss for Watch credits', async () => {
  const policy = new PlaybackPolicy();
  policy.begin(fence, [{id: 'credits', kind: 'credits', startSeconds: 500, endSeconds: 550, automaticSafe: true}], 600);
  const postPlay = {nextEntryId: 'next', available: true, reason: 'ready' as const, countdownSeconds: 10, autoplay: true, passoutCheckDue: false, automaticAdvances: 0, startDuringCredits: false};
  const view = policy.update({fence, nowMs: 0, positionSeconds: 520, durationSeconds: 600, rate: 1, playing: true, expanded: true, ended: false, kind: 'episode', modes: {credits: 'ask'}, postPlay})!.view;
  assert.equal(view.prompt?.kind, 'up-next');
  const shown = snapshot({presentation: {...snapshot().presentation!, policy: view}});
  let live = shown, calls = 0;
  const binding = {getSnapshot: () => live, isCurrent: () => true, reportError: (error: unknown) => { throw error; }, policyAction: async (captured: QueuePlaybackFence, action: {kind: 'skip' | 'dismiss' | 'undo'; markerId: string}) => {
    calls++; assert.deepEqual(action, {kind: 'skip', markerId: 'credits'});
    assert.equal(policy.skip(captured, action.markerId)?.positionSeconds, 550);
  }};
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'dismiss', markerId: 'credits'}), false);
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'foreign'}), false);
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'credits'}), true);
  live = {...shown, fence: {...fence, generation: 2}};
  assert.equal(await dispatchWatchMarker(binding, shown, {kind: 'skip', markerId: 'credits'}), false);
  assert.equal(calls, 1);
});

test('the version chooser follows actual grouped options order and permits one remaining failure choice', async () => {
  const version = (id: string): MediaVersion => ({id, label: 'transport label must not become chooser copy', durationMs: 200000, parts: [{id, index: 0, durationMs: 200000}], video: [], audio: [], subtitles: []});
  const options: PlaybackOptions = {itemId: 'item', kind: 'movie', versions: [version('failed'), version('remaining')], chapters: [], markers: [], qualities: [], resume: {positionMs: 123456, partIndex: 0}};
  const choices = {options, versions: options.versions.slice(1), selectedVersionId: 'failed', failedVersionId: 'failed', fence, isCurrent: () => true};
  const rows = [{id: 'remaining', heading: '1080p', detail: 'Dolby Digital 5.1, 9.8 GB'}];
  let calls = 0;
  const binding = {choices, rows, reportError() {}, playVersion: async (request: {choices: typeof choices; versionId: string; signal: AbortSignal}) => {
    calls++; assert.equal(request.choices, choices); assert.equal(request.choices.options, options);
    assert.equal(request.versionId, 'remaining'); assert.deepEqual(Object.keys(request).sort(), ['choices', 'signal', 'versionId']);
  }};
  assert.equal(watchVersionRows(binding), rows);
  assert.equal(await dispatchWatchVersion(binding, 'failed', new AbortController().signal), false);
  assert.equal(await dispatchWatchVersion(binding, 'remaining', new AbortController().signal), true);
  assert.equal(calls, 1);
  assert.equal(options.resume?.positionMs, 123456);
  assert.equal(Object.hasOwn(choices, 'rememberedVersion'), false);
});

test('a chooser cannot dispatch stale, aborted, reordered or unoffered version rows', async () => {
  const version = (id: string): MediaVersion => ({id, label: '', durationMs: 1, parts: [{id, index: 0, durationMs: 1}], video: [], audio: [], subtitles: []});
  const options: PlaybackOptions = {itemId: 'item', kind: 'movie', versions: [version('one'), version('two')], chapters: [], markers: [], qualities: []};
  let current = true, calls = 0;
  const choices = {options, versions: options.versions, isCurrent: () => current};
  const rows = [{id: 'one', heading: '4K Dolby Vision', detail: ''}, {id: 'two', heading: '1080p', detail: ''}];
  const binding = {choices, rows, reportError() {}, playVersion: async () => { calls++; }};
  for (const invalid of [rows.toReversed(), rows.slice(1), [{...rows[0]!, id: 'other'}, rows[1]!]]) assert.equal(watchVersionRows({...binding, rows: invalid}), null);
  assert.equal(watchVersionRows({...binding, choices: {...choices, versions: options.versions.toReversed()}}), null);
  const abort = new AbortController(); abort.abort();
  assert.equal(await dispatchWatchVersion(binding, 'one', abort.signal), false);
  assert.equal(await dispatchWatchVersion(binding, 'other', new AbortController().signal), false);
  current = false;
  assert.equal(await dispatchWatchVersion(binding, 'one', new AbortController().signal), false);
  assert.equal(calls, 0);
});

test('unknown canonical version text preserves the actual row without a label or placeholder substitute', async () => {
  const version: MediaVersion = {id: 'one', label: 'legacy label must not fill missing canonical facts', durationMs: 1, parts: [{id: 'one', index: 0, durationMs: 1}], video: [], audio: [], subtitles: []};
  const options: PlaybackOptions = {itemId: 'item', kind: 'movie', versions: [version], chapters: [], markers: [], qualities: []};
  const choices = {options, versions: options.versions, isCurrent: () => true};
  const rows = [{id: 'one', heading: '', detail: ''}];
  let calls = 0;
  const binding = {choices, rows, reportError() {}, playVersion: async (request: {choices: typeof choices; versionId: string; signal: AbortSignal}) => {
    calls++; assert.equal(request.choices, choices); assert.equal(request.choices.versions[0], version); assert.equal(request.versionId, 'one');
  }};
  assert.equal(watchVersionRows(binding), rows);
  assert.equal(await dispatchWatchVersion(binding, 'one', new AbortController().signal), true);
  assert.equal(calls, 1);
  assert.deepEqual(rows, [{id: 'one', heading: '', detail: ''}]);
});

test('chooser completion waits for the real start and a closed chooser aborts its retained command', async () => {
  const version: MediaVersion = {id: 'one', label: '', durationMs: 1, parts: [{id: 'one', index: 0, durationMs: 1}], video: [], audio: [], subtitles: []};
  const options: PlaybackOptions = {itemId: 'item', kind: 'movie', versions: [version], chapters: [], markers: [], qualities: []};
  let release = () => {}, settled = false;
  const wait = new Promise<void>(resolve => { release = resolve; });
  const binding = {choices: {options, versions: options.versions, isCurrent: () => true}, rows: [{id: 'one', heading: '1080p', detail: ''}], reportError() {}, playVersion: async () => wait};
  const abort = new AbortController(), pending = dispatchWatchVersion(binding, 'one', abort.signal).then(started => { settled = true; return started; });
  await Promise.resolve(); assert.equal(settled, false);
  abort.abort(); release();
  assert.equal(await pending, false);
});

test('the actual screen renders one empty picture target and real identity, without owning media', async context => {
  const loader = registerHooks({load(url, context, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  context.after(() => loader.deregister());
  const {WatchScreen} = await import('../src/screens/watch/WatchScreen.tsx');
  const shown = snapshot();
  const html = renderToStaticMarkup(createElement(WatchScreen, {
    binding: {runtime: {getSnapshot: () => shown, bufferedRanges: () => [], onAction: async () => { throw new Error('render_must_not_command'); }} as never, artwork: {} as never, environment: {t: (key: string) => key} as never, skips: {back: 10, forward: 30}, reportError: error => { throw error; }},
    snapshot: shown, expanded: true, surfaceSlotRef: {current: null}, onCollapse() {},
  }));
  assert.equal((html.match(/data-watch-picture-slot/g) ?? []).length, 1);
  assert.equal((html.match(/data-watch-retained-body/g) ?? []).length, 1);
  assert.doesNotMatch(html, /<video|<audio|Movie night|1080p/);
  assert.match(html, /<h2>Title<\/h2>/);
  assert.match(html, /aria-label="Pause"/);
  const dock = new PlaybackDock(fence.playback.scope), playback = {kind: 'video' as const, fence, queue: null, positionSeconds: 30, playing: true, expanded: true};
  dock.start(playback);
  dock.beginRemote({id: 'cast', kind: 'cast', phase: 'connecting', sequence: '1', playback, positionSeconds: 0}, 0);
  const pending = snapshot({dock: dock.getSnapshot()});
  const render = (value: WebPlaybackRuntimeSnapshot) => renderToStaticMarkup(createElement(WatchScreen, {
    binding: {runtime: {getSnapshot: () => value, bufferedRanges: () => []} as never, artwork: {} as never, environment: {t: (key: string) => key} as never, skips: {back: 10, forward: 30}, reportError: error => { throw error; }},
    snapshot: value, expanded: true, surfaceSlotRef: {current: null}, onCollapse() {},
  }));
  assert.doesNotMatch(render(pending), /class="watch" hidden/);
  dock.remote({id: 'cast', kind: 'cast', phase: 'playing', sequence: '2', playback, positionSeconds: 35}, 1000);
  const closed = render(snapshot({dock: dock.getSnapshot()}));
  assert.match(closed, /class="watch" hidden/);
  // A-03: a closed root never carries visible controls, so the commit that shows it can't paint them at full opacity.
  assert.match(closed, /data-controls-visible="false"/);
});
