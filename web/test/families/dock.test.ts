import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackDock, type DockPlayback, type PlaybackFence, type QueuePlaybackFence, type ChannelPlaybackFence} from '../../../packages/player-core/src/index.ts';
import {dockView, commandFor, dispatchDockAction, actionCurrent, seekTarget, progressOf} from '../../src/kit/dock/model.ts';
import {PlayerMorph, GestureRelease} from '../../src/kit/dock/motion.ts';
import type {DockAction, DockPresentation} from '../../src/kit/dock/types.ts';
import {playbackIdentity} from '../../../packages/client-core/src/presentation/kit-model/index.ts';
import {defaultI18n} from '@i18n';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from '../transform-tsx.mjs';

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'viewer', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const fence: QueuePlaybackFence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'source', entryId: 'entry', generation: 1};
const channelFence: ChannelPlaybackFence = {kind: 'channel', channelKind: 'live-source', playback: {scope, run: 2}, sessionId: 'live-session', sourceId: 'guide-source', channelId: 'channel', channelGeneration: 'guide-1', generation: 1};
type QueueFixture = Extract<DockPlayback, {kind: 'video' | 'music' | 'audiobook'}>;
const playback = (kind: QueueFixture['kind'] = 'video', over: Partial<Omit<QueueFixture, 'kind'>> = {}): QueueFixture => ({kind, fence, queue: null, positionSeconds: 14, playing: true, expanded: false, ...over});
const presentation = (source: PlaybackFence = fence): DockPresentation => ({fence: source, identity: source.kind === 'channel' ? {id: source.channelId, sourceId: source.sourceId, kind: 'channel', title: 'Channel programme', subtitle: []} : {id: source.itemId, kind: 'video', title: 'Title', subtitle: []}, durationSeconds: 100, controls: ['play', 'pause', 'seek'], skipBackSeconds: 10, skipForwardSeconds: 30});

