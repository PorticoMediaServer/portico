/**
 * What a viewer chooses for one playback (quality, version, audio track) and the Quality menu's
 * model. This is the one place those choices become protocol requests: the player screens and the
 * settings speak in `QualityChoice`; `qualityRequest` turns one into the session's `quality` field.
 *
 * The menu, in order: Original (the default: the file as it is, converted only when the device
 * cannot play it), Automatic (starts at Original and moves down, and back up, as the connection
 * requires; see `AutomaticQuality`), then all seven canonical fixed ceilings.
 */
import {qualityLadder, type QualityId, type QualityInForce, type QualityRequest} from './quality.ts';
import type {DiscTitleRef} from './types.ts';

/** Choices that travel with a start or a change of the live session. Absent fields are left as they are. */
export type PlaybackChoices = Readonly<{
  /** Plan from the newly published device capabilities without starting another occurrence. One request only. */
  replan?: boolean;
  quality?: QualityRequest;
  /** The version of the title to play (an edition, another copy). */
  versionId?: string;
  /** One measured title in that physical version; used only by a new session start. */
  discTitle?: DiscTitleRef;
  /** The audio track, by its playback-options id (`a1`, `a2`…). */
  audioTrackId?: string;
  /** The subtitle track, by its playback-options id; `null` turns subtitles off. Sent with a change only. */
  subtitleTrackId?: string | null;
  /** How a styled or image subtitle track is to reach the viewer: `embeddedClient` when this device
   * draws the kind itself (it is handed the original file and the video is left alone), `burn` to
   * have it put in the picture, `sidecar` for a styled track's plain words beside the stream.
   * Absent: the server decides from what the device said it draws and, for a styled track an
   * engine cannot draw, from the viewer's "Styled subtitles" setting (off: plain words, not a
   * burned-in picture). Sent with a change that names a track. */
  subtitleDelivery?: 'embeddedClient' | 'burn' | 'sidecar';
  /** The viewer's subtitle timing correction, in milliseconds. Sent with a change that names a track. */
  subtitleOffsetUs?: string;
  subtitleOffsetMs?: number;
  /** This device's estimate of its connection to the server, sent with a quality change so the server can refuse one the connection cannot carry. */
  throughputKbps?: number;
}>;

export type FixedQualityId = QualityId;
export type QualityChoice = 'original' | 'automatic' | FixedQualityId;

/** A fixed quality: the tallest picture and the video bit rate a stream at that height is given. */
export type FixedQuality = Readonly<{id: FixedQualityId; height: number; videoKbps: number; audioKbps?: number}>;

/** Fixed qualities, best first: the server's ladder (`quality.ts` `qualityLadder`), one for one. */
export const fixedQualities: readonly FixedQuality[] = Object.freeze(qualityLadder.map(step => Object.freeze({id: step.id, height: step.height, videoKbps: step.videoKbps, audioKbps: step.audioKbps})));

const serverId = (fixed: FixedQuality): QualityId => fixed.id;

export const qualityChoiceOrder: readonly QualityChoice[] = Object.freeze(['original', 'automatic', ...fixedQualities.map(q => q.id)]);

export function isQualityChoice(value: unknown): value is QualityChoice {
  return typeof value === 'string' && (qualityChoiceOrder as readonly string[]).includes(value);
}

export function fixedQuality(id: string | undefined): FixedQuality | undefined {
  return fixedQualities.find(q => q.id === id);
}

/** Automatic uses only genuinely smaller evidenced targets. Menus use the
 * full qualityLadder / server qualities, never this adaptation subset. */
export function offeredQualities(source: Readonly<{height?: number; bitrateKbps?: number}> = {}): readonly FixedQuality[] {
 if (!(source.height && source.height > 0) || !(source.bitrateKbps && source.bitrateKbps > 0)) return Object.freeze([]);
 return Object.freeze(fixedQualities.filter(q => (q.height <= source.height! || source.height! < fixedQualities[fixedQualities.length-1].height && q.id===fixedQualities[fixedQualities.length-1].id) && q.videoKbps + (q.audioKbps ?? 0) < source.bitrateKbps!));
}

/** Existing source-resolution classification; it never selects a quality preset. */
export function heightClass(height: number): number {
  if (height > 1500) return 2160;
  if (height > 1100) return 1440;
  if (height > 780) return 1080;
  if (height > 520) return 720;
  if (height > 400) return 480;
  return 360;
}

