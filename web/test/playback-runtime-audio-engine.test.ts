import test from 'node:test';
import assert from 'node:assert/strict';
import {Deck, edgeFrames, linkDecks} from '../src/platform/playback/audio/engine.ts';
import {MixGraph} from '../src/platform/playback/audio/mix-graph.ts';
/** A fake BaseAudioContext for node: records what the engine schedules and plays it back as
 * `advance()` moves the clock (onended fires for buffers that finished). */
export function fakeContext(sampleRate = 48000) {
  const scheduled: {start: number; frames: number; node: any}[] = [];
  const param = (value = 1) => ({value, events: [] as unknown[], setValueAtTime(v: number, t: number) { this.events.push(['set', v, t]); }, linearRampToValueAtTime(v: number, t: number) { this.events.push(['ramp', v, t]); this.value = v; }, cancelScheduledValues() {}, setValueCurveAtTime(c: Float32Array, t: number, d: number) { this.events.push(['curve', c[0], c[c.length - 1], t, d]); }, setTargetAtTime(v: number) { this.value = v; }});
  const listeners = new Map<string, Set<(event: Event) => void>>();
  const ctx: any = {
    sampleRate, currentTime: 0, state: 'running', destination: {channelCount: 2},
    addEventListener(type: string, callback: (event: Event) => void) { if (!listeners.has(type)) listeners.set(type, new Set()); listeners.get(type)!.add(callback); },
    removeEventListener(type: string, callback: (event: Event) => void) { listeners.get(type)?.delete(callback); },
    dispatchEvent(event: Event) { for (const callback of listeners.get(event.type) ?? []) callback(event); return true; },
    createGain: () => ({gain: param(), connect() {}, disconnect() {}}),
    createBuffer: (channels: number, length: number, rate: number) => ({numberOfChannels: channels, length, sampleRate: rate, data: Array.from({length: channels}, () => new Float32Array(length)), copyToChannel(src: Float32Array, ch: number) { this.data[ch].set(src); }}),
    createBufferSource: () => {
      const node: any = {buffer: null, onended: null, started: undefined, connect() {}, disconnect() {}, start(t: number, offset = 0, duration?: number) { node.started = t; node.offset = offset; scheduled.push({start: Math.round(t * sampleRate), frames: duration === undefined ? node.buffer.length : Math.round(duration * sampleRate), node}); }, stop() { node.stopped = true; }};
      return node;
    },
  };
  return {
    ctx, scheduled,
    advance(seconds: number) {
      ctx.currentTime += seconds;
      const frame = Math.round(ctx.currentTime * sampleRate);
      for (const s of scheduled) if (!s.node.done && !s.node.stopped && s.start + s.frames <= frame) { s.node.done = true; s.node.onended?.(); }
    },
    /** Frames the scheduled buffers hold that haven't played yet. */
    unplayed() { const frame = Math.round(ctx.currentTime * sampleRate); return scheduled.filter(s => !s.node.done && !s.node.stopped).reduce((n, s) => n + Math.min(s.frames, s.start + s.frames - Math.max(frame, s.start)), 0); },
  };
}

/** A synthetic PCM source: `frames` of a sine, generated as read (no memory of its own). */
export function sineSource(frames: number, sampleRate: number, channels = 2) {
  let at = 0, closed = false;
  return {
    sampleRate, channels, get closed() { return closed; },
    async read(max: number) {
      if (at >= frames) return undefined;
      const n = Math.min(max, frames - at), c = Array.from({length: channels}, () => new Float32Array(n));
      for (let i = 0; i < n; i++) { const v = 0.5 * Math.sin(2 * Math.PI * 441 * (at + i) / sampleRate); for (const ch of c) ch[i] = v; }
      at += n;
      return {channels: c, frames: n};
    },
    close() { closed = true; },
  };
}


test('(j) memory is bounded: a 2-hour 24/96 track holds at most the ahead window decoded', async () => {
  const rate = 96000, f = fakeContext(rate), frames = 2 * 3600 * rate;
  const deck = new Deck(f.ctx, {} as AudioNode, sineSource(frames, rate), {frames, gain: 1});
  await deck.pump(2);
  deck.start(Math.ceil(0.08 * rate));
  let worst = 0;
  // Ten minutes of playback in half-second steps, pumping as the tick does.
  for (let t = 0; t < 600; t += 0.5) {
    await deck.pump(30);
    worst = Math.max(worst, f.unplayed() / rate);
    f.advance(0.5);
  }
  assert.ok(worst <= 31.1, `decoded ahead ${worst.toFixed(2)} s`);
  assert.ok(deck.decoded < 700 * rate, 'only what the clock reached plus the window was decoded');
  deck.close();
});

