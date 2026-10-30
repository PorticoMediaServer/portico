import test from 'node:test';
import assert from 'node:assert/strict';
import {AppleMediaPlayer, PlaybackSessionController, type AppleFact, type AppleVideoEngine, type PlayerEvent} from '../src/playback-v1/index.ts';
import {FakePlaybackServer, fakeHttp} from '../src/playback-v1/testing/fake-server.ts';

const settle = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate); };

/**
 * A stand-in for PorticoPlaybackModule/PorticoPlaybackController: the lease, the command validation
 * rules that matter to the adapter (PorticoPlaybackController.m `acceptCommand`), and facts.
 */
class FakeEngine implements AppleVideoEngine {
  commands: Record<string, any>[] = [];
  released: string[] = [];
  private leaseN = 0;
  private listener?: (f: AppleFact) => void;
  lease?: {lease: string; instanceId: string};
  private seq = 0;
  private revision = 0;
  private intent = 0;
  async acquire() { this.lease = {lease: 'lease-' + ++this.leaseN, instanceId: 'inst-' + this.leaseN}; this.revision = 0; this.seq = 0; return this.lease; }
  async command(c: Record<string, any>) {
    if (c.binding?.lease !== this.lease?.lease) throw new Error('Playback lease is no longer active.');
    if (c.commandRevision <= this.revision) throw new Error('Stale playback command.');
    if (!Number.isInteger(c.binding.intentId) || c.binding.intentId < this.intent) throw new Error('Stale playback binding.');
    if (typeof c.binding.sessionId !== 'string' || typeof c.binding.itemId !== 'string' || typeof c.paused !== 'boolean') throw new Error('Invalid playback command.');
    this.revision = c.commandRevision; this.intent = c.binding.intentId;
    this.commands.push(c);
    return true;
  }
  async release(lease: string) { this.released.push(lease); return true; }
  onFact(l: (f: AppleFact) => void) { this.listener = l; return () => { this.listener = undefined; }; }
  get last() { return this.commands.at(-1)!; }
  fact(type: string, extra: Partial<AppleFact> = {}, binding = this.last.binding) {
    this.listener?.({type, instanceId: this.lease!.instanceId, binding, factSequence: ++this.seq, lastCommandRevision: this.revision, paused: this.last.paused, effectiveRate: this.last.paused ? 0 : this.last.rate, position: 0, ...extra});
  }
}

const source = (over: Record<string, unknown> = {}) => ({url: '/v1/media/grant/1/master.m3u8', generation: 1, mode: 'stream' as const, startPositionMs: 0, subtitles: [{trackId: 's4', format: 'webvtt', url: '/v1/media/grant/1/subs/s4.vtt'}], autoplay: true, sessionId: 's1', itemId: 'movie-1', ...over});
const resolveUrl = (u: string) => new URL(u, 'https://portico.test').href;

test('load: one lease, a binding per load with the presentation generation, absolute source, resume as the first seek', async () => {
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl});
  player.load(source({startPositionMs: 90_000}));
  await settle();
  const c = engine.last;
  assert.deepEqual(c.binding, {lease: 'lease-1', intentId: 1, sessionId: 's1', generation: 1, itemId: 'movie-1'});
  assert.equal(c.source, 'https://portico.test/v1/media/grant/1/master.m3u8');
  assert.equal(c.paused, false);
  assert.deepEqual(c.pendingSeek, {revision: 1, positionSeconds: 90});
  assert.equal(c.listeningOwner, null);
  player.load(source({generation: 2, url: '/v1/media/grant/2/master.m3u8'}));
  await settle();
  assert.equal(engine.last.binding.intentId, 2);
  assert.equal(engine.last.binding.generation, 2);
  assert.equal(engine.released.length, 0, 'the lease is kept across generations');
  player.dispose();
  assert.deepEqual(engine.released, ['lease-1']);
});

test('facts: only the current binding, instance and rising sequence; old generations are dropped', async () => {
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl});
  const events: PlayerEvent[] = [];
  player.onEvent(e => events.push(e));
  player.load(source());
  await settle();
  const first = engine.last.binding;
  player.load(source({generation: 2}));
  await settle();
  const count = events.length;
  engine.fact('progress', {position: 50}, first); // late fact from generation 1
  assert.equal(events.length, count);
  engine.fact('ready');
  engine.fact('progress', {position: 12.5});
  assert.equal(player.observe().state, 'playing');
  assert.equal(player.observe().positionMs, 12_500);
  assert.equal(events.at(-1)!.type, 'time');
  assert.equal(events.at(-1)!.observation.generation, 2);
  player.dispose();
});

test('state changes send the full state with a rising revision; identical states are not resent', async () => {
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl});
  player.load(source());
  await settle();
  const n = engine.commands.length;
  player.pause();
  player.pause();
  await settle();
  assert.equal(engine.commands.length, n + 1);
  assert.equal(engine.last.paused, true);
  player.setRate(1.5);
  await settle();
  assert.equal(engine.last.rate, 1.5);
  assert.ok(engine.commands.every((c, i) => i === 0 || c.commandRevision > engine.commands[i - 1]!.commandRevision));
  player.dispose();
});

