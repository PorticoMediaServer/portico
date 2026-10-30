import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
import {TrackStream, browserEngine} from '../src/platform/playback/audio/track.ts';
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
      const node: any = {buffer: null, onended: null, started: undefined, connect() {}, disconnect() {}, start(t: number) { node.started = t; scheduled.push({start: Math.round(t * sampleRate), frames: node.buffer.length, node}); }, stop() { node.stopped = true; }};
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

const dir = new URL('../../fixtures/audio-gapless/', import.meta.url);
const fileReader = (bytes: Uint8Array) => ({size: bytes.length, read: async (o: number, n: number) => bytes.slice(o, Math.min(bytes.length, o + n))});
const sidecars = readdirSync(dir).filter(f => f.endsWith('.json') && f !== 'album.json').map(f => JSON.parse(readFileSync(new URL(f, dir), 'utf8')));

async function check(s: any, from: number) {
  const stream = await TrackStream.open(s, fileReader(new Uint8Array(readFileSync(new URL(s.file, dir)))), from);
  assert.ok(stream, s.file);
  let n = from, worst = 0;
  const {amplitude: a, frequency: f, offset} = s.signal;
  for (;;) {
    const block = await stream!.read(4096);
    if (!block) break;
    for (let i = 0; i < block.frames; i++, n++) worst = Math.max(worst, Math.abs(block.channels[0]![i]! - a * Math.sin(2 * Math.PI * f * (n + offset) / s.sampleRate)));
  }
  stream!.close();
  assert.equal(n, s.durationFrames, `${s.file} from ${from}`);
  assert.ok(worst <= s.tolerance, `${s.file} from ${from}: ${worst}`);
}

test('lossless tracks come out exactly: durationFrames frames matching the signal, from the start and after a seek', async () => {
  for (const s of sidecars.filter(x => x.codec === 'flac' || x.codec.startsWith('pcm'))) {
    await check(s, 0);
    await check(s, 12345);
    await check(s, s.durationFrames - 1);
  }
});

test('the engine is told apart by user agent', () => {
  assert.equal(browserEngine('Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0 Safari/605.1.15'), 'webkit');
  assert.equal(browserEngine('Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0 Safari/537.36'), 'blink');
  assert.equal(browserEngine('Mozilla/5.0 (Macintosh; rv:140.0) Gecko/20100101 Firefox/140.0'), 'gecko');
});

test('resampling for output: exact frame totals and a clean signal (44.1 → 48 kHz, 96 → 48 kHz)', async () => {
  const {Resampler} = await import('../src/platform/playback/audio/resampler.ts');
  for (const [from, to] of [[44100, 48000], [96000, 48000], [48000, 44100]] as const) {
    const n = from * 2, r = new Resampler(from, to, 1);
    const sig = (i: number, rate: number) => 0.5 * Math.sin(2 * Math.PI * 441 * i / rate);
    const out: number[] = [];
    for (let at = 0; at < n; at += 1000) {
      const len = Math.min(1000, n - at), block = new Float32Array(len);
      for (let i = 0; i < len; i++) block[i] = sig(at + i, from);
      out.push(...r.process([block], len).channels[0]!);
    }
    out.push(...r.flush().channels[0]!);
    assert.equal(out.length, Resampler.outputFrames(n, from, to), `${from}→${to}`);
    let worst = 0;
    for (let j = 200; j < out.length - 200; j++) worst = Math.max(worst, Math.abs(out[j]! - sig(j, to)));
    assert.ok(worst < 2e-3, `${from}→${to}: ${worst}`);
  }
});

test('speed changes keep pitch: WSOLA at 0.5×, 1.5× and 3× keeps a 441 Hz tone at 441 Hz, for input ÷ rate frames', async () => {
  const {Stretch} = await import('../src/platform/playback/audio/stretch.ts');
  const rate = 44100, frames = rate * 4;
  for (const speed of [0.5, 1.5, 3]) {
    const s = new Stretch(sineSource(frames, rate), speed, frames);
    const out: number[] = [];
    for (;;) { const b = await s.read(4096); if (!b) break; for (const v of b.channels[0]!) out.push(v); }
    assert.equal(out.length, Math.floor(frames / speed), `${speed}×`);
    // Frequency from zero crossings in the steady middle.
    const mid = out.slice(Math.floor(out.length * 0.25), Math.floor(out.length * 0.75));
    let crossings = 0;
    for (let i = 1; i < mid.length; i++) if (mid[i - 1]! < 0 && mid[i]! >= 0) crossings++;
    const hz = crossings / (mid.length / rate);
    assert.ok(Math.abs(hz - 441) < 5, `${speed}×: ${hz.toFixed(1)} Hz`);
    let peak = 0; for (const v of mid) peak = Math.max(peak, Math.abs(v));
    assert.ok(peak > 0.45 && peak < 0.55, `${speed}×: level ${peak}`);
  }
});

