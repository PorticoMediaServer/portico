/** Two engines, one coordinator: a title starts on the chosen engine, and moves across mid-title
 * at the position it had reached, in the same session. */
import test from 'node:test';
import assert from 'node:assert/strict';
import {NativePlaybackCoordinator, type NativePlaybackTransport} from '../src-next/platform/playback/NativePlaybackCoordinator.ts';

type Fact = Record<string, unknown>;
class FakeEngine implements NativePlaybackTransport {
  readonly name: string;
  acquired = 0;
  released: string[] = [];
  commands: Record<string, any>[] = [];
  listener?: (fact: any) => void;
  constructor(name: string) { this.name = name; }
  async acquire() { this.acquired++; return {lease: `${this.name}-lease-${this.acquired}`, instanceId: `${this.name}-instance`}; }
  async command(value: Record<string, unknown>) { this.commands.push(value); return true; }
  async release(lease: string): Promise<unknown> { this.released.push(lease); return true; }
  subscribe(listener: (fact: any) => void) { this.listener = listener; return () => { if (this.listener === listener) this.listener = undefined; }; }
  emit(fact: Fact) { this.listener?.(fact); }
}

/** The part of PlaybackService the coordinator uses. */
class FakeService {
  snapshot: any = {intentId: 1, phase: 'ready', intent: 'playing', itemId: 'item-1', positionSeconds: 0, session: {id: 'session-1', generation: 1, streamUrl: '/v1/media/grant/file', mode: 'direct', duration: 100, resumeSeconds: 0}, audio: {}, pendingSeek: undefined};
  listeners = new Set<() => void>();
  lost: number[] = [];
  failed: string[] = [];
  getSnapshot = () => this.snapshot;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  update(patch: Record<string, unknown>) { this.snapshot = {...this.snapshot, ...patch}; this.listeners.forEach(l => l()); }
  engineLost(intentId: number) { this.lost.push(intentId); this.update({seekRevision: 1, pendingSeek: {revision: 1, positionSeconds: this.snapshot.positionSeconds}}); }
  fail(_intent: number, message: string) { this.failed.push(message); this.update({phase: 'error'}); }
}

const settle = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };

function make(initial: 'native' | 'mpv') {
  const service = new FakeService(), native = new FakeEngine('native'), mpv = new FakeEngine('mpv');
  const wanted = {engine: initial as string};
  const facts: Fact[] = [];
  let owner = 0;
  const coordinator = new NativePlaybackCoordinator(service as never, native, () => `owner-${++owner}`, s => (s.session ? 'https://portico.test' + s.session.streamUrl : ''), f => facts.push(f), {
    initial: 'native',
    choose: () => wanted.engine,
    transport: engine => (engine === 'mpv' ? mpv : native),
  });
  return {service, native, mpv, wanted, facts, coordinator};
}

test('a title the plan gives to mpv starts on mpv; the native engine is never asked', async () => {
  const {native, mpv, coordinator} = make('mpv');
  coordinator.start();
  await settle();
  assert.equal(coordinator.getEngine(), 'mpv');
  assert.equal(native.acquired, 0);
  assert.equal(mpv.acquired, 1);
  assert.equal(mpv.commands.length, 1);
  assert.equal(mpv.commands[0]!.binding.lease, 'mpv-lease-1');
  assert.equal(mpv.commands[0]!.source, 'https://portico.test/v1/media/grant/file');
  assert.equal('audioTrack' in mpv.commands[0]!, false, 'no audio track was chosen, so the command has no such field');
  coordinator.stop();
});

test('moving to the other engine mid-title releases the old lease and resumes where the title stood, in the same session', async () => {
  const {service, native, mpv, wanted, coordinator} = make('native');
  coordinator.start();
  await settle();
  assert.equal(native.acquired, 1);
  assert.equal(native.commands.at(-1)!.pendingSeek, null);
  service.update({positionSeconds: 612.5});
  await settle();

  wanted.engine = 'mpv';
  coordinator.reconsiderEngine();
  await settle();
  assert.equal(coordinator.getEngine(), 'mpv');
  assert.deepEqual(native.released, ['native-lease-1']);
  assert.deepEqual(service.lost, [1], 'the service was told the engine went, which makes it ask for the position as a seek');
  assert.equal(mpv.acquired, 1);
  const first = mpv.commands.at(-1)!;
  assert.equal(first.binding.sessionId, 'session-1', 'the same session: the queue is untouched');
  assert.equal(first.binding.lease, 'mpv-lease-1');
  assert.deepEqual(first.pendingSeek, {revision: 1, positionSeconds: 612.5});
  assert.equal(first.commandRevision, 1, 'a new lease starts its own revisions');
  assert.deepEqual(service.failed, []);
  coordinator.stop();
});

