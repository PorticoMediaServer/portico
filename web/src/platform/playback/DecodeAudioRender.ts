import type {PlaybackService, PlaybackSnapshot} from '@core/index';
import {audioGainLinear, parseSeekIndex, playableAudio, type AudioRenderV2, type PlayableAudioRender, type SeekIndex} from '@core/playback-v1/audio-render.ts';
import {RangeReader, audioByteCache, type ByteCache} from './audio/bytes';
import {Deck, linkDecks, edgeFrames, type PcmSource} from './audio/engine';
import {MixGraph} from './audio/mix-graph';
import {ConvertedSource} from './audio/converted';
import {TrackStream} from './audio/track';
import {Stretch} from './audio/stretch';
import {BrowserEqualizer, parseEqualizer, type EqualizerSettings} from './audio/equalizer';
import {loadDeviceEqualizer} from './audio/equalizer-store';
import {configureDestination, outputClockLag, type BrowserAudioFacts} from './audio/output';
import {MediaCarrier} from './audio/carrier';
import {listeningDiagnostic, type ListeningEffectsPort} from './listening-effects';
import type {AppLogRecorder} from '../app-log';

/**
 * Music decoded on the device (spec §18.1 version 2; Plan §9.6), Plexamp's model: the original file
 * is fetched with Range, decoded here (WebCodecs, or the JavaScript FLAC decoder), trimmed to its
 * exact frames and scheduled on the AudioContext's sample clock. The next track, prepared by the
 * queue (`:prepare-next`), joins gaplessly or crossfades; commit (`:commit-next`) happens shortly
 * before the edge, and the boundary moves the session.
 *
 * Owned by the web adapter: it has no element, queue, lease
 * or progress transport of its own; it reports to the playback service.
 */
const AHEAD_SECONDS = 30; // decoded ahead of the playhead, per deck (§9.6: at most about 60 s)
const PREPARE_SECONDS = 10; // a candidate decodes its first 10 s before it is "prepared"
const START_SECONDS = 1.5; // buffered before the first track starts
const LEAD = 0.08; // seconds between scheduling and the first frame
const COMMIT_BEFORE = 2; // commit at most crossfade + 2 s before the edge


/** Where a deck's audio comes from: the original file by Range (`direct`), or the server's FLAC or
 * Opus stream (`converted`). `unlock()` at commit lifts the prepared presentation's limits. */
type Feed = {unlock(): void; close(): void; prefetchAll(): Promise<void>; open(fromFrame: number): Promise<PcmSource | undefined>};
type Playing = {deck: Deck; plan: PlayableAudioRender; key: string; fromFrame: number; reader: Feed; token?: string; speed: number};
type Candidate = {deck: Deck; plan: PlayableAudioRender; token: string; reader: Feed; ready: boolean; speed: number};
type Linked = Candidate & {edge: number; fade: number};
type ReadyStart = {revision: number; primary: Playing; graph: MixGraph; intent: number; seek?: number; seekSettled: boolean; publishing: boolean};
type BufferHold = {revision: number; context: AudioContext; graph: MixGraph; primary: Playing; incoming?: Linked; retiring?: Playing; key: string; intent: number; seek: string; token?: string; suspended: boolean; rebased: boolean; resuming: boolean; suspending: boolean};

const sessionKey = (s: PlaybackSnapshot['session']) => (s ? `${s.id}:${s.generation}` : '');
/** The control facts a synchronous notification is not allowed to replace mid-call. */
const sameControl = (a: PlaybackSnapshot, b: PlaybackSnapshot) =>
  a.intent === b.intent && a.intentId === b.intentId && a.phase === b.phase &&
  a.session === b.session && sessionKey(a.session) === sessionKey(b.session) &&
  a.pendingSeek === b.pendingSeek && a.seekRevision === b.seekRevision &&
  a.seekError === b.seekError && a.failedSeek === b.failedSeek &&
  a.audioEffects.rendering === b.audioEffects.rendering && a.audioEffects.rate === b.audioEffects.rate &&
  a.audioEffects.prepared === b.audioEffects.prepared && a.audioEffects.committed === b.audioEffects.committed;


export type SameAlbum = (currentItemId: string | undefined, nextItemId: string) => boolean;

export class DecodeAudioRender {
  private context: AudioContext;
  private master: GainNode;
  private carrier: MediaCarrier;
  private readonly equalizer: BrowserEqualizer;
  private equalizerRevision = 0;
  private equalizerFailed = false;
  private outputCount = 2;
  private clockBasis: BrowserAudioFacts['clock'] = 'latency';
  /** Seconds between the context's clock and what is heard (the output path's latency), smoothed. */
  private lag = 0;
  private primary?: Playing;
  private candidate?: Candidate;
  private incoming?: Linked;
  private retiring?: Playing;
  private snapshot: PlaybackSnapshot;
  private readonly service: PlaybackService;
  private readonly url: (path: string) => string;
  private readonly element: HTMLVideoElement;
  private readonly sameAlbum: SameAlbum;
  private readonly byteCache: ByteCache;
  private readonly timer: ReturnType<typeof setInterval>;
  private disposed = false;
  private revision = 0;
  private seekKey = '';
  private lastRate = 1;
  private committing?: string;
  private askedForGesture = false;
  private factIntent = -1;
  private factPosition = -1;
  private starting = false;
  private processingReport = '';
  private resumeAttempt = 0;
  private readonly listening: ListeningEffectsPort;
  private graph?: MixGraph;
  private readyStart?: ReadyStart;
  private resetting?: Readonly<{revision: number; control: PlaybackSnapshot}>;
  private hold?: BufferHold;
  private suspension?: Promise<void>;
  private resumePending?: object;