test('seek: progress is ignored until seekApplied for that revision; then the seek is retired and seeked reported', async () => {
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl});
  const types: string[] = [];
  player.onEvent(e => types.push(e.type));
  player.load(source());
  await settle();
  engine.fact('ready');
  player.seek(300_000);
  await settle();
  assert.deepEqual(engine.last.pendingSeek, {revision: 1, positionSeconds: 300});
  engine.fact('progress', {position: 5});
  assert.notEqual(player.observe().positionMs, 5000, 'a progress from before the seek is not believed');
  engine.fact('seekApplied', {seekRevision: 99, position: 1});
  assert.ok(!types.includes('seeked'), 'another seek revision is ignored');
  engine.fact('seekApplied', {seekRevision: 1, position: 300});
  await settle();
  assert.equal(types.filter(t => t === 'seeked').length, 1);
  assert.equal(player.observe().positionMs, 300_000);
  assert.equal(engine.last.pendingSeek, null, 'retired natively by a command without it');
  player.dispose();
});

test('errors, stalls and end map to port states; a refused command fails the presentation', async () => {
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl});
  const errors: string[] = [];
  player.onEvent(e => { if (e.type === 'error') errors.push(e.code); });
  player.load(source());
  await settle();
  engine.fact('stalled');
  assert.equal(player.observe().state, 'buffering');
  engine.fact('error', {engineCode: 'decode_error', errorDetail: 'AVFoundationErrorDomain -11819'});
  assert.deepEqual(errors, ['decode_error']);
  assert.equal(player.observe().state, 'error');
  await settle();
  assert.equal(engine.last.paused, true, 'a failed presentation is paused natively');
  // A refused command (lease lost, say).
  engine.lease = {lease: 'someone-else', instanceId: 'x'};
  player.load(source({generation: 2}));
  await settle();
  assert.equal(errors.at(-1), 'engine_error');
  player.dispose();
});

test('sidecar subtitles: ignored until native WebVTT support; then a sidecar command, and not carried in PiP/AirPlay', async () => {
  const off = new FakeEngine();
  const a = new AppleMediaPlayer(off, {owner: 'video', resolveUrl});
  a.load(source());
  await settle();
  a.selectSidecarSubtitle('s4');
  await settle();
  assert.equal(off.last.subtitles, null);
  a.dispose();
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl, sidecar: 'webvtt'});
  player.load(source());
  await settle();
  player.selectSidecarSubtitle('s4');
  await settle();
  assert.deepEqual(engine.last.subtitles, {binding: {intentId: 1, sessionId: 's1', generation: 1}, mode: 'sidecar', revision: 1, trackId: 's4', format: 'webvtt', url: 'https://portico.test/v1/media/grant/1/subs/s4.vtt'});
  assert.equal(player.observe().subtitleTrackId, 's4');
  assert.equal(player.observe().subtitlesCarried, undefined);
  player.setExternalRoute('pip');
  assert.equal(player.observe().externalRoute, 'pip');
  assert.equal(player.observe().subtitlesCarried, false, 'the overlay does not follow the picture into PiP');
  player.selectSidecarSubtitle(null);
  assert.equal(player.observe().subtitlesCarried, true);
  player.dispose();
});

test('driven by PlaybackSessionController against the fake server', async () => {
  const server = new FakePlaybackServer();
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl});
  let n = 0;
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, key: () => 'key_apple_player_' + String(++n).padStart(4, '0')});
  const s = (await ctl.start({itemId: 'movie-1'}))!;
  await settle();
  assert.equal(engine.last.binding.sessionId, s.id, 'the session id is the binding identity');
  engine.fact('ready');
  await settle();
  await ctl.flush();
  assert.ok(server.session(s.id)!.reports.some(r => r.state === 'playing'));
  await ctl.setAudio('a2');
  await settle();
  assert.equal(engine.last.binding.generation, 2);
  assert.equal(engine.last.binding.intentId, 2);
  await ctl.stop();
  assert.equal(engine.last.paused, true);
  ctl.dispose();
  player.dispose();
});

test('PiP with an app-drawn sidecar asks the server for the same track in the manifest (Plan §8.3)', async () => {
  const server = new FakePlaybackServer();
  const engine = new FakeEngine();
  const player = new AppleMediaPlayer(engine, {owner: 'video', resolveUrl, sidecar: 'webvtt'});
  let n = 0;
  const ctl = new PlaybackSessionController({http: fakeHttp(server), player, key: () => 'key_apple_pip_' + String(++n).padStart(4, '0')});
  const s = (await ctl.start({itemId: 'movie-1'}))!;
  await settle();
  engine.fact('ready');
  ctl.setSubtitles('s4');
  await settle();
  player.setExternalRoute('pip');
  await settle();
  assert.deepEqual(server.session(s.id)!.subtitles, {trackId: 's4', delivery: 'embeddedClient'});
  assert.equal(engine.last.binding.generation, 2, 'a new generation with the track in the manifest');
  assert.equal(engine.last.subtitles, null, 'no overlay any more');
  ctl.dispose();
  player.dispose();
});
