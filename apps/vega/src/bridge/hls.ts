import {MediaSource, type SourceBuffer, type VideoPlayer} from '@amazon-devices/react-native-w3cmedia';
import {trace} from '../debug';
import {mp4Codecs} from './mp4-codecs';
import {checkResponse, onCredentialExposure, originOf, requestCredentials} from './credential-boundary';
import {isVideoCodec, liveStart, numberAt, parseMaster, parseMedia, segmentAtDate, segmentNumbered, type Playlist, type Rendition, type Segment} from './hls-playlist';

export type {Rendition};

/**
 * Plays the server's HLS through media source extensions: the app fetches the playlists and the
 * segments, and hands the bytes to the media element. On Vega this is the only way to play from a
 * server reached over plain HTTP (the element refuses to open such an address itself), and the
 * only way to play HLS at all (the element has no HLS client).
 *
 * It reads what the Portico server writes (`hls-playlist.ts`), in two ways:
 *
 * - **A title.** Positions are seconds from the start of the playlist. The segments are given to
 *   the element with the timestamps they carry, which do not start at zero: `offset` is the
 *   difference, found from the first segment.
 * - **A live channel** (`originMs` given). The playlist slides and a change of programme restarts
 *   the segments' own clock, so nothing can be read from their timestamps. Each segment is asked
 *   to follow the one before it wherever its own clock says it belongs (the source buffer's
 *   `sequence` mode), where each one really landed is read back from the buffer, and a position
 *   is the moment of broadcast the playlist gives for that segment, counted from the channel's
 *   origin. Where the device does not place segments in sequence, a change of programme lands
 *   somewhere else in the element's time and playback is carried across to it.
 *
 * Because every byte passes through here, this is also where the connection is measured for
 * Automatic quality: how fast the segments came while they were being fetched one after another.
 */

/**
 * Why a stream stopped. `status` is the server's answer when it gave one (400 to 599). `network`
 * is true when it gave none, or gave one that waiting may change. `refused` is set when the
 * media element would not take the stream at all (`unsupported`) or its bytes (`decode`).
 */
export class SourceError extends Error {
  constructor(readonly network: boolean, detail: string, readonly status = 0, readonly refused: '' | 'unsupported' | 'decode' = '') { super(detail); }
}

/** Only a refused rendition playlist (422) requests the contract's default fallback. */
export class AudioRenditionUnavailable extends SourceError {}
export type AudioSelectionOwner = Readonly<{current: () => boolean; sourceCurrent: () => boolean; willMutate: () => void}>;
export type AudioSelection = Readonly<{done: Promise<void>; cancel: () => void}>;
type AudioOperation = {owner: AudioSelectionOwner; controller: AbortController; baseGeneration: number; commitGeneration?: number;
  cancelled: boolean; completed: boolean; mutated: boolean; writing: boolean; clock?: () => boolean; current: () => boolean};

const AHEAD_S = 30, BEHIND_S = 30, RELOAD_MS = 4000, IDLE_MS = 400;
/** The server answers 503 while it is still writing a segment. */
const PREPARING_TRIES = 30, PREPARING_MS = 1000;
/** A measurement of the connection is said only while it is fresh, and only from a download big
 * enough to say something. */
const FRESH_MS = 15000, MEASURED_BYTES = 65536;
const MEDIA_BUDGET = 128 * 1024 * 1024;

/** Bound UTF-8 response bytes without allocating a second copy of an oversized playlist. */
function textExceedsBytes(text: string, limit: number): boolean {
  let bytes = 0;
  for (let index = 0; index < text.length; index++) {
    const unit = text.charCodeAt(index);
    if (unit <= 0x7f) bytes++;
    else if (unit <= 0x7ff) bytes += 2;
    else if (unit >= 0xd800 && unit <= 0xdbff && index + 1 < text.length && text.charCodeAt(index + 1) >= 0xdc00 && text.charCodeAt(index + 1) <= 0xdfff) { bytes += 4; index++; }
    else bytes += 3;
    if (bytes > limit) return true;
  }
  return false;
}

const wait = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));

type Track = {kind: 'video' | 'audio'; mime: string; url: string; playlist: Playlist; buffer: SourceBuffer; /** The number of the segment to fetch next. */ next: number; loaded: number; bytesPerSecond?: number; deferred?: {number: number; bytes: number}};
/** A segment in the element's own time: where it landed in the buffer. */
type Placed = {number: number; start: number; end: number; dateMs?: number};
type Arrival = {dateMs: number; resolve(elementTime: number): void; reject(error: Error): void};
/** Unknown placement is retained conservatively until the whole track is removed. */
type RetainedSegment = {buffer: SourceBuffer; generation: number; bytes: number; kind: 'media' | 'init' | 'staging'; sealed?: boolean};
/** A sealed whole-buffer cohort: no per-segment placement is inferred. */
type RetainedCohort = {track: Track; buffer: SourceBuffer; generation: number; entries: readonly RetainedSegment[];
  dependencies: readonly RetainedSegment[]; ranges: readonly (readonly [number, number])[]; frontier: number};

export class HlsSource {
  private readonly source = new MediaSource();
  private tracks: Track[] = [];
  private renditions: Rendition[] = [];
  private generation = 0;
  private generationController = new AbortController();
  private closed = false;
  private requests = new Set<XMLHttpRequest>();
  private readonly pending = new Set<() => void>();
  /** A response keeps its reservation until its owner appends or discards it. */
  private mediaBusy = false;
  private readonly mediaWaiters: Array<{grant(): void; cancel(): void}> = [];
  private readonly retained = new Set<RetainedSegment>();
  private readonly cohorts = new Map<SourceBuffer, RetainedCohort>();
  private cohortRemoving?: RetainedCohort;
  private cohortControl = 0;

  private audioOperation?: AudioOperation;
  private audioDirty = false;
  private audioHold?: AudioOperation;
  private readonly pumpRuns = new Map<number, Promise<void | 'held'>>();
  private readonly pumpControllers = new Map<number, AbortController>();
  /** Where playback is headed while a seek is being asked for. */
  private target: number | undefined;
  /**
   * A title: how far the element's clock runs ahead of the playlist's. The server's segments do
   * not start at zero (its packager starts them a second in), and a media source keeps the
   * timestamps it is given; a player with its own HLS client hides this. Positions given to and
   * by this class are the playlist's; `elementTime` and `position` convert.
   */
  private offset = 0;
  /** A live channel: the segments in the buffer, oldest first, and a seek waiting for its segment. */
  private placed: Placed[] = [];
  private arrival: Arrival | undefined;
  private begin = NaN;
  private samples: {bytes: number; ms: number; at: number}[] = [];

  /** `originMs`: the channel's origin (milliseconds since 1970) for a live channel; absent for a title.
   * `now`: the clock the connection is measured against, in milliseconds. */
  private readonly stopExposure: () => void;
  constructor(private readonly player: VideoPlayer, private readonly url: string, private readonly failed: (error: SourceError) => void, private readonly originMs?: number, private readonly now: () => number = Date.now) {
    this.stopExposure = onCredentialExposure(({origin, origins = [origin]}) => {
      if (!this.closed && origins.includes(originOf(this.url))) { this.close(); this.failed(new SourceError(false, 'media credential exposed')); }
    });
  }

  /** Retire requests and reservation waits owned by the old feed before starting its successor. */
  private nextGeneration(): number {
    this.cohortControl++;
    this.generationController.abort();
    for (const controller of this.pumpControllers.values()) controller.abort();
    for (const track of this.tracks) track.deferred = undefined;
    // Retire only the plan. Its charges remain until an owned removal proves absence.
    this.cohorts.clear();
    this.generationController = new AbortController();
    return ++this.generation;
  }

  private get live(): boolean { return this.originMs !== undefined; }

  /** The element's clock for a position in a title. */
  elementTime(position: number): number { return position + this.offset; }

  /** Where playback is: seconds into the title, or on the channel's timeline. NaN while a
   * channel's place is not known yet. */
  position(): number {
    const now = Number(this.player.currentTime);
    if (!this.live) return Math.max(0, now - this.offset);
    const at = this.placedAt(now);
    if (!at || at.dateMs === undefined) return NaN;
    return (at.dateMs + Math.max(0, Math.min(at.end, now) - at.start) * 1000 - this.originMs!) / 1000;
  }