  private contextFrame(seconds = this.context.currentTime) { return Math.floor(seconds * this.context.sampleRate); }
  private logicalFrame(seconds = this.context.currentTime) { return this.graph?.clock.at(this.contextFrame(seconds)) ?? 0; }
  private decks(): Deck[] { return [...new Set([this.primary?.deck, this.incoming?.deck, this.retiring?.deck].filter((d): d is Deck => !!d))]; }
  private suspendContext(): Promise<void> {
    if (this.suspension) return this.suspension;
    const run = this.context.suspend();
    this.suspension = run;
    void run.then(() => { if (this.suspension === run) this.suspension = undefined; }, () => { if (this.suspension === run) this.suspension = undefined; });
    return run;
  }
  private holdCurrent(h: BufferHold): boolean {
    const s = this.service.getSnapshot();
    return !this.disposed && this.hold === h && this.revision === h.revision && this.context === h.context && this.graph === h.graph &&
      this.primary === h.primary && this.incoming === h.incoming && this.retiring === h.retiring && this.seekKey === h.seek &&
      sessionKey(s.session) === h.key && s.intentId === h.intent && (!h.token || s.audioEffects.committed?.token === h.token) && !s.pendingSeek && s.phase !== 'error' && s.phase !== 'ended';
  }
  private beginHold(): void {
    if (this.hold || !this.graph || !this.primary) return;
    const s = this.service.getSnapshot();
    const h: BufferHold = {revision: this.revision, context: this.context, graph: this.graph, primary: this.primary, incoming: this.incoming, retiring: this.retiring,
      key: sessionKey(s.session), intent: s.intentId, seek: this.seekKey, token: this.incoming?.token, suspended: false, rebased: false, resuming: false, suspending: false};
    this.hold = h; ++this.resumeAttempt;
    this.carrier.pause();
    this.service.audioRenderStatus?.(0, true);
    this.suspendHold(h);
  }
  private suspendHold(h: BufferHold): void {
    if (!this.holdCurrent(h) || h.suspending) return;
    h.suspending = true; h.suspended = false; h.rebased = false; h.resuming = false;
    ++this.resumeAttempt;
    void this.suspendContext().then(() => {
      h.suspending = false;
      if (!this.holdCurrent(h) || this.context.state !== 'suspended') return;
      h.suspended = true;
      // This uses the OLD epoch's clamped final suspension frame. No decoded frame is replayed.
      h.graph.rebase(this.contextFrame(), this.contextFrame() + Math.max(1, Math.round(LEAD * this.context.sampleRate)));
      h.rebased = true;
      this.recoverHold(h);
    }, () => {
      h.suspending = false;
      if (this.holdCurrent(h)) this.service.audioRenderFailed('audio_render_failed');
    });
  }
  private recoverHold(h: BufferHold): void {
    if (!this.holdCurrent(h) || !h.suspended || !h.rebased || h.resuming || this.context.state !== 'suspended') return;
    const s = this.service.getSnapshot();
    if (s.intent !== 'playing' || s.seekError) return;
    const decks = this.decks(), held = h.graph.clock.frontier;
    const end = Math.max(...decks.map(d => d.endFrame ?? held));
    const required = Math.min(end, held + Math.ceil(START_SECONDS * this.context.sampleRate));
    const ready = h.graph.preview(required).frontier;
    if (ready < required || (required === end && decks.some(d => !d.sourceComplete))) return;
    // The context has stayed suspended while decoding. One common fresh anchor, all owned gains.
    for (const deck of decks) deck.rebuildAutomation(held);
    h.graph.admit(Math.min(end, held + Math.round(AHEAD_SECONDS * this.context.sampleRate)));
    h.resuming = true;
    if (!this.resumeContext(h)) h.resuming = false;
  }

