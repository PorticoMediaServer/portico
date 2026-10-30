import test from 'node:test';
import assert from 'node:assert/strict';
import {TrackStream, browserEngine, lastAtOrBefore, recordDecoderSkip, WEBCODECS_SKIP} from '../src/platform/playback/audio/track.ts';
import {parseSeekIndex, type SeekIndex} from '@core/playback-v1/audio-render.ts';

/**
 * A part-way start in a file with no index of its own (MP3, ADTS, Ogg) reads the server's seek
 * index and the file from the nearest entry: only that much is fetched, and the first sample
 * returned is exactly the frame asked for. The streams here are built so every packet names its
 * own number in its payload, and the stand-in decoder outputs each sample's true raw position;
 * a start that lands even one frame off is seen.
 */

/** An MPEG-1 Layer III frame, 128 kbit/s at 44.1 kHz (417 bytes, 1152 frames), numbered `n`. */
function mp3Frame(n: number, info = false): Uint8Array {
  const f = new Uint8Array(417);
  f.set([0xff, 0xfb, 0x90, 0xc4]); // MPEG-1 L3, no CRC, 128 kbit/s, 44.1 kHz, no padding, mono
  if (info) f.set([0x58, 0x69, 0x6e, 0x67], 4 + 17); // "Xing" after a mono side-info block
  new DataView(f.buffer).setUint32(200, n);
  return f;
}
/** An ADTS AAC-LC frame at 44.1 kHz, mono, one raw data block (1024 frames), numbered `n`. */
function adtsFrame(n: number): Uint8Array {
  const length = 300, f = new Uint8Array(length);
  f.set([0xff, 0xf1, (1 << 6) | (4 << 2), (1 << 6) | (length >> 11), (length >> 3) & 0xff, ((length & 7) << 5) | 0x1f, 0xfc]);
  new DataView(f.buffer).setUint32(100, n);
  return f;
}
const join = (parts: Uint8Array[]) => { const out = new Uint8Array(parts.reduce((s, p) => s + p.length, 0)); let o = 0; for (const p of parts) { out.set(p, o); o += p.length; } return out; };

/** A stand-in WebCodecs decoder: each chunk's samples are its frame number × frames + i. */
function installDecoder(frames: number, payloadAt: number) {
  const g = globalThis as any, saved = {AudioDecoder: g.AudioDecoder, EncodedAudioChunk: g.EncodedAudioChunk};
  g.EncodedAudioChunk = class { data: Uint8Array; constructor(init: {data: Uint8Array}) { this.data = init.data; } };
  g.AudioDecoder = class {
    state = 'configured'; decodeQueueSize = 0; private output: (d: unknown) => void;
    constructor(init: {output: (d: unknown) => void}) { this.output = init.output; }
    static async isConfigSupported() { return {supported: true}; }
    configure() {}
    decode(chunk: {data: Uint8Array}) {
      const n = new DataView(chunk.data.buffer, chunk.data.byteOffset).getUint32(payloadAt);
      this.output({numberOfFrames: frames, numberOfChannels: 1, format: 'f32-planar', copyTo: (dest: Float32Array) => { for (let i = 0; i < frames; i++) dest[i] = n * frames + i; }, close() {}});
    }
    async flush() {}
    close() { this.state = 'closed'; }
  };
  return () => { g.AudioDecoder = saved.AudioDecoder; g.EncodedAudioChunk = saved.EncodedAudioChunk; };
}

function reader(bytes: Uint8Array) {
  const reads: [number, number][] = [];
  return {reads, size: bytes.length, read: async (o: number, n: number) => { reads.push([o, n]); return bytes.slice(o, Math.min(bytes.length, o + n)); }};
}

async function firstSample(plan: any, bytes: Uint8Array, from: number, index?: SeekIndex) {
  const r = reader(bytes);
  const stream = await TrackStream.open(plan, r, from, undefined, index ? {index: async () => index} : {});
  if (!stream) return {stream: undefined, reads: r.reads};
  const block = await stream.read(1), next = await stream.read(1);
  stream.close();
  return {value: block?.channels[0]![0], second: next?.channels[0]![0], reads: r.reads};
}

test('an MP3 started part-way reads from the nearest index entry and lands on the exact frame', async () => {
  const engine = browserEngine(), prior = {...WEBCODECS_SKIP[engine]};
  const restore = installDecoder(1152, 200);
  try {
    recordDecoderSkip(engine, 'mp3', 0);
    // An ID3 tag, the Xing frame (not audio), then 6000 frames (about 157 s).
    const id3 = new Uint8Array(1024); id3.set([0x49, 0x44, 0x33, 3, 0, 0, 0, 0, 7, 0x76]);
    const frames = Array.from({length: 6000}, (_, i) => mp3Frame(i));
    const bytes = join([id3, mp3Frame(0, true), ...frames]);
    const first = 1024 + 417;
    // The server's entries: every 115th audio frame (about 3 s).
    const offsets: number[] = [], positions: number[] = [];
    for (let i = 0; i < 6000; i += 115) { offsets.push(first + i * 417); positions.push(i * 1152); }
    const index = parseSeekIndex({version: 1, offsets, frames: positions})!;
    const start = 1105;
    const plan = {id: 'mp3', container: 'mp3', codec: 'mp3', sampleRate: 44100, channels: 1, durationFrames: 6000 * 1152 - start - 600, trim: {startFrames: start, endFrames: 600}, bytes: bytes.length};
    for (const from of [44100 * 40 + 7, 44100 * 100, 115 * 1152 * 3 - start, 1152 * 5999 - start - 1]) {
      const got = await firstSample(plan, bytes, from, index);
      assert.equal(got.value, start + from, `from ${from}`);
      assert.equal(got.second, start + from + 1);
      // Nothing before the entry's preroll was read: one entry back at most.
      const lowest = Math.min(...got.reads.filter(([o]) => o > 4096).map(([o]) => o));
      const entry = lastAtOrBefore(positions, start + from);
      assert.ok(lowest >= offsets[Math.max(0, entry - 1)]! , `from ${from}: read from ${lowest}`);
    }
    // Without an index a deep start is left to the ordinary player, as before.
    assert.equal((await firstSample(plan, bytes, 44100 * 40)).stream, undefined);
    // A start inside the first entry's step walks from the beginning, exactly.
    assert.equal((await firstSample(plan, bytes, 1000, index)).value, start + 1000);
  } finally {
    restore();
    for (const key of Object.keys(WEBCODECS_SKIP[engine])) delete (WEBCODECS_SKIP[engine] as Record<string, unknown>)[key];
    Object.assign(WEBCODECS_SKIP[engine], prior);
  }
});