  /** A channel: the element's time to begin at once the stream is open. NaN for a title. */
  beginning(): number { return this.begin; }

  /** Which of `audio()` is playing, or -1. */
  audioIndex(): number {
    const track = this.tracks.find(candidate => candidate.kind === 'audio');
    if (this.closed || !track || this.audioDirty || !this.spans(track.buffer).some(([start, end]) => start <= Number(this.player.currentTime) && end > Number(this.player.currentTime))) return -1;
    return this.renditions.findIndex(rendition => rendition.url === track.url);
  }

  /** Whether the stream has a picture (known once it is open). */
  hasPicture(): boolean { return this.tracks.some(track => track.kind === 'video'); }

  /** The audio renditions the server offers, in its order (empty when audio is with the video). */
  audio(): readonly Rendition[] { return this.renditions; }

  /**
   * What the downloads of the last few seconds say of the connection, in kilobits a second, or
   * undefined when nothing fresh was measured (a full buffer downloads nothing, and an old figure
   * says nothing about the connection now).
   */
  throughputKbps(): number | undefined {
    const now = this.now();
    this.samples = this.samples.filter(sample => now - sample.at <= FRESH_MS);
    const bytes = this.samples.reduce((sum, sample) => sum + sample.bytes, 0), ms = this.samples.reduce((sum, sample) => sum + sample.ms, 0);
    if (!bytes || !ms) return undefined;
    return Math.max(1, Math.min(1000000, Math.round((bytes * 8) / ms)));
  }

  /** Measured compressed rate across active tracks constrains the speculative window. */
  private aheadTarget(): number {
    const rate = this.tracks.reduce((sum, track) => sum + (track.bytesPerSecond ?? 0), 0);
    return rate > 0 ? Math.min(AHEAD_S, MEDIA_BUDGET / rate) : AHEAD_S;
  }

  private measureSegment(track: Track, bytes: number, duration: number): void {
    if (!Number.isFinite(duration) || duration <= 0 || !Number.isFinite(bytes) || bytes <= 0) return;
    // Keep the observed high watermark so a short cheap segment cannot immediately reopen a
    // speculative window after a large one. Actual retained-byte accounting remains authoritative.
    track.bytesPerSecond = Math.max(track.bytesPerSecond ?? 0, bytes / duration);
  }

  private retainedBytes(): number {
    let total = 0;
    for (const entry of this.retained) total += entry.bytes;
    return total;
  }

  /** A required segment cannot wait for an unknown interval to become safely evictable. */
  private charge(buffer: SourceBuffer, generation: number, bytes: number, kind: RetainedSegment['kind']): RetainedSegment {
    if (bytes > MEDIA_BUDGET || this.retainedBytes() + bytes > MEDIA_BUDGET) {
      throw new SourceError(false, 'media working set too large', 0, 'unsupported');
    }
    // Unknown-placement media can only be retired as a whole-buffer group. Coalesce it by
    // original buffer and generation so many tiny segments cannot grow an unbounded ledger.
    const existing = kind === 'media' && bytes > 0
      ? [...this.retained].find(entry => entry.kind === kind && entry.buffer === buffer && entry.generation === generation && !entry.sealed)
      : undefined;
    if (existing) { existing.bytes += bytes; return existing; }
    const entry: RetainedSegment = {buffer, generation, bytes, kind};
    if (bytes > 0) this.retained.add(entry);
    return entry;
  }

  /** Fresh native ranges prove a whole cohort's frontier, never an appended segment's interval. */
  private cohortRanges(buffer: SourceBuffer): [number, number][] {
    const ranges = buffer.buffered, length = ranges.length, result: [number, number][] = [];
    if (!Number.isSafeInteger(length) || length < 0 || buffer.updating) throw new SourceError(false, 'buffer clock unavailable');
    for (let index = 0; index < length; index++) {
      const from = ranges.start(index), to = ranges.end(index);
      if (!Number.isFinite(from) || !Number.isFinite(to) || from < 0 || to <= from || (index && from < result[index - 1]![1])) throw new SourceError(false, 'buffer clock unavailable');
      result.push([from, to]);
    }
    return result;
  }

  /** Freeze bounded exact charge tokens before waiting for their original frontier. */
  private async sealCohorts(generation: number): Promise<void> {
    for (const track of this.tracks) {
      const buffer = track.buffer;
      if (this.cohorts.has(buffer)) continue;
      await this.free(buffer, this.generationController.signal);
      this.assertCurrent(generation);
      if (this.audioHold) return;
      const entries = [...this.retained].filter(entry => entry.buffer === track.buffer && entry.kind === 'media');
      if (!entries.length) continue;
      const control = this.cohortControl;
      const dependencies = [...this.retained].filter(entry => entry.buffer === buffer && entry.kind === 'init');
      const sizes = [...entries, ...dependencies].map(entry => entry.bytes);
      let ranges: [number, number][];
      try { ranges = this.cohortRanges(buffer); } catch { continue; }
      this.assertCurrent(generation);
      if (control !== this.cohortControl || this.audioHold || this.audioDirty || this.cohorts.has(buffer) ||
        !this.tracks.includes(track) || track.buffer !== buffer || !this.cohortContents(buffer, entries, dependencies) ||
        [...entries, ...dependencies].some((entry, index) => entry.bytes !== sizes[index])) continue;
      // An unreadable/empty/discontinuous placement supplies no automatic playback proof.
      // Its charges remain for a later exact selection/seek/source retirement.
      if (ranges.length !== 1) continue;
      for (const entry of entries) entry.sealed = true;
      this.cohorts.set(buffer, {track, buffer, generation, entries,
        dependencies, ranges, frontier: ranges[0]![1]});
    }
  }

  private cohortOwned(cohort: RetainedCohort): boolean {
    if (this.closed || this.generation !== cohort.generation || this.cohorts.get(cohort.buffer) !== cohort || this.audioHold || this.audioDirty ||
      this.target !== undefined || this.arrival || !this.tracks.includes(cohort.track) || cohort.track.buffer !== cohort.buffer) return false;
    return this.cohortContents(cohort.buffer, cohort.entries, cohort.dependencies);
  }

  private cohortContents(buffer: SourceBuffer, media: readonly RetainedSegment[], init: readonly RetainedSegment[]): boolean {
    const entries = [...this.retained].filter(entry => entry.buffer === buffer);
    return entries.every(entry => entry.kind === 'media' ? media.includes(entry) : entry.kind === 'init' && init.includes(entry)) &&
      media.every(entry => this.retained.has(entry)) && init.every(entry => this.retained.has(entry));
  }

  /** Only a fully exhausted original cohort may be cleared; no current/arrival media is evicted. */
  private cohortPlayed(cohort: RetainedCohort): boolean {
    const control = this.cohortControl;
    if (!this.cohortOwned(cohort)) return false;
    const now = Number(this.player.currentTime);
    if (!Number.isFinite(now) || now < cohort.frontier) return false;
    try {
      const ranges = this.cohortRanges(cohort.buffer);
      const matching = ranges.length === cohort.ranges.length && ranges.every(([from, to], index) => from === cohort.ranges[index]![0] && to === cohort.ranges[index]![1]);
      // All native getters have returned. The final proof is pure original state.
      return matching && control === this.cohortControl && this.cohortOwned(cohort);
    } catch { return false; }
  }

  private async retirePlayedCohorts(generation: number): Promise<void> {
    for (const cohort of [...this.cohorts.values()]) {
      this.assertCurrent(generation);
      if (!this.cohortPlayed(cohort)) continue;
      this.cohortRemoving = cohort;
      try {
        await this.remove(cohort.buffer, 0, Number.MAX_SAFE_INTEGER, generation, undefined, cohort);
        this.assertCurrent(generation);
        if (this.cohorts.get(cohort.buffer) === cohort) this.cohorts.delete(cohort.buffer);
      } finally { if (this.cohortRemoving === cohort) this.cohortRemoving = undefined; }
    }
  }

