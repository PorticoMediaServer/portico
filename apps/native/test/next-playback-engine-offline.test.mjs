// Node 24: node --experimental-vm-modules --experimental-transform-types --import ./test/register.mjs test/next-playback-engine-offline.test.mjs
// Runs the real runtime command/fact methods and coordinator against explicit service/engine
// ports. Full PlaybackService/server/native integration remains in next-playback-runtime.test.ts.
import test from 'node:test';
import assert from 'node:assert/strict';
import {sourceModule} from './helpers/offline-source-module.mjs';
import * as recovery from '../src-next/platform/playback/playback-recovery.ts';
import * as audio from '../src-next/platform/playback/audio-track-selection.ts';
import * as offline from '../src-next/platform/playback/offline-tracks.ts';
import * as media from '../src-next/platform/playback/engine-media.ts';
import * as captions from '../src-next/platform/playback/caption-style.ts';
import * as external from '../src-next/platform/playback/external-presentation.ts';
import * as failures from '../src-next/platform/playback/playback-failure.ts';
import * as styles from '../src-next/platform/playback/subtitle-style-choices.ts';
import * as intent from '../src-next/platform/playback/start-intent.ts';
import * as videoPolicy from '../src-next/platform/playback/video-policy.ts';

const unused = () => {throw new Error('Unexpected boundary call in the focused engine test.');};
const serviceText = key => key;
const {NativePlaybackCoordinator} = await sourceModule(new URL('../src-next/platform/playback/NativePlaybackCoordinator.ts', import.meta.url), {
  '../../../../../packages/client-core/src/presentation/service-text': {serviceText},
});
const {NativePlaybackRuntime} = await sourceModule(new URL('../src-next/platform/playback/runtime.ts', import.meta.url), {
  'react-native': {AppState: {}, NativeModules: {}},
  './start-failure': {startFailureHttp: unused}, './capacity-retry': {capacityRetryHttp: unused}, './listening-controls': {createNativeListeningReadPort: unused},
  '../../../../../packages/client-core/src/index': {PlaybackService: unused}, '../../../../../packages/client-core/src/detail': {DetailService: unused},
  '../../../../../packages/client-core/src/media-item': {parseMediaItem: unused}, '../../../../../packages/contracts/src/routes': {encodedRoutePath: unused, routePath: unused},
  '../../../../../packages/client-core/src/playback-v1/sessions': {SessionsClient: unused}, '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: unused},
  '../../../../../packages/client-core/src/playback-v1/http': {call: unused}, '../../../../../packages/client-core/src/playback-v1/options': {PlaybackOptionsClient: unused},
  '../../../../../packages/client-core/src/playback-v1/v1-queue-player': {V1QueuePlayer: unused, serverSupportsQueueTransitions: unused, queueVideoKind: unused},
  '../../../../../packages/client-core/src/playback-v1/queue': {QueueClient: unused},
  '../../../../../packages/client-core/src/playback-v1/channel-control': {V1ChannelControl: unused, serverSupportsV1Channels: unused},
  '../../../../../packages/client-core/src/playback/linear-protocol': {channelSeconds: unused},
  '../../../../../packages/client-core/src/guide/directory': {fetchGuideSourceSummaries: unused}, '../../../../../packages/client-core/src/guide/legacy-source': {legacyChannel: unused},
  '../../../../../packages/client-core/src/playback-v1/types': {obj: unused, parseDiscTitleRef: unused, parseQueue: unused, parseSession: unused},
  '../../../../../packages/client-core/src/playback-v1/quality': {qualityOptionRequest: unused}, '../../../../../packages/client-core/src/subtitles': {SubtitleService: unused},
  '../../../../../packages/client-core/src/subtitle-originals': {forcedChoiceOf: unused, parseSubtitleLines: unused}, './forced-image': {forcedImageLinesPath: unused},
  '../../../../../packages/client-core/src/presentation/service-text': {serviceText},
  '../../../../../packages/player-core/src': {PlaybackDock: unused, PlayerSessionChoices: unused, activeSegment: unused, skipsAutomatically: unused,
    skipTarget: unused, parseQueueSnapshot: unused, playbackFence: unused, sameFence: unused, sameScope: unused},
  './NativePlaybackCoordinator': {NativePlaybackCoordinator}, './external-presentation': external,
  './native-playback': {NativePlayback: unused, automaticSteps: unused}, './scope-http': {PlaybackScopeHttp: unused},
  './preparing-owner': {captureNativePreparingOwner: unused, nativePreparingStarts: unused},
  './start-presentation': {captureNativeStartPresentation: unused, nativeFirstVideoFrame: unused}, './offline-playback': {NativeOfflinePlayback: unused},
  './caption-style': captions, './subtitle-controls': {boundSubtitleControls: unused}, './subtitle-file-upload': {uploadPickedSubtitle: unused},
  './engine-media': media, './playback-failure': failures, './playback-recovery': recovery, './audio-track-selection': audio, './offline-tracks': offline,
  './native-listening-kinds': {nativeListeningFor: unused, serverPlaybackV1Kinds: unused}, './selection': {selectionSegments: unused, selectionStartSeconds: unused},
  './video-exit-permission': {retainNativeVideoExitImage: unused},
  './subtitle-style-choices': styles, './start-intent': intent, './video-policy': videoPolicy,
});
const drain = async () => {for (let i = 0; i < 12; i++) await Promise.resolve();};
const plain = value => JSON.parse(JSON.stringify(value));
function clock() {
  let time = 0, serial = 0; const jobs = new Map();
  return {setTimer(fn, ms) {const id = ++serial; jobs.set(id, {at: time + ms, fn}); return id;}, clearTimer(id) {jobs.delete(id);},
    advance(ms) {const end = time + ms; for (;;) {const job = [...jobs.entries()].filter(([, j]) => j.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
      if (!job) break; jobs.delete(job[0]); time = job[1].at; job[1].fn();} time = end;}, jobs};
}

async function fixture(engine = 'mpv', local = false, withOptions = true) {
  const runtime = Object.create(NativePlaybackRuntime.prototype), timers = clock(), commands = [], observations = [], listeners = new Set();
  let current = true, listener, sequence = 0, reportedAudio, probes = 0;
  const state = {intentId: 1, playbackRun: 1, phase: 'ready', intent: 'playing', itemId: 'item', positionSeconds: 30,
    session: {id: local ? 'local:copy:1' : 'session', generation: 1, mode: local ? 'local' : 'direct', streamUrl: local ? 'file:///Downloads/copy/media' : 'https://invalid.test/media'}};
  const accepted = {id: 'session', itemId: 'item', kind: 'vod', role: 'local', versionId: 'version', presentation: {generation: 1, mode: 'direct', audioTrackId: 'a7', quality: {mode: 'original'}}};
  const settings = {engine: 'automatic', videoRate: 1, audiobookRate: 1, passthroughFormats: {dd: 'off'},
    subtitles: {size: 'medium', color: 'white', background: 'translucent', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false}};
  const service = {getSnapshot: () => state, subscribe(fn) {listeners.add(fn); return () => listeners.delete(fn);},
    recovering(id, on) {assert.equal(id, state.intentId); state.recovering = on || undefined;},
    ready(id) {assert.equal(id, state.intentId); state.phase = 'ready';},
    fact(id, position) {assert.equal(id, state.intentId); state.positionSeconds = position;},
    fail(id, message) {assert.equal(id, state.intentId); state.phase = 'error'; state.intent = 'paused'; state.error = message;},
    pause() {state.intent = 'paused';}, engineLost() {},
  };
  let instances = 0, currentInstance = 'instance';
  const transport = {acquire: async () => {
    currentInstance = ++instances === 1 ? 'instance' : `${engine}-${instances}`;
    return {lease: `lease-${instances}`, instanceId: currentInstance};
  }, release: async () => {},
    command: async command => {commands.push(command);}, subscribe(fn) {listener = fn; return () => {listener = undefined;};}};
  const coordinator = new NativePlaybackCoordinator(service, transport, () => 'owner', () => state.session.streamUrl, fact => runtime.acceptFact(fact),
    {initial: engine, choose: () => engine, transport: () => transport});
  Object.assign(runtime, {retired: false, factDepth: 0, factPublication: false, playback: service, coordinator, expanded: true, displaySubtitles: true,
    options: withOptions ? {itemId: 'item', versions: [{id: 'version', audio: [{id: 'a1'}, {id: 'a7'}]}]} : undefined,
    recovery: new recovery.NativePlaybackRecovery(timers), audioTrackSelection: new audio.NativeAudioTrackSelection(),
    subtitleStyles: new styles.NativeSubtitleStyleChoices(),
    choices: {get: (_key, _playback, fallback) => fallback, has: () => false},
    o: {settings: () => settings, nativeLocalSubtitleTiming: true, engineFeatures: () => ({subtitleRendering: ['text']}), engines: {transport: () => transport},
      api: {mediaUrl: unused, getRouteConnection: () => ({}), recoverRoute: async () => {probes++;}}},
    path: {setRate() {}, setTracks(id) {reportedAudio = id;}, setAutomatic() {}, engineFact(fact) {observations.push(fact);}},
    active: () => current, currentSession: () => local ? undefined : accepted,
    fence: () => ({kind: local ? 'local' : 'queue', sessionId: state.session.id, generation: state.session.generation, playback: {run: state.playbackRun}}),
    local: () => ({kind: 'video'}), automaticSkip() {}, publish() {},
  });
  const off = coordinator.subscribe(() => runtime.applyOptions());
  runtime.applyOptions(); coordinator.start(); await drain();
  const emit = (type, extra = {}) => {const command = commands.at(-1); listener({type, instanceId: currentInstance, binding: command.binding,
    lastCommandRevision: command.commandRevision, factSequence: ++sequence, position: state.positionSeconds, paused: state.intent === 'paused', ...extra});};
  return {runtime, coordinator, state, accepted, commands, timers, observations, emit, settings,
    async switchTo(next) {engine = next; coordinator.reconsiderEngine(); await drain();},
    captureFacts() {const send = listener, command = commands.at(-1), instanceId = currentInstance;
      return (type, extra = {}) => send({type, instanceId, binding: command.binding,
        lastCommandRevision: command.commandRevision, factSequence: ++sequence, position: state.positionSeconds, paused: false, ...extra});},
    reportedAudio: () => reportedAudio, probes: () => probes, retire: () => {current = false; runtime.recovery.clear();},
    dispose() {runtime.recovery.clear(); off(); coordinator.stop();}};
}

test('runtime sends the accepted nonfirst mpv audio and reports only its applied result', async t => {
  const f = await fixture(); t.after(() => f.dispose());
  const command = f.commands.at(-1); assert.equal(command.audioTrack.streamIndex, 7); assert.equal(f.reportedAudio(), undefined);
  assert.equal(f.runtime.audioTrackSelection.getSnapshot().state, 'pending');
  f.emit('audioTrackApplied', {audioTrackRevision: command.audioTrack.revision, streamIndex: 1}); assert.equal(f.reportedAudio(), undefined);
  f.emit('audioTrackApplied', {audioTrackRevision: command.audioTrack.revision, streamIndex: 7}); assert.equal(f.reportedAudio(), 'a7');
  f.accepted.presentation.mode = 'stream'; f.runtime.applyOptions(); await drain();
  assert.equal(f.commands.at(-1).audioTrack, undefined, 'stream delivery carries no direct file selection');
});

test('direct mpv starts with its accepted audio before options arrive and refuses contradictory offers', async t => {
  const f = await fixture('mpv', false, false); t.after(() => f.dispose());
  assert.ok(f.commands.length > 0); assert.ok(f.commands.every(command => command.audioTrack?.streamIndex === 7));
  f.runtime.options = {itemId: 'item', versions: [{id: 'version', audio: [{id: 'a1'}]}]};
  f.runtime.applyOptions(); await drain();
  assert.equal(f.state.phase, 'error'); assert.equal(f.commands.at(-1).paused, true);
});

test('a lone network failure plus stationary progress still reaches the runtime Retry error', async t => {
  const f = await fixture(); t.after(() => f.dispose());
  f.emit('error', {deliveryError: true, networkError: true}); assert.equal(f.state.recovering, true);
  f.timers.advance(1000); assert.equal(f.probes(), 1);
  const reports = f.observations.length; f.emit('progress'); assert.equal(f.state.recovering, true); assert.equal(f.observations.length, reports);
  f.timers.advance(89_000); assert.equal(f.state.phase, 'error'); assert.equal(f.state.intent, 'paused'); assert.ok(f.state.error);
});

test('verified native readiness clears the same outage while a paused tick does not invent progress', async t => {
  const f = await fixture(); t.after(() => f.dispose());
  f.emit('error', {networkError: true}); f.state.intent = 'paused'; f.emit('progress', {paused: true});
  assert.equal(f.state.positionSeconds, 30); assert.equal(f.state.intent, 'paused'); assert.equal(f.state.recovering, true);
  f.emit('ready'); assert.equal(f.state.recovering, undefined); f.timers.advance(100_000); assert.equal(f.state.phase, 'ready');
});

test('local subtitle and Media3 audio commands use admitted local facts and the global passthrough policy', async t => {
  const f = await fixture('native', true); t.after(() => f.dispose());
  f.runtime.localTracks = new offline.NativeOfflineTracks({entryId: 'copy', itemId: 'item', playId: 'play', fileUrl: f.state.session.streamUrl,
    durationSeconds: 60, startSeconds: 0, extras: {backdropUrl: '', subtitles: [{id: 'captions', revision: 2, format: 'vtt', language: 'en', title: 'English',
      default: true, forced: false, url: 'file:///Downloads/copy/extras/sub.vtt'}]}}, {intentId: 1, sessionId: f.state.session.id, generation: 1});
  f.runtime.applyOptions(); await drain();
  f.emit('mediaInfo', {audioTrackCatalogRevision: 5, audioTracks: [{id: 'local-1', selected: true, codec: 'aac'}, {id: 'local-2', codec: 'aac'}]});
  f.runtime.localTracks.choose({subtitleTrackId: 'captions', subtitleOffsetMs: -500, audioTrackId: 'local-2'});
  f.runtime.applyOptions(); await drain(); const command = f.commands.at(-1);
  assert.equal(command.source, 'file:///Downloads/copy/media'); assert.equal(command.subtitles.documentUrl, 'file:///Downloads/copy/extras/sub.vtt');
  assert.equal(command.subtitles.offsetUs, '-500000'); assert.equal(command.localAudioTrack.trackId, 'local-2'); assert.equal(command.localAudioTrack.catalogRevision, 5);
  assert.deepEqual(plain(command.passthrough), {disabled: false, formats: {dd: 'off'}});
  assert.equal(f.runtime.localTracks.getSnapshot().audioTrackId, 'local-1');
  f.emit('localAudioTrackApplied', {audioTrackRevision: command.localAudioTrack.revision, catalogRevision: 5, trackId: 'local-2'});
  assert.equal(f.runtime.localTracks.getSnapshot().audioTrackId, 'local-2');
  f.settings.overrides = {disablePassthrough: true}; f.runtime.applyOptions(); await drain(); assert.equal(f.commands.at(-1).passthrough.disabled, true);
});

test('Apple local subtitles use the supported rich renderer, saved VTT fallback and session timing', async t => {
  const f = await fixture('native', true); t.after(() => f.dispose());
  f.runtime.o.engines.transport = () => undefined;
  f.runtime.o.engineFeatures = () => ({subtitleRendering: ['text', 'ass', 'pgs']});
  f.runtime.localTracks = new offline.NativeOfflineTracks({entryId: 'copy', itemId: 'item', playId: 'play', fileUrl: f.state.session.streamUrl,
    durationSeconds: 60, startSeconds: 0, extras: {backdropUrl: '', subtitles: [
      {id: 'captions', format: 'ass', language: 'en', title: 'English', default: false, forced: false,
        url: 'file:///Downloads/copy/extras/sub.ass', textUrl: 'file:///Downloads/copy/extras/sub.vtt'},
      {id: 'image', format: 'pgs', language: 'en', title: 'English images', default: false, forced: false, url: 'file:///Downloads/copy/extras/sub.pgs'},
    ]}}, {intentId: 1, sessionId: f.state.session.id, generation: 1});
  f.runtime.applyOptions();
  const tracks = f.runtime.localTracks;
  tracks.choose({subtitleTrackId: 'captions'}); f.runtime.applyOptions(); await drain();
  const command = f.commands.at(-1).subtitles;
  assert.equal(command.documentUrl, 'file:///Downloads/copy/extras/sub.vtt');
  assert.deepEqual(plain(command.render), {kind: 'ass', url: 'file:///Downloads/copy/extras/sub.ass'});
  assert.equal(command.offsetUs, '0'); assert.equal(tracks.getSnapshot().subtitleTimingSupported, true);
  tracks.choose({subtitleOffsetMs: 500}); f.runtime.applyOptions(); await drain();
  assert.equal(f.commands.at(-1).subtitles.offsetUs, '500000');
  assert.equal(f.commands.at(-1).subtitles.localSessionOffsetUs, '500000');
  tracks.choose({subtitleTrackId: 'image'}); f.runtime.applyOptions(); await drain();
  assert.deepEqual(plain(f.commands.at(-1).subtitles.render), {kind: 'pgs', url: 'file:///Downloads/copy/extras/sub.pgs'});
  tracks.choose({subtitleTrackId: null}); f.runtime.applyOptions(); await drain();
  assert.equal(f.commands.at(-1).subtitles.mode, 'off');
  assert.equal(tracks.getSnapshot().subtitleOffsetMs, 0);
});

test('the coordinator drops old-source audio acknowledgements before they reach the runtime', async t => {
  const f = await fixture(); t.after(() => f.dispose()); const command = f.commands.at(-1);
  f.emit('audioTrackApplied', {audioTrackRevision: command.audioTrack.revision, streamIndex: 7, binding: {...command.binding, generation: 0}});
  assert.equal(f.runtime.audioTrackSelection.getSnapshot().state, 'pending'); assert.equal(f.reportedAudio(), undefined);
});

test('offline runtime preserves a proven source choice across engines and fences old engine readback', async t => {
  const f = await fixture('native', true); t.after(() => f.dispose());
  f.runtime.localTracks = new offline.NativeOfflineTracks({entryId: 'copy', itemId: 'item', playId: 'play', fileUrl: f.state.session.streamUrl,
    durationSeconds: 60, startSeconds: 0}, {intentId: 1, sessionId: f.state.session.id, generation: 1});
  const identity = trackId => ({container: 'isobmff', trackId});
  f.runtime.applyOptions(); await drain();
  f.emit('mediaInfo', {audioTrackCatalogRevision: 5, audioTracks: [
    {id: 'local-1', selected: true, sourceTrackIdentity: identity('137')}, {id: 'local-2', sourceTrackIdentity: identity('902')},
  ]});
  f.runtime.localTracks.choose({audioTrackId: 'local-2'}); f.runtime.applyOptions(); await drain();
  const native = f.commands.at(-1).localAudioTrack, retired = f.captureFacts();
  f.emit('localAudioTrackApplied', {audioTrackRevision: native.revision, catalogRevision: 5, trackId: 'local-2'});
  await f.switchTo('mpv');
  assert.equal(f.commands.at(-1).localAudioTrack, undefined); assert.equal(f.commands.at(-1).audioTrack, undefined);
  retired('mediaInfo', {audioTrackCatalogRevision: 9, audioTracks: [{id: 'foreign', sourceTrackIdentity: identity('902')}]});
  assert.deepEqual(f.runtime.localTracks.getSnapshot().audio, []);
  f.emit('mediaInfo', {audioTracks: [{streamIndex: 7, selected: true, sourceTrackIdentity: identity('137')},
    {streamIndex: 1, sourceTrackIdentity: identity('902')}]}); await drain();
  const mpv = f.commands.at(-1).audioTrack;
  assert.equal(mpv.streamIndex, 1); assert.ok(mpv.revision > native.revision);
  retired('localAudioTrackApplied', {audioTrackRevision: native.revision, catalogRevision: 5, trackId: 'local-2'});
  f.emit('audioTrackApplied', {audioTrackRevision: native.revision, streamIndex: 1});
  assert.equal(f.runtime.localTracks.getSnapshot().audioTrackId, undefined);
  f.emit('audioTrackApplied', {audioTrackRevision: mpv.revision, streamIndex: 1});
  assert.equal(f.runtime.localTracks.getSnapshot().audioTrackId, 'a1');
  await f.switchTo('native');
  f.emit('mediaInfo', {audioTrackCatalogRevision: 1, audioTracks: [{id: 'replacement', selected: true, sourceTrackIdentity: identity('902')}]});
  await drain(); const restored = f.commands.at(-1).localAudioTrack;
  assert.equal(restored.catalogRevision, 1); assert.equal(restored.trackId, 'replacement'); assert.ok(restored.revision > mpv.revision);
  assert.equal(f.commands.at(-1).audioTrack, undefined); assert.equal(f.runtime.localTracks.getSnapshot().audioTrackId, undefined);
  f.emit('localAudioTrackApplied', {audioTrackRevision: restored.revision, catalogRevision: 1, trackId: restored.trackId});
  assert.equal(f.runtime.localTracks.getSnapshot().audioTrackId, 'replacement');
});
