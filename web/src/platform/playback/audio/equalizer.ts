/** Open-ended device EQ: ten octave bands and preamp, off by default. */
export const eqFrequencies = Object.freeze([31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000]);
export type EqualizerSettings = Readonly<{enabled: boolean; preampDb: number; bandsDb: readonly number[]}>;
export const flatEqualizer: EqualizerSettings = Object.freeze({enabled: false, preampDb: 0, bandsDb: Object.freeze(eqFrequencies.map(() => 0))});

/** Reject corrupt persisted/input data rather than turning invalid settings into audible processing. */
export function parseEqualizer(value: unknown): EqualizerSettings {
  const v = value as Partial<EqualizerSettings> | null;
  if (!v || typeof v.enabled !== 'boolean' || !validGain(v.preampDb) || !Array.isArray(v.bandsDb) || v.bandsDb.length !== 10 || !v.bandsDb.every(validGain)) throw new Error('invalid_equalizer');
  return Object.freeze({enabled: v.enabled, preampDb: v.preampDb, bandsDb: Object.freeze([...v.bandsDb])});
}

const validGain = (n: unknown): n is number => typeof n === 'number' && Number.isFinite(n) && n >= -12 && n <= 12 && Number.isInteger(n * 2);

/** The same peaking-filter coefficients as the renderer, at a known context/output rate.
 * This previews the saved curve even with the switch off; it is never an output observation. */
export function equalizerResponse(settings: EqualizerSettings, sampleRate: number, frequencies: readonly number[]): readonly number[] {
  const value = parseEqualizer(settings);
  if (!Number.isFinite(sampleRate) || sampleRate <= 0) throw new Error('equalizer_rate_unknown');
  return frequencies.map(frequency => {
    if (!Number.isFinite(frequency) || frequency <= 0 || frequency >= sampleRate / 2) return NaN;
    const w = 2 * Math.PI * frequency / sampleRate;
    let db = value.preampDb;
    eqFrequencies.forEach((center, i) => {
      if (center >= sampleRate / 2) return;
      const a = 10 ** (value.bandsDb[i]! / 40), omega = 2 * Math.PI * center / sampleRate;
      const alpha = Math.sin(omega) / (2 * Math.SQRT2), cos = Math.cos(omega);
      const magnitude = (c0: number, c1: number, c2: number) => (c0 + c1 * Math.cos(w) + c2 * Math.cos(2 * w)) ** 2 + (c1 * Math.sin(w) + c2 * Math.sin(2 * w)) ** 2;
      db += 10 * Math.log10(magnitude(1 + alpha * a, -2 * cos, 1 - alpha * a) / magnitude(1 + alpha / a, -2 * cos, 1 - alpha / a));
    });
    return db;
  });
}

/** One shared graph across decks: filtering never resets at a gapless boundary. Off is a dry path. */
export class BrowserEqualizer {
  readonly input: GainNode;
  readonly output: GainNode;
  private readonly dry: GainNode;
  private readonly wet: GainNode;
  private preamp?: GainNode;
  private filters: BiquadFilterNode[] = [];
  private value = flatEqualizer;

  constructor(private readonly context: BaseAudioContext) {
    this.input = context.createGain(); this.output = context.createGain();
    this.dry = context.createGain(); this.wet = context.createGain();
    this.wet.gain.value = 0;
    this.input.connect(this.dry); this.dry.connect(this.output); this.wet.connect(this.output);
  }
  get settings(): EqualizerSettings { return this.value; }
  /** Read the actual configured nodes, including their current smoothed parameter values. */
  frequencyResponse(frequencies: readonly number[]): readonly number[] {
    if (!this.value.enabled || !this.preamp) return frequencies.map(() => 0);
    const hz = new Float32Array(frequencies), magnitude = new Float32Array(hz.length), phase = new Float32Array(hz.length);
    const db = frequencies.map(() => 20 * Math.log10(this.preamp!.gain.value));
    for (const filter of this.filters) {
      filter.getFrequencyResponse(hz, magnitude, phase);
      magnitude.forEach((gain, i) => { db[i] = db[i]! + 20 * Math.log10(gain); });
    }
    return db;
  }
  /** A failed filter is removed from the signal path synchronously. Gain automation cannot
   * leave a half-installed wet chain audible while this song continues without the EQ. */
  bypass(): void {
    this.input.disconnect();
    this.preamp?.disconnect();
    this.filters.forEach(filter => filter.disconnect());
    this.preamp = undefined; this.filters = [];
    this.dry.gain.cancelScheduledValues(this.context.currentTime); this.dry.gain.value = 1;
    this.wet.gain.cancelScheduledValues(this.context.currentTime); this.wet.gain.value = 0;
    this.input.connect(this.dry);
    this.value = flatEqualizer;
  }
  apply(settings: EqualizerSettings): void {
    const value = parseEqualizer(settings), time = this.context.currentTime;
    if (value.enabled && !this.preamp) {
      const preamp = this.context.createGain(), filters: BiquadFilterNode[] = [];
      let previous: AudioNode = preamp;
      try { eqFrequencies.forEach(frequency => {
        const filter = this.context.createBiquadFilter();
        filter.type = 'peaking'; filter.frequency.value = frequency;
        // One-octave bandwidth, Q = sqrt(2). All channels are processed independently.
        filter.Q.value = Math.SQRT2;
        previous.connect(filter); previous = filter;
        filters.push(filter);
      });
      previous.connect(this.wet); this.input.connect(preamp);
      this.preamp = preamp; this.filters = filters;
      } catch (error) { preamp.disconnect(); filters.forEach(filter => filter.disconnect()); throw error; }
    }
    const set = (param: AudioParam, target: number) => { param.cancelScheduledValues(time); param.setTargetAtTime(target, time, 0.005); };
    if (this.preamp) {
      set(this.preamp.gain, 10 ** (value.preampDb / 20));
      this.filters.forEach((filter, i) => set(filter.gain, filter.frequency.value < this.context.sampleRate / 2 ? value.bandsDb[i]! : 0));
    }
    set(this.dry.gain, value.enabled ? 0 : 1); set(this.wet.gain, value.enabled ? 1 : 0);
    this.value = value;
  }
  dispose(): void {
    for (const node of [this.input, this.output, this.dry, this.wet, this.preamp, ...this.filters]) node?.disconnect();
  }
}