  constructor(service: PlaybackService, url: (path: string) => string, element: HTMLVideoElement, sameAlbum: SameAlbum = () => false, byteCache: ByteCache = audioByteCache, private readonly recordDiagnostic?: AppLogRecorder) {
    this.service = service; this.url = url; this.element = element; this.sameAlbum = sameAlbum; this.byteCache = byteCache;
    this.listening = service;
    this.snapshot = service.getSnapshot();
    // On iPhone and iPad, audio from a bare AudioContext follows the silent switch and stops when
    // the page goes to the background unless the page says it is playing media.
    try { const session = (navigator as Navigator & {audioSession?: {type: string}}).audioSession; if (session && session.type !== 'playback') session.type = 'playback'; } catch { /* not offered here */ }
    this.context = createContext();
    this.outputCount = configureDestination(this.context.destination);
    this.context.addEventListener('statechange', this.contextChanged);
    document.addEventListener('visibilitychange', this.interrupted);
    this.master = this.context.createGain();
    this.equalizer = new BrowserEqualizer(this.context);
    this.equalizer.output.connect(this.master);
    // Through a media element where the browser allows it, so media keys and the lock screen reach music too.
    this.carrier = new MediaCarrier(this.context, this.master, () => { if (!this.disposed && this.service.getSnapshot().intent === 'playing') this.service.pause(); });
    this.outputChanged();
    const equalizerRevision = this.equalizerRevision;
    void loadDeviceEqualizer().then(settings => {
      if (!this.disposed && equalizerRevision === this.equalizerRevision) this.applyEqualizer(settings);
    }).catch(() => { /* Storage/graph failure grants no processing facts. The caller can retry. */ });
    this.context.addEventListener('sinkchange', this.outputChanged);
    navigator.mediaDevices?.addEventListener?.('devicechange', this.outputChanged);
    this.volume();
    element.addEventListener('volumechange', this.volume);
    document.addEventListener('click', this.gesture);
    document.addEventListener('keydown', this.gesture);
    this.timer = setInterval(() => this.tick(), 50);
  }

  /** The system interrupted the context (a call, another app's audio) or suspended it in the
   * background, and does not start it again by itself. While the listener still means it to
   * play, it is started again once; a browser that refuses changes nothing and is not asked twice. */
  private interrupted = () => {
    if (this.disposed || this.hold || this.starting || this.context.state === 'running' || this.context.state === 'closed') return;
    if (document.visibilityState !== 'visible' || this.service.getSnapshot().intent !== 'playing' || this.service.getSnapshot().seekError) return;
    this.resumeContext();
  };
  /** While the context is not running nothing is heard, so the position stops; once it runs
   * again the schedule is brought up to date. An interruption is also answered as above. */
  private contextChanged = () => {
    if (this.disposed) return;
    if (this.context.state === 'running') {
      const s = this.service.getSnapshot();
      // A delayed resume cannot bypass the current user's pause or a newer buffering ticket.
      if (s.intent !== 'playing' || s.seekError || this.hold && !this.hold.resuming) {
        if (this.hold) this.suspendHold(this.hold); else void this.suspendContext().catch(() => {}); return;
      }
      this.tick();
    }
    else this.service.audioRenderStatus?.(0, false);
    this.interrupted();
  };
  private volume = () => { this.master.gain.setTargetAtTime(this.element.muted ? 0 : this.element.volume, this.context.currentTime, 0.01); };
  private gesture = () => {
    queueMicrotask(() => {
      if (!this.disposed && this.service.getSnapshot().intent === 'playing') {
        this.askedForGesture = false;
        // An autoplay-blocked resume may remain pending indefinitely. A fresh explicit gesture
        // owns a new attempt; the old promise cannot publish refusal into it.
        this.resumePending = undefined;
        if (this.hold) {
          if (this.hold.resuming) this.resumeContext(this.hold);
          else this.recoverHold(this.hold);
        } else this.resumeContext();
      }
    });
  };

  private resumeContext(hold?: BufferHold): boolean {
    if (this.hold && this.hold !== hold || hold && !this.holdCurrent(hold) || this.resumePending) return false;
    const before = this.service.getSnapshot(), key = sessionKey(before.session);
    if (before.intent !== 'playing' || before.seekError || !this.primary || this.primary.key !== key) return false;
    const context = this.context, graph = this.graph, pending = {};
    this.resumePending = pending;
    const attempt = ++this.resumeAttempt, revision = this.revision;
    const refused = () => {
      const current = this.service.getSnapshot();
      if (this.disposed || attempt !== this.resumeAttempt || revision !== this.revision || current.intentId !== before.intentId || sessionKey(current.session) !== key || current.intent !== 'playing') return;
      if (hold && this.holdCurrent(hold)) hold.resuming = false;
      this.service.pause();
      this.listening.listeningNotice('audio_autoplay_blocked');
      this.recordDiagnostic?.('audio_autoplay_blocked', 'warn');
    };
    void context.resume().then(() => {
      if (this.resumePending === pending) this.resumePending = undefined;
      const current = this.service.getSnapshot();
      if (this.disposed || context !== this.context || graph !== this.graph || revision !== this.revision || attempt !== this.resumeAttempt || sessionKey(current.session) !== key) return;
      if (current.intent !== 'playing' || current.seekError) { if (hold && this.holdCurrent(hold)) this.suspendHold(hold); else void this.suspendContext().catch(() => {}); return; }
      if (context.state !== 'running') { refused(); return; }
      if (hold && this.holdCurrent(hold)) this.hold = undefined;
      this.tick();
    }, () => { if (this.resumePending === pending) this.resumePending = undefined; refused(); });
    // AudioContext.resume may remain pending under autoplay policy. An actual media-element
    // NotAllowedError supplies refusal evidence; a running direct path needs no notice.
    this.carrier.play(error => {
      if (error && typeof error === 'object' && 'name' in error && error.name === 'NotAllowedError' && this.context.state !== 'running') refused();
    });
    return true;
  }