test('an ADTS stream started part-way lands on the exact frame; an entry too close to the target is passed over for the one before', async () => {
  const engine = browserEngine(), prior = {...WEBCODECS_SKIP[engine]};
  const restore = installDecoder(1024, 100 - 7); // the packet is the frame without its header
  try {
    recordDecoderSkip(engine, 'aac', 0);
    const bytes = join(Array.from({length: 5000}, (_, i) => adtsFrame(i)));
    const offsets: number[] = [], positions: number[] = [];
    for (let i = 0; i < 5000; i += 130) { offsets.push(i * 300); positions.push(i * 1024); }
    const index = parseSeekIndex({version: 1, offsets, frames: positions})!;
    const plan = {id: 'aac', container: 'adts', codec: 'aac', sampleRate: 44100, channels: 1, durationFrames: 5000 * 1024 - 2048, trim: {startFrames: 2048, endFrames: 0}, bytes: bytes.length};
    // Right on an entry: the decoder needs packets before it, so the entry before is used.
    const onEntry = 130 * 20 * 1024 - 2048;
    const got = await firstSample(plan, bytes, onEntry, index);
    assert.equal(got.value, onEntry + 2048);
    assert.ok(got.reads.some(([o, n]) => o <= offsets[19]! && o + n > offsets[19]!), 'read from the entry before');
    for (const from of [44100 * 30, 44100 * 90 + 3]) assert.equal((await firstSample(plan, bytes, from, index)).value, from + 2048);
  } finally {
    restore();
    for (const key of Object.keys(WEBCODECS_SKIP[engine])) delete (WEBCODECS_SKIP[engine] as Record<string, unknown>)[key];
    Object.assign(WEBCODECS_SKIP[engine], prior);
  }
});

test('the index entry for a target is the last at or before it', () => {
  assert.equal(lastAtOrBefore([0, 10, 20], 0), 0);
  assert.equal(lastAtOrBefore([0, 10, 20], 19), 1);
  assert.equal(lastAtOrBefore([0, 10, 20], 20), 2);
  assert.equal(lastAtOrBefore([5, 10], 4), -1);
});

test('an MP3 part-way start feeds the decoder the bit reservoir: enough bytes before the frame ahead of the target', async () => {
  const engine = browserEngine(), prior = {...WEBCODECS_SKIP[engine]};
  const restore = installDecoder(1152, 50);
  const fed: number[] = [];
  const g = globalThis as any, decode = g.AudioDecoder.prototype.decode;
  g.AudioDecoder.prototype.decode = function (chunk: {data: Uint8Array}) { fed.push(new DataView(chunk.data.buffer, chunk.data.byteOffset).getUint32(50)); return decode.call(this, chunk); };
  try {
    recordDecoderSkip(engine, 'mp3', 0);
    // 32 kbit/s frames (104 bytes): the 511-byte reservoir spans several of them.
    const small = (n: number, info = false) => { const f = new Uint8Array(104); f.set([0xff, 0xfb, 0x10, 0xc4]); if (info) f.set([0x58, 0x69, 0x6e, 0x67], 4 + 17); new DataView(f.buffer).setUint32(50, n); return f; };
    const bytes = join([small(0, true), ...Array.from({length: 3000}, (_, i) => small(i))]);
    const offsets: number[] = [], positions: number[] = [];
    for (let i = 0; i < 3000; i += 115) { offsets.push(104 + i * 104); positions.push(i * 1152); }
    const index = parseSeekIndex({version: 1, offsets, frames: positions})!;
    const start = 1105;
    const plan = {id: 'mp3-small', container: 'mp3', codec: 'mp3', sampleRate: 44100, channels: 1, durationFrames: 3000 * 1152 - start - 600, trim: {startFrames: start, endFrames: 600}, bytes: bytes.length};
    for (const from of [44100 * 40 + 7, 115 * 1152 * 4 - start + 3]) {
      fed.length = 0;
      const got = await firstSample(plan, bytes, from, index);
      assert.equal(got.value, start + from, `from ${from}`);
      const targetFrame = Math.floor((start + from) / 1152);
      // The frames fed ahead of the one before the target's hold at least 1024 bytes (twice the reservoir).
      assert.ok((targetFrame - 1 - fed[0]!) * 104 >= 1024, `from ${from}: decoding began at frame ${fed[0]} for frame ${targetFrame}`);
    }
  } finally {
    g.AudioDecoder.prototype.decode = decode;
    restore();
    for (const key of Object.keys(WEBCODECS_SKIP[engine])) delete (WEBCODECS_SKIP[engine] as Record<string, unknown>)[key];
    Object.assign(WEBCODECS_SKIP[engine], prior);
  }
});
