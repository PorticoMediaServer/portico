import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService} from '../../../packages/client-core/src/index.ts';
import {FakePlaybackServer, fakeHttp} from '../../../packages/client-core/src/playback-v1/testing/fake-server.ts';
import {NativePlayback} from '../src-next/platform/playback/native-playback.ts';
import {V1QueuePlayer} from '../../../packages/client-core/src/playback-v1/v1-queue-player.ts';
import {NativePlaybackCoordinator} from '../src-next/platform/playback/NativePlaybackCoordinator.ts';

const viewer = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile'};
const settle = async (n = 30) => { for (let i = 0; i < n; i++) await new Promise(r => setImmediate(r)); };

/** PorticoPlaybackController, as far as these rules go: a new binding loads; ready once loaded; a
 * pending seek applies after ready (seekApplied at the target); unpaused, it plays (progress). */
class FakeNative {
  commands: Record<string, any>[] = [];
  private listener?: (f: any) => void;
  private seq = 0;
  private seekRevision = 0;
  private loaded = '';
  position = 0;
  /** An engine that never answers (the start that never got its first frame). */
  silent = false;
  async acquire() { return {lease: 'lease-1', instanceId: 'inst-1'}; }
  async release() { return true; }
  subscribe(l: (f: any) => void) { this.listener = l; return () => { this.listener = undefined; }; }
  async command(c: Record<string, any>) {
    this.commands.push(c);
    if (!this.silent) queueMicrotask(() => this.apply(c));
    return true;
  }
  private fact(c: Record<string, any>, type: string, extra: Record<string, unknown> = {}) {
    this.listener?.({type, instanceId: 'inst-1', binding: c.binding, intentId: c.binding.intentId, factSequence: ++this.seq, lastCommandRevision: c.commandRevision, position: this.position, paused: c.paused, ...extra});
  }
  private apply(c: Record<string, any>) {
    const key = JSON.stringify(c.binding) + c.source;
    if (key !== this.loaded) { this.loaded = key; this.position = 0; this.fact(c, 'ready'); }
    const pending = c.pendingSeek;
    if (pending && pending.revision > this.seekRevision) {
      this.seekRevision = pending.revision;
      this.position = pending.positionSeconds;
      this.fact(c, 'seekApplied', {seekRevision: pending.revision});
      return;
    }
    if (!c.paused && !pending) { this.position += 1; this.fact(c, 'progress'); }
  }
}

function apple(options: {startupTimeoutMs?: number} = {}) {
  const server = new FakePlaybackServer();
  const http = fakeHttp(server);
  let serial = 0;
  const path = new NativePlayback({http, timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const api = path.transport;
  const playback = new PlaybackService(api, () => `r${++serial}`, {queueRequired: true, deferRecovery: true, ...options});
  path.bind(playback);
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  const stop = player.connect();
  const native = new FakeNative();
  // usePlayer's fact handler, the parts these rules reach.
  const accept = (f: any) => {
    const s = playback.getSnapshot();
    if (f.intentId !== s.intentId) return;
    path.engineFact(f);
    if (f.type === 'seekApplied') { playback.seekApplied(f.intentId, f.seekRevision, f.position, f.paused ? 'paused' : 'playing'); return; }
    if (f.type === 'ready') playback.ready(f.intentId);
    else playback.fact(f.intentId, f.position, f.paused ? 'paused' : 'playing');
  };
  const coordinator = new NativePlaybackCoordinator(playback, native, () => `owner-${++serial}`, s => s.session ? 'https://portico.test' + s.session.streamUrl : '', accept);
  coordinator.start();
  return {server, playback, player, native, coordinator, stop};
}

// Demo, 23 Sep (Justin's iPhone): Resume on Dune created a v1 session at 6 s that never played;
// it showed paused at 0:00, and Play started a second session from 0:00, losing the position.
test('Apple, v1: Resume plays from the saved position without a tap', async () => {
  const {playback, player, native, coordinator, stop, server} = apple();
  try {
    await playback.play('movie-1', 6);
    await settle();
    const s = playback.getSnapshot();
    const last = native.commands.at(-1);
    assert.equal(s.error, undefined, s.error);
    assert.equal(s.intent, 'playing', 'Resume plays');
    assert.equal(last?.paused, false, 'the engine was told to play');
    assert.ok(s.positionSeconds >= 6, `position ${s.positionSeconds}`);
    const session = server.session(s.session!.id)!;
    assert.equal(session.ended, false);
  } finally { coordinator.stop(); stop(); player.dispose(); }
});

// The same run: the first start never played, and Play (usePlayer's retry) started a new session at
// 0:00. A retry keeps the position the viewer asked for.
test('Apple, v1: a Resume that never started retries at its position, not 0:00', async () => {
  const {playback, player, native, coordinator, stop, server} = apple({startupTimeoutMs: 30});
  try {
    native.silent = true;
    await playback.play('movie-1', 6);
    await new Promise(r => setTimeout(r, 60));
    const failed = playback.getSnapshot();
    assert.equal(failed.phase, 'error');
    const first = failed.session!.id;
    // usePlayer's retry.
    native.silent = false;
    await playback.play('movie-1', playback.recoveryTarget() ?? (failed.positionSeconds || 0));
    await settle();
    const s = playback.getSnapshot();
    assert.notEqual(s.session!.id, first);
    assert.equal(server.session(s.session!.id)!.positionMs, 6000, 'the new session starts at the resume point');
    assert.ok(s.positionSeconds >= 6, `position ${s.positionSeconds}`);
    assert.equal(s.intent, 'playing');
  } finally { coordinator.stop(); stop(); player.dispose(); }
});

test('a start that times out says, in development builds, what it was waiting on', async () => {
  const warned: unknown[][] = [];
  const warn = console.warn;
  console.warn = (...args: unknown[]) => { warned.push(args); };
  const server = new FakePlaybackServer();
  const http = fakeHttp(server);
  let serial = 0;
  const path = new NativePlayback({http, timers: {setTimer: () => 0, clearTimer: () => {}}, key: () => `k${++serial}`});
  const api = path.transport;
  const playback = new PlaybackService(api, () => `r${++serial}`, {queueRequired: true, deferRecovery: true, startupTimeoutMs: 30, diagnostics: true});
  path.bind(playback);
  const player = new V1QueuePlayer({http, api, playback, viewer, key: () => `q${++serial}-key-000000`, pendingPollMs: 10});
  const stop = player.connect();
  const native = new FakeNative();
  native.silent = true;
  const coordinator = new NativePlaybackCoordinator(playback, native, () => `owner-${++serial}`, s => s.session ? 'https://portico.test' + s.session.streamUrl : '', () => {});
  coordinator.start();
  playback.setStartupProbe(() => coordinator.diagnostics());
  try {
    await playback.play('movie-1', 6);
    await new Promise(r => setTimeout(r, 60));
    assert.equal(playback.getSnapshot().phase, 'error');
    const line = warned.find(a => a[0] === '[playback] start timed out');
    assert.ok(line, 'logged');
    const detail = JSON.parse(String(line![1]));
    assert.equal(detail.protocol, 'v1');
    assert.equal(detail.engine.commandSent, true);
    assert.equal(detail.engine.factSequence, 0, 'the engine never answered');
  } finally { console.warn = warn; coordinator.stop(); stop(); player.dispose(); }
});