  private reader(plan: PlayableAudioRender, prepared: boolean): Feed {
    if (plan.mode === 'converted') {
      let source: ConvertedSource | undefined, unlocked = !prepared;
      return {
        unlock: () => { unlocked = true; source?.unlock(); },
        close: () => source?.close(),
        prefetchAll: async () => {},
        open: async fromFrame => { source = new ConvertedSource({...plan, url: this.url(plan.url)}, fromFrame, {locked: !unlocked}); return source; },
      };
    }
    // A start or seek that is still reading the file is alive, however long the link makes it take.
    const alive = () => { if (!this.disposed && !prepared) this.service.engineProgress(this.service.getSnapshot().intentId); };
    const range = new RangeReader({url: this.url(plan.url), key: plan.id, size: plan.bytes, cache: this.byteCache, onBytes: alive, ...(prepared && plan.prefetchBytes ? {limit: plan.prefetchBytes} : {})});
    // A file with no index of its own (MP3, ADTS, Ogg) started part-way reads the server's seek
    // index (once) and the file from the nearest entry; nothing before it is read and nothing
    // is converted.
    const indexUrl = plan.seekIndex?.url;
    const abort = new AbortController();
    let indexRead: Promise<SeekIndex | undefined> | undefined;
    const index = indexUrl ? () => (indexRead ??= fetch(this.url(indexUrl), {signal: abort.signal, credentials: 'omit', cache: 'no-store', redirect: 'error'})
      .then(r => (r.ok ? r.json() : undefined)).then(parseSeekIndex, () => undefined)) : undefined;
    const open = (fromFrame: number): Promise<PcmSource | undefined> => TrackStream.open(plan, range, fromFrame, undefined, {index});
    return {unlock: () => range.unlock(), close: () => { abort.abort(); range.close(); }, prefetchAll: () => range.prefetchAll(), open};
  }

  private gainFor(plan: AudioRenderV2) { const settings = this.snapshot.audioEffects.settings; return audioGainLinear(plan, settings.normalization, 0, settings.neverBoost === true); }

  /** A deck for `plan` from `fromFrame`; at a speed other than 1 the audio is time-stretched so
   * it keeps its pitch (§9.6 rule 3). */
  private async openDeck(plan: PlayableAudioRender, reader: Feed, fromFrame: number, label: string, speed: number): Promise<Deck | undefined> {
    const stream = await reader.open(fromFrame);
    if (!stream) return undefined;
    const frames = plan.durationFrames - fromFrame;
    const source = speed === 1 ? stream : new Stretch(stream, speed, frames);
    return new Deck(this.context, this.equalizer.input, source, {frames: speed === 1 ? frames : (source as Stretch).outputFrames, gain: this.gainFor(plan), label, holdTail: this.snapshot.audioEffects.settings.gapless || this.snapshot.audioEffects.settings.crossfadeSeconds === 0});
  }

  /** A candidate cancelled before its deck opened has no deck yet; its reader still closes. */
  private closePlaying(p?: Playing | Candidate) { if (!p) return; (p.deck as Deck | undefined)?.close(); p.reader.close(); }
  private reset(control = this.service.getSnapshot()) {
    const owner = {revision: ++this.revision, control}, graph = this.graph;
    const decks = [...new Set([this.primary, this.candidate, this.incoming, this.retiring])];
    this.resetting = owner; ++this.resumeAttempt;
    // Detach every field before callbacks. Cleanup below owns only the captured old resources.
    this.resumePending = undefined; this.hold = undefined; this.readyStart = undefined; this.graph = undefined;
    this.primary = this.candidate = this.incoming = this.retiring = undefined;
    this.askedForGesture = false; this.committing = undefined; this.starting = false;
    try {
      graph?.close();
      for (const deck of decks) this.closePlaying(deck);
      if (this.revision === owner.revision) this.reportProcessing();
    } finally { if (this.resetting === owner) this.resetting = undefined; }
  }