/**
 * The session `quality` for a choice, in the server's own values: the original, one fixed quality
 * by id, or Automatic. `step` is the fixed quality Automatic has moved to; without one Automatic
 * is on the original, and says so (`id: 'original'`) so a change brings a stepped session back.
 */
export function qualityRequest(choice: QualityChoice, step?: FixedQuality): QualityRequest {
  if (choice === 'automatic') return Object.freeze({mode: 'automatic', id: step ? serverId(step) : 'original'});
  const fixed = fixedQuality(choice);
  return fixed ? Object.freeze({mode: 'fixed', id: serverId(fixed)}) : Object.freeze({mode: 'original'});
}

/** The fixed quality a quality request or the quality in force corresponds to, if any (the menu's tick, the step Automatic is on). */
export function fixedQualityOf(request: QualityRequest | QualityInForce | undefined): FixedQuality | undefined {
  if (!request) return undefined;
  if ((request.mode === 'fixed' || request.mode === 'automatic') && 'id' in request && request.id) return fixedQualities.find(q => serverId(q) === request.id);
  if (request.mode !== 'limit' || !request.maxVideoBitrateKbps) return undefined;
  return fixedQualities.find(q => q.videoKbps === request.maxVideoBitrateKbps && (!request.maxHeight || q.height === request.maxHeight) && (!request.maxAudioBitrateKbps || q.audioKbps === request.maxAudioBitrateKbps));
}

/** The menu choice a quality request or the quality in force on a session stands for. */
export function qualityChoiceOf(quality: QualityRequest | QualityInForce | undefined): QualityChoice | undefined {
  if (!quality) return undefined;
  if (quality.mode === 'original') return 'original';
  if (quality.mode === 'automatic') return 'automatic';
  return fixedQualityOf(quality)?.id;
}

/** The audio request for a track id, or for a legacy absolute stream index. */
export function audioTrackId(stream: number | string | undefined): string | undefined {
  if (typeof stream === 'string') return /^a\d{1,4}$/.test(stream) ? stream : undefined;
  return typeof stream === 'number' && Number.isInteger(stream) && stream >= 0 && stream <= 9999 ? 'a' + stream : undefined;
}

/** The stream index of a playback-options audio id (`a3` → 3). */
export function audioStreamIndex(trackId: string | undefined): number | undefined {
  const m = trackId ? /^a(\d{1,4})$/.exec(trackId) : null;
  return m ? Number(m[1]) : undefined;
}

// ── Automatic ───────────────────────────────────────────────────────────────

export type AutomaticOptions = Readonly<{
  /** The fixed qualities this source offers, best first (`offeredQualities`). */
  steps: readonly FixedQuality[];
  /** The bit rate of the original, when known: what Original needs from the connection. */
  originalKbps?: number;
  /** The step already in force when this title starts (the queue carried it from the title before). */
  startAt?: FixedQuality;
  now?: () => number;
  /** A shortfall must last this long before stepping down. */
  shortfallMs?: number;
  /** Headroom must last this long before stepping up. */
  headroomMs?: number;
  /** Stalls inside this window count together. */
  stallWindowMs?: number;
  /** The least time between two changes: a change costs a rebuffer. */
  holdMs?: number;
}>;

/**
 * Automatic quality for one playback. It starts at Original and is told what the connection
 * manages (`sample`, from media downloads) and when the picture ran dry (`stalled`). `decide`
 * answers with the step to move to, or nothing.
 *
 * Down: the measured throughput stays under what the current quality needs for `shortfallMs`, or
 * the picture stalls twice inside `stallWindowMs`. It then moves to the best step the connection
 * carries with a fifth to spare (always at least one step down).
 * Up: the throughput stays half as much again above what the next quality up needs for
 * `headroomMs`, with no stall in that time. One step at a time.
 * Not flapping: at least `holdMs` between changes; a step up that stalls soon afterwards doubles
 * the wait before the next attempt to go up (to at most ten minutes), and that wait relaxes again
 * after a step up that holds.
 */