test('an MP4 seek starts from the target sample and reads nothing before it', async () => {
  const {openMp4} = await import('../src/platform/playback/audio/mp4.ts');
  const s = sidecars.find(x => x.file.endsWith('.m4a'))!;
  const bytes = new Uint8Array(readFileSync(new URL(s.file, dir)));
  const reads: {offset: number; length: number}[] = [];
  const reader = {size: bytes.length, read: async (o: number, n: number) => { reads.push({offset: o, length: n}); return bytes.slice(o, Math.min(bytes.length, o + n)); }};
  const demuxer = await openMp4(reader);
  const header = reads.reduce((sum, r) => sum + r.length, 0);
  reads.length = 0;
  const t = demuxer.table;
  const scale = demuxer.config.sampleRate / t.timescale;
  const last = t.sizes.length - 3;
  let raw = 0;
  for (let i = 0; i < last; i++) raw += Math.round(t.durations[i]! * scale);
  const {packets, rawStart} = demuxer.seek!(raw + 1, 1);
  const first = await packets.next();
  assert.equal(first.done, false);
  assert.equal(first.value!.offset, t.offsets[last - 1], 'starts one packet (the preroll) before the target');
  assert.equal(rawStart, raw - Math.round(t.durations[last - 1]! * scale));
  assert.ok(reads.every(r => r.offset >= t.offsets[last - 1]!), 'read nothing before the preroll packet');
  assert.ok(header < bytes.length, 'the header read is not the whole file');
});

test('a damaged MP4 sample count fails cleanly instead of allocating from it', async () => {
  const {openMp4} = await import('../src/platform/playback/audio/mp4.ts');
  const s = sidecars.find(x => x.file.endsWith('.m4a'))!;
  const bytes = new Uint8Array(readFileSync(new URL(s.file, dir)));
  const at = Buffer.from(bytes).indexOf('stsz');
  assert.ok(at > 0);
  assert.equal(new DataView(bytes.buffer).getUint32(at + 8), 0, 'fixture has per-sample sizes');
  new DataView(bytes.buffer).setUint32(at + 12, 40_000_000);
  await assert.rejects(openMp4(fileReader(bytes)), /Damaged MP4 sample sizes/);
});

test('resampler carries history and fractional phase across several nonaligned track boundaries', async () => {
  const {Resampler} = await import('../src/platform/playback/audio/resampler.ts');
  for (const [from, to] of [[44100, 48000], [96000, 48000], [48000, 44100]]) {
    const sizes = [10001, 11003, 12007];
    const input = Float32Array.from({length: sizes.reduce((a,b) => a+b,0)}, (_,i) => 0.3*Math.sin(i*0.057)+0.1*Math.cos(i*0.137));
    const whole = new Resampler(from!, to!, 1);
    const expected = [...whole.process([input],input.length).channels[0]!, ...whole.flush().channels[0]!];
    let split = new Resampler(from!, to!, 1), offset = 0;
    const actual: number[] = [];
    for (let i=0;i<sizes.length;i++) {
      const n=sizes[i]!;
      actual.push(...split.process([input.subarray(offset,offset+n)],n).channels[0]!); offset+=n;
      if(i+1<sizes.length){const carry=split.continue([input.subarray(offset,offset+64)],64);actual.push(...carry.tail.channels[0]!);split=carry.next;}
      else actual.push(...split.flush().channels[0]!);
    }
    assert.deepEqual(actual,expected,`${from} → ${to}: filtering the album equals filtering its tracks continuously`);
  }
});

test('a decoder cannot silently discard real leading frames when its intrinsic skip exceeds the plan',async()=>{
 const {WEBCODECS_SKIP,recordDecoderSkip}=await import('../src/platform/playback/audio/track.ts');
 const globals=globalThis as any, original=globals.AudioDecoder, chunk=globals.EncodedAudioChunk, engine=browserEngine();
 const prior={...WEBCODECS_SKIP[engine]};let closed=0;
 try {
  globals.AudioDecoder=class {state='configured';static async isConfigSupported(){return{supported:true};}configure(){}close(){closed++;this.state='closed';}};
  globals.EncodedAudioChunk=class {};
  recordDecoderSkip(engine,'mp3',529);
  const s=sidecars.find(x=>x.codec==='mp3');
  const stream=await TrackStream.open({...s,trim:{startFrames:0,endFrames:0}},fileReader(new Uint8Array(readFileSync(new URL(s.file,dir)))));
  assert.equal(stream,undefined);assert.equal(closed,1);
 }finally{globals.AudioDecoder=original;globals.EncodedAudioChunk=chunk;for(const key of Object.keys(WEBCODECS_SKIP[engine]))delete (WEBCODECS_SKIP[engine] as Record<string,unknown>)[key];Object.assign(WEBCODECS_SKIP[engine],prior);}
});