  apply(s: PlaybackSnapshot) {
    if (this.disposed || !this.renderWitness(s)()) return;
    // A reset's own processing notification is not a fresh start request. A changed control
    // state (pause/replacement) still enters independently and owns its own reset epoch.
    if (this.resetting && sameControl(s, this.resetting.control)) return;
    this.snapshot = s;
    if (!s.session || !s.audioEffects.rendering || s.phase === 'error' || s.phase === 'ended' || s.failedSeek) { this.reset(); return; }
    const plan = s.session.audio;
    if (!playableAudio(plan)) { this.service.audioRenderFailed('audio_render_failed'); return; }
    const seek = s.pendingSeek, seekKey = seek ? `${s.intentId}:${seek.revision}` : '';
    const rateChanged = this.lastRate !== s.audioEffects.rate;
    const key = sessionKey(s.session);
    // A boundary promotes the committed deck (same key); anything else that moves the track starts over.
    if (this.primary?.key !== key || (seek && seekKey !== this.seekKey) || rateChanged) {
      const position = seek?.positionSeconds ?? (this.primary?.key === key ? this.position() : s.positionSeconds);
      const resetRevision = this.revision + 1;
      this.reset(s);
      if (this.revision !== resetRevision || this.primary || this.candidate || this.incoming || this.retiring || this.graph || this.starting || !this.renderWitness(s)()) return;
      this.seekKey = seekKey; this.lastRate = s.audioEffects.rate;
      void this.start(plan, key, Math.max(0, Math.min(plan.durationFrames - 1, Math.round(position * plan.sampleRate))), s, resetRevision);
      return;
    }
    const current = this.renderWitness(s);
    for (const p of [this.primary, this.incoming, this.retiring]) if (p) p.deck.setGain(this.gainFor(p.plan));
    this.reportProcessing();
    if (!current()) return;
    // ready/seekApplied notify synchronously. Their nested apply may pause, but cannot resume
    // or prepare another edge before this original publication sequence finishes its checks.
    if (this.readyStart?.publishing) { if (s.intent === 'paused' || s.seekError) this.transport(s); return; }
    if (this.publishStart(s)) return;
    this.transport(s);
    if (!current()) return;
    this.prepared(s);
    if (!current()) return;
    this.committed(s);
  }

  private transport(s: PlaybackSnapshot) {
    if (!this.renderWitness(s)()) return;
    if (s.intent === 'paused' || s.seekError) { this.carrier.pause(); if (this.hold && (!this.hold.suspended || this.hold.resuming)) this.suspendHold(this.hold); else void this.suspendContext().catch(() => {}); return; }
    if (this.hold) { this.recoverHold(this.hold); return; }
    if ((this.context.state as string === 'suspended' || this.context.state as string === 'interrupted') && !this.starting && !this.askedForGesture) {
      // An autoplay denial needs a gesture: say so rather than failing.
      this.askedForGesture = true;
      this.resumeContext();
    } else this.carrier.play();
  }

  private async start(plan: PlayableAudioRender, key: string, fromFrame: number, original: PlaybackSnapshot, revision: number) {
    // Never capture a successor's epoch on behalf of the old call, even before reader creation.
    if (this.disposed || this.revision !== revision || this.primary || this.graph || this.starting || !this.renderWitness(original)()) return;
    this.starting = true;
    const reader = this.reader(plan, false);
    try {
      const speed = original.audioEffects.rate;
      const deck = await this.openDeck(plan, reader, fromFrame, 'primary', speed);
      if (revision !== this.revision || this.disposed) { deck?.close(); reader.close(); return; }
      if (!deck) { reader.close(); this.service.audioRenderFailed('audio_render_failed'); return; }
      this.primary = {deck, plan, key, fromFrame, reader, speed};
      await deck.pump(START_SECONDS);
      if (revision !== this.revision || this.disposed) return;
      if (deck.failure) throw deck.failure;
      // An older requested suspension must settle before a successor installs any source nodes.
      if (this.suspension) await this.suspension;
      if (revision !== this.revision || this.disposed) return;
      const graph = new MixGraph(this.context, this.contextFrame() + Math.max(1, Math.round(LEAD * this.context.sampleRate)));
      this.graph = graph;
      deck.attachGraph(graph); deck.start(0);
      graph.admit(Math.round(AHEAD_SECONDS * this.context.sampleRate));
      this.starting = false;
      this.readyStart = {revision, primary: this.primary, graph, intent: original.intentId, seek: original.pendingSeek?.revision, seekSettled: false, publishing: false};
      const current = this.renderWitness(original);
      if (!current()) return;
      this.reportProcessing();
      if (!current()) return;
      this.publishStart(original);
    } catch {
      if (revision === this.revision && !this.disposed) this.service.audioRenderFailed('audio_render_failed');
    }
  }

  /** Publish only an actually installed source, under this call's captured control state.
   * A later apply/tick may complete paused readiness using its own fresh witness; the old
   * playing continuation never reads and adopts that newer state after a notification. */
  private publishStart(s: PlaybackSnapshot): boolean {
    const start = this.readyStart;
    if (!start || start.publishing) return false;
    const current = this.renderWitness(s);
    if (!current() || this.revision !== start.revision || this.primary !== start.primary || this.graph !== start.graph ||
        s.intentId !== start.intent || sessionKey(s.session) !== start.primary.key) return true;
    if (start.seek !== undefined && !start.seekSettled && s.pendingSeek?.revision !== start.seek) return true;
    start.publishing = true;
    try {
      // These are the exact known effects of ready/seekApplied, not a newly borrowed snapshot.
      let expected: PlaybackSnapshot = s.phase === 'starting' ? {...s, phase: 'ready'} : s;
      this.service.ready(start.intent);
      if (!current(expected)) return true;
      if (start.seek !== undefined && !start.seekSettled) {
        const applied = this.service.seekApplied(start.intent, start.seek, start.primary.fromFrame / start.primary.plan.sampleRate, 'paused');
        start.seekSettled = applied;
        expected = {...expected, pendingSeek: undefined, seekError: undefined, failedSeek: undefined};
        if (!applied || !current(expected)) return true;
      }
      this.readyStart = undefined;
      this.transport(expected);
      if (!current(expected)) return true;
      void start.primary.deck.pump(AHEAD_SECONDS);
      return true;
    } finally {
      start.publishing = false;
    }
  }