test('a presentation from a retired source cannot expose a current dock', () => {
  const dock = new PlaybackDock(scope); dock.start(playback());
  assert.equal(dockView(dock.getSnapshot(), [presentation({...fence, generation: 2})]), null);
  assert.equal(dockView(dock.getSnapshot(), [presentation()])?.playback.fence.sourceId, 'source');
  dock.expand(fence); assert.equal(dockView(dock.getSnapshot(), [presentation()]), null);
});
test('a late local renderer callback cannot control a new source or a remote takeover', () => {
  const dock = new PlaybackDock(scope), delivered: DockAction[] = [], binding = {getSnapshot: dock.getSnapshot, onAction: (action: DockAction) => delivered.push(action)};
  dock.start(playback()); const rendered = dockView(dock.getSnapshot(), [presentation()])!, action = commandFor(rendered, {kind: 'seek', seconds: 37});
  assert.equal(dispatchDockAction(binding, action), true); assert.equal(delivered.length, 1);
  const replacement = {...fence, sourceId: 'new-source', generation: 2}; dock.start(playback('video', {fence: replacement}));
  assert.equal(dispatchDockAction(binding, action), false);
  dock.beginRemote({id: 'remote', kind: 'cast', sequence: '1', phase: 'playing', playback: playback('video', {fence: replacement}), positionSeconds: 37}, 0);
  assert.equal(dispatchDockAction(binding, {...action, fence: replacement}), false); assert.equal(delivered.length, 1);
});
test('remote control callbacks are fenced by remote identity and exact report sequence', () => {
  const dock = new PlaybackDock(scope), remote = {id: 'remote', kind: 'cast' as const, sequence: '1', phase: 'playing' as const, playback: playback(), positionSeconds: 21};
  dock.beginRemote(remote, 0); const rendered = dockView(dock.getSnapshot(), [presentation()])!, command = commandFor(rendered, {kind: 'pause'});
  assert.equal(actionCurrent(dock.getSnapshot(), command), true);
  dock.remote({...remote, sequence: '2', positionSeconds: 22}, 1); assert.equal(actionCurrent(dock.getSnapshot(), command), false);
  dock.dismissRemote(remote.id); assert.equal(actionCurrent(dock.getSnapshot(), command), false);
});
test('bookmark actions target only the retained bookmark and never a different viewer', () => {
  const dock = new PlaybackDock(scope), audioFence = {...fence, sessionId: 'audio', itemId: 'song', entryId: 'song'};
  dock.start(playback('music', {fence: audioFence, positionSeconds: 107})); dock.start(playback('video', {fence: {...fence, playback: {...fence.playback, run: 2}}}));
  assert.equal(actionCurrent(dock.getSnapshot(), {kind: 'restore-audio', fence: audioFence}), true);
  assert.equal(actionCurrent(dock.getSnapshot(), {kind: 'restore-audio', fence}), false);
  dock.reset({...scope, profileId: 'other'}); assert.equal(actionCurrent(dock.getSnapshot(), {kind: 'restore-audio', fence: audioFence}), false);
});
test('live dock seeking stays within the actual rewind window and rejects nonfinite commands', () => {
  const dock = new PlaybackDock(scope); dock.start({kind: 'live', fence: channelFence, queue: null, positionSeconds: 950, playing: true, expanded: false});
  const view = dockView(dock.getSnapshot(), [{...presentation(channelFence), durationSeconds: 1200, seekWindow: {startSeconds: 900, endSeconds: 1000}}])!;
  assert.equal(seekTarget(view, 0), 900); assert.equal(seekTarget(view, 1200), 1000); assert.equal(progressOf(view), .5); assert.equal(seekTarget(view, NaN), null);
  let calls = 0; const binding = {getSnapshot: dock.getSnapshot, onAction: () => { calls++; }};
  assert.equal(dispatchDockAction(binding, commandFor(view, {kind: 'seek', seconds: NaN})), false);
  assert.equal(dispatchDockAction(binding, commandFor(view, {kind: 'volume', volume: Infinity})), false); assert.equal(calls, 0);
});
test('live commands keep channel identity and reject a new guide generation or source', () => {
  const dock = new PlaybackDock(scope), active: DockPlayback = {kind: 'live', fence: channelFence, queue: null, positionSeconds: 950, playing: true, expanded: false};
  dock.start(active);
  const view = dockView(dock.getSnapshot(), [presentation(channelFence)]);
  assert.ok(view);
  const command = commandFor(view, {kind: 'pause'});
  assert.equal(command.fence.kind, 'channel');
  assert.equal('itemId' in command.fence, false);
  assert.equal('entryId' in command.fence, false);
  assert.equal(actionCurrent(dock.getSnapshot(), command), true);
  dock.start({...active, fence: {...channelFence, channelGeneration: 'guide-2'}});
  assert.equal(actionCurrent(dock.getSnapshot(), command), false);
  dock.start({...active, fence: {...channelFence, sourceId: 'other-guide-source'}});
  assert.equal(actionCurrent(dock.getSnapshot(), command), false);
});
test('the player spring retargets without dropping its current position or velocity', () => {
  const spring = new PlayerMorph(0); spring.retarget(1, 0); const before = spring.sample(140);
  assert.ok(before.position > 0 && before.position < 1); assert.ok(before.velocity > 0);
  spring.retarget(0, 140); const after = spring.sample(140);
  assert.equal(after.position, before.position); assert.equal(after.velocity, before.velocity);
  assert.ok(spring.sample(150).position > after.position, 'momentum survives reversal before the spring turns');
  assert.ok(Math.abs(spring.sample(2500).position) < .002); assert.ok(spring.settled(2500));
});
test('a finger release passes its physical speed to the same spring', () => {
  const spring = new PlayerMorph(0); spring.track(.42, .9, 100); spring.retarget(1, 100, .9);
  assert.equal(spring.sample(100).position, .42); assert.equal(spring.sample(100).velocity, .9);
  const dx = (spring.sample(100.01).position - spring.sample(100).position) / .00001;
  assert.ok(Math.abs(dx - .9) < .005);
});
test('dock release commits by authored distance or velocity and can reverse a nearly completed drag', () => {
  assert.deepEqual(GestureRelease.player(.34, 0, 600), {target: 0, velocity: 0});
  assert.equal(GestureRelease.player(.36, 0, 600).target, 1);
  assert.equal(GestureRelease.player(.1, 601, 600).target, 1);
  assert.equal(GestureRelease.player(.9, -601, 600).target, 0);
  assert.equal(GestureRelease.dismiss(120, 0, 374).dismiss, false);
  assert.equal(GestureRelease.dismiss(125, 0, 374).dismiss, true);
  assert.deepEqual(GestureRelease.dismiss(5, -801, 374), {dismiss: true, target: -374, velocity: -801});
});

