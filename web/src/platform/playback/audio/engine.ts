import {Resampler} from './resampler';
import type {MixGraph} from './mix-graph';
import {gainAt, rebuildGain, type GainKnot} from './mix-gain';
import type {Planar} from './decoder';

/**
 * The two-deck scheduler (§9.6 rules 1, 4, 7, 8). Every deck plays one track's exact frames on the
 * context's sample clock, from an integer start frame, in fixed-size buffers scheduled back to
 * back; nothing is timed by timers. A gapless successor starts at the frame after the current
 * track's last frame; a crossfade overlaps them with equal-power curves. Each deck decodes at most
 * `aheadSeconds` past the playhead, so memory is bounded however long the track or queue.
 *
 * Works on any BaseAudioContext, so an OfflineAudioContext renders the same schedule for tests.
 */
export interface PcmSource {
  readonly sampleRate: number;
  readonly channels: number;
  read(max: number): Promise<Planar | undefined>;
  close(): void;
}

export type DeckOptions = Readonly<{
  /** Trimmed frames of the track this deck plays (from its start frame to its end), at the file's rate. */
  frames: number;
  /** Linear gain (normalization, already peak-limited). */
  gain: number;
  /** Diagnostics and tests: a label. */
  label?: string;
  /** Hold the final filter taps briefly for a gapless successor. */
  holdTail?: boolean;
  /** Shared logical mix graph; an unlinked candidate only retains decoded PCM. */
  graph?: MixGraph;
}>;

const now = (c: BaseAudioContext) => c.currentTime;

export class Deck {
  readonly source: PcmSource;
  readonly label: string;
  readonly norm: GainNode;
  readonly fade: GainNode;
  /** Output frames at the context rate. */
  outFrames: number;
  /** Integer context frame of the first output frame; undefined until scheduled. */
  startFrame?: number;
  /** Output frames decoded so far (scheduled or held). */
  decoded = 0;
  /** Output frames scheduled on the clock. */
  scheduled = 0;
  closed = false;
  ended = false;
  /** Held buffers before the deck has a start frame (a candidate), in order. */
  private held: AudioBuffer[] = [];
  private chunk: Float32Array[] | undefined;
  private chunkFill = 0;
  private readonly chunkFrames: number;
  private resampler?: Resampler;
  private inputOrigin = 0;
  private readonly inputFrames: number;
  private tailPending = false;
  private readonly holdTail: boolean;
  private predecessor?: Deck;
  private readonly context: BaseAudioContext;
  private graph?: MixGraph;
  private readonly nodes = new Set<AudioBufferSourceNode>();
  private pumpRun?: Promise<void>;
  private requestedAhead = 0;
  private continuation?: {previous: Deck; tail: {channels: Float32Array[]; frames: number}};
  private exhausted = false;
  private inputRead = 0;
  failure?: unknown;

  constructor(context: BaseAudioContext, destination: AudioNode, source: PcmSource, o: DeckOptions) {
    this.context = context;
    this.graph = o.graph;
    this.source = source;
    this.label = o.label ?? '';
    this.inputFrames = o.frames; this.holdTail = o.holdTail === true;
    this.norm = context.createGain();
    this.fade = context.createGain();
    this.norm.gain.value = o.gain;
    this.gainValue = o.gain;
    this.gainInitial = o.gain;
    this.norm.connect(this.fade);
    this.fade.connect(destination);
    const rate = context.sampleRate;
    if (source.sampleRate !== rate) this.resampler = new Resampler(source.sampleRate, rate, source.channels);
    this.outFrames = this.resampler ? Resampler.outputFrames(o.frames, source.sampleRate, rate) : o.frames;
    this.chunkFrames = Math.round(rate); // one second per buffer
  }

  get rate() { return this.context.sampleRate; }
  get mixGraph() { return this.graph; }
  /** Context time of the frame after this deck's last. */
  get endFrame(): number | undefined { return this.startFrame === undefined ? undefined : this.startFrame + this.outFrames; }
  /** Seconds decoded past the playhead (or held, before starting). */
  get aheadSeconds(): number {
    if (this.startFrame === undefined) return this.decoded / this.rate;
    const position = this.graph ? this.graph.clock.at(Math.floor(now(this.context) * this.rate)) : Math.floor(now(this.context) * this.rate);
    return Math.max(0, (this.startFrame + this.decoded - position) / this.rate);
  }
  /** Frames of this track played by context time `at` (now by default), at the context rate. */
  playedFrames(at = now(this.context)): number {
    if (this.startFrame === undefined) return 0;
    const frame = Math.floor(at * this.rate);
    const position = this.graph ? this.graph.clock.at(frame) : frame;
    return Math.max(0, Math.min(this.outFrames, position - this.startFrame));
  }