  private prepared(s: PlaybackSnapshot) {
    const prepared = s.audioEffects.prepared;
    if (!prepared && !s.audioEffects.committed && this.candidate) { this.closePlaying(this.candidate); this.candidate = undefined; this.committing = undefined; return; }
    if (!prepared?.audio || !playableAudio(prepared.audio) || prepared.token === this.candidate?.token || prepared.token === this.incoming?.token) return;
    this.closePlaying(this.candidate);
    this.committing = undefined;
    const plan = prepared.audio, token = prepared.token, revision = this.revision;
    const reader = this.reader(plan, true);
    const speed = s.audioEffects.rate;
    const pending: Candidate = {deck: undefined as unknown as Deck, plan, token, reader, ready: false, speed};
    this.candidate = pending;
    void (async () => {
      try {
        const deck = await this.openDeck(plan, reader, 0, 'candidate', speed);
        if (!deck) throw new Error('No decoder.');
        if (this.candidate !== pending || revision !== this.revision) { deck.close(); return; }
        pending.deck = deck;
        const previous = this.primary;
        const joins = previous && previous.speed === 1 && speed === 1
          && (s.audioEffects.settings.crossfadeSeconds === 0 || !!prepared.itemId && this.sameAlbum(s.itemId, prepared.itemId));
        if (joins) {
          await previous.deck.pump(AHEAD_SECONDS);
          if (this.candidate !== pending || revision !== this.revision) { deck.close(); return; }
          deck.continueFrom(previous.deck);
        }
        const overlap = Math.min(12, Math.max(0, s.audioEffects.settings.crossfadeSeconds));
        // A full 12-second fade cannot be admitted from a ten-second candidate.
        await deck.pump(Math.max(PREPARE_SECONDS, overlap));
        if (this.candidate !== pending) return;
        if (deck.failure) throw deck.failure;
        pending.ready = true;
        this.service.audioPrepared(token);
      } catch {
        if (this.candidate === pending) { this.closePlaying(pending); this.candidate = undefined; listeningDiagnostic('audio_transition_unavailable', undefined, this.recordDiagnostic); this.listening.audioPreparationFailed(); }
      }
    })();
  }

  private committed(s: PlaybackSnapshot) {
    const committed = s.audioEffects.committed, c = this.candidate, p = this.primary;
    if (this.hold || !this.graph || !committed || !c || committed.token !== c.token || !c.ready || this.incoming || !p || p.deck.endFrame === undefined) return;
    if (committed.session.audio?.id !== c.plan.id) { this.service.audioRenderFailed('audio_render_failed'); return; }
    const current = this.renderWitness(s);
    if (!current()) return;
    // The whole next file may now be fetched; bring it into the cache before its edge.
    c.reader.unlock();
    if (!current()) return;
    void c.reader.prefetchAll();
    if (!current()) return;
    const sameAlbum = this.sameAlbum(s.itemId, committed.itemId);
    if (!current()) return;
    const graph = this.graph;
    const contextNow = this.contextFrame();
    // Never link at a past physical time, or change an interval already heard.
    const earliest = graph.clock.at(contextNow + Math.max(1, Math.round(LEAD * this.context.sampleRate)));
    const planned = edgeFrames({endFrame: p.deck.endFrame, outFrames: p.deck.outFrames}, c.deck,
      {crossfadeSeconds: Math.min(12, s.audioEffects.settings.crossfadeSeconds), sameAlbum, rate: this.context.sampleRate});
    const start = Math.max(planned.start, earliest);
    if (start < graph.clock.frontier && c.deck.decoded < Math.min(c.deck.outFrames, graph.clock.frontier - start)) { void c.deck.pump(AHEAD_SECONDS); return; }
    c.deck.attachGraph(graph);
    const edge = linkDecks(this.context, p.deck, c.deck, {crossfadeSeconds: Math.min(12, s.audioEffects.settings.crossfadeSeconds), sameAlbum, earliest});
    graph.admit(this.logicalFrame() + Math.round(AHEAD_SECONDS * this.context.sampleRate));
    if (edge.late) listeningDiagnostic('audio_edge_late', undefined, this.recordDiagnostic);
    if (!current()) return;
    this.incoming = {...c, edge: edge.start, fade: edge.fade};
    this.candidate = undefined;
  }

  /** Seconds into the current track, as heard: the sample clock less the output path's latency. */
  private position(): number {
    const p = this.primary;
    if (!p) return this.snapshot.positionSeconds;
    // Output frames at the context rate → track frames at the file's rate, times the speed.
    const played = p.deck.playedFrames(this.heardTime()) * p.plan.sampleRate / this.context.sampleRate * p.speed;
    return (p.fromFrame + played) / p.plan.sampleRate;
  }