test('(b) a gapless successor starts at the frame after the current track’s last frame', async () => {
  const rate = 44100, f = fakeContext(rate);
  const a = new Deck(f.ctx, {} as AudioNode, sineSource(110000, rate), {frames: 110000, gain: 1});
  const b = new Deck(f.ctx, {} as AudioNode, sineSource(117919, rate), {frames: 117919, gain: 1});
  await a.pump(100); await b.pump(10);
  a.start(3528);
  const edge = linkDecks(f.ctx, a, b, {crossfadeSeconds: 6, sameAlbum: true});
  assert.equal(edge.start, 3528 + 110000);
  assert.equal(edge.fade, 0, 'same-album neighbors never crossfade');
  const aFrames = f.scheduled.filter(s => s.node.buffer.length && s.start < edge.start);
  assert.equal(aFrames.reduce((n, s) => n + s.frames, 0), 110000, 'exactly the track’s frames');
  assert.equal(Math.max(...aFrames.map(s => s.start + s.frames)), edge.start, 'no gap and no overlap');
  assert.equal(Math.min(...f.scheduled.filter(s => s.start >= edge.start).map(s => s.start)), edge.start);
});

test('(c) crossfade: starts crossfadeSeconds before the end, clamped to half the shorter track; late links start late', () => {
  const rate = 48000;
  assert.deepEqual(edgeFrames({endFrame: 1_000_000, outFrames: 600_000}, {outFrames: 600_000}, {crossfadeSeconds: 6, sameAlbum: false, rate}), {start: 1_000_000 - 288_000, fade: 288_000});
  assert.deepEqual(edgeFrames({endFrame: 1_000_000, outFrames: 200_000}, {outFrames: 600_000}, {crossfadeSeconds: 6, sameAlbum: false, rate}), {start: 900_000, fade: 100_000}, 'half the shorter track');
  assert.deepEqual(edgeFrames({endFrame: 1_000_000, outFrames: 600_000}, {outFrames: 600_000}, {crossfadeSeconds: 6, sameAlbum: true, rate}), {start: 1_000_000, fade: 0});
  assert.deepEqual(edgeFrames({endFrame: 1_000_000, outFrames: 600_000}, {outFrames: 600_000}, {crossfadeSeconds: 0, sameAlbum: false, rate}), {start: 1_000_000, fade: 0});
});

test('(c) the crossfade is equal-power on both decks', async () => {
  const rate = 48000, f = fakeContext(rate);
  const a = new Deck(f.ctx, {} as AudioNode, sineSource(rate * 20, rate), {frames: rate * 20, gain: 1});
  const b = new Deck(f.ctx, {} as AudioNode, sineSource(rate * 20, rate), {frames: rate * 20, gain: 1});
  await a.pump(1); await b.pump(1);
  a.start(0);
  const edge = linkDecks(f.ctx, a, b, {crossfadeSeconds: 6, sameAlbum: false});
  assert.equal(edge.start, rate * 14);
  const curveIn = (b.fade.gain as any).events.find((e: unknown[]) => e[0] === 'curve'), curveOut = (a.fade.gain as any).events.find((e: unknown[]) => e[0] === 'curve');
  assert.deepEqual(curveIn.slice(1), [0, 1, 14, 6]);
  assert.ok(Math.abs(curveOut[1] - 1) < 1e-6 && Math.abs(curveOut[2]) < 1e-6);
  assert.deepEqual(curveOut.slice(3), [14, 6]);
});

test('(d) normalization ramps over 20 ms, and only when the value changes', () => {
  const f = fakeContext(48000);
  const deck = new Deck(f.ctx, {} as AudioNode, sineSource(48000, 48000), {frames: 48000, gain: 0.5});
  assert.equal(deck.norm.gain.value, 0.5);
  f.ctx.currentTime = 1;
  deck.setGain(0.25);
  deck.setGain(0.25);
  const ramps = (deck.norm.gain as any).events.filter((e: unknown[]) => e[0] === 'ramp');
  assert.deepEqual(ramps, [['ramp', 0.25, 1.02]]);
});

test('the byte cache clears every private compressed block', async () => {
  const {ByteCache} = await import('../src/platform/playback/audio/bytes.ts');
  const cache = new ByteCache(1 << 20);
  cache.put('m1:0', new Uint8Array(1000)); cache.put('m2:0', new Uint8Array(500));
  cache.clear();
  assert.equal(cache.size, 0);
  assert.equal(cache.get('m1:0'), undefined);
  assert.equal(cache.get('m2:0'), undefined);
});