  /** A sealed cohort may wait while current media can play toward its frontier.
   * Once required coverage is gone, an invalid original proof cannot be healed
   * by adopting native ranges or silently idling forever with frozen charges. */
  private waitForCohort(track: Track, generation: number): boolean {
    const buffer = track.buffer, cohort = this.cohorts.get(buffer), control = this.cohortControl;
    if (!cohort) return false;
    const original = () => !this.closed && this.generation === generation && this.cohortControl === control &&
      this.tracks.includes(track) && track.buffer === buffer && this.cohorts.get(buffer) === cohort && !this.audioHold && !this.audioDirty;
    let covered = false, failure: unknown;
    try {
      const at = this.target !== undefined && !this.live ? this.elementTime(this.target) : Number(this.player.currentTime);
      const ranges = this.cohortRanges(buffer);
      if (!Number.isFinite(at)) throw new SourceError(false, 'buffer clock unavailable');
      covered = ranges.some(([start, end]) => start <= at && end > at);
    } catch (error) { failure = error; }
    // Reentrant seek/selection/retirement owns its own disposition. No native
    // callback occurs after this final original-demand proof.
    if (!original()) return true;
    if (failure) throw failure;
    if (!covered) throw new SourceError(false, 'media working set cannot progress', 0, 'unsupported');
    return true;
  }

  private coversCurrent(track: Track): boolean {
    if (this.arrival) return false;
    const at = this.target !== undefined && !this.live ? this.elementTime(this.target) : Number(this.player.currentTime);
    return Number.isFinite(at) && this.spans(track.buffer).some(([start, end]) => start <= at + 0.05 && end > at + 0.05);
  }

  /** Seconds of media held ahead of where playback is. */
  bufferedSeconds(): number {
    const track = this.tracks[0];
    if (!track) return 0;
    try {
      const ranges = track.buffer.buffered, now = Number(this.player.currentTime);
      for (let index = 0; index < ranges.length; index++) if (ranges.start(index) <= now + 0.1 && ranges.end(index) > now) return Math.max(0, ranges.end(index) - now);
    } catch { /* not readable while updating */ }
    return 0;
  }

  /** Reserve the full unknown response before creating its XHR. Each caller owns one wait. */
  private reserveMedia(signal?: AbortSignal): Promise<() => void> {
    return new Promise((resolve, reject) => {
      let settled = false;
      const remove = () => {
        const index = this.mediaWaiters.indexOf(waiter);
        if (index >= 0) this.mediaWaiters.splice(index, 1);
        this.pending.delete(cancel);
        signal?.removeEventListener('abort', cancel);
      };
      const cancel = () => {
        if (settled) return;
        settled = true; remove();
        reject(new SourceError(false, this.closed ? 'closed' : 'superseded'));
      };
      const waiter = {cancel, grant: () => {
        if (settled) return;
        settled = true; remove(); this.mediaBusy = true;
        let released = false;
        resolve(() => {
          if (released) return;
          released = true; this.mediaBusy = false;
          if (!this.closed) this.mediaWaiters[0]?.grant();
        });
      }};
      if (this.closed || signal?.aborted) { cancel(); return; }
      this.pending.add(cancel);
      signal?.addEventListener('abort', cancel, {once: true});
      if (this.mediaBusy) this.mediaWaiters.push(waiter); else waiter.grant();
    });
  }

  private async getMedia(url: string, signal?: AbortSignal): Promise<{data: ArrayBuffer; release(): void}> {
    const releaseReservation = await this.reserveMedia(signal);
    let released = false;
    const release = () => {
      if (released) return;
      released = true;
      releaseReservation();
    };
    try {
      if (this.closed || signal?.aborted) throw new SourceError(false, this.closed ? 'closed' : 'superseded');
      const data = await this.get(url, 'arraybuffer', signal);
      if (this.closed || signal?.aborted) throw new SourceError(false, this.closed ? 'closed' : 'superseded');
      return {data, release};
    } catch (error) { release(); throw error; }
  }

  private get<T extends 'text' | 'arraybuffer'>(url: string, type: T, signal?: AbortSignal): Promise<T extends 'text' ? string : ArrayBuffer> {
    return new Promise((resolve, reject) => {
      let active: XMLHttpRequest | undefined, retry: ReturnType<typeof setTimeout> | undefined;
      let finishActive: (() => void) | undefined, settled = false;
      const clean = () => {clearTimeout(retry); finishActive?.(); this.pending.delete(cancel); signal?.removeEventListener('abort', abort);};
      const refuse = (error: SourceError) => {if (settled) return; settled = true; clean(); reject(error);};
      const accept = (value: any) => {if (settled) return; settled = true; clean(); resolve(value);};
      const cancelRequest = (detail: string) => {
        const request = active;
        refuse(new SourceError(false, detail));
        try {request?.abort();} catch { /* already completed */ }
      };
      const abort = () => cancelRequest(this.closed ? 'closed' : 'superseded');
      const cancel = () => cancelRequest('closed');
      if (signal?.aborted) {abort(); return;}
      signal?.addEventListener('abort', abort, {once: true});
      this.pending.add(cancel);
      const attempt = (left: number) => {
        if (this.closed || signal?.aborted) { refuse(new SourceError(false, this.closed ? 'closed' : 'superseded')); return; }
        try { if (originOf(url) !== originOf(this.url)) throw new Error('Media origin changed.'); requestCredentials(url, {}, null); } catch { refuse(new SourceError(false, 'invalid media origin')); return; }
        const request = new XMLHttpRequest(); active = request;
        this.requests.add(request);
        try {
          request.open('GET', url);
          request.responseType = type;
          request.timeout = 0;
          request.withCredentials = false;
          request.setRequestHeader('Cache-Control', 'no-store');
        } catch {
          this.requests.delete(request);
          active = undefined;
          refuse(new SourceError(true, 'request failed'));
          return;
        }
        const limit = type === 'text' ? 1048576 : MEDIA_BUDGET;
        const tooLarge = () => new SourceError(false, 'media response too large', 0, type === 'arraybuffer' ? 'unsupported' : '');
        const sent = this.now();
        let answered = 0, loaded = 0;
        let idle: ReturnType<typeof setTimeout> | undefined;
        const done = () => {clearTimeout(idle); this.requests.delete(request); if (active === request) { active = undefined; finishActive = undefined; }};
        finishActive = done;
        const armIdle = () => {
          clearTimeout(idle);
          idle = setTimeout(() => {
            done();
            // Settle before abort: an XHR may dispatch abort synchronously.
            refuse(new SourceError(true, 'request timed out'));
            try { request.abort(); } catch { /* already completed */ }
          }, 20000);
        };
        const invalid = () => {
          if (request.status === 0 && !request.responseURL) return false;
          try { checkResponse(url, request.responseURL, request.status, true); } catch { done(); refuse(new SourceError(false, 'media redirect')); try { request.abort(); } catch { /* already completed */ } return true; }
          const size = type === 'text' ? request.responseText?.length ?? 0 : (request.response as ArrayBuffer | null)?.byteLength ?? 0;
          if (Number(request.getResponseHeader('Content-Length')) > limit || size > limit) { done(); refuse(tooLarge()); try { request.abort(); } catch { /* already completed */ } return true; }
          return false;
        };
        request.onreadystatechange = () => { if (!settled && active === request && request.readyState >= 2) { if (invalid()) return; if (!answered) answered = this.now(); } };
        request.onprogress = event => {
          if (settled || active !== request || invalid()) return;
          if (event.loaded > limit) {
            done(); refuse(tooLarge());
            try { request.abort(); } catch { /* already completed */ }
            return;
          }
          if (event.loaded > loaded) { loaded = event.loaded; armIdle(); }
        };
        request.onload = () => {
          if (settled || active !== request) return;
          done();
          if (invalid()) return;
          if (type === 'text' && textExceedsBytes(request.responseText, limit)) { refuse(tooLarge()); return; }
          const status = request.status;
          if (signal?.aborted) {refuse(new SourceError(false, 'superseded')); return;}
          if (status === 503 && left > 0) { retry = setTimeout(() => attempt(left - 1), PREPARING_MS); return; }
          if (status < 200 || status >= 300) { refuse(new SourceError(status === 0 || status === 408 || status === 429 || status >= 500, `status ${status}`, status >= 400 && status <= 599 ? status : 0)); return; }
          if (type === 'arraybuffer') this.measure((request.response as ArrayBuffer | null)?.byteLength ?? 0, answered || sent, sent);
          accept((type === 'text' ? request.responseText : request.response) as never);
        };
        request.onerror = () => { done(); refuse(new SourceError(true, 'request failed')); };
        request.ontimeout = () => { done(); refuse(new SourceError(true, 'request timed out')); };
        request.onabort = () => { done(); refuse(new SourceError(false, 'closed')); };
        try {armIdle(); request.send();} catch {done(); refuse(new SourceError(true, 'request failed'));}
      };
      attempt(PREPARING_TRIES);
    });
  }