  /** Decodes until `seconds` of audio lie ahead of the playhead (or are held), then stops. */
  pump(seconds: number): Promise<void> {
    if (this.closed || this.failure) return this.pumpRun ?? Promise.resolve();
    this.requestedAhead = Math.max(this.requestedAhead, Math.min(30, Math.max(0, Number.isNaN(seconds) ? 0 : seconds)));
    if (this.pumpRun) return this.pumpRun;
    const run = Promise.resolve().then(() => this.drain(() => {
      if (this.pumpRun === run) { this.pumpRun = undefined; this.requestedAhead = 0; }
    }));
    this.pumpRun = run;
    return run;
  }

  private async drain(finish: () => void): Promise<void> {
    try {
      while (!this.closed && !this.exhausted && this.aheadSeconds < this.requestedAhead) {
        const block = await this.source.read(Math.round(this.source.sampleRate / 4));
        if (this.closed) return;
        if (!block) {
          // Finite plan lengths are source facts, not permission to complete a truncated stream.
          // Validate input before flushing or handing a resampler tail to another deck.
          if (this.inputRead !== this.inputFrames) throw new Error('audio_source_ended_early');
          this.exhausted = true;
          this.tailPending = !!this.resampler;
          if (!this.holdTail) this.finishTail();
          if (!this.tailPending) this.finishChunk();
          break;
        }
        if (!Number.isSafeInteger(block.frames) || block.frames <= 0 || block.frames > this.inputFrames - this.inputRead ||
            block.channels.length !== this.source.channels || block.channels.some(channel => channel.length < block.frames)) throw new Error('audio_source_length_invalid');
        this.inputRead += block.frames;
        if (this.predecessor) {
          const previous = this.predecessor; this.predecessor = undefined;
          if (previous.tailPending && previous.resampler) {
            const continuation = previous.resampler.continue(block.channels, block.frames);
            this.continuation = {previous, tail: continuation.tail};
            this.resampler = continuation.next; this.inputOrigin = continuation.origin;
            this.outFrames = Resampler.outputFrames(this.inputOrigin + this.inputFrames, this.source.sampleRate, this.rate)
              - Resampler.outputFrames(this.inputOrigin, this.source.sampleRate, this.rate);
          }
        }
        this.append(this.resampler ? this.resampler.process(block.channels, block.frames) : block);
      }
    } catch (e) {
      this.failure = e;
    } finally {
      // Clear while the drain is still on the stack: a subsequent caller must not join an
      // already-finished run whose promise has yet to propagate its completion microtask.
      finish();
    }
  }

  /** Set before pumping a candidate. Only exhausted, compatible gapless tracks share a grid. */
  continueFrom(previous: Deck): boolean {
    if (this.decoded || this.chunkFill || !previous.tailPending || !this.resampler || !previous.resampler
      || this.source.sampleRate !== previous.source.sampleRate || this.source.channels !== previous.source.channels) return false;
    this.predecessor = previous;
    return true;
  }

  commitContinuation(previous: Deck) {
    if (this.continuation?.previous === previous && previous.tailPending) {
      previous.append(this.continuation.tail); previous.tailPending = false; previous.finishChunk();
    }
    this.continuation = undefined;
  }

  /** Last moment to release an unscheduled terminal buffer without starving playback. */
  get tailDeadlineSeconds() {
    return this.tailPending && this.startFrame !== undefined ? (this.startFrame + this.decoded) / this.rate - 0.08 : Infinity;
  }

  /** Release a terminal track against silence when no successor arrived before its deadline. */
  finishTail() {
    if (!this.tailPending) return;
    this.append(this.resampler!.flush()); this.tailPending = false; this.finishChunk();
  }

  private append(block: {channels: readonly Float32Array[]; frames: number}) {
    let at = 0;
    const channels = block.channels;
    while (at < block.frames) {
      const room = Math.min(this.chunkFrames - this.chunkFill, this.outFrames - (this.decoded + this.chunkFill));
      if (room <= 0) { this.finishChunk(); if (this.decoded >= this.outFrames) return; continue; }
      if (!this.chunk) { this.chunk = channels.map(() => new Float32Array(this.chunkFrames)); this.chunkFill = 0; }
      const n = Math.min(room, block.frames - at);
      for (let ch = 0; ch < this.chunk.length; ch++) this.chunk[ch]!.set(channels[ch]!.subarray(at, at + n), this.chunkFill);
      this.chunkFill += n; at += n;
      if (this.chunkFill === this.chunkFrames) this.finishChunk();
    }
  }

