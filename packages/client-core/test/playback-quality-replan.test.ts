import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackApiError, PlaybackSessionController, V1PlaybackApi, qualityOptionRequest, qualityOptionSelected, requestInForce, sameQuality, savedQualities, savedQualityRequest,
  type MediaPlayer, type PlayerEvent, type PlayerObservation, type PlayerSource} from '../src/playback-v1/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';

const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate); };

class FakePlayer implements MediaPlayer {
  loads: PlayerSource[] = [];
  state: PlayerObservation['state'] = 'idle';
  generation = 0;
  positionMs = 0;
  private listeners = new Set<(e: PlayerEvent) => void>();
  load(s: PlayerSource) { this.loads.push(s); this.generation = s.generation; this.positionMs = s.startPositionMs; this.state = s.autoplay ? 'playing' : 'paused'; }
  play() { this.state = 'playing'; this.emit('state'); }
  pause() { this.state = 'paused'; this.emit('state'); }
  seek(ms: number) { this.positionMs = ms; this.emit('seeked'); }
  setRate() {}
  selectSidecarSubtitle() {}
  observe(): PlayerObservation { return {generation: this.generation, state: this.state, positionMs: this.positionMs, rate: 1}; }
  onEvent(l: (e: PlayerEvent) => void) { this.listeners.add(l); return () => { this.listeners.delete(l); }; }
  emit(type: 'state' | 'time' | 'seeked') { for (const l of [...this.listeners]) l({type, observation: this.observe()} as PlayerEvent); }
  /** The picture has reached `ms`. */
  at(ms: number) { this.positionMs = ms; this.emit('time'); }
  dispose() { this.listeners.clear(); }
}
const player = () => new FakePlayer();

test('quality: a saved default or a menu entry becomes a request; the menu marks the quality in force', () => {
  assert.deepEqual(savedQualities.slice(0, 3), ['original', 'automatic', '2160p']);
  assert.deepEqual(savedQualityRequest('original'), {mode: 'original'});
  assert.deepEqual(savedQualityRequest('automatic'), {mode: 'automatic'});
  assert.deepEqual(savedQualityRequest('720p-high'), {mode: 'fixed', id: '720p-high'});
  assert.deepEqual(savedQualityRequest('data-saver'), {mode: 'original'}, 'anything unknown is the original');
  assert.deepEqual(savedQualityRequest(undefined), {mode: 'original'});
  assert.deepEqual(qualityOptionRequest({mode: 'fixed', id: '1080p-low', maxHeight: 1080}), {mode: 'fixed', id: '1080p-low'});
  assert.deepEqual(qualityOptionRequest({mode: 'automatic'}), {mode: 'automatic'});
  assert.equal(qualityOptionSelected({mode: 'automatic'}, {mode: 'automatic', id: '720p-high'}), true, 'Automatic is selected whatever step it is on');
  assert.equal(qualityOptionSelected({mode: 'fixed', id: '720p-high'}, {mode: 'automatic', id: '720p-high'}), false);
  assert.equal(qualityOptionSelected({mode: 'fixed', id: '720p-high'}, {mode: 'fixed', id: '720p-high'}), true);
  assert.equal(qualityOptionSelected({mode: 'original'}, undefined), true);
  assert.equal(sameQuality({mode: 'fixed', id: '720p-high'}, {mode: 'fixed', id: '720p-high'}), true);
  // The quality in force is what the next title of a viewing asks for again.
  assert.deepEqual(requestInForce({mode: 'automatic', id: '720p-high'}), {mode: 'automatic', id: '720p-high'});
  assert.deepEqual(requestInForce({mode: 'automatic'}), {mode: 'automatic'});
  assert.deepEqual(requestInForce({mode: 'fixed', id: '1080p-low'}), {mode: 'fixed', id: '1080p-low'});
  assert.deepEqual(requestInForce({mode: 'original'}), {mode: 'original'});
});