  /**
   * One download, as a measurement of the connection. The time counted is from the server's
   * first word to the last byte, so a segment the server was still writing does not read as a
   * slow connection; when that time is too short to mean anything, the whole request is counted.
   */
  private measure(bytes: number, answered: number, sent: number): void {
    if (bytes < MEASURED_BYTES) return;
    const now = this.now();
    const ms = now - answered >= 20 ? now - answered : Math.max(1, now - sent);
    this.samples.push({bytes, ms, at: now});
    if (this.samples.length > 6) this.samples.shift();
  }

  /**
   * Opens the stream on the media element, to start at `position` (seconds into a title; on the
   * channel's timeline for a channel, where anything that is not a number means "where live
   * viewing begins"). Resolves once the first picture data is in and the clocks can be told
   * apart; a failure is reported to `failed`.
   */
  async open(position: number): Promise<void> {
    const generation = this.nextGeneration();
    try {
      this.assertCurrent(generation);
      const text = await this.get(this.url, 'text');
      this.assertCurrent(generation);
      const master = parseMaster(text, this.url);
      // A media playlist given directly is one muxed track.
      const video = master?.video ?? this.url, codecs = master?.codecs ?? [];
      this.renditions = master?.audio ?? [];
      const chosen = this.renditions.find(rendition => rendition.isDefault) ?? this.renditions[0];
      const [videoList, audioList] = await Promise.all([
        master ? this.get(video, 'text').then(body => this.read(body, video)) : Promise.resolve(this.read(text, video)),
        chosen ? this.get(chosen.url, 'text').then(body => this.read(body, chosen.url)) : Promise.resolve(null),
      ]);
      this.assertCurrent(generation);
      await this.attach(generation);
      this.assertCurrent(generation);
      const videoCodecs = codecs.filter(isVideoCodec);
      const soundOnly = codecs.length > 0 && videoCodecs.length === 0;
      const separateAudio = !!audioList && !soundOnly;
      this.tracks = [];
      // Every buffer declares only the codecs of its own selected rendition.
      // Missing fMP4 metadata is read before addSourceBuffer; MPEG-TS may ask the
      // media source to detect its container payload without invented codecs.
      const firstTrack = await this.createTrack(video, videoList, separateAudio ? videoCodecs : codecs, soundOnly ? 'audio' : separateAudio && videoCodecs.length ? 'video' : undefined, generation, separateAudio);
      if (audioList && chosen && firstTrack.kind !== 'audio') await this.createTrack(chosen.url, audioList, chosen.codecs ?? [], 'audio', generation);
      if (this.live) { await this.openLive(position, generation); return; }
      const total = Math.max(...this.tracks.map(track => track.playlist.segments.reduce((sum, segment) => sum + segment.duration, 0)));
      if (total > 0 && videoList.ended) { try { this.source.duration = total; } catch { /* the element works it out */ } }
      // Filled from where playback will begin: the element is still at zero until it is sent there.
      this.assertCurrent(generation);
      this.target = position;
      this.point(position);
      const video0 = this.tracks[0], first = segmentNumbered(video0.playlist, video0.next);
      if (first) {
        const firstMedia = await this.getMedia(first.url, this.generationController.signal);
        try {
          this.charge(video0.buffer, generation, firstMedia.data.byteLength, 'media');
          await this.append(video0.buffer, firstMedia.data, generation);
        }
        finally { firstMedia.release(); }
        this.assertCurrent(generation);
        video0.next = first.number + 1;
        this.measureSegment(video0, firstMedia.data.byteLength, first.duration);
        try {
          const ranges = video0.buffer.buffered;
          const shift = ranges.length ? ranges.start(0) - first.start : 0;
          // A frame's worth of difference is how the picture is stored, not a shifted clock.
          this.offset = Number.isFinite(shift) && Math.abs(shift) > 0.1 ? shift : 0;
        } catch { this.offset = 0; }
        trace(`hls clock offset ${this.offset.toFixed(3)} s`);
      }
      this.assertCurrent(generation);
      this.startPump(generation);
    } catch (error) {
      if (generation === this.generation) this.fail(error);
    }
  }

  /** A channel: begin at `position` on its timeline, or where live viewing begins. */
  private async openLive(position: number, generation: number): Promise<void> {
    this.assertCurrent(generation);
    for (const track of this.tracks) {
      this.assertCurrent(generation);
      // Each segment follows the one before it, whatever its own clock says (a new programme
      // restarts it). A device that will not do that keeps the segments' own times, and playback
      // is carried over the gaps (`cross`).
      try { track.buffer.mode = 'sequence' as never; } catch { /* the segments' own times are used */ }
    }
    this.assertCurrent(generation);
    const list = this.tracks[0].playlist;
    const wanted = Number.isFinite(position) ? this.originMs! + position * 1000 : NaN;
    const first = (Number.isFinite(wanted) ? segmentAtDate(list, wanted) : undefined) ?? liveStart(list);
    if (!first) throw new SourceError(true, 'the channel has no segments yet');
    for (const track of this.tracks) track.next = first.number;
    const result = await this.feed(this.tracks[0], first, generation);
    const landed = result === 'deferred' ? undefined : result;
    this.assertCurrent(generation);
    // Inside the segment when a moment was asked for and it holds that moment.
    const into = landed && Number.isFinite(wanted) && landed.dateMs !== undefined ? Math.max(0, Math.min(landed.end - landed.start, (wanted - landed.dateMs) / 1000)) : 0;
    this.begin = landed ? landed.start + into : NaN;
    trace(`hls channel begins at segment ${first.number}, element time ${this.begin.toFixed(2)}`);
    this.assertCurrent(generation);
    this.startPump(generation);
  }

  private read(text: string, url: string): Playlist {
    trace('hls playlist ' + (url.split('/').pop() ?? '').split('?')[0] + ': ' + text.split(/\r?\n/).filter(Boolean).slice(0, 12).map(line => line.trim().replace(/[A-Za-z0-9_-]{30,}/g, '<long>')).join(' | ').slice(0, 700));
    return parseMedia(text, url);
  }

  /** A source buffer for `type`, or the element's refusal of it. */
  private add(type: string): SourceBuffer {
    try { return this.source.addSourceBuffer(type); } catch (error) { throw new SourceError(false, `${type}: ${String((error as Error)?.message ?? error)}`, 0, 'unsupported'); }
  }

  private assertCurrent(generation: number, operation?: AudioOperation): void {
    if (this.closed) throw new SourceError(false, 'closed');
    if (generation !== this.generation || (operation && !operation.current())) throw new SourceError(false, 'superseded');
  }

