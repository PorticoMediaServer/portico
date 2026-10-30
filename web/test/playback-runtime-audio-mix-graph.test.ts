import test from 'node:test';
import assert from 'node:assert/strict';
import {MixGraph} from '../src/platform/playback/audio/mix-graph.ts';

function context() {
  const starts: {time: number; offset: number; duration: number; stopped: boolean}[] = [];
  const ctx: any = {sampleRate: 10, currentTime: 0, state: 'running', createBufferSource() {
    const event = {time: -1, offset: -1, duration: -1, stopped: false};
    return {buffer: undefined, onended: null, connect() {}, disconnect() {}, start(time: number, offset: number, duration: number) { Object.assign(event, {time, offset, duration}); starts.push(event); }, stop() { event.stopped = true; }};
  }};
  return {ctx, starts};
}

test('late decoded data is held after the common frontier instead of started in the past', () => {
  const {ctx, starts} = context(), graph = new MixGraph(ctx, 1), voice = {};
  graph.addVoice(voice, 0, 20, {} as AudioNode);
  const one = {length: 10} as AudioBuffer;
  graph.addPacket(voice, {start: 0, end: 10, offset: 0, buffer: one});
  assert.equal(graph.admit(20).frontier, 10);
  ctx.currentTime = 5;
  graph.addPacket(voice, {start: 10, end: 20, offset: 0, buffer: one});
  assert.equal(graph.admit(20).frontier, 10);
  assert.equal(starts.length, 1);
  ctx.state = 'suspended';
  assert.equal(graph.rebase(50, 51), 10);
  assert.equal(graph.admit(20).frontier, 20);
  assert.deepEqual(starts.map(s => [s.time, s.offset, s.duration]), [[0.1, 0, 1], [5.1, 0, 1]]);
  assert.equal(starts[0]!.stopped, true);
  graph.close();
});

test('early suspension rebuilds only the original partial buffer suffix', () => {
  const {ctx, starts} = context(), graph = new MixGraph(ctx, 1), voice = {};
  graph.addVoice(voice, 0, 20, {} as AudioNode);
  graph.addPacket(voice, {start: 0, end: 20, offset: 0, buffer: {length: 20} as AudioBuffer});
  graph.admit(20);
  ctx.currentTime = 0.6; ctx.state = 'suspended';
  assert.equal(graph.rebase(6, 7), 5);
  graph.admit(20);
  assert.deepEqual(starts.map(s => [s.time, s.offset, s.duration]), [[0.1, 0, 2], [0.7, 0.5, 1.5]]);
  graph.close();
});

test('an overlapping link requires the complete already-admitted incoming overlap', () => {
  const {ctx, starts} = context(), graph = new MixGraph(ctx, 10), a = {}, b = {};
  graph.addVoice(a, 0, 20, {} as AudioNode);
  graph.addPacket(a, {start: 0, end: 20, offset: 0, buffer: {length: 20} as AudioBuffer});
  graph.admit(15);
  assert.throws(() => graph.linkVoice(b, 10, 30, {} as AudioNode, [{start: 10, end: 12, offset: 0, buffer: {length: 2} as AudioBuffer}]), /not ready/);
  assert.equal(starts.length, 1);
  graph.linkVoice(b, 10, 30, {} as AudioNode, [{start: 10, end: 30, offset: 0, buffer: {length: 20} as AudioBuffer}]);
  assert.deepEqual(starts.slice(1).map(s => [s.time, s.offset, s.duration]), [[2, 0, 0.5]]);
  assert.equal(graph.admit(20).frontier, 20);
  graph.close();
});

test('both fade voices stop at the missing participant frontier, even with delayed suspension', () => {
  const {ctx, starts} = context(), graph = new MixGraph(ctx, 1), outgoing = {}, incoming = {};
  graph.addVoice(outgoing, 0, 30, {} as AudioNode);
  graph.addPacket(outgoing, {start: 0, end: 30, offset: 0, buffer: {length: 30} as AudioBuffer});
  graph.admit(10);
  graph.linkVoice(incoming, 5, 40, {} as AudioNode, [{start: 5, end: 15, offset: 0, buffer: {length: 10} as AudioBuffer}]);
  assert.equal(graph.admit(40).frontier, 15);
  assert(starts.every(s => s.time + s.duration <= 1.6), 'no audible voice extends past the joint frontier');
  ctx.currentTime = 5;
  graph.addPacket(incoming, {start: 15, end: 40, offset: 0, buffer: {length: 25} as AudioBuffer});
  assert.equal(graph.admit(40).frontier, 15);
  ctx.state = 'suspended'; assert.equal(graph.rebase(50, 51), 15);
  const count = starts.length;
  assert.equal(graph.admit(40).frontier, 40);
  assert(starts.slice(count).every(s => s.time >= 5.1));
  const suffixes = starts.slice(count).map(s => [s.offset, s.duration]);
  assert(suffixes.some(([offset, duration]) => offset === 1.5 && duration === 1.5), 'outgoing retains its own unheard suffix');
  graph.close();
});