test('gapless decks share the resampler grid at a mismatched output rate', async () => {
  const {Resampler} = await import('../src/platform/playback/audio/resampler.ts');
  const rate=44100, output=48000, sizes=[10001,11003], total=sizes[0]!+sizes[1]!;
  const pcm=Float32Array.from({length:total},(_,i)=>Math.sin(i*0.063)*0.3);
  const make=(offset:number,frames:number)=>{let at=0;return{sampleRate:rate,channels:1,async read(max:number){if(at===frames)return;const n=Math.min(max,frames-at);const channels=[pcm.slice(offset+at,offset+at+n)];at+=n;return{channels,frames:n};},close(){}};};
  const f=fakeContext(output);
  const a=new Deck(f.ctx,{} as AudioNode,make(0,sizes[0]!),{frames:sizes[0]!,gain:1,holdTail:true});
  const b=new Deck(f.ctx,{} as AudioNode,make(sizes[0]!,sizes[1]!),{frames:sizes[1]!,gain:1});
  await a.pump(Infinity);a.start(0);assert.ok(b.continueFrom(a));await b.pump(Infinity);
  linkDecks(f.ctx,a,b,{crossfadeSeconds:0,sameAlbum:true});
  const actual=f.scheduled.flatMap(s=>Array.from(s.node.buffer.data[0]));
  const whole=new Resampler(rate,output,1);
  const expected=[...whole.process([pcm],total).channels[0]!,...whole.flush().channels[0]!];
  assert.deepEqual(actual,expected);
  assert.equal(a.outFrames+b.outFrames,Math.floor(total*output/rate));
});

test('cancelled resampler preparation retains the current track and a finite tail deadline',async()=>{
 const f=fakeContext(48000),frames=44100;
 const a=new Deck(f.ctx,{} as AudioNode,sineSource(frames,44100),{frames,gain:1,holdTail:true});
 const b=new Deck(f.ctx,{} as AudioNode,sineSource(frames,44100),{frames,gain:1});
 await a.pump(Infinity);a.start(3840);assert.ok(Number.isFinite(a.tailDeadlineSeconds));
 assert.ok(b.continueFrom(a));await b.pump(Infinity);b.close();
 a.finishTail();
 assert.equal(f.scheduled.reduce((n,s)=>n+s.frames,0),a.outFrames,'cancelled preparation did not consume the outgoing tail');
 assert.ok(a.complete);
});

test('stalled pumps share one read and one promise, including a larger target', async () => {
  const rate = 100, f = fakeContext(rate);
  let release!: (value: {channels: Float32Array[]; frames: number} | undefined) => void;
  let reads = 0;
  const source = {sampleRate: rate, channels: 1, read: () => { reads++; return new Promise<{channels: Float32Array[]; frames: number} | undefined>(resolve => { release = resolve; }); }, close: () => release?.(undefined)};
  const deck = new Deck(f.ctx, {} as AudioNode, source, {frames: rate * 10, gain: 1});
  const first = deck.pump(1);
  for (let i = 0; i < 3000; i++) assert.strictEqual(deck.pump(i === 2999 ? 2 : 1), first);
  await Promise.resolve();
  assert.equal(reads, 1);
  release({channels: [new Float32Array(rate)], frames: rate});
  await Promise.resolve(); await Promise.resolve();
  assert.equal(reads, 2, 'larger target continues the same drain');
  deck.close();
  await first;
  assert.equal(reads, 2, 'retirement does not restart decoding');
});

test('pump reports a decoder failure without starting another read', async () => {
  const f = fakeContext(100), failure = new Error('decoder failed');
  let reads = 0;
  const source = {sampleRate: 100, channels: 1, async read() { reads++; throw failure; }, close() {}};
  const deck = new Deck(f.ctx, {} as AudioNode, source, {frames: 1000, gain: 1});
  await deck.pump(1);
  assert.equal(deck.failure, failure);
  await deck.pump(30);
  assert.equal(reads, 1);
  deck.close();
});

test('close settles the original stalled pump and never revives it', async () => {
  const f = fakeContext(100);
  let reads = 0, closeCount = 0, release!: () => void;
  const source = {sampleRate: 100, channels: 1,
    read: () => { reads++; return new Promise<undefined>(resolve => { release = () => resolve(undefined); }); },
    close: () => { closeCount++; release(); }};
  const deck = new Deck(f.ctx, {} as AudioNode, source, {frames: 1000, gain: 1});
  const pump = deck.pump(1);
  await Promise.resolve();
  deck.close();
  assert.strictEqual(deck.pump(30), pump);
  await pump;
  deck.close();
  await deck.pump(30);
  assert.equal(closeCount, 1);
  assert.equal(reads, 1);
});

