/**
 * Output-only resampling (§9.6 rule 3): a track decodes at its own rate and is converted to the
 * output device's rate only when they differ, with a windowed-sinc (Kaiser, 32 zero crossings a
 * side) filter, not the linear interpolation a Web Audio source node would use. Streaming: blocks
 * in, blocks out; the total is exactly `floor(inputFrames × out / in)` so an album keeps its
 * sample-exact joins at the output rate.
 */
const ZEROS = 32, PHASES = 256;

function bessel0(x: number): number {
  let sum = 1, term = 1;
  for (let k = 1; k < 32; k++) { term *= (x / (2 * k)) ** 2; sum += term; if (term < 1e-12 * sum) break; }
  return sum;
}

const tables = new Map<string, Float32Array>();
/** Kernel samples h((phase/PHASES) + k) for k in [-ZEROS+1, ZEROS], per phase. */
function table(cutoff: number): Float32Array {
  const key = cutoff.toFixed(6);
  const hit = tables.get(key);
  if (hit) return hit;
  const beta = 9, norm = bessel0(beta), taps = 2 * ZEROS;
  const t = new Float32Array((PHASES + 1) * taps);
  for (let p = 0; p <= PHASES; p++) {
    for (let j = 0; j < taps; j++) {
      const x = j - ZEROS + 1 - p / PHASES; // distance from the output point
      const u = x / ZEROS;
      const window = Math.abs(u) >= 1 ? 0 : bessel0(beta * Math.sqrt(1 - u * u)) / norm;
      const s = x === 0 ? 1 : Math.sin(Math.PI * cutoff * x) / (Math.PI * cutoff * x);
      t[p * taps + j] = cutoff * s * window;
    }
  }
  tables.set(key, t);
  return t;
}

export class Resampler {
  readonly inRate: number;
  readonly outRate: number;
  private readonly channels: number;
  private readonly kernel: Float32Array;
  private readonly step: number; // input frames per output frame
  /** Input history: the last 2×ZEROS frames before `base`, then new input. */
  private history: Float32Array[];
  private base = 0; // absolute input index of history[ZEROS * 2]... see below
  private consumed = 0; // input frames received
  private produced = 0; // output frames emitted

  constructor(inRate: number, outRate: number, channels: number) {
    this.inRate = inRate; this.outRate = outRate; this.channels = channels;
    this.step = inRate / outRate;
    const cutoff = Math.min(1, outRate / inRate) * 0.97;
    this.kernel = table(cutoff);
    // Leading zeros stand in for the samples before the track.
    this.history = Array.from({length: channels}, () => new Float32Array(2 * ZEROS));
    this.base = -2 * ZEROS;
  }

  /** Output frames for `totalIn` input frames. */
  static outputFrames(totalIn: number, inRate: number, outRate: number) { return Math.floor(totalIn * outRate / inRate); }

  private emit(limitIn: number, total?: number): Float32Array[] {
    const taps = 2 * ZEROS;
    const out: number[][] = Array.from({length: this.channels}, () => []);
    for (;;) {
      if (total !== undefined && this.produced >= total) break;
      const t = this.produced * this.step; // input position of this output frame
      const center = Math.floor(t);
      if (center + ZEROS > limitIn - 1 && total === undefined) break; // need more input
      const frac = t - center;
      const phase = frac * PHASES, p0 = Math.floor(phase), w = phase - p0;
      const first = center - ZEROS + 1; // absolute input index of tap 0
      for (let ch = 0; ch < this.channels; ch++) {
        const h = this.history[ch]!;
        let acc = 0;
        for (let j = 0; j < taps; j++) {
          const idx = first + j - this.base;
          const x = idx >= 0 && idx < h.length ? h[idx]! : 0;
          if (x === 0) continue;
          const k0 = this.kernel[p0 * taps + j]!, k1 = this.kernel[(p0 + 1) * taps + j]!;
          acc += x * (k0 + (k1 - k0) * w);
        }
        out[ch]!.push(acc);
      }
      this.produced++;
    }
    return out.map(a => Float32Array.from(a));
  }

  /** Feeds input; returns the output it makes possible. */
  process(input: readonly Float32Array[], frames: number): {channels: Float32Array[]; frames: number} {
    // Append, keeping only what the filter can still reach.
    for (let ch = 0; ch < this.channels; ch++) {
      const h = this.history[ch]!, next = new Float32Array(h.length + frames);
      next.set(h); next.set(input[ch]!.subarray(0, frames), h.length);
      this.history[ch] = next;
    }
    this.consumed += frames;
    const channels = this.emit(this.consumed);
    // Drop history no longer needed: everything before the next output's first tap.
    const nextFirst = Math.floor(this.produced * this.step) - ZEROS + 1;
    const drop = Math.max(0, nextFirst - this.base);
    if (drop > 0) { for (let ch = 0; ch < this.channels; ch++) this.history[ch] = this.history[ch]!.slice(drop); this.base += drop; }
    return {channels, frames: channels[0]?.length ?? 0};
  }

  /** Continue on the same sample grid, carrying the filter history and fractional phase.
   * `lookahead` belongs to the next track; it completes this track's last filter taps.
   * It is not counted as this track's input and will be fed once by the successor. */
  continue(lookahead: readonly Float32Array[], frames: number): {tail: {channels: Float32Array[]; frames: number}; next: Resampler; origin: number} {
    const saved = this.history, produced = this.produced;
    this.history = saved.map((h, ch) => {
      const joined = new Float32Array(h.length + frames);
      joined.set(h); joined.set(lookahead[ch]!.subarray(0, frames), h.length);
      return joined;
    });
    const tail = this.flush();
    this.history = saved;
    const next = new Resampler(this.inRate, this.outRate, this.channels);
    next.history = saved.map(h => h.slice());
    next.base = this.base; next.consumed = this.consumed; next.produced = this.produced;
    this.produced = produced; // preparation does not commit the outgoing tail
    return {tail, next, origin: this.consumed};
  }

  /** The last output frames, up to exactly `outputFrames(total input)`. */
  flush(): {channels: Float32Array[]; frames: number} {
    const total = Resampler.outputFrames(this.consumed, this.inRate, this.outRate);
    const channels = this.emit(this.consumed, total);
    return {channels, frames: channels[0]?.length ?? 0};
  }
}