export class AutomaticQuality {
  private readonly steps: readonly FixedQuality[];
  private readonly originalKbps?: number;
  private readonly now: () => number;
  private readonly shortfallMs: number;
  private readonly headroomMs: number;
  private readonly stallWindowMs: number;
  private readonly holdMs: number;
  /** -1 is Original; otherwise an index into `steps`. */
  private index = -1;
  private estimateKbps?: number;
  private shortSince?: number;
  private roomSince?: number;
  private stalls: number[] = [];
  private changedAt = -Infinity;
  private wentUpAt = -Infinity;
  private upHoldMs: number;

  constructor(options: AutomaticOptions) {
    this.steps = options.originalKbps && options.originalKbps > 0 ? options.steps.filter(q => q.videoKbps + (q.audioKbps ?? 0) < options.originalKbps!) : [];
    this.originalKbps = options.originalKbps && options.originalKbps > 0 ? options.originalKbps : undefined;
    this.now = options.now ?? Date.now;
    this.shortfallMs = options.shortfallMs ?? 10_000;
    this.headroomMs = options.headroomMs ?? 30_000;
    this.stallWindowMs = options.stallWindowMs ?? 30_000;
    this.holdMs = options.holdMs ?? 15_000;
    this.upHoldMs = this.headroomMs;
    if (options.startAt) this.index = this.steps.findIndex(s => s.id === options.startAt!.id);
  }

  /** The step in force: undefined is Original. */
  get step(): FixedQuality | undefined { return this.index < 0 ? undefined : this.steps[this.index]; }
  get estimate(): number | undefined { return this.estimateKbps; }

  private needs(index: number): number | undefined {
    return index < 0 ? this.originalKbps : (this.steps[index] ? this.steps[index].videoKbps + (this.steps[index].audioKbps ?? 0) : undefined);
  }

  /** One media download: how many kilobits a second it managed. Smoothed, falling faster than it rises. */
  sample(kbps: number): void {
    if (!Number.isFinite(kbps) || kbps <= 0) return;
    const previous = this.estimateKbps;
    this.estimateKbps = previous === undefined ? kbps : kbps < previous ? previous * 0.5 + kbps * 0.5 : previous * 0.8 + kbps * 0.2;
    const at = this.now(), estimate = this.estimateKbps, need = this.needs(this.index);
    if (need !== undefined && estimate < need) this.shortSince ??= at; else this.shortSince = undefined;
    const above = this.index >= 0 ? this.needs(this.index - 1) : undefined;
    if (this.index >= 0 && above !== undefined && estimate >= above * 1.5) this.roomSince ??= at; else this.roomSince = undefined;
  }

  /** The picture ran out of media while it should have been playing. */
  stalled(): void {
    const at = this.now();
    this.stalls = [...this.stalls.filter(t => at - t <= this.stallWindowMs), at];
    this.roomSince = undefined;
    // A step up that cannot hold: wait longer before trying again.
    if (at - this.wentUpAt <= this.stallWindowMs) { this.upHoldMs = Math.min(600_000, this.upHoldMs * 2); this.wentUpAt = -Infinity; }
  }

  /** The step to change to now (`null` is back to Original), or undefined to stay. */
  decide(): FixedQuality | null | undefined {
    const at = this.now();
    if (at - this.changedAt < this.holdMs) return undefined;
    this.stalls = this.stalls.filter(t => at - t <= this.stallWindowMs);
    const short = this.shortSince !== undefined && at - this.shortSince >= this.shortfallMs;
    if ((short || this.stalls.length >= 2) && this.index < this.steps.length - 1) {
      let next = this.index + 1;
      const usable = this.estimateKbps === undefined ? undefined : this.estimateKbps * 0.8;
      if (usable !== undefined) while (next < this.steps.length - 1 && this.needs(next)! > usable) next++;
      return this.move(next, at);
    }
    if (this.index >= 0 && this.roomSince !== undefined && at - this.roomSince >= this.upHoldMs && this.stalls.length === 0) {
      // A step up that held relaxes the wait again.
      if (this.wentUpAt !== -Infinity && at - this.wentUpAt > this.stallWindowMs) this.upHoldMs = this.headroomMs;
      this.wentUpAt = at;
      return this.move(this.index - 1, at);
    }
    return undefined;
  }

  private move(index: number, at: number): FixedQuality | null {
    this.index = index;
    this.changedAt = at;
    this.shortSince = undefined;
    this.roomSince = undefined;
    this.stalls = [];
    return index < 0 ? null : this.steps[index]!;
  }
}