test('a larger request after a completed empty drain starts a fresh drain', async () => {
  const rate = 100, f = fakeContext(rate);
  let reads = 0;
  const source = {sampleRate: rate, channels: 1, async read() { reads++; return {channels: [new Float32Array(rate)], frames: rate}; }, close() {}};
  const deck = new Deck(f.ctx, {} as AudioNode, source, {frames: rate * 2, gain: 1});
  const empty = deck.pump(0);
  await Promise.resolve(); // the drain has finished, but its returned promise may still settle
  await deck.pump(1);
  await empty;
  assert.equal(reads, 1);
  assert.equal(deck.decoded, rate);
  deck.close();
});

test('shared graph holds an actual Deck frontier and schedules refill only after rebase', async () => {
  const rate = 100, f = fakeContext(rate), graph = new MixGraph(f.ctx, 8);
  const deck = new Deck(f.ctx, {} as AudioNode, sineSource(rate * 4, rate), {frames: rate * 4, gain: 1, graph});
  await deck.pump(1);
  deck.start(0);
  assert.equal(graph.admit(100).frontier, 100);
  f.advance(5);
  assert.equal(deck.playedFrames(), 100);
  await deck.pump(2);
  assert.equal(graph.admit(300).frontier, 100, 'no late buffers are scheduled against the old clock');
  assert.equal(f.scheduled.length, 1);
  f.ctx.state = 'suspended';
  assert.equal(graph.rebase(500, 508), 100);
  assert.equal(graph.admit(300).frontier, 300);
  assert.deepEqual(f.scheduled.map(s => [s.start, s.frames]), [[8, 100], [508, 100], [608, 100]]);
  assert.equal(deck.playedFrames(), 100);
  deck.close(); graph.close();
});

test('linked graph decks preserve the original crossfade gain after a partial hold', async () => {
  const rate = 100, f = fakeContext(rate), graph = new MixGraph(f.ctx, 8);
  const a = new Deck(f.ctx, {} as AudioNode, sineSource(rate * 20, rate), {frames: rate * 20, gain: 1, graph});
  const b = new Deck(f.ctx, {} as AudioNode, sineSource(rate * 20, rate), {frames: rate * 20, gain: 1, graph});
  await a.pump(20); await b.pump(20); a.start(0);
  assert.equal(graph.admit(1500).frontier, 1500);
  const edge = linkDecks(f.ctx, a, b, {crossfadeSeconds: 6, sameAlbum: false});
  assert.equal(edge.start, 1400);
  f.ctx.currentTime = 14.58; f.ctx.state = 'suspended';
  const held = graph.rebase(1458, 1466);
  assert.equal(held, 1450);
  a.rebuildAutomation(held); b.rebuildAutomation(held);
  const event = (b.fade.gain as any).events.filter((item: unknown[]) => item[0] === 'set').at(-1);
  assert.ok(Math.abs(event[1] - Math.sin(Math.PI / 24)) < 0.002, 'the original fade value is evaluated at the held frame');
  a.close(); b.close(); graph.close();
});

test('a premature finite EOF fails without changing duration or claiming resampler completion', async () => {
  for (const outputRate of [44100, 48000]) {
    const f = fakeContext(outputRate), rate = 44100;
    const deck = new Deck(f.ctx, {} as AudioNode, sineSource(rate, rate), {frames: rate * 2, gain: 1, holdTail: true});
    const declared = deck.outFrames;
    await deck.pump(30);
    assert.match(String(deck.failure), /audio_source_ended_early/);
    assert.equal(deck.outFrames, declared, 'the measured duration is never shortened to hide truncation');
    assert.equal(deck.sourceComplete, false);
    assert.equal(deck.complete, false);
    deck.finishTail();
    assert.equal(deck.sourceComplete, false, 'a failed source cannot manufacture a valid filter tail');
    deck.close();
  }
});

test('a genuine short finite remainder validates before releasing its real resampler tail', async () => {
  for (const outputRate of [44100, 48000]) {
    const rate = 44100, frames = 11025, f = fakeContext(outputRate);
    const deck = new Deck(f.ctx, {} as AudioNode, sineSource(frames, rate), {frames, gain: 1, holdTail: true});
    await deck.pump(30);
    assert.equal(deck.failure, undefined);
    if (outputRate !== rate) assert.equal(deck.sourceComplete, false, 'actual filter tail is still pending');
    deck.finishTail();
    assert.equal(deck.sourceComplete, true);
    deck.start(0);
    assert.equal(f.scheduled.reduce((sum, entry) => sum + entry.frames, 0), deck.outFrames);
    assert.equal(deck.complete, true);
    deck.close();
  }
});