  /** The context time of the sample being heard now. */
  private heardTime(): number { return Math.max(0, this.context.currentTime - this.lag); }

  /**
   * How far the speakers are behind the context's clock (A-17.26): the browser's own output
   * timestamp where it gives one, else its stated output and processing latencies. Measured while
   * the context runs (a suspended clock stands still while the page's clock moves on), bounded to
   * half a second, and smoothed so the position never jumps.
   */
  private measureLag() {
    const sample = outputClockLag(this.context, performance.now(), this.carrier.carrying);
    if (!sample) return;
    this.clockBasis = sample.basis;
    this.lag = this.lag === 0 ? sample.seconds : this.lag + (sample.seconds - this.lag) * 0.1;
  }

  /** Notifications are synchronous and may pause without changing intentId or the deck.
   * Retain this tick's original control state; never continue by adopting a subscriber's state. */
  private renderWitness(s: PlaybackSnapshot): (expected?: PlaybackSnapshot) => boolean {
    const primary = this.primary, incoming = this.incoming, retiring = this.retiring, graph = this.graph;
    const context = this.context, revision = this.revision, seek = this.seekKey;
    return (expected = s) => {
      const current = this.service.getSnapshot();
      return !this.disposed && this.primary === primary && this.incoming === incoming && this.retiring === retiring &&
        this.graph === graph && this.context === context && this.revision === revision && this.seekKey === seek &&
        sameControl(current, expected);
    };
  }

  private tick() {
    if (this.disposed) return;
    const s = this.service.getSnapshot(), p = this.primary, graph = this.graph, current = this.renderWitness(s);
    this.reportProcessing();
    if (!current()) return;
    if (!p || !graph || s.phase === 'error' || s.phase === 'ended' || this.starting) return;
    if (this.readyStart?.publishing || this.publishStart(s)) return;
    this.measureLag();
    if (!current()) return;
    const decks = this.decks();
    for (const deck of decks) {
      if (deck.failure) { this.service.audioRenderFailed('audio_render_failed'); if (current()) this.reset(); return; }
      // Exact shared pump promise: ticks never create an awaiting continuation for stalled IO.
      void deck.pump(AHEAD_SECONDS);
    }
    if (this.hold) {
      this.service.audioRenderStatus?.(0, true);
      if (!current() || !this.hold) return;
      this.recoverHold(this.hold);
      return;
    }
    const frame = this.logicalFrame(), heard = this.logicalFrame(this.heardTime());
    if (!this.incoming && frame >= p.deck.tailDeadlineSeconds * this.context.sampleRate) p.deck.finishTail();
    const endOfMix = Math.max(...decks.map(d => d.endFrame ?? frame));
    if (!current()) return;
    graph.admit(Math.min(endOfMix, frame + Math.round(AHEAD_SECONDS * this.context.sampleRate)));
    const missing = decks.some(d => !d.sourceComplete || graph.clock.frontier < (d.endFrame ?? 0));
    // Nodes are already bounded by the common frontier, so even a late tick cannot overshoot.
    if (missing && graph.clock.contextAt(graph.clock.frontier) - this.contextFrame() <= Math.ceil(0.05 * this.context.sampleRate)) {
      this.beginHold(); return;
    }
    const next = this.incoming;
    if (!current()) return;
    if (next && s.intent === 'playing' && !s.pendingSeek && !s.seekError && this.context.state === 'running' && heard >= next.edge) {
      this.retiring = p;
      this.primary = {deck: next.deck, plan: next.plan, key: sessionKey(s.audioEffects.committed?.session), fromFrame: 0, reader: next.reader, token: next.token, speed: next.speed};
      this.incoming = undefined; this.committing = undefined; this.seekKey = '';
      this.service.audioBoundary(next.token, this.position());
      return;
    }
    if (this.retiring && this.retiring.deck.sourceComplete && heard >= (this.retiring.deck.endFrame ?? Infinity)) {
      this.closePlaying(this.retiring);
      if (!current()) return;
      this.retiring = undefined;
      return;
    }
    const playing = s.intent === 'playing' && this.context.state === 'running' && this.contextFrame() >= graph.clock.contextStart && p.deck.startFrame !== undefined && frame >= p.deck.startFrame;
    this.service.audioRenderStatus?.(playing ? s.audioEffects.rate : 0, false);
    if (!current()) return;
    const end = p.deck.endFrame ?? Infinity;
    if (playing && !s.pendingSeek) {
      const position = this.position();
      if (p.deck.sourceComplete && heard >= end && !this.incoming && !this.committing && !s.audioEffects.prepared) this.service.fact(s.intentId, position, 'ended');
      else if (s.intentId !== this.factIntent || Math.abs(position - this.factPosition) >= 0.25) { this.factIntent = s.intentId; this.factPosition = position; this.service.fact(s.intentId, position, 'playing'); }
    }
    if (!current()) return;
    graph.discardBefore(heard, this.contextFrame(this.heardTime()));
    // Link a deferred original candidate only after actual overlap PCM becomes ready.
    this.committed(s);
    if (!current()) return;
    const c = this.candidate;
    if (s.intent === 'playing' && c?.ready && !this.committing && end !== Infinity) {
      const fade = Math.min(s.audioEffects.settings.crossfadeSeconds, 12);
      if ((end - frame) / this.context.sampleRate <= fade + COMMIT_BEFORE) { this.committing = c.token; this.service.audioCommit(c.token); }
    }
  }