  private finishChunk() {
    if (!this.chunk || this.chunkFill === 0) { this.chunk = undefined; return; }
    const buffer = this.context.createBuffer(this.chunk.length, this.chunkFill, this.rate);
    for (let ch = 0; ch < this.chunk.length; ch++) buffer.copyToChannel(this.chunk[ch]!.subarray(0, this.chunkFill) as Float32Array<ArrayBuffer>, ch);
    this.chunk = undefined;
    const frames = this.chunkFill;
    this.chunkFill = 0;
    if (this.startFrame === undefined) this.held.push(buffer);
    else if (this.graph) this.graph.addPacket(this, {start: this.startFrame + this.decoded, end: this.startFrame + this.decoded + frames, offset: 0, buffer});
    else this.play(buffer);
    this.decoded += frames;
  }

  private play(buffer: AudioBuffer) {
    const node = this.context.createBufferSource();
    node.buffer = buffer;
    node.connect(this.norm);
    const frame = this.startFrame! + this.scheduled;
    node.start(frame / this.rate);
    this.scheduled += buffer.length;
    this.nodes.add(node);
    node.onended = () => { this.nodes.delete(node); node.disconnect(); if (this.scheduled >= this.outFrames && this.nodes.size === 0) this.ended = true; };
  }

  /** Puts the deck on the clock at integer context frame `frame`. */
  start(frame: number) {
    if (this.startFrame !== undefined || this.closed) return;
    const held = this.held;
    if (this.graph) {
      let cursor = frame;
      const packets = held.map(buffer => { const start = cursor; cursor += buffer.length; return {start, end: cursor, offset: 0, buffer}; });
      if (frame < this.graph.clock.frontier) this.graph.linkVoice(this, frame, frame + this.outFrames, this.norm, packets);
      else {
        this.graph.addVoice(this, frame, frame + this.outFrames, this.norm);
        for (const packet of packets) this.graph.addPacket(this, packet);
      }
      this.startFrame = frame;
      this.held = [];
      return;
    }
    this.startFrame = frame;
    this.held = [];
    for (const b of held) this.play(b);
  }

  /** True only after the source and its real resampler tail have finished. */
  get sourceComplete() { return !this.failure && this.exhausted && this.inputRead === this.inputFrames && !this.tailPending && !this.chunk && this.decoded === this.outFrames; }

  /** The audio is exhausted and every scheduled frame has been handed to the clock. */
  get complete() {
    const handed = this.graph && this.startFrame !== undefined ? Math.max(0, this.graph.clock.frontier - this.startFrame) : this.scheduled;
    return this.sourceComplete && this.held.length === 0 && handed >= this.decoded;
  }

  /** Ramps the normalization gain (20 ms, §9.6 rule 7). */
  private gainValue?: number;
  private gainInitial = 1;
  private gainKnots: GainKnot[] = [];
  private fadeInitial = 1;
  private fadeKnots: GainKnot[] = [];

  /** Attach a candidate or primary before it takes its place in the original mix. */
  attachGraph(graph: MixGraph) {
    if (this.closed || this.startFrame !== undefined || (this.graph && this.graph !== graph)) throw new Error('deck already belongs to a mix');
    this.graph = graph;
  }

  private gainTime(held: number, frame: number): number {
    return (this.graph!.clock.contextAt(held) + frame - held) / this.rate;
  }

  /** Restore only this deck's owned gain automation after its graph epoch is retired. */
  rebuildAutomation(held: number) {
    if (!this.graph || this.closed) return;
    const time = (frame: number) => this.gainTime(held, frame);
    rebuildGain(this.norm.gain, held, this.gainInitial, this.gainKnots, time);
    rebuildGain(this.fade.gain, held, this.fadeInitial, this.fadeKnots, time);
  }

  setFadeTimeline(initial: number, knots: readonly GainKnot[]) {
    if (!this.graph || this.closed) throw new Error('deck has no mix graph');
    this.fadeInitial = initial; this.fadeKnots = [...knots];
    const held = this.graph.clock.at(Math.floor(now(this.context) * this.rate));
    rebuildGain(this.fade.gain, held, initial, this.fadeKnots, frame => this.gainTime(held, frame));
  }