test('controller: quality steps, a fixed quality and a player failure re-plan the same session where the player is', async () => {
  const server = new FakePlaybackServer();
  const p = player();
  const controller = new PlaybackSessionController({http: fakeHttp(server), player: p});
  try {
  await controller.start({itemId: 'movie-1'}, {quality: {mode: 'automatic'}, throughputKbps: 30_000});
  const id = controller.getSnapshot().session!.id;
  assert.equal(controller.getSnapshot().session!.presentation.engine, 'native');
  assert.deepEqual(controller.getSnapshot().session!.presentation.quality, {mode: 'automatic'});
  p.at(95_000);
  const down = await controller.stepQuality('down');
  assert.equal(down!.id, id, 'the same session');
  assert.deepEqual(down!.presentation.quality, {mode: 'automatic', id: '2160p'});
  assert.equal(down!.presentation.generation, 2);
  assert.equal(down!.presentation.startPositionMs, 95_000, 'the new presentation starts where the player was, not at the last report');
  assert.equal(p.loads.at(-1)!.generation, 2);
  const up = await controller.stepQuality('up');
  assert.deepEqual(up!.presentation.quality, {mode: 'automatic'});
  const fixed = await controller.setQuality({mode: 'fixed', id: '720p-high'});
  assert.deepEqual(fixed!.presentation.quality, {mode: 'fixed', id: '720p-high'});
  p.at(120_000);
  const recovered = await controller.playerFailed('decode_error', 'MEDIA_ERR_DECODE\n' + 'x'.repeat(900));
  assert.equal(recovered!.id, id);
  assert.equal(recovered!.presentation.generation, fixed!.presentation.generation + 1);
  assert.equal(recovered!.presentation.startPositionMs, 120_000);
  await controller.playerFailed('decode_error');
  await controller.playerFailed('decode_error');
  await assert.rejects(controller.playerFailed('decode_error'), (e: unknown) => e instanceof PlaybackApiError && e.code === 'unsupported_media', 'nothing left to try is said plainly');
  assert.equal(controller.getSnapshot().session!.id, id, 'and the session is left as it is');
  await settle();
  } finally { controller.dispose(); p.dispose(); }
});

test('controller: a quality the measured connection cannot carry is refused and the session plays on', async () => {
  const server = new FakePlaybackServer({neededKbps: 20_000});
  const p = player();
  const controller = new PlaybackSessionController({http: fakeHttp(server), player: p});
  try {
  await controller.start({itemId: 'movie-1'}, {quality: {mode: 'fixed', id: '720p-high'}});
  const before = controller.getSnapshot().session!;
  await assert.rejects(controller.setQuality({mode: 'original'}, 5000), (e: unknown) => e instanceof PlaybackApiError && e.code === 'connection_too_slow');
  assert.equal(controller.getSnapshot().session!.presentation.generation, before.presentation.generation);
  assert.equal(p.loads.length, 1);
  } finally { controller.dispose(); p.dispose(); }
});

test('bridge: a player failure re-plans the session in place and tells the session’s owner; a stale revision is retried silently', async () => {
  const server = new FakePlaybackServer();
  const api = new V1PlaybackApi(fakeHttp(server));
  const started = await api.start({itemId: 'movie-1'}, {state: 'playing'});
  const changed: string[] = [];
  api.onSessionChanged(s => changed.push(`${s.id}:${s.presentation.generation}`));
  // Another client changed the session meanwhile: the bridge re-reads and sends the change once more.
  server.bump(started.session.id, {state: 'paused'});
  const result = await api.reportPlayerFailure(started.session.id, 'decode_error', 'MEDIA_ERR_DECODE', 37.4);
  assert.equal(result!.escalates, true);
  assert.equal(result!.session!.id, started.session.id);
  assert.equal(result!.session!.generation, 2);
  assert.equal(result!.session!.resumeSeconds, 37.4);
  assert.deepEqual(changed, [`${started.session.id}:2`]);
  server.bump(started.session.id, {state: 'playing'});
  const stale = await api.change(started.session.id, {quality: {mode: 'fixed', id: '1080p-low'}});
  assert.equal(stale.session.id, started.session.id, 'a revision that moved is never shown to the viewer');
  assert.deepEqual(stale.session.presentation.quality, {mode: 'fixed', id: '1080p-low'});
  for (let i = 0; i < 2; i++) assert.equal((await api.reportPlayerFailure(started.session.id, 'decode_error', '', 40))!.escalates, true);
  assert.deepEqual(await api.reportPlayerFailure(started.session.id, 'decode_error', '', 40), {escalates: false});
  await api.stopPlayback(started.session.id);
});