  private outputChanged = () => {
    // A route change may alter channel limits, never the app's EQ choice or filter state.
    if (!this.disposed) this.outputCount = this.carrier.refreshOutput();
  };

  /** Apply committed device settings; only a successfully configured graph reports EQ on. */
  applyEqualizer(settings: EqualizerSettings): void {
    const value = parseEqualizer(settings);
    if (this.disposed) throw new Error('equalizer_unavailable');
    ++this.equalizerRevision;
    // The deck this renderer is actually playing must own the fact: it is captured before
    // the failing operation, so a re-entrant fallback cannot hand us a successor's frame.
    const live = this.service.getSnapshot();
    const owner = this.primary && live.session && this.primary.key === sessionKey(live.session)
      ? {deck: this.primary, sessionId: live.session.id, generation: live.session.generation, itemId: live.itemId, intentId: live.intentId} : undefined;
    try { this.equalizer.apply(value); this.equalizerFailed = false; }
    catch (error) {
      this.equalizerFailed = true;
      try { this.equalizer.bypass(); }
      catch { this.service.fallbackAudioRender(); }
      // This song continues through the dry path. Saved intent remains on, actual EQ is false.
      listeningDiagnostic('equalizer_filter_unavailable', error, this.recordDiagnostic);
      if (owner && this.primary === owner.deck) this.service.listeningFallback('eq', {sessionId: owner.sessionId, generation: owner.generation, itemId: owner.itemId, intentId: owner.intentId});
    }
    this.reportProcessing();
  }
  getEqualizer(): EqualizerSettings { return this.equalizer.settings; }
  audioFacts(): BrowserAudioFacts | undefined {
    const plan = this.primary?.plan;
    const snapshot = this.service.getSnapshot();
    if (!plan || this.starting || this.primary?.deck.startFrame === undefined || this.primary.deck.failure || this.primary.key !== sessionKey(snapshot.session) || !snapshot.audioEffects.rendering || snapshot.phase === 'error' || snapshot.phase === 'ended') return undefined;
    const levelingGain = this.primary.deck.norm.gain.value;
    return {engine:'web-audio',bitPerfect:false,sourceRate:plan.sampleRate,contextRate:this.context.sampleRate,
      resampledToContext:plan.sampleRate!==this.context.sampleRate,deviceRate:null,browserMayResample:true,
      sourceChannels:plan.channels,outputChannels:Math.min(plan.channels,this.outputCount),eqApplied:!this.equalizerFailed && this.equalizer.settings.enabled,levelingApplied:Number.isFinite(levelingGain) && levelingGain !== 1,
      ...(Number.isFinite(levelingGain) && levelingGain > 0 ? {levelingGainDb:20*Math.log10(levelingGain)} : {}),clock:this.carrier.carrying?'unmeasured-carrier':this.clockBasis};
  }

  private reportProcessing(): void {
    const snapshot = this.service.getSnapshot(), session = snapshot.session;
    if (!session) { this.processingReport = ''; return; }
    const facts = this.audioFacts(), levelingGainDb = facts && facts.levelingApplied === true ? facts.levelingGainDb : undefined;
    const value = {eqApplied: facts?.eqApplied ?? false, levelingApplied: facts?.levelingApplied ?? false, ...(levelingGainDb !== undefined ? {levelingGainDb} : {})};
    const key = `${snapshot.intentId}:${session.id}:${session.generation}:${value.eqApplied}:${value.levelingApplied}:${levelingGainDb ?? ''}`;
    if (key === this.processingReport) return;
    this.processingReport = key;
    this.listening.audioProcessingApplied({intentId: snapshot.intentId, sessionId: session.id, generation: session.generation}, value);
  }

  transportPosition() { return this.primary ? this.position() : this.snapshot.positionSeconds; }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    clearInterval(this.timer);
    this.reset();
    document.removeEventListener('click', this.gesture);
    document.removeEventListener('keydown', this.gesture);
    this.element.removeEventListener('volumechange', this.volume);
    this.context.removeEventListener('statechange', this.contextChanged);
    document.removeEventListener('visibilitychange', this.interrupted);
    this.context.removeEventListener('sinkchange', this.outputChanged);
    navigator.mediaDevices?.removeEventListener?.('devicechange', this.outputChanged);
    this.carrier.dispose();
    this.equalizer.dispose();
    void this.context.close();
  }
}

/** Let the browser choose its real context output rate; it may still resample in the OS path. */
function createContext(): AudioContext {
  return new AudioContext({latencyHint: 'playback'});
}