  private attach(generation: number): Promise<void> {
    this.assertCurrent(generation);
    return new Promise((resolve, reject) => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      const finish = (error?: SourceError) => {
        clearTimeout(timer); this.source.removeEventListener('sourceopen', opened); this.pending.delete(cancel);
        if (error) reject(error); else resolve();
      };
      const opened = () => finish();
      const cancel = () => finish(new SourceError(false, 'closed'));
      if (this.closed) {cancel(); return;}
      this.pending.add(cancel);
      this.source.addEventListener('sourceopen', opened);
      timer = setTimeout(() => finish(new SourceError(false, 'media source did not open')), 10000);
      try { this.player.srcObject = this.source as never; }
      catch (error) { finish(new SourceError(false, String((error as Error)?.message ?? error))); }
    });
  }

  private mediaType(playlist: Playlist, declared: readonly string[], kind?: 'video' | 'audio', init?: ArrayBuffer, separateAudio = false): Readonly<{mime: string; kind: 'video' | 'audio'}> {
    const packets = !playlist.init && /\.(ts|aac)(\?|$)/i.test(playlist.segments[0]?.url ?? '');
    let codecs = declared;
    if (!codecs.length || kind === 'audio' && codecs.length !== 1) {
      if (init) {
        try {
          const detected = mp4Codecs(init);
          // Declaring a muxed init video-only does not remove its audio bytes.
          // Refuse that ambiguous topology before adding a second audio buffer;
          // a primary init proving audio-only still remains audio-only.
          if (separateAudio && detected.some(isVideoCodec) && detected.some(codec => !isVideoCodec(codec))) throw new SourceError(false, 'muxed_primary_with_external_audio', 0, 'unsupported');
          codecs = detected.filter(codec => kind === undefined || isVideoCodec(codec) === (kind === 'video'));
        }
        catch (error) {
          if (error instanceof SourceError) throw error;
          throw new SourceError(false, 'initialization codecs unavailable', 0, 'unsupported');
        }
        if (!codecs.length || kind === 'audio' && codecs.length !== 1) throw new SourceError(false, 'initialization has no unambiguous selected track', 0, 'unsupported');
      } else if (!packets) throw new SourceError(false, 'initialization codecs unavailable', 0, 'unsupported');
      else codecs = [];
    }
    const selectedKind = kind ?? (codecs.length && !codecs.some(isVideoCodec) ? 'audio' : 'video');
    return {kind: selectedKind, mime: `${packets ? 'video/mp2t' : selectedKind === 'audio' ? 'audio/mp4' : 'video/mp4'}${codecs.length ? `; codecs="${codecs.join(',')}"` : ''}`};
  }

  /** Fetch once, resolve the exact MIME, then append those same initialization
   * bytes under the existing in-flight reservation and retained-byte budget. */
  private async createTrack(url: string, playlist: Playlist, codecs: readonly string[], kind: 'video' | 'audio' | undefined, generation: number, separateAudio = false): Promise<Track> {
    this.assertCurrent(generation);
    const init = playlist.init ? await this.getMedia(playlist.init, this.generationController.signal) : undefined;
    try {
      this.assertCurrent(generation);
      const selected = this.mediaType(playlist, codecs, kind, init?.data, separateAudio);
      const buffer = this.add(selected.mime);
      this.assertCurrent(generation);
      const track: Track = {...selected, url, playlist, buffer, next: 0, loaded: this.now()};
      this.tracks.push(track);
      if (this.live) {try {track.buffer.mode = 'sequence' as never;} catch { /* Native timestamps remain supported. */ }}
      if (init) {
        this.charge(track.buffer, generation, init.data.byteLength, 'init');
        await this.append(track.buffer, init.data, generation);
      }
      return track;
    } finally {init?.release();}
  }

  /** Wait for an existing buffer operation; closing the source retires its wait immediately. */
  private free(buffer: SourceBuffer, signal?: AbortSignal): Promise<void> {
    return new Promise((resolve, reject) => {
      if (this.closed || signal?.aborted) {reject(new SourceError(false, this.closed ? 'closed' : 'superseded')); return;}
      let busy = false;
      try { busy = buffer.updating; } catch { /* asked as it was removed */ }
      if (!busy) { resolve(); return; }
      let timer: ReturnType<typeof setTimeout> | undefined;
      const finish = (error?: SourceError) => {
        clearTimeout(timer); buffer.removeEventListener('updateend', done); signal?.removeEventListener('abort', cancel); this.pending.delete(cancel);
        if (error) reject(error); else resolve();
      };
      const done = () => finish();
      const cancel = () => finish(new SourceError(false, signal?.aborted ? 'superseded' : 'closed'));
      this.pending.add(cancel); signal?.addEventListener('abort', cancel, {once: true});
      timer = setTimeout(done, 5000);
      buffer.addEventListener('updateend', done);
    });
  }

  private async append(buffer: SourceBuffer, data: ArrayBuffer, generation: number, operation?: AudioOperation, admit?: () => boolean): Promise<boolean> {
    await this.free(buffer, operation?.controller.signal);
    this.assertCurrent(generation, operation);
    return new Promise((resolve, reject) => {
      const off = () => { buffer.removeEventListener('updateend', done); buffer.removeEventListener('error', broke); operation?.controller.signal.removeEventListener('abort', cancel); this.pending.delete(cancel); };
      const done = () => { off(); resolve(true); };
      const broke = () => { off(); reject(new SourceError(false, 'append failed', 0, 'decode')); };
      const cancel = () => {off(); try {if (operation && buffer.updating) buffer.abort();} catch { /* source retirement owns any partial pipeline */ } reject(new SourceError(false, operation?.controller.signal.aborted ? 'superseded' : 'closed'));};
      this.pending.add(cancel); operation?.controller.signal.addEventListener('abort', cancel, {once: true});
      buffer.addEventListener('updateend', done);
      buffer.addEventListener('error', broke);
      try { this.audioMutation(generation, operation, () => {
        if (admit && !admit()) { off(); resolve(false); return; }
        buffer.appendBuffer(data);
      }); } catch (error) { off(); reject(new SourceError(false, String((error as Error)?.message ?? error), 0, 'decode')); }
    });
  }

  /** A superseded seek/audio switch cannot remove a successor's buffered data. */
  private async remove(buffer: SourceBuffer, from: number, to: number, generation: number, operation?: AudioOperation, cohort?: RetainedCohort): Promise<void> {
    this.assertCurrent(generation, operation);
    if (!(to > from)) return;
    await this.free(buffer, operation?.controller.signal);
    this.assertCurrent(generation, operation);
    if (cohort && !this.cohortPlayed(cohort)) throw new SourceError(false, 'superseded');
    // Capture the original entries before the asynchronous removal; a successor's entries
    // can never be refunded by this operation's late updateend.
    const wholeBuffer = from <= 0 && to === Number.MAX_SAFE_INTEGER;
    const removed = cohort ? cohort.entries : wholeBuffer ? [...this.retained].filter(entry => entry.buffer === buffer && entry.kind !== 'staging' &&
      (entry.kind !== 'init' || !!operation)) : [];
    // Never add a newer append to a charge captured by this asynchronous removal.
    for (const entry of removed) entry.sealed = true;
    return new Promise((resolve, reject) => {
      const off = () => {buffer.removeEventListener('updateend', done); buffer.removeEventListener('error', broke); buffer.removeEventListener('abort', broke); operation?.controller.signal.removeEventListener('abort', cancel); this.pending.delete(cancel);};
      const done = () => {
        off();
        try {
          this.assertCurrent(generation, operation);
          const control = this.cohortControl;
          const clock = cohort ? Number(this.player.currentTime) : NaN;
          if (cohort && (!Number.isFinite(clock) || clock < cohort.frontier)) throw new SourceError(false, 'superseded');
          if (wholeBuffer) {
            // A successful updateend alone does not establish a whole clear. Read the actual
            // current buffer strictly: unreadable or residual ranges retain all captured charges.
            const ranges = buffer.buffered, length = ranges.length, updating = buffer.updating;
            if (!Number.isSafeInteger(length) || length !== 0 || updating) throw new SourceError(false, 'buffer not fully removed');
            this.assertCurrent(generation, operation);
            if (this.closed || generation !== this.generation || (operation && (operation.cancelled || this.audioOperation !== operation)) ||
              (cohort && (control !== this.cohortControl || !this.cohortOwned(cohort)))) throw new SourceError(false, 'superseded');
            // No native/owner callback occurs between this final proof and refund.
            for (const entry of removed) this.retained.delete(entry);
            const retired = this.cohorts.get(buffer);
            if (retired && retired.entries.every(entry => !this.retained.has(entry))) this.cohorts.delete(buffer);
          }
          resolve();
        } catch (error) { reject(error instanceof SourceError ? error : new SourceError(false, 'buffer removal unverified')); }
      };
      const broke = () => {off(); reject(new SourceError(false, 'buffer removal failed'));};
      const cancel = () => {off(); try {if (operation && buffer.updating) buffer.abort();} catch { /* removal may already be in flight */ } reject(new SourceError(false, operation?.controller.signal.aborted ? 'superseded' : 'closed'));};
      this.pending.add(cancel); operation?.controller.signal.addEventListener('abort', cancel, {once: true});
      buffer.addEventListener('updateend', done); buffer.addEventListener('error', broke); buffer.addEventListener('abort', broke);
      try { this.audioMutation(generation, operation, () => {
        if (cohort && !this.cohortPlayed(cohort)) throw new SourceError(false, 'superseded');
        buffer.remove(from, to);
      }); } catch (error) {off(); reject(error instanceof SourceError ? error : new SourceError(false, 'buffer removal failed'));}
    });
  }

  private spans(buffer: SourceBuffer): [number, number][] {
    try {
      const ranges = buffer.buffered, spans: [number, number][] = [];
      for (let index = 0; index < ranges.length; index++) spans.push([ranges.start(index), ranges.end(index)]);
      return spans;
    } catch { return []; }
  }

  private ranges(track: Track): string {
    return this.spans(track.buffer).map(([start, end]) => `${start.toFixed(2)}-${end.toFixed(2)}`).join(',') || 'nothing';
  }

  private covered(track: Track, position: number): boolean {
    const at = position + this.offset;
    return this.spans(track.buffer).some(([start, end]) => start <= at + 0.05 && end > at + 0.5);
  }

  /** Aims every track at `position` seconds of a title. */
  private point(position: number): void {
    for (const track of this.tracks) track.next = numberAt(track.playlist, position);
  }

  /**
   * Called before the element is sent to `position`. Answers the element's time for it.
   *
   * A title: at once. What is already buffered there is kept; otherwise the tracks are emptied
   * and filled again from the segment that holds it, and the element waits there for the data.
   *
   * A channel: once the segment that was on air at that moment is in the buffer (at once when it
   * already is). A moment that has left the channel's window is its oldest segment; one that has
   * not been broadcast yet is where live viewing begins.
   */
  seek(position: number): number | Promise<number> {
    this.cohortControl++;
    if (this.audioOperation) this.retireAudio(this.audioOperation);
    if (this.audioDirty) return this.live ? Promise.reject(new SourceError(false, 'audio pipeline interrupted')) : this.elementTime(position);
    if (this.closed || !this.tracks.length) return this.live ? Promise.reject(new SourceError(false, 'closed')) : this.elementTime(position);
    if (this.live) return this.seekLive(position);
    this.target = position;
    if (!this.cohortRemoving && this.tracks.every(track => this.covered(track, position))) return this.elementTime(position);
    const generation = this.nextGeneration();
    void (async () => {
      try {
        for (const track of this.tracks) await this.remove(track.buffer, 0, Number.MAX_SAFE_INTEGER, generation);
        if (generation !== this.generation || this.closed) return;
        this.point(position);
        this.startPump(generation);
      } catch (error) {
        if (generation === this.generation) this.fail(error);
      }
    })();
    return this.elementTime(position);
  }

  private seekLive(position: number): Promise<number> {
    const dateMs = this.originMs! + position * 1000;
    this.arrival?.reject(new SourceError(false, 'superseded'));
    this.arrival = undefined;
    const held = this.placed.find(entry => entry.dateMs !== undefined && dateMs >= entry.dateMs && dateMs < entry.dateMs + (entry.end - entry.start) * 1000);
    if (held && !this.cohortRemoving) return Promise.resolve(held.start + (dateMs - held.dateMs!) / 1000);
    const generation = this.nextGeneration();
    return new Promise<number>((resolve, reject) => {
      this.arrival = {dateMs, resolve, reject};
      void (async () => {
        try {
          for (const track of this.tracks) await this.remove(track.buffer, 0, Number.MAX_SAFE_INTEGER, generation);
          if (generation !== this.generation || this.closed) return;
          this.placed = [];
          const list = this.tracks[0].playlist;
          const on = segmentAtDate(list, dateMs), first = on ?? liveStart(list);
          if (!first) throw new SourceError(true, 'the channel has no segments');
          // A moment not broadcast yet is where live viewing begins: that is what is arrived at.
          if (!on && this.arrival) this.arrival = {...this.arrival, dateMs: first.dateMs ?? dateMs};
          for (const track of this.tracks) track.next = first.number;
          this.startPump(generation);
        } catch (error) {
          if (generation === this.generation) this.fail(error);
        }
      })();
    });
  }

  /** The element has arrived (or the seek was given up). */
  settled(): void { if (this.target !== undefined) this.cohortControl++; this.target = undefined; }

  private audioRanges(buffer: SourceBuffer): [number, number][] {
    try {
      const ranges = buffer.buffered, result: [number, number][] = [];
      for (let index = 0; index < ranges.length; index++) {
        const from = ranges.start(index), to = ranges.end(index);
        if (!Number.isFinite(from) || !Number.isFinite(to) || to < from) throw new Error();
        result.push([from, to]);
      }
      return result;
    } catch {throw new SourceError(false, 'audio buffer clock unavailable');}
  }

  /** Retiring an operation never leaves a stopped pump pretending to be current audio. */
  private retireAudio(operation: AudioOperation): void {
    if (operation.cancelled) return;
    const owns = this.audioOperation === operation, held = this.audioHold === operation;
    if (held) this.audioHold = undefined;
    if (owns) this.audioOperation = undefined;
    operation.cancelled = true;
    operation.controller.abort();
    if (!owns || operation.completed || this.closed || !operation.owner.sourceCurrent()) return;
    if (operation.mutated || operation.writing || this.audioDirty) {this.fail(new SourceError(false, 'audio pipeline interrupted')); return;}
    // This is a fresh root-source retirement operation. No retired continuation may resume a pump.
    if (operation.commitGeneration === this.generation) this.startPump(this.nextGeneration());
    else if (held && operation.commitGeneration === undefined) this.startPump(this.generation);
  }

  private audioMutation(generation: number, operation: AudioOperation | undefined, write: () => void): void {
    this.assertCurrent(generation, operation);
    if (operation) {
      if (operation.clock && !operation.clock()) throw new SourceError(false, 'audio clock moved before mutation');
      operation.owner.willMutate();
      this.assertCurrent(generation, operation); // facts can synchronously retire the original owner
      if (operation.clock && !operation.clock()) throw new SourceError(false, 'audio clock moved before mutation');
      operation.writing = true;
    }
    try {
      write();
      if (operation) {operation.mutated = true; this.audioDirty = true;}
    } finally {if (operation) operation.writing = false;}
  }

  private async audioBarrier(operation: AudioOperation): Promise<void> {
    const signal = operation.controller.signal;
    await new Promise<void>((resolve, reject) => {
      const done = () => {signal.removeEventListener('abort', cancel); resolve();};
      const cancel = () => {signal.removeEventListener('abort', cancel); reject(new SourceError(false, 'superseded'));};
      if (signal.aborted) {cancel(); return;}
      signal.addEventListener('abort', cancel, {once: true});
      void Promise.allSettled([...this.pumpRuns.values()]).then(done);
    });
  }

  /** Real media at the active element clock, not a requested URL or init segment, commits a choice. */
  selectAudio(index: number, owner: AudioSelectionOwner): AudioSelection {
    this.cohortControl++;
    if (this.audioOperation) this.retireAudio(this.audioOperation);
    const controller = new AbortController(), capturedOwner = Object.freeze({...owner});
    const operation: AudioOperation = {owner: capturedOwner, controller, baseGeneration: this.generation, cancelled: false, completed: false, mutated: false, writing: false,
      current: () => !this.closed && !operation.cancelled && this.audioOperation === operation && capturedOwner.current()
        && this.generation === (operation.commitGeneration ?? operation.baseGeneration)};
    this.audioOperation = operation;
    const done = this.changeAudio(index, operation);
    return Object.freeze({done, cancel: () => this.retireAudio(operation)});
  }

  private async changeAudio(index: number, operation: AudioOperation): Promise<void> {
    const requireCurrent = () => this.assertCurrent(operation.commitGeneration ?? operation.baseGeneration, operation);
    const staged: RetainedSegment[] = [];
    try {
      requireCurrent();
      const rendition = this.renditions[index], track = this.tracks.find(candidate => candidate.kind === 'audio');
      if (!rendition || !track) throw new SourceError(false, 'no such audio rendition');
      if (this.audioIndex() === index) {operation.completed = true; return;}
      let text: string;
      try {text = await this.get(rendition.url, 'text', operation.controller.signal);}
      catch (error) {if (error instanceof SourceError && error.status === 422) throw new AudioRenditionUnavailable(false, 'rendition playlist unavailable', 422); throw error;}
      requireCurrent();
      const playlist = this.read(text, rendition.url);
      const position = this.target ?? this.position();
      if (!Number.isFinite(position)) throw new SourceError(false, 'audio clock unavailable');
      const segment = this.live ? segmentAtDate(playlist, this.originMs! + position * 1000) : segmentNumbered(playlist, numberAt(playlist, position));
      if (!segment) throw new SourceError(false, 'audio segment unavailable at current clock');
      const now = this.target ?? this.position();
      const start = this.live && segment.dateMs !== undefined ? (segment.dateMs - this.originMs!) / 1000 : segment.start;
      if (!Number.isFinite(now) || now < start || now >= start + segment.duration) throw new SourceError(false, 'audio clock moved during preparation');
      operation.clock = () => {
        const at = this.target ?? this.position();
        return Number.isFinite(at) && at >= start && at < start + segment.duration;
      };
      // Hold new work, then let the working pump finish accepted IO and update its cursors.
      // The generation is still the working pump's until this cancellable barrier settles.
      this.audioHold = operation;
      // A waiting original pump gives up its own request/wait. An append already accepted by
      // SourceBuffer is not aborted: the barrier joins its updateend before candidate IO.
      this.pumpControllers.get(operation.baseGeneration)?.abort();
      await this.audioBarrier(operation); requireCurrent();
      if (!operation.clock()) throw new SourceError(false, 'audio clock moved during preparation');
      const stage = async (url: string) => {
        const lease = await this.getMedia(url, operation.controller.signal);
        try {
          requireCurrent();
          if (!operation.clock?.()) throw new SourceError(false, 'audio clock moved during preparation');
          const bytes = lease.data.byteLength;
          const used = () => this.retainedBytes();
          if (used() + bytes > MEDIA_BUDGET) throw new SourceError(false, 'media working set too large', 0, 'unsupported');
          const entry: RetainedSegment = {buffer: track.buffer, generation: operation.baseGeneration, bytes, kind: 'staging'};
          this.retained.add(entry); staged.push(entry);
          // Charge the exact candidate bytes before handing off the full in-flight reservation.
          return {data: lease.data, entry};
        } finally { lease.release(); }
      };
      const init = playlist.init ? await stage(playlist.init) : undefined;
      requireCurrent();
      const media = await stage(segment.url);
      requireCurrent();
      const selectedType = this.mediaType(playlist, rendition.codecs ?? [], 'audio', init?.data).mime;
      if (!operation.clock()) throw new SourceError(false, 'audio clock moved during preparation');
      let sequenceStart: number | undefined;
      if (this.live) {
        if (String(track.buffer.mode) !== 'sequence') throw new SourceError(false, 'audio sequence alignment unavailable');
        const placed = this.placedAt(Number(this.player.currentTime));
        if (!placed || placed.dateMs === undefined || segment.dateMs === undefined) throw new SourceError(false, 'audio alignment unavailable');
        sequenceStart = placed.start + (segment.dateMs - placed.dateMs) / 1000;
      }
      // All candidate bytes are in hand. The actually committed track/pump stayed live until here.
      operation.commitGeneration = this.nextGeneration();
      requireCurrent();
      await this.remove(track.buffer, 0, Number.MAX_SAFE_INTEGER, operation.commitGeneration, operation);
      requireCurrent();
      if (this.audioRanges(track.buffer).length) throw new SourceError(false, 'previous audio remained after removal');
      // Clearing samples does not change a SourceBuffer's decoding type. Replace
      // it only when the resolved type changes, including container-only TS types.
      if (selectedType !== track.mime) {
        const previous = track.buffer;
        this.audioMutation(operation.commitGeneration, operation, () => this.source.removeSourceBuffer(previous));
        requireCurrent();
        this.audioMutation(operation.commitGeneration, operation, () => {
          const replacement = this.add(selectedType);
          requireCurrent();
          track.buffer = replacement; track.mime = selectedType;
        });
        requireCurrent();
        for (const entry of staged) entry.buffer = track.buffer;
        if (this.live) this.audioMutation(operation.commitGeneration, operation, () => {track.buffer.mode = 'sequence' as never;});
      }
      if (sequenceStart !== undefined) {
        await this.free(track.buffer, operation.controller.signal); requireCurrent();
        this.audioMutation(operation.commitGeneration, operation, () => {track.buffer.timestampOffset = sequenceStart!;});
      }
      if (init) {
        await this.append(track.buffer, init.data, operation.commitGeneration, operation);
        init.entry.kind = 'init';
      }
      requireCurrent();
      await this.append(track.buffer, media.data, operation.commitGeneration, operation);
      media.entry.kind = 'media';
      requireCurrent();
      const clock = Number(this.player.currentTime);
      if (!this.audioRanges(track.buffer).some(([from, to]) => from <= clock && to > clock)) throw new SourceError(false, 'selected audio did not cover the playback clock');
      track.url = rendition.url; track.playlist = playlist; track.loaded = this.now(); track.next = segment.number + 1;
      track.bytesPerSecond = undefined;
      this.measureSegment(track, media.data.byteLength, segment.duration);
      requireCurrent();
      this.audioDirty = false; operation.completed = true;
      if (this.audioHold === operation) this.audioHold = undefined;
      this.startPump(operation.commitGeneration);
    } catch (error) {
      const held = this.audioHold === operation;
      if (held) this.audioHold = undefined;
      if (operation.current() && operation.owner.sourceCurrent()) {
        if (operation.mutated || operation.writing || this.audioDirty || (error instanceof SourceError && error.refused === 'unsupported')) this.fail(error);
        else if (operation.commitGeneration === this.generation) this.startPump(this.nextGeneration());
        else if (held && operation.commitGeneration === undefined) this.startPump(this.generation);
      }
      operation.completed = true;
      throw error;
    } finally {
      for (const entry of staged) if (entry.kind === 'staging') this.retained.delete(entry);
    }
  }

  // ── A channel's segments in the element's time ──────────────────────────

  /** The segment in the buffer that holds the element's time `now`: the one it is in, else the
   * last one that began before it. */
  private placedAt(now: number): Placed | undefined {
    let before: Placed | undefined;
    for (const entry of this.placed) {
      if (now >= entry.start - 0.05 && now < entry.end) return entry;
      if (entry.start <= now && (!before || entry.start > before.start)) before = entry;
    }
    return before ?? this.placed[0];
  }

  /** Fetches and appends one segment of `track`. For the first track of a channel, answers where
   * it landed in the element's time. */
  private async feed(track: Track, segment: Segment, generation: number, signal = this.generationController.signal): Promise<Placed | 'deferred' | undefined> {
    this.assertCurrent(generation);
    const buffer = track.buffer;
    const current = () => generation === this.generation && !this.closed && this.tracks.includes(track) && track.buffer === buffer;
    await this.retirePlayedCohorts(generation);
    if (this.cohorts.has(track.buffer)) return 'deferred';
    const media = await this.getMedia(segment.url, signal);
    const clock = this.live && track === this.tracks[0];
    const before = this.spans(track.buffer);
    try {
      if (!current()) return undefined;
      if (this.retainedBytes() + media.data.byteLength > MEDIA_BUDGET) {
        await this.sealCohorts(generation);
        await this.retirePlayedCohorts(generation);
        this.assertCurrent(generation);
        if (!current()) return undefined;
        if (this.cohorts.has(buffer)) return 'deferred';
        if (this.retainedBytes() + media.data.byteLength > MEDIA_BUDGET) {
          if (!this.coversCurrent(track)) throw new SourceError(false, 'media working set too large', 0, 'unsupported');
          track.deferred = {number: segment.number, bytes: media.data.byteLength};
          return 'deferred';
        }
      }
      // Another track may have freed enough bytes. That cannot unseal/adopt this buffer.
      if (this.cohorts.has(track.buffer)) return 'deferred';
      this.assertCurrent(generation);
      const appended = await this.append(buffer, media.data, generation, undefined, () => {
        // free() and native listeners can yield/reenter. Reprove the freeze at
        // the final admission point, before either charging or native append.
        if (!current() || this.cohorts.has(buffer)) return false;
        this.charge(buffer, generation, media.data.byteLength, 'media');
        return true;
      });
      if (!appended) return 'deferred';
      track.deferred = undefined;
    } finally { media.release(); }
    if (!current()) return undefined;
    track.next = segment.number + 1;
    this.measureSegment(track, media.data.byteLength, segment.duration);
    if (!clock) return undefined;
    // Where it landed: the stretch of the buffer that is new. It follows the last segment when
    // the device places segments in sequence (or the stream's clock simply went on); after a
    // change of programme on a device that does not, it is a stretch somewhere else.
    const after = this.spans(track.buffer);
    const last = this.placed[this.placed.length - 1];
    let landed: [number, number] | undefined;
    const grown = last ? after.find(([start, end]) => start <= last.end + 0.25 && end > last.end + 0.05) : undefined;
    if (grown && last) landed = [last.end, grown[1]];
    else {
      const fresh = after.find(([start, end]) => !before.some(([s, e]) => Math.abs(s - start) < 0.01 && Math.abs(e - end) < 0.01));
      const was = fresh ? before.find(([s]) => Math.abs(s - fresh[0]) < 0.01) : undefined;
      if (fresh) landed = [was ? was[1] : fresh[0], fresh[1]];
    }
    if (!landed) return undefined;
    const entry: Placed = {number: segment.number, start: landed[0], end: landed[1], ...(segment.dateMs !== undefined ? {dateMs: segment.dateMs} : {})};
    this.placed.push(entry);
    const arrival = this.arrival;
    if (arrival) {
      // The moment asked for is in this segment, or (clamped to the channel's window) before it.
      const length = (entry.end - entry.start) * 1000;
      if (entry.dateMs === undefined || arrival.dateMs < entry.dateMs + length) {
        this.arrival = undefined;
        arrival.resolve(entry.start + (entry.dateMs === undefined ? 0 : Math.max(0, arrival.dateMs - entry.dateMs) / 1000));
      }
    }
    return entry;
  }

  /** A channel on a device that keeps the segments' own times: where the buffer stops because the
   * next segment landed elsewhere (a change of programme), playback is carried to it. */
  private cross(): void {
    const now = Number(this.player.currentTime);
    const here = this.placedAt(now);
    if (!here || this.player.paused || now < here.end - 0.2) return;
    const next = this.placed.find(entry => entry.number === here.number + 1);
    if (!next || Math.abs(next.start - here.end) < 0.25) return;
    trace(`hls channel carried from ${now.toFixed(2)} to ${next.start.toFixed(2)}`);
    try { this.player.currentTime = next.start + 0.01; } catch { /* the next pass tries again */ }
  }

  /** A channel: what has been played and left behind is given back. */
  private async trim(generation: number): Promise<void> {
    const here = this.placedAt(Number(this.player.currentTime));
    if (!here) return;
    let kept = 0;
    for (let index = this.placed.indexOf(here) - 1; index >= 0; index--) {
      const entry = this.placed[index];
      if (kept < BEHIND_S) { kept += entry.end - entry.start; continue; }
      for (const track of this.tracks) if (!this.cohorts.has(track.buffer)) await this.remove(track.buffer, entry.start, entry.end, generation);
      this.assertCurrent(generation);
      this.placed.splice(index, 1);
    }
  }

  /** Keeps each track filled a little ahead of where playback is. */
  private startPump(generation: number): void {
    if (this.closed || this.audioHold || generation !== this.generation || this.pumpRuns.has(generation)) return;
    const controller = new AbortController();
    this.pumpControllers.set(generation, controller);
    const run = this.pump(generation, controller.signal);
    this.pumpRuns.set(generation, run);
    void run.then(reason => {
      if (this.pumpRuns.get(generation) === run) this.pumpRuns.delete(generation);
      if (this.pumpControllers.get(generation) === controller) this.pumpControllers.delete(generation);
      // A hold can be retired while an old run is completing. Resume only that current root.
      if (reason === 'held' && !this.closed && !this.audioHold && generation === this.generation) this.startPump(generation);
    });
  }

  private async pump(generation: number, signal: AbortSignal): Promise<void | 'held'> {
    try {
      while (generation === this.generation && !this.closed) {
        if (this.audioHold) return 'held';
        await this.retirePlayedCohorts(generation);
        const position = this.target ?? this.position();
        let fed = false, finished = true;
        if (this.live) this.cross();
        // Missing current coverage gets the first admission opportunity on every pass.
        const ordered = [...this.tracks].sort((a, b) => Number(this.coversCurrent(a)) - Number(this.coversCurrent(b)));
        for (const track of ordered) {
          if (this.audioHold) return 'held';
          // A playlist that is still growing (or sliding) is read again for its new segments.
          const reload = this.live ? Math.min(RELOAD_MS, Math.max(1000, track.playlist.targetDuration * 500)) : RELOAD_MS;
          if (!track.playlist.ended && track.next >= track.playlist.sequence + track.playlist.segments.length && this.now() - track.loaded > reload) {
            const playlist = this.read(await this.get(track.url, 'text', signal), track.url);
            if (generation !== this.generation) return;
            if (this.audioHold) return 'held';
            track.playlist = playlist; track.loaded = this.now();
          }
          const segment = segmentNumbered(track.playlist, track.next);
          if (!segment) { if (!track.playlist.ended) finished = false; continue; }
          finished = false;
          if (this.live) {
            // Enough is held ahead unless a seek is still waiting for its segment.
            const last = this.placed[this.placed.length - 1];
            if (!this.arrival && last && last.end - Number(this.player.currentTime) > this.aheadTarget() && Number(this.player.currentTime) >= this.placed[0].start - 0.05) continue;
          } else if (segment.start > position + this.aheadTarget()) continue;
          // A sealed buffer admits no new media until its exact old cohort is gone.
          if (this.waitForCohort(track, generation)) continue;
          if (track.deferred?.number === segment.number && this.retainedBytes() + track.deferred.bytes > MEDIA_BUDGET) {
            if (this.coversCurrent(track)) continue;
            throw new SourceError(false, 'media working set too large', 0, 'unsupported');
          }
          try {
            const outcome = await this.feed(track, segment, generation, signal);
            if (outcome === 'deferred') continue;
          } catch (error) {
            // A channel's segment that the server no longer has (viewing was paused while the
            // channel moved on): the playlist is read again, and playback goes on from the oldest
            // segment it still lists. A segment the playlist still names is a real failure.
            const gone = this.live && error instanceof SourceError && (error.status === 404 || error.status === 410);
            if (!gone || generation !== this.generation) throw error;
            const playlist = this.read(await this.get(track.url, 'text', signal), track.url);
            if (generation !== this.generation) return;
            if (this.audioHold) return 'held';
            track.playlist = playlist; track.loaded = this.now();
            if (track.next >= playlist.sequence) throw error;
            continue;
          }
          if (generation !== this.generation) return;
          if (this.audioHold) return 'held';
          fed = true;
          trace(`hls ${track.kind} segment ${segment.number} (${segment.start.toFixed(2)}+${segment.duration.toFixed(2)}) buffered ${this.ranges(track)} at ${Number(this.player.currentTime).toFixed(2)}`);
          if (!this.live && !this.cohorts.has(track.buffer) && position > BEHIND_S * 2) await this.remove(track.buffer, 0, position - BEHIND_S + this.offset, generation);
        }
        if (this.audioHold) return 'held';
        if (this.live && fed) await this.trim(generation);
        if (finished) { try { if (this.source.readyState === 'open') this.source.endOfStream(); } catch { /* already ended */ } }
        if (!fed) await wait(IDLE_MS);
      }
    } catch (error) {
      if (signal.aborted && generation === this.generation && !this.closed) return 'held';
      if (generation === this.generation) this.fail(error);
    }
  }

  private fail(error: unknown): void {
    if (this.closed) return;
    const known = error instanceof SourceError ? error : new SourceError(false, String((error as Error)?.message ?? error));
    if (known.message === 'closed' || known.message === 'superseded') return;
    this.arrival?.reject(known);
    this.arrival = undefined;
    this.failed(known);
  }

  close(): void {
    if (this.closed) return;
    this.stopExposure();
    this.closed = true;
    if (this.audioOperation) this.retireAudio(this.audioOperation);
    this.nextGeneration();
    for (const cancel of [...this.pending]) cancel();
    this.arrival?.reject(new SourceError(false, 'closed'));
    this.arrival = undefined;
    for (const request of [...this.requests]) { try { request.abort(); } catch { /* finished */ } }
    this.retained.clear(); this.cohorts.clear();
  }
}