test('a finished dock offers Replay and Next while preventing Forward past the end', async () => {
  registerHooks({load(url, context, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  const {DockBar} = await import('../../src/kit/dock/DockBar.tsx');
  const dock = new PlaybackDock(scope); dock.start(playback('video', {positionSeconds: 100, playing: false}));
  const view = dockView(dock.getSnapshot(), [{...presentation(), finished: true, controls: ['play', 'pause', 'replay', 'previous', 'next', 'seek']}])!;
  const props = {view, binding: {getSnapshot: dock.getSnapshot, onAction() {}}, videoSurface: true};
  const html = renderToStaticMarkup(createElement(DockBar, props));
  assert.match(html, /aria-label="Replay"/);
  assert.match(html, /aria-label="Forward 30 seconds"[^>]*disabled/);
  assert.doesNotMatch(html, /aria-label="Next"[^>]*disabled/);
  assert.throws(() => renderToStaticMarkup(createElement(DockBar, {...props, videoSurface: false})), /persistent media surface/);
});

test('a bookmark notice appears after300ms and expires after its eight-second action lifetime', async () => {
  const {DockNotice} = await import('../../src/kit/dock/DockNotice.tsx');
  const dock = new PlaybackDock(scope), saved = playback('music');
  dock.start(saved); dock.start(playback('video', {fence: {...fence, playback: {...fence.playback, run: 2}}}));
  const props = {notice: {kind: 'paused-audio' as const, playback: saved, presentation: presentation(), changedAtMs: 100}, binding: {getSnapshot: dock.getSnapshot, onAction() {}}};
  assert.equal(renderToStaticMarkup(createElement(DockNotice, {...props, nowMs: 399})), '');
  assert.match(renderToStaticMarkup(createElement(DockNotice, {...props, nowMs: 400})), /Resume music/);
  assert.match(renderToStaticMarkup(createElement(DockNotice, {...props, nowMs: 8399})), /Resume music/);
  assert.equal(renderToStaticMarkup(createElement(DockNotice, {...props, nowMs: 8400})), '');
});

test('a restored paused phone song states Paused beside its canonical artist', async () => {
  const {Identity} = await import('../../src/kit/dock/parts.tsx');
  const dock = new PlaybackDock(scope); dock.start(playback('music', {playing: false}));
  const view = dockView(dock.getSnapshot(), [{...presentation(), identity: {id: fence.itemId, kind: 'music', title: 'Song', subtitle: [{id: 'artist', role: 'identity', text: 'Artist'}]}}])!;
  const html = renderToStaticMarkup(createElement(Identity, {view, binding: {getSnapshot: dock.getSnapshot, onAction() {}}, phone: true}));
  assert.match(html, /Artist/);
  assert.match(html, /data-fact-role="status">Paused/);
});

test('desktop subtitle tools select the exact canonical reference glyph for local and remote playback', async () => {
  const {EndControls} = await import('../../src/kit/dock/parts.tsx');
  const dock = new PlaybackDock(scope); dock.start(playback());
  const model = {...presentation(), controls: ['subtitles-audio'] as const};
  for (const remote of [false, true]) {
    if (remote) dock.beginRemote({id: 'receiver', kind: 'cast', sequence: '1', phase: 'playing', playback: playback(), positionSeconds: 14}, 0);
    const view = dockView(dock.getSnapshot(), [model])!;
    const html = renderToStaticMarkup(createElement(EndControls, {view, binding: {getSnapshot: dock.getSnapshot, onAction() {}}}));
    assert.match(html, /aria-label="Subtitles and audio"/);
    assert.match(html, /href="#d-cc"/);
    assert.doesNotMatch(html, /href="#i-cc"/);
  }
});

test('desktop paused music punctuates the canonical artist and omits punctuation when artist is absent', async () => {
  const {DockNotice} = await import('../../src/kit/dock/DockNotice.tsx');
  const dock = new PlaybackDock(scope), saved = playback('music');
  dock.start(saved); dock.start(playback('video', {fence: {...fence, playback: {...fence.playback, run: 2}}}));
  for (const artist of ['The Weeknd', undefined]) {
    const identity = playbackIdentity({kind: 'music', item: {kind: 'song', id: 'song', title: 'Hardest To Love', artist}}, 'dock', {t: defaultI18n.t});
    const html = renderToStaticMarkup(createElement(DockNotice, {notice: {kind: 'paused-audio', playback: saved, presentation: {...presentation(), identity}, changedAtMs: 100}, binding: {getSnapshot: dock.getSnapshot, onAction() {}}, nowMs: 400}));
    if (artist) assert.match(html, /Hardest To Love, The Weeknd/);
    else assert.doesNotMatch(html, /Hardest To Love,/);
    assert.match(html, /Resume music/);
  }
});

// O-159: the starting dock is the same bar in the connecting look: title, art under a pending
// ring, "Starting…", Stop only. It never claims playing (no Pause, no progress, no expand).
test('a starting dock draws the pressed title with a pending ring and only Stop, on desktop and phone', async () => {
  registerHooks({load(url, context, next) {
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  }});
  const {DockHost} = await import('../../src/kit/dock/DockHost.tsx');
  const dock = new PlaybackDock(scope);
  let stopped = 0;
  const starting = {key: '1', kind: 'video' as const, identity: {id: 'item', kind: 'movie' as const, title: 'The pressed film', subtitle: []}, stop: () => {stopped++;}};
  for (const layout of ['desktop', 'phone'] as const) {
    const html = renderToStaticMarkup(createElement(DockHost, {state: dock.getSnapshot(), presentations: [], binding: {getSnapshot: dock.getSnapshot, onAction() {}}, layout, starting}));
    assert.match(html, /The pressed film/, layout);
    assert.match(html, /Starting…/, layout);
    assert.match(html, /data-pending/, layout);
    assert.match(html, layout === 'phone' ? /aria-label="Stop"/ : />Stop</, layout);
    assert.doesNotMatch(html, /aria-label="(Pause|Play|Next|Previous|Expand)"/, layout);
    assert.doesNotMatch(html, /progress|slider/i, layout);
  }
  // The real occupant always wins: a starting dock never draws over an actual dock.
  dock.start(playback('music'));
  const html = renderToStaticMarkup(createElement(DockHost, {state: dock.getSnapshot(), presentations: [presentation()], binding: {getSnapshot: dock.getSnapshot, onAction() {}}, layout: 'desktop', videoSurface: true, starting}));
  assert.doesNotMatch(html, /The pressed film/);
  assert.equal(stopped, 0);
});

// A Live start the server reports as waiting on its source says so, and still offers Stop.
test('a starting dock waiting on the channel source says so instead of "Starting…" and still offers Stop', async () => {
  const {DockHost} = await import('../../src/kit/dock/DockHost.tsx');
  const dock = new PlaybackDock(scope);
  const starting = {key: '1', kind: 'video' as const, identity: {id: 'channel', kind: 'channel' as const, title: 'News', subtitle: []} as never, stop: () => {}, waiting: 'channel_source' as const};
  for (const layout of ['desktop', 'phone'] as const) {
    const html = renderToStaticMarkup(createElement(DockHost, {state: dock.getSnapshot(), presentations: [], binding: {getSnapshot: dock.getSnapshot, onAction() {}}, layout, starting}));
    assert.match(html, /The channel’s source isn’t responding\. Still trying…/, layout);
    assert.doesNotMatch(html, /Starting…/, layout);
    assert.match(html, layout === 'phone' ? /aria-label="Stop"/ : />Stop</, layout);
    assert.doesNotMatch(html, /aria-label="(Pause|Play|Next|Previous|Expand)"/, layout);
  }
});
