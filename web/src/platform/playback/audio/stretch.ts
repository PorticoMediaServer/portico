import type {Planar} from './decoder';
import type {PcmSource} from './engine';

/**
 * Speed changes that keep pitch (§9.6 rule 3: audiobooks 0.5–3.0): WSOLA time-scale modification.
 * 40 ms Hann frames overlap by half at the output; each frame's input is taken near its nominal
 * position (output position × rate), at the offset (±12 ms) that best continues the previous
 * frame's waveform, so speech and music keep their pitch without phasing. The output is exactly
 * `floor(inputFrames / rate)` frames; the first 20 ms fade in (a speed change is never a gapless
 * edge).
 */
export class Stretch implements PcmSource {
  readonly sampleRate: number;
  readonly channels: number;
  readonly rate: number;
  readonly outputFrames: number;
  private readonly source: PcmSource;
  private readonly n: number; // frame length
  private readonly hop: number; // output hop (n / 2)
  private readonly tolerance: number;
  private readonly window: Float32Array;
  /** Input kept from absolute frame `base`. */
  private input: Float32Array[];
  private base = 0;
  private inputEnded = false;
  /** Overlap-add accumulator for output from absolute frame `outBase`. */
  private acc: Float32Array[];
  private produced = 0; // output frames returned
  private frame = 0; // next output frame index k (placed at k × hop)
  private previous = -1; // input position of the previous frame
  private closed = false;

  constructor(source: PcmSource, rate: number, inputFrames: number) {
    this.source = source;
    this.sampleRate = source.sampleRate;
    this.channels = source.channels;
    this.rate = rate;
    this.outputFrames = Math.floor(inputFrames / rate);
    this.n = 2 * Math.round(source.sampleRate * 0.02);
    this.hop = this.n / 2;
    this.tolerance = Math.round(source.sampleRate * 0.012);
    this.window = new Float32Array(this.n);
    for (let i = 0; i < this.n; i++) this.window[i] = 0.5 - 0.5 * Math.cos(2 * Math.PI * i / this.n); // periodic Hann: halves sum to 1
    this.input = Array.from({length: this.channels}, () => new Float32Array(0));
    this.acc = Array.from({length: this.channels}, () => new Float32Array(2 * this.n));
  }

  private get inputEnd() { return this.base + (this.input[0]?.length ?? 0); }

  private async fill(until: number): Promise<void> {
    while (!this.inputEnded && this.inputEnd < until) {
      const block = await this.source.read(8192);
      if (!block) { this.inputEnded = true; break; }
      for (let ch = 0; ch < this.channels; ch++) {
        const old = this.input[ch]!, next = new Float32Array(old.length + block.frames);
        next.set(old); next.set(block.channels[ch]!.subarray(0, block.frames), old.length);
        this.input[ch] = next;
      }
    }
  }

  private sample(ch: number, at: number): number {
    const i = at - this.base, a = this.input[ch]!;
    return i >= 0 && i < a.length ? a[i]! : 0;
  }

  /** The input offset near `nominal` whose waveform best continues `natural` (mono, decimated coarse search, then fine). */
  private bestOffset(nominal: number, natural: number): number {
    const len = this.hop, lo = Math.max(this.base, nominal - this.tolerance), hi = nominal + this.tolerance;
    const mono = (at: number) => { let v = 0; for (let ch = 0; ch < this.channels; ch++) v += this.sample(ch, at); return v; };
    const score = (start: number, step: number) => { let s = 0; for (let i = 0; i < len; i += step) s += mono(start + i) * mono(natural + i); return s; };
    let best = Math.max(lo, Math.min(hi, nominal)), bestScore = -Infinity;
    for (let p = lo; p <= hi; p += 4) { const s = score(p, 4); if (s > bestScore) { bestScore = s; best = p; } }
    const coarse = best;
    bestScore = -Infinity;
    for (let p = Math.max(lo, coarse - 4); p <= Math.min(hi, coarse + 4); p++) { const s = score(p, 1); if (s > bestScore) { bestScore = s; best = p; } }
    return best;
  }

  async read(max: number): Promise<Planar | undefined> {
    if (this.closed || this.produced >= this.outputFrames) return undefined;
    // Place frames until the first `hop` output frames of the accumulator are final.
    while (this.frame * this.hop < this.produced + this.hop && this.frame * this.hop < this.outputFrames + this.hop) {
      const nominal = Math.round(this.frame * this.hop * this.rate);
      await this.fill(nominal + this.tolerance + this.n + this.hop);
      const at = this.previous < 0 ? nominal : this.bestOffset(nominal, this.previous + this.hop);
      const outStart = this.frame * this.hop - this.produced; // offset in the accumulator
      for (let ch = 0; ch < this.channels; ch++) {
        const acc = this.acc[ch]!;
        for (let i = 0; i < this.n; i++) if (outStart + i < acc.length) acc[outStart + i] = acc[outStart + i]! + this.sample(ch, at + i) * this.window[i]!;
      }
      this.previous = at;
      this.frame++;
      // Input before the earliest position a later frame can reach is no longer needed.
      const keep = Math.min(this.previous, Math.round(this.frame * this.hop * this.rate) - this.tolerance);
      const drop = keep - this.base;
      if (drop > 0) { for (let ch = 0; ch < this.channels; ch++) this.input[ch] = this.input[ch]!.slice(Math.min(drop, this.input[ch]!.length)); this.base += drop; }
    }
    const take = Math.min(max, this.hop, this.outputFrames - this.produced);
    const channels = this.acc.map(a => a.slice(0, take));
    for (let ch = 0; ch < this.channels; ch++) { const a = this.acc[ch]!, next = new Float32Array(a.length); next.set(a.subarray(take)); this.acc[ch] = next; }
    this.produced += take;
    return {channels, frames: take};
  }

  close() { this.closed = true; this.source.close(); this.input = []; }
}
