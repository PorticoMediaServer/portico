import {createDecoder, type PacketDecoder, type Planar} from './decoder';
import {openDemuxer} from './demux';
import {JsFlacDecoder} from './flac-decoder';
import {DemuxError, u16le, type ByteReader, type Demuxer, type Packet, type SeekResult, type TrackConfig} from './types';
import type {SeekIndex} from '@core/playback-v1/audio-render.ts';

/**
 * One track, decoded and trimmed exactly (§18.1, §9.6 rule 2): `read()` returns the track's frames
 * [fromFrame, durationFrames) at the file's own rate, and nothing else. The raw decoded output is
 * counted from the packets fed; a platform decoder that drops leading frames by itself (Safari's
 * MP3 decoder drops its 529-frame delay; every Opus decoder applies the pre-skip) is accounted for
 * from the conformance table, so nothing is trimmed twice.
 */
export type TrackPlan = Readonly<{id: string; container: string; codec: string; sampleRate: number; channels: number; durationFrames: number; trim: Readonly<{startFrames: number; endFrames: number}>; bytes?: number}>;

export type BrowserEngine = 'blink' | 'webkit' | 'gecko' | 'other';
export function browserEngine(ua = typeof navigator === 'undefined' ? '' : navigator.userAgent): BrowserEngine {
  if (/Firefox\//.test(ua)) return 'gecko';
  if (/Chrome\/|Chromium\/|Edg\//.test(ua)) return 'blink';
  if (/AppleWebKit\//.test(ua)) return 'webkit';
  return 'other';
}

/**
 * Leading frames each engine's WebCodecs decoder drops by itself, per codec family, as measured on
 * the §18.8 fixtures (Chrome 152, Safari 27; `src/dev/audio-conformance.ts`). `preskip` means the
 * Opus header's pre-skip. A family missing here isn't used through WebCodecs on that engine.
 */
export const WEBCODECS_SKIP: Readonly<Record<BrowserEngine, Readonly<Record<string, number | 'preskip'>>>> = {
  blink: {mp3: 0, aac: 0, flac: 0, opus: 'preskip'},
  webkit: {mp3: 529, aac: 0, opus: 'preskip'},
  gecko: {},
  other: {},
};

/** Only the runtime probe writes new alignment evidence for the current browser. */
export function recordDecoderSkip(engine: BrowserEngine, family: string, skip: number) {
  if (!Number.isSafeInteger(skip) || skip < 0 || skip > 4096) return;
  (WEBCODECS_SKIP[engine] as Record<string, number | 'preskip'>)[family] = family === 'opus' ? 'preskip' : skip;
}

/** Packets a decoder must see before the one it should output from, after a seek. */
const PREROLL: Record<string, number> = {mp3: 3, aac: 2, opus: 4, vorbis: 2};
/** Bytes of packets a cold decoder needs ahead of the packet before the target's. An MP3 frame's
 * audio data can begin up to 511 bytes back, in earlier frames (the bit reservoir), and the frame
 * before the target has to decode exactly too (its second half overlaps the target's first): with
 * fewer, the first milliseconds after a part-way start differ from a continuous decode. Counted
 * as whole packets (headers and side information included), so twice the reservoir. */
const PREROLL_BYTES: Record<string, number> = {mp3: 1024};

export type DecoderChoice = Readonly<{decoder: PacketDecoder; skip: number; via: string}>;

export async function chooseDecoder(config: TrackConfig, engine = browserEngine()): Promise<DecoderChoice | undefined> {
  if (config.family === 'pcm') { const d = await createDecoder(config); return d && {decoder: d, skip: 0, via: 'pcm'}; }
  const known = WEBCODECS_SKIP[engine][config.family];
  if (known !== undefined) {
    const d = await createDecoder(config);
    if (d) return {decoder: d, skip: known === 'preskip' ? u16le(config.description ?? new Uint8Array(12), 10) : known, via: `webcodecs-${engine}`};
  }
  if (config.family === 'flac') return {decoder: new JsFlacDecoder(config), skip: 0, via: 'js-flac'};
  return undefined;
}

export class TrackStream {
  readonly sampleRate: number;
  readonly channels: number;
  readonly via: string;
  /** Trimmed frames still to come. */
  private remaining: number;
  private raw: number;
  private readonly from: number;
  private readonly to: number;
  private readonly decoder: PacketDecoder;
  private readonly packets: AsyncGenerator<Packet>;
  private pending: Planar[] = [];
  private ended = false;
  private closed = false;
  private readonly pad: boolean;
  /** Trimmed frames returned so far. */
  delivered = 0;

  private constructor(o: {plan: TrackPlan; choice: DecoderChoice; packets: AsyncGenerator<Packet>; rawStart: number; fromFrame: number; pad: boolean}) {
    this.pad = o.pad;
    this.sampleRate = o.plan.sampleRate;
    this.channels = o.plan.channels;
    this.via = o.choice.via;
    this.decoder = o.choice.decoder;
    this.packets = o.packets;
    this.raw = o.rawStart + o.choice.skip;
    this.from = o.plan.trim.startFrames + o.fromFrame;
    this.to = o.plan.trim.startFrames + o.plan.durationFrames;
    this.remaining = Math.max(0, o.plan.durationFrames - o.fromFrame);
  }

  /** Opens `plan` from trimmed frame `fromFrame` (a seek). Undefined when no decoder here can play
   * it. `index` reads the server's seek index of a stream with none of its own (MP3, ADTS, Ogg). */
  static async open(plan: TrackPlan, reader: ByteReader, fromFrame = 0, signal?: AbortSignal, o: {pad?: boolean; index?: () => Promise<SeekIndex | undefined>} = {}): Promise<TrackStream | undefined> {
    const demuxer = await openDemuxer(plan.container, reader);
    if (demuxer.config.sampleRate !== plan.sampleRate) throw new DemuxError('The file’s sample rate isn’t the plan’s.');
    // A stream with no index of its own starts part-way from the server's: read from the nearest
    // entry, never what comes before it.
    const index = fromFrame > 0 && !demuxer.seek && demuxer.packetsFrom && demuxer.config.family !== 'vorbis' && o.index ? await o.index() : undefined;
    // Without one, a deep frame can't be found without fetching the prefix: the ordinary player
    // owns that seek (including its codec preroll and VBR index).
    if (fromFrame > plan.sampleRate * 30 && !demuxer.seek && !demuxer.locate && !index) return undefined;
    const choice = await chooseDecoder(demuxer.config);
    if (!choice) return undefined;
    // A decoder that has discarded more than the intended leading trim cannot reconstruct
    // the start (for example a no-LAME MP3 on a decoder with intrinsic delay handling).
    if (fromFrame === 0 && choice.skip > plan.trim.startFrames) { choice.decoder.close(); return undefined; }
    const target = plan.trim.startFrames + Math.max(0, Math.min(fromFrame, plan.durationFrames));
    let packets: AsyncGenerator<Packet>, rawStart: number;
    try {
      ({packets, rawStart} = demuxer.config.family === 'vorbis'
        ? {packets: demuxer.packets(signal), rawStart: 0}
        : await startAt(demuxer, target - choice.skip, PREROLL[demuxer.config.family] ?? 0, signal, index, PREROLL_BYTES[demuxer.config.family] ?? 0));
    } catch (e) { choice.decoder.close(); throw e; }
    return new TrackStream({plan, choice, packets, rawStart, fromFrame: Math.max(0, fromFrame), pad: o.pad !== false});
  }

  get done() { return this.remaining === 0; }

  /** Up to `max` frames of the track, or undefined at its end. */
  async read(max: number): Promise<Planar | undefined> {
    while (!this.closed && this.remaining > 0) {
      const block = this.pending.shift();
      if (block) {
        const start = this.raw, end = start + block.frames;
        this.raw = end;
        const a = Math.max(start, this.from), b = Math.min(end, this.to);
        if (b <= a) continue;
        const take = Math.min(b - a, max, this.remaining);
        const channels = block.channels.map(c => c.subarray(a - start, a - start + take));
        this.remaining -= take;
        this.delivered += take;
        if (a - start + take < block.frames) {
          // Keep the rest of this block for the next read.
          const rest = a - start + take;
          this.raw = start + rest;
          this.pending.unshift({channels: block.channels.map(c => c.subarray(rest)), frames: block.frames - rest});
        }
        return {channels, frames: take};
      }
      if (this.ended) {
        // A stream that ended early (a converted stream cut at a prepared presentation's budget):
        // the caller continues it from `delivered`.
        if (!this.pad) return undefined;
        // The decoder produced less than the plan says: pad with silence so every later edge stays
        // exact (a short file is a server fact error, not a reason to shift the album).
        const take = Math.min(max, this.remaining);
        this.remaining -= take;
        this.delivered += take;
        return {channels: Array.from({length: this.channels}, () => new Float32Array(take)), frames: take};
      }
      const next = await this.packets.next();
      if (next.done) { this.pending.push(...await this.decoder.flush()); this.ended = true; continue; }
      this.decoder.decode(next.value);
      this.pending.push(...await this.decoder.drain(8));
    }
    return undefined;
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    this.decoder.close();
    void this.packets.return(undefined).catch(() => {});
    this.pending = [];
  }
}

/**
 * Packets from the one that lets the decoder output raw frame `target`, `preroll` packets earlier
 * so it has settled. `rawStart` is the raw index of the first packet's first frame. Packets are
 * skipped by their known lengths without decoding.
 */
async function startAt(demuxer: Demuxer, target: number, preroll: number, signal?: AbortSignal, index?: SeekIndex, prerollBytes = 0): Promise<{packets: AsyncGenerator<Packet>; rawStart: number}> {
  if (target <= 0) return {packets: demuxer.packets(signal), rawStart: 0};
  if (demuxer.seek) {
    const found = await demuxer.seek(target, preroll, signal);
    if (found) return found;
  }
  if (index && demuxer.packetsFrom) {
    // The last entry at or before the target, or an earlier one when it leaves too few packets
    // before the target to settle the decoder. Only that much of the file is read.
    for (let i = lastAtOrBefore(index.frames, target); i >= 0; i--) {
      const found = await walk(demuxer.packetsFrom(index.offsets[i]!, signal), index.frames[i]!, target, preroll, prerollBytes);
      if (found === 'counted') break;
      if (found !== 'short') return found;
    }
  }
  const found = await walk(demuxer.packets(signal), 0, target, preroll, prerollBytes);
  if (typeof found === 'string') throw new DemuxError('This stream can’t be started part-way.');
  return found;
}

/** Index of the last of `frames` (rising) at or before `target`, or -1. */
export function lastAtOrBefore(frames: readonly number[], target: number): number {
  let lo = 0, hi = frames.length - 1, found = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (frames[mid]! <= target) { found = mid; lo = mid + 1; } else hi = mid - 1;
  }
  return found;
}

/**
 * Walks packets starting at raw frame `raw` to the one holding `target`, keeping the `preroll`
 * before it, and at least `prerollBytes` of packets ahead of the one before it. 'short': fewer
 * than that came before the target (start earlier, unless this is the stream's start); 'counted':
 * a packet with no length in frames, so no exact start here.
 */
async function walk(all: AsyncGenerator<Packet>, raw: number, target: number, preroll: number, prerollBytes = 0): Promise<SeekResult | 'short' | 'counted'> {
  const recent: {packet: Packet; raw: number}[] = [];
  // Bytes of the held packets other than the last two (the newest, and the one before it).
  let ahead = 0;
  const enough = () => recent.length >= preroll + 1 && ahead >= prerollBytes;
  const startRaw = raw;
  for (;;) {
    const next = await all.next();
    if (next.done) break;
    const p = next.value;
    if (p.frames === undefined) { void all.return(undefined).catch(() => {}); return 'counted'; }
    recent.push({packet: p, raw});
    if (recent.length > 2) ahead += recent[recent.length - 3]!.packet.data.length;
    // Drop the oldest while what remains is still enough.
    while (recent.length > preroll + 1 && (recent.length > 2 ? ahead - recent[0]!.packet.data.length : 0) >= prerollBytes) {
      if (recent.length > 2) ahead -= recent[0]!.packet.data.length;
      recent.shift();
    }
    if (raw + p.frames > target) break;
    raw += p.frames;
  }
  if (startRaw > 0 && !enough()) { void all.return(undefined).catch(() => {}); return 'short'; }
  const first = recent[0];
  const held = recent.map(r => r.packet);
  async function* chain(): AsyncGenerator<Packet> { yield* held; yield* all; }
  return {packets: chain(), rawStart: first?.raw ?? raw};
}