  setGain(value: number) {
    if (value === this.gainValue) return;
    this.gainValue = value;
    if (this.graph) {
      const held = this.graph.clock.at(Math.floor(now(this.context) * this.rate));
      const current = gainAt(held, this.gainInitial, this.gainKnots);
      this.gainInitial = current;
      this.gainKnots = [{frame: held, value: current}, {frame: held + Math.max(1, Math.round(this.rate * 0.02)), value}];
      rebuildGain(this.norm.gain, held, current, this.gainKnots, frame => this.gainTime(held, frame));
      return;
    }
    const t = now(this.context), g = this.norm.gain;
    g.cancelScheduledValues(t);
    g.setValueAtTime(g.value, t);
    g.linearRampToValueAtTime(value, t + 0.02);
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    this.continuation = undefined; this.predecessor = undefined;
    for (const n of this.nodes) { n.onended = null; try { n.stop(); } catch { /* not started */ } n.disconnect(); }
    this.nodes.clear();
    this.graph?.removeVoice(this);
    this.held = [];
    this.chunk = undefined;
    this.norm.disconnect();
    this.fade.disconnect();
    this.source.close();
  }
}

/** Equal-power curves for a crossfade of `n` points. */
export function equalPower(n = 64): {fadeIn: Float32Array; fadeOut: Float32Array} {
  const fadeIn = new Float32Array(n), fadeOut = new Float32Array(n);
  for (let i = 0; i < n; i++) { const x = i / (n - 1); fadeIn[i] = Math.sin(x * Math.PI / 2); fadeOut[i] = Math.cos(x * Math.PI / 2); }
  return {fadeIn, fadeOut};
}

/**
 * Where the next track starts (context frames), gapless or crossfaded (§9.6 rule 4). The fade is
 * clamped to half the shorter track; same-album neighbors join gaplessly.
 */
export function edgeFrames(current: {endFrame: number; outFrames: number}, next: {outFrames: number}, o: {crossfadeSeconds: number; sameAlbum: boolean; rate: number}): {start: number; fade: number} {
  if (o.sameAlbum || o.crossfadeSeconds <= 0) return {start: current.endFrame, fade: 0};
  const fade = Math.max(0, Math.min(Math.round(o.crossfadeSeconds * o.rate), Math.floor(current.outFrames / 2), Math.floor(next.outFrames / 2)));
  return {start: current.endFrame - fade, fade};
}

/** Schedules `next` after `current` (both on one context). Returns where it starts. A link made
 * after its edge (`earliest`, the first frame still schedulable) starts late rather than cutting
 * frames from the incoming track, and shortens the fade to what's left. */
export function linkDecks(context: BaseAudioContext, current: Deck, next: Deck, o: {crossfadeSeconds: number; sameAlbum: boolean; earliest?: number}): {start: number; fade: number; late: boolean} {
  const end = current.endFrame;
  if (end === undefined) throw new Error('The current track is not on the clock.');
  const planned = edgeFrames({endFrame: end, outFrames: current.outFrames}, next, {...o, rate: context.sampleRate});
  const late = o.earliest !== undefined && planned.start < o.earliest;
  const edge = late ? {start: o.earliest!, fade: Math.max(0, Math.min(planned.fade, end - o.earliest!))} : planned;
  if (current.mixGraph || next.mixGraph) {
    if (!current.mixGraph || current.mixGraph !== next.mixGraph) throw new Error('linked decks require one mix graph');
    // Prove incoming coverage of every already admitted overlap before changing either gain or
    // consuming a predecessor continuation. A failed link leaves the old accepted mix alone.
    next.start(edge.start);
    next.commitContinuation(current);
    current.finishTail();
    if (edge.fade > 0) {
      const {fadeIn, fadeOut} = equalPower(Math.min(64, edge.fade + 1));
      const knots = (curve: Float32Array) => Array.from(curve, (value, index) => ({frame: edge.start + Math.round(edge.fade * index / (curve.length - 1)), value}));
      next.setFadeTimeline(0, knots(fadeIn));
      current.setFadeTimeline(1, knots(fadeOut));
    }
    return {...edge, late};
  }
  if (edge.fade > 0) {
    const {fadeIn, fadeOut} = equalPower();
    const t = edge.start / context.sampleRate, d = edge.fade / context.sampleRate;
    next.fade.gain.setValueAtTime(0, 0);
    next.fade.gain.setValueCurveAtTime(fadeIn, t, d);
    current.fade.gain.setValueCurveAtTime(fadeOut, t, d);
  }
  next.commitContinuation(current);
  current.finishTail();
  next.start(edge.start);
  return {...edge, late};
}
