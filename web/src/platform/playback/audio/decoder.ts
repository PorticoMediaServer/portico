import {pcmToPlanar} from './pcm';
import type {Packet, TrackConfig} from './types';

/**
 * Packet decoding. WebCodecs `AudioDecoder` where the browser has it for the codec; PCM here.
 * Output is planar float at the file's own rate, in order. A decoder is fed packets and yields
 * whatever it has produced so far; `flush()` drains it at the end.
 */
export type Planar = Readonly<{channels: readonly Float32Array[]; frames: number}>;

export interface PacketDecoder {
  /** Frames a platform decoder drops by itself when it starts (its own priming/delay handling). */
  readonly via: string;
  decode(packet: Packet): void;
  /** Output produced so far (in order), taken. */
  take(): Planar[];
  /** Waits until queued packets are decoded (bounded backlog), then returns output. */
  drain(maxQueue?: number): Promise<Planar[]>;
  flush(): Promise<Planar[]>;
  close(): void;
}

class PcmDecoder implements PacketDecoder {
  readonly via = 'pcm';
  private out: Planar[] = [];
  private readonly config: TrackConfig;
  constructor(config: TrackConfig) { this.config = config; }
  decode(p: Packet) {
    const channels = pcmToPlanar(p.data, this.config.numberOfChannels, this.config.pcm!);
    this.out.push({channels, frames: channels[0]?.length ?? 0});
  }
  take() { const o = this.out; this.out = []; return o; }
  async drain() { return this.take(); }
  async flush() { return this.take(); }
  close() { this.out = []; }
}

/** The subset of WebCodecs this file uses (the DOM lib may lack AudioDecoder in some targets). */
type WCAudioData = {numberOfFrames: number; numberOfChannels: number; format: string | null; copyTo(dest: Float32Array, opts: {planeIndex: number; format?: string}): void; close(): void};
type WCAudioDecoder = {decodeQueueSize: number; state: string; configure(c: unknown): void; decode(chunk: unknown): void; flush(): Promise<void>; close(): void; addEventListener?(t: string, f: () => void): void};
type WC = {
  AudioDecoder: {new (init: {output: (d: WCAudioData) => void; error: (e: unknown) => void}): WCAudioDecoder; isConfigSupported(c: unknown): Promise<{supported?: boolean}>};
  EncodedAudioChunk: new (init: {type: string; timestamp: number; duration?: number; data: Uint8Array}) => unknown;
};
const wc = (): WC | undefined => {
  const g = globalThis as unknown as Partial<WC>;
  return g.AudioDecoder && g.EncodedAudioChunk ? (g as WC) : undefined;
};

/** AudioData → planar float, converting when a browser can't copy to `f32-planar` itself. */
export function planes(data: WCAudioData): Float32Array[] {
  const frames = data.numberOfFrames, n = data.numberOfChannels, out: Float32Array[] = [];
  try {
    for (let ch = 0; ch < n; ch++) { const plane = new Float32Array(frames); data.copyTo(plane, {planeIndex: ch, format: 'f32-planar'}); out.push(plane); }
    return out;
  } catch { out.length = 0; }
  const format = data.format ?? '';
  const planar = format.endsWith('-planar'), base = format.replace('-planar', '');
  const scale = base === 's16' ? 1 / 32768 : base === 's32' ? 1 / 2147483648 : base === 'u8' ? 1 / 128 : 1;
  const Typed = base === 's16' ? Int16Array : base === 's32' ? Int32Array : base === 'u8' ? Uint8Array : Float32Array;
  const convert = (v: number) => (base === 'u8' ? (v - 128) * scale : v * scale);
  for (let ch = 0; ch < n; ch++) out.push(new Float32Array(frames));
  if (planar) {
    for (let ch = 0; ch < n; ch++) {
      const raw = new Typed(frames);
      data.copyTo(raw as unknown as Float32Array, {planeIndex: ch});
      for (let i = 0; i < frames; i++) out[ch]![i] = convert(raw[i]!);
    }
  } else {
    const raw = new Typed(frames * n);
    data.copyTo(raw as unknown as Float32Array, {planeIndex: 0});
    for (let i = 0; i < frames; i++) for (let ch = 0; ch < n; ch++) out[ch]![i] = convert(raw[i * n + ch]!);
  }
  return out;
}

export function webCodecsConfig(config: TrackConfig): Record<string, unknown> {
  return {codec: config.codec, sampleRate: config.sampleRate, numberOfChannels: config.numberOfChannels, ...(config.description ? {description: config.description} : {})};
}

export async function webCodecsSupports(config: TrackConfig): Promise<boolean> {
  const api = wc();
  if (!api || config.codec === 'pcm') return false;
  try { return (await api.AudioDecoder.isConfigSupported(webCodecsConfig(config))).supported === true; } catch { return false; }
}

class WebCodecsDecoder implements PacketDecoder {
  readonly via = 'webcodecs';
  private out: Planar[] = [];
  private failure: unknown;
  private timestamp = 0;
  private readonly decoder: WCAudioDecoder;
  private readonly chunk: WC['EncodedAudioChunk'];
  private readonly rate: number;
  private waiters: (() => void)[] = [];
  constructor(api: WC, config: TrackConfig) {
    this.chunk = api.EncodedAudioChunk;
    this.rate = config.sampleRate;
    this.decoder = new api.AudioDecoder({
      output: data => {
        try {
          const channels = planes(data);
          this.out.push({channels, frames: data.numberOfFrames});
        } catch (e) { this.failure = e; } finally { data.close(); }
        this.wake();
      },
      error: e => { this.failure = e; this.wake(); },
    });
    this.decoder.configure(webCodecsConfig(config));
    this.decoder.addEventListener?.('dequeue', () => this.wake());
  }
  private wake() { const w = this.waiters; this.waiters = []; for (const f of w) f(); }
  private check() { if (this.failure) throw this.failure instanceof Error ? this.failure : new Error('The audio decoder failed.'); }
  decode(p: Packet) {
    this.check();
    const frames = p.frames ?? 0;
    this.decoder.decode(new this.chunk({type: 'key', timestamp: Math.round(this.timestamp * 1e6 / this.rate), ...(frames ? {duration: Math.round(frames * 1e6 / this.rate)} : {}), data: p.data}));
    this.timestamp += frames;
  }
  take() { this.check(); const o = this.out; this.out = []; return o; }
  async drain(maxQueue = 16) {
    while (this.decoder.decodeQueueSize > maxQueue && !this.failure) await new Promise<void>(r => { this.waiters.push(r); setTimeout(r, 50); });
    return this.take();
  }
  async flush() { await this.decoder.flush(); return this.take(); }
  close() { try { if (this.decoder.state !== 'closed') this.decoder.close(); } catch { /* closed */ } this.out = []; this.wake(); }
}

export async function createDecoder(config: TrackConfig): Promise<PacketDecoder | undefined> {
  if (config.codec === 'pcm') return new PcmDecoder(config);
  const api = wc();
  if (!api || !(await webCodecsSupports(config))) return undefined;
  return new WebCodecsDecoder(api, config);
}