test('facts are taken from the engine in use only', async () => {
  const {native, mpv, wanted, facts, coordinator} = make('native');
  coordinator.start();
  await settle();
  wanted.engine = 'mpv';
  coordinator.reconsiderEngine();
  await settle();
  assert.equal(native.listener, undefined, 'the engine left behind is no longer listened to');
  const binding = mpv.commands.at(-1)!.binding;
  mpv.emit({type: 'mediaInfo', instanceId: 'mpv-instance', binding, factSequence: 1, lastCommandRevision: 1, intentId: 1});
  mpv.emit({type: 'mediaInfo', instanceId: 'native-instance', binding, factSequence: 2, lastCommandRevision: 1, intentId: 1});
  assert.equal(facts.length, 1);
  assert.equal(facts[0]!.instanceId, 'mpv-instance');
  coordinator.stop();
});

test('an audio track chosen for the engine travels in the command, bound to its playback', async () => {
  const {mpv, coordinator} = make('mpv');
  coordinator.start();
  await settle();
  coordinator.setAudioTrack({binding: {intentId: 1, sessionId: 'session-1', generation: 1}, revision: 3, streamIndex: 2});
  await settle();
  assert.deepEqual(mpv.commands.at(-1)!.audioTrack, {revision: 3, streamIndex: 2});
  coordinator.setAudioTrack({binding: {intentId: 1, sessionId: 'another', generation: 1}, revision: 4, streamIndex: 1});
  await settle();
  assert.equal('audioTrack' in mpv.commands.at(-1)!, false, 'a choice made for another playback is not sent');
  coordinator.stop();
});

test('a build without the wanted engine stays where it is', async () => {
  const service = new FakeService(), native = new FakeEngine('native');
  const coordinator = new NativePlaybackCoordinator(service as never, native, () => 'owner', s => (s.session ? 'https://portico.test' + s.session.streamUrl : ''), () => {}, {initial: 'native', choose: () => 'mpv', transport: engine => (engine === 'native' ? native : undefined)});
  coordinator.start();
  await settle();
  assert.equal(coordinator.getEngine(), 'native');
  assert.equal(native.acquired, 1);
  coordinator.stop();
});

test('the next engine waits for the old lease to finish releasing', async () => {
  const {native, mpv, wanted, coordinator} = make('native');
  coordinator.start();
  await settle();
  let released!: () => void;
  native.release = lease => {
    native.released.push(lease);
    return new Promise<void>(resolve => { released = resolve; });
  };
  wanted.engine = 'mpv';
  coordinator.reconsiderEngine();
  await settle();
  assert.equal(mpv.acquired, 0);
  released();
  await settle();
  assert.equal(mpv.acquired, 1);
  coordinator.stop();
});

test('switching during an acquisition waits for that stale lease to be released', async () => {
  const {native, mpv, wanted, coordinator} = make('native');
  let attached!: (lease: {lease: string; instanceId: string}) => void;
  native.acquire = () => new Promise(resolve => { attached = resolve; });
  coordinator.start();
  await settle();
  wanted.engine = 'mpv';
  coordinator.reconsiderEngine();
  await settle();
  assert.equal(mpv.acquired, 0);
  attached({lease: 'late-native', instanceId: 'native-instance'});
  await settle();
  assert.deepEqual(native.released, ['late-native']);
  assert.equal(mpv.acquired, 1);
  coordinator.stop();
});

test('a rejected release prevents a second engine from taking a lease', async () => {
  const {native, mpv, wanted, service, coordinator} = make('native');
  coordinator.start();
  await settle();
  native.release = async () => { throw new Error('release failed'); };
  wanted.engine = 'mpv';
  coordinator.reconsiderEngine();
  await settle();
  assert.equal(mpv.acquired, 0);
  assert.equal(service.failed.length, 1);
  coordinator.stop();
});

test('stopping and restarting waits for an acquisition from the previous lifecycle', async () => {
  const {native, coordinator} = make('native');
  let first!: (lease: {lease: string; instanceId: string}) => void;
  let calls = 0;
  native.acquire = async () => {
    if (++calls === 1) return new Promise(resolve => { first = resolve; });
    return {lease: 'fresh-native', instanceId: 'native-instance'};
  };
  coordinator.start();
  await settle();
  coordinator.stop();
  coordinator.start();
  await settle();
  assert.equal(calls, 1);
  first({lease: 'stale-native', instanceId: 'native-instance'});
  await settle();
  assert.deepEqual(native.released, ['stale-native']);
  assert.equal(calls, 2);
  assert.equal(coordinator.getSnapshot(), 'fresh-native');
  coordinator.stop();
});

test('a stale acquisition whose release fails prevents the replacement from starting', async () => {
  const {native, mpv, wanted, service, coordinator} = make('native');
  let first!: (lease: {lease: string; instanceId: string}) => void;
  native.acquire = () => new Promise(resolve => { first = resolve; });
  native.release = async () => { throw new Error('release failed'); };
  coordinator.start();
  await settle();
  wanted.engine = 'mpv';
  coordinator.reconsiderEngine();
  await settle();
  first({lease: 'stale-native', instanceId: 'native-instance'});
  await settle();
  assert.equal(mpv.acquired, 0);
  assert.equal(service.failed.length, 1);
  coordinator.stop();
});
