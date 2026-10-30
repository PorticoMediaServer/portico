import {MixClock} from './mix-clock';
import {planMix, type MixAdmission, type MixPacket} from './mix-frontier';

/** A graph voice's immutable logical lifetime and its retained decoded buffers. */
type Voice = {id: object; start: number; end: number; output: AudioNode; packets: MixPacket[]};
type Scheduled = {node: AudioBufferSourceNode; epoch: number; voice: object};

/**
 * Private shared graph scheduler. The owner supplies only voices already accepted for this mix;
 * a candidate cannot join here until its previously admitted overlap is accounted for.
 */
export class MixGraph {
  readonly clock = new MixClock();
  private readonly voices = new Map<object, Voice>();
  private readonly nodes = new Set<Scheduled>();
  private epoch = 0;
  private closed = false;

  constructor(private readonly context: BaseAudioContext, contextStart: number, logicalStart = 0) {
    this.clock.start(contextStart, logicalStart);
  }

  addVoice(id: object, start: number, end: number, output: AudioNode): void {
    if (this.closed || this.voices.has(id) || !Number.isSafeInteger(start) || !Number.isSafeInteger(end) || end < start) throw new Error('invalid mix voice');
    // A late entrant cannot silently truncate or overlap already admitted original audio.
    if (start < this.clock.frontier) throw new Error('mix link must account for admitted overlap');
    this.voices.set(id, {id, start, end, output, packets: []});
  }

  /** Link only after every already-admitted overlap frame has real incoming PCM. */
  linkVoice(id: object, start: number, end: number, output: AudioNode, packets: readonly MixPacket[]): void {
    if (this.closed || this.voices.has(id) || start >= this.clock.frontier) throw new Error('invalid overlapping link');
    const nowFrame = Math.floor(this.context.currentTime * this.context.sampleRate);
    if (this.clock.contextAt(start) <= nowFrame) throw new Error('overlap is already past');
    const voice: Voice = {id, start, end, output, packets: [...packets]};
    const plan = planMix([voice], start, this.clock.frontier);
    if (plan.frontier !== this.clock.frontier) throw new Error('overlap PCM is not ready');
    this.voices.set(id, voice);
    try { this.submit(plan); }
    catch (error) { this.voices.delete(id); throw error; }
  }

  addPacket(id: object, packet: MixPacket): void {
    const voice = this.voices.get(id);
    if (this.closed || !voice) throw new Error('retired mix voice');
    const previous = voice.packets[voice.packets.length - 1];
    if (!Number.isSafeInteger(packet.start) || !Number.isSafeInteger(packet.end) || !Number.isSafeInteger(packet.offset) || packet.start < voice.start || packet.end > voice.end || packet.start >= packet.end || packet.offset < 0 || packet.offset + packet.end - packet.start > packet.buffer.length || (previous && previous.end > packet.start)) throw new Error('invalid PCM packet');
    voice.packets.push(packet);
  }

  /** The contiguous jointly available window, without starting nodes or advancing the clock. */
  preview(limit: number): MixAdmission {
    if (this.closed) throw new Error('retired mix graph');
    return planMix([...this.voices.values()], this.clock.frontier, limit);
  }

  /** Node submission is atomic with respect to publishing the shared admitted frontier. */
  admit(limit: number): MixAdmission {
    if (this.closed) throw new Error('retired mix graph');
    const from = this.clock.frontier;
    const nowFrame = Math.floor(this.context.currentTime * this.context.sampleRate);
    if (nowFrame >= this.clock.contextAt(from)) return {frontier: from, slices: []};
    const plan = this.preview(limit);
    this.submit(plan);
    this.clock.admit(plan.frontier);
    return plan;
  }

  private submit(plan: MixAdmission): void {
    const created: Scheduled[] = [];
    try {
      for (const slice of plan.slices) {
        const voice = this.voices.get(slice.voice)!;
        const node = this.context.createBufferSource();
        node.buffer = slice.buffer; node.connect(voice.output);
        const scheduled: Scheduled = {node, epoch: this.epoch, voice: slice.voice};
        created.push(scheduled);
        node.onended = () => {
          if (scheduled.epoch !== this.epoch) return;
          this.nodes.delete(scheduled); node.disconnect();
        };
        node.start(this.clock.contextAt(slice.start) / this.context.sampleRate, slice.offset / this.context.sampleRate, slice.frames / this.context.sampleRate);
      }
      for (const scheduled of created) this.nodes.add(scheduled);
    } catch (error) {
      for (const {node} of created) { node.onended = null; try { node.stop(); } catch { /* not started */ } node.disconnect(); }
      throw error;
    }
  }

  /** Retire exactly this graph's nodes after its owner has actually suspended the context. */
  rebase(suspendedFrame: number, nextAnchor: number): number {
    if (this.closed || this.context.state !== 'suspended') throw new Error('mix graph must be suspended');
    if (!Number.isSafeInteger(suspendedFrame) || !Number.isSafeInteger(nextAnchor) || suspendedFrame !== Math.floor(this.context.currentTime * this.context.sampleRate) || nextAnchor <= suspendedFrame) throw new Error('invalid mix graph rebase');
    const held = this.clock.at(suspendedFrame);
    ++this.epoch;
    for (const {node} of this.nodes) { node.onended = null; try { node.stop(); } catch { /* already ended */ } node.disconnect(); }
    this.nodes.clear();
    this.clock.rebase(suspendedFrame, nextAnchor);
    return held;
  }

  /** Retire only this voice's nodes; callers must first retire or finish its accepted mix. */
  removeVoice(id: object): void {
    this.voices.delete(id);
    for (const scheduled of [...this.nodes]) {
      if (scheduled.voice !== id) continue;
      this.nodes.delete(scheduled);
      scheduled.node.onended = null;
      try { scheduled.node.stop(); } catch { /* already ended */ }
      scheduled.node.disconnect();
    }
  }

  /** Call only after the output-lag observation no longer needs this rendered prefix. */
  discardBefore(logicalFrame: number, contextFrame: number): void {
    for (const voice of this.voices.values()) voice.packets = voice.packets.filter(packet => packet.end > logicalFrame);
    this.clock.trimBefore(contextFrame);
  }

  close(): void {
    if (this.closed) return;
    this.closed = true; ++this.epoch;
    for (const {node} of this.nodes) { node.onended = null; try { node.stop(); } catch { /* already ended */ } node.disconnect(); }
    this.nodes.clear(); this.voices.clear();
  }
}
