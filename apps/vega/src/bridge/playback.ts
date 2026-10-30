import {defaultI18n} from '../../../../packages/i18n/src/index.ts';
import React, {useCallback, useContext, useLayoutEffect, useRef, useState} from 'react';
import {AppState, StyleSheet, View, useKeplerAppStateManager, type LayoutChangeEvent, type ViewProps} from '@amazon-devices/react-native-kepler';
import {KeplerVideoSurfaceView, VideoPlayer} from '@amazon-devices/react-native-w3cmedia';
import {trace} from '../debug';
import {ModuleEvents, failure, withEvents} from './events';
import {AudioRenditionUnavailable, HlsSource, type AudioSelection, type SourceError} from './hls';
import {uuid} from './random';
import {applySubtitles, clearSubtitles, tickSubtitles, type SubtitleCommand} from './subtitles';
import {registerSystemMediaSource, TransportKeys, type SystemMediaRegistration} from './system-media';
import {VegaSurfaceComposition} from './surface-composition';
import {CaptionLabel} from './caption-label';

/**
 * `NativeModules.PorticoPlaybackModule` and the `PorticoPlayer` view, over the SDK's W3C media
 * element. This is the app's one player engine, written to the engine contract every platform's
 * engines keep (`apps/native/Native/ENGINE-CONTRACT.md`): one engine, leased to one owner; driven
 * by commands that each carry the whole desired state and a rising revision; reporting facts that
 * carry the binding they belong to, a rising sequence and the revision of the last command
 * applied. Events: `PorticoPlaybackFact`, `PorticoListeningSnapshot`, `PorticoPictureInPicture`.
 * What it does and does not do is said in the module's `features` (and, for the server, in the
 * capability document, `capability-profile.ts`): the player screen hides the rest.
 *
 * What the media element cannot do (SDK 0.24): change speed (`supportedRates` is `[1]`), open an
 * `http:` address, or play HLS from an address. The server's HLS is played by `hls.ts`, which
 * fetches the segments itself; a file plays from its address when that is HTTPS. There is no
 * Picture in Picture and no AirPlay, and the engine draws plain-text subtitles only (the server
 * burns styled and image subtitles into the picture for this device, or sends a styled track's
 * plain words when "Styled subtitles" is off).
 *
 * Not done here, because the SDK offers no way to: asking the display to match a title's frame
 * rate or dynamic range before it starts (the contract's display matching), and reading the
 * viewer's system caption style.
 *
 * Music and audiobooks play here too. Apple hands them to a native owner (the `listening…`
 * methods) so that they go on while JavaScript sleeps; a Vega app's JavaScript runs while it
 * plays, so the shared queue player keeps them itself and those methods refuse or do nothing
 * (`overrides/native-next/platform/playback/native-listening-kinds.ts`).
 */
export const playbackEvents = new ModuleEvents();

type Binding = Readonly<{lease: string; intentId: number; sessionId: string; generation: number; itemId: string}>;
type PendingSeek = Readonly<{revision: number; positionSeconds: number}>;
type AudioBinding = Readonly<Record<string, unknown>>;
type AudioCommand = Readonly<{binding: AudioBinding; epoch: number; renditions: readonly Readonly<{id: string; name: string}>[]; defaultRenditionId: string; pending: Readonly<{revision: number; renditionId: string}> | null}>;
type Command = Readonly<{
  binding: Binding;
  source: string;
  paused: boolean;
  rate: number;
  commandRevision: number;
  pendingSeek?: PendingSeek | null;
  connectionState?: string;
  routeRevision?: number;
  timelineOriginMs?: number | null;
  audio?: AudioCommand | null;
  subtitles?: SubtitleCommand | null;
}>;

const MAX = 9007199254740991;
const whole = (value: unknown, least: number): value is number => typeof value === 'number' && Number.isFinite(value) && Math.floor(value) === value && value >= least && value <= MAX;
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
/** Attachment scopes are the existing three-field engine contract, not a new playback lease. */
const inBinding = (value: unknown, binding: Binding): boolean => {
  if (!value || typeof value !== 'object') return false;
  const scoped = value as Record<string, unknown>;
  return scoped.intentId === binding.intentId && scoped.sessionId === binding.sessionId && scoped.generation === binding.generation;
};

const PROGRESS_MS = 500, SUBTITLE_MS = 100;
const SEEK_LIMIT_MS = 10000;
// The shared player shows these engine facts using the product catalogue.
const RATE_UNAVAILABLE = defaultI18n.t('playback.engine.rateUnavailable');
const SEEK_UNAVAILABLE = defaultI18n.t('playback.engine.seekUnavailable');
const NETWORK_ERROR = defaultI18n.t('playback.engine.networkInterrupted');
const MEDIA_ERROR = defaultI18n.t('playback.engine.mediaUnavailable');
const ADDRESS_ERROR = defaultI18n.t('playback.engine.invalidAddress');

/**
 * Why playback cannot continue, as the contract's error facts say it (section 5): the server's
 * answer when it gave one, a connection that gave none, the engine refusing the media, or
 * (`uncertain`) something that is not the media's fault.
 */
type Failure = Readonly<{network?: boolean; status?: number; engineCode?: 'decode_error' | 'source_not_supported'; uncertain?: boolean; message?: string}>;

const instanceId = uuid();
let lease: string | undefined, owner: string | undefined;
let commandRevision = 0, factSequence = 0;
let accepted: Command | undefined;
let lastSeek: PendingSeek | undefined;
let engine: Engine | undefined;
/** The place reached when the route to the server was put in doubt: the media is closed until the
 * route is ready again, and opens there. */
let kept: {binding: Binding; position: number} | undefined;
/** The app is not on screen. */
let away = false;
/** The surface the mounted `PorticoPlayer` view draws on. */
let surface: Readonly<{owner: object; lease: string; handle: string}> | null = null;
/** The app's component, which the system addresses transport keys to. */
let component: unknown;

/** One media element playing one source for one binding. */
class Engine {
  readonly id = uuid();
  private readonly player = new VideoPlayer();
  private opened = false;
  private closed = false;
  private ready = false;
  private shown = false;
  private waiting = false;
  private over = false;
  private failed = false;
  private wantPaused = true;
  private attached: string | null = null;
  private seekRevision = -1;
  private seek?: {revision: number; target: number; timer: ReturnType<typeof setTimeout>};
  private restoring?: {target: number; started: boolean; timer: ReturnType<typeof setTimeout>};
  private ticker?: ReturnType<typeof setInterval>;
  private captions?: ReturnType<typeof setInterval>;
  private audioKey = '';
  private audioRevision = 0;
  private audioSequence = 0;
  private audioMap = new Map<string, number>();
  private audioCommand?: AudioCommand;
  private audioMappingValid?: boolean;
  private audioObserved: string | null | undefined;
  private audioRequest?: {key: string; kind: 'select' | 'fallback'; revision: number; renditionId: string;
    selection?: AudioSelection; current: () => boolean; mediaAccepted: boolean; applied: boolean};
  private readonly listeners: [string, () => void][] = [];
  private hls?: HlsSource;
  private keyed = false;
  private transportKeys?: SystemMediaRegistration;
  private described = false;
  private filled = false;
  /** A picture was shown or time advanced: a failure after that is not an incompatibility. */
  private played = false;
  private lastProgress = '';
  private lastPosition = NaN;
  /** The viewer has been told the video was paused because the app left the screen. */
  private toldAway = false;

  /**
   * `start`: where to open (the place reached on another route to the same item), or NaN for the
   * beginning. `originMs`: the channel's origin for a live channel, where positions are seconds on
   * the channel's timeline; undefined for a title.
   */
  constructor(readonly binding: Binding, readonly source: string, private start: number, private readonly originMs?: number) {
    void this.open();
  }

  private on(type: string, listener: () => void): void {
    this.listeners.push([type, listener]);
    const traced = () => { trace('media ' + type); listener(); };
    this.listeners[this.listeners.length - 1][1] = traced;
    this.player.addEventListener(type, traced);
  }

  private async open(): Promise<void> {
    try {
      await this.player.initialize();
    } catch (error) {
      trace(`player initialize failed: ${String((error as Error)?.message ?? error)}`, true);
      this.fail({uncertain: true}, 'initialize');
      return;
    }
    if (this.closed) { void this.player.deinitialize().catch(() => {}); return; }
    this.opened = true;
    this.on('loadedmetadata', () => { this.described = true; this.loaded(); });
    this.on('playing', () => { this.waiting = false; this.played = true; if (!this.shown) { this.shown = true; this.publish('firstFrame'); } this.publish('rateStatus'); });
    this.on('pause', () => this.publish('rateStatus'));
    // Running dry while playing is a stall; waiting for a seek or for the first picture is not.
    this.on('waiting', () => { this.waiting = true; if (this.shown && !this.seek) this.publish('stalled'); });
    this.on('seeked', () => { if (this.restoring) { if (this.restoring.started) this.restored(true); } else this.sought(true); });
    this.on('ended', () => { this.over = true; this.publish('ended'); });
    this.on('error', () => {
      const code = Number((this.player.error as {code?: number} | null)?.code);
      // 2 is the network; 3 a decode failure; 4 a source the element will not take.
      this.fail(code === 2 ? {network: true} : code === 3 ? {engineCode: 'decode_error'} : code === 4 ? {engineCode: 'source_not_supported'} : {uncertain: true}, `media error ${code}`);
    });
    this.player.autoplay = false;
    this.attach();
    this.takeKeys();
    if (!/^https?:\/\/[^/?#@\s]+([/?#]|$)/i.test(this.source)) {
      this.fail({uncertain: true, message: ADDRESS_ERROR}, 'address');
      return;
    }
    if (/\.m3u8(\?|#|$)/i.test(this.source)) {
      this.hls = new HlsSource(this.player, this.source, error => this.failSource(error), this.originMs);
      // Filled from where playback will start, not from the beginning.
      const first = accepted && engine === this ? accepted.pendingSeek?.positionSeconds : undefined;
      void this.hls.open(first ?? (this.originMs === undefined && !Number.isFinite(this.start) ? 0 : this.start)).then(() => { this.filled = true; this.loaded(); });
    } else {
      // The element cannot report a direct file's final address. The capability document asks
      // for HLS, including a plain remux, so all authenticated responses stay observable.
      this.fail({engineCode: 'source_not_supported'}, 'media requires an observable stream');
      return;
    }
    this.ticker = setInterval(() => this.tick(), PROGRESS_MS);
    this.captions = setInterval(() => { if (this.ready && engine === this) { const position = this.position(); if (Number.isFinite(position)) tickSubtitles(position); } }, SUBTITLE_MS);
  }

  /** Progress: twice a second while the position moves, and once when it stops. A paused or
   * waiting picture is not reported again and again. */
  private tick(): void {
    if (!this.ready || this.over || this.failed) return;
    this.sampleAudio();
    const position = this.position(), paused = this.player.paused;
    const moving = !paused && !this.waiting;
    if (moving && Number.isFinite(position) && Number.isFinite(this.lastPosition) && position > this.lastPosition + 0.05) this.played = true;
    this.lastPosition = position;
    const signature = `${Number.isFinite(position) ? Math.floor(position * 2) : -1}/${paused}/${this.waiting}`;
    if (!moving && signature === this.lastProgress) return;
    this.lastProgress = signature;
    this.publish('progress', this.connection());
  }

  /**
   * What was measured of the connection and how much is held ahead of the picture, for Automatic
   * quality and the session's timeline. For a title streamed from the server only, never a
   * channel. The speed of the connection is known where the app fetches the stream itself; a file
   * the media element opens from its address is fetched out of sight, so only its buffer is said.
   */
  private connection(): Readonly<Record<string, unknown>> {
    if (this.originMs !== undefined) return {};
    if (this.hls) {
      const throughputKbps = this.hls.throughputKbps();
      return {...(throughputKbps !== undefined ? {throughputKbps} : {}), bufferedSeconds: this.hls.bufferedSeconds()};
    }
    try {
      const ranges = this.player.buffered, now = Number(this.player.currentTime);
      for (let index = 0; index < ranges.length; index++) if (ranges.start(index) <= now + 0.1 && ranges.end(index) > now) return {bufferedSeconds: Math.max(0, ranges.end(index) - now)};
    } catch { /* not readable yet */ }
    return {};
  }

  /** The remote's transport keys and voice requests go to the shared player, not to the element. */
  private takeKeys(): void {
    if (this.keyed || !component || !this.opened || this.closed) return;
    this.keyed = true;
    this.transportKeys = registerSystemMediaSource({instanceId, itemInstanceId: this.id, binding: this.binding},
      () => !this.closed && engine === this && lease === this.binding.lease && !!accepted && same(accepted.binding, this.binding));
    void this.player.setMediaControlFocus(component as never, new TransportKeys(this.transportKeys)).catch(error => trace(`media control focus: ${String((error as Error)?.message ?? error)}`, true));
  }

  /** Gives the element the view's surface, or takes it away. */
  attach(): void {
    this.takeKeys();
    const wanted = surface?.lease === this.binding.lease ? surface.handle : null;
    if (!this.opened || this.closed || this.attached === wanted) return;
    try {
      if (this.attached) this.player.clearSurfaceHandle(this.attached);
      if (wanted) this.player.setSurfaceHandle(wanted);
      this.attached = wanted;
    } catch (error) {
      trace(`surface: ${String((error as Error)?.message ?? error)}`, true);
    }
  }

  /** Ready once the element knows what it is playing and the stream has its first data. */
  private loaded(): void {
    if (this.closed || this.ready || this.failed || !this.described || !this.filled) return;
    if (Number.isFinite(this.start) && !accepted?.pendingSeek) { this.restorePosition(this.start); return; }
    if (this.originMs !== undefined) {
      // A channel was filled from where it is to begin (the place kept, the place asked for, or
      // where live viewing begins): the element is sent to that segment.
      const begin = this.hls?.beginning() ?? NaN;
      if (Number.isFinite(begin) && Math.abs(Number(this.player.currentTime) - begin) > 0.25) { try { this.player.currentTime = begin; } catch { /* plays from where the buffer starts */ } }
    }
    this.completeReady();
  }

  private completeReady(): void {
    if (this.closed || this.failed || this.ready) return;
    this.ready = true;
    this.publish('ready');
    if (accepted && engine === this) this.apply(accepted);
    // Nothing sent the element anywhere: it plays from where the stream was filled.
    if (!this.seek) this.hls?.settled();
  }

  private restorePosition(target: number): void {
    this.restoring = {target, started: false, timer: setTimeout(() => this.restored(false, true), SEEK_LIMIT_MS)};
    const go = (elementTime: number) => {
      if (!this.restoring || this.closed || this.failed) return;
      if (!Number.isFinite(elementTime)) { this.restored(false); return; }
      this.restoring.started = true;
      if (Math.abs(Number(this.player.currentTime) - elementTime) < 0.25) { this.restored(true); return; }
      try { this.player.currentTime = elementTime; } catch { this.restored(false); }
    };
    const destination = this.hls ? this.hls.seek(target) : target;
    if (typeof destination === 'number') go(destination); else destination.then(go, () => this.restored(false));
  }

  private restored(applied: boolean, timedOut = false): void {
    const held = this.restoring;
    if (!held || this.closed) return;
    clearTimeout(held.timer); this.restoring = undefined;
    if (!applied || !Number.isFinite(this.position()) || Math.abs(this.position() - held.target) > 0.5) {
      this.fail({uncertain: true, message: timedOut ? defaultI18n.t('playback.engine.restoredPositionUnavailable') : defaultI18n.t('playback.engine.previousPositionUnavailable')}, 'position restoration');
      return;
    }
    this.completeReady();
  }

  apply(command: Command): void {
    this.wantPaused = command.paused;
    if (this.restoring && command.pendingSeek) {
      clearTimeout(this.restoring.timer); this.restoring = undefined; this.completeReady();
    }
    if (!this.ready || this.closed || this.failed) return;
    const pending = command.pendingSeek;
    if (pending && pending.revision > this.seekRevision) {
      this.seekRevision = pending.revision;
      if (this.seek) clearTimeout(this.seek.timer);
      this.seek = {revision: pending.revision, target: pending.positionSeconds, timer: setTimeout(() => this.sought(false), SEEK_LIMIT_MS)};
      this.over = false;
      // A channel's timeline has no beginning at zero; a title's does.
      const position = this.originMs === undefined ? Math.max(0, pending.positionSeconds) : pending.positionSeconds;
      const go = (elementTime: number) => {
        if (this.seek?.revision !== pending.revision || this.closed || this.failed) return;
        if (!Number.isFinite(elementTime)) { this.sought(false); return; }
        // The element says nothing when it is sent to where it already is.
        if (Math.abs(Number(this.player.currentTime) - elementTime) < 0.25) this.sought(true);
        else { try { this.player.currentTime = elementTime; } catch { this.sought(false); } }
      };
      // A title's place in the element is known at once; a channel's once its segment is in hand.
      const target = this.hls ? this.hls.seek(position) : position;
      if (typeof target === 'number') go(target);
      else target.then(go, () => { if (this.seek?.revision === pending.revision) this.sought(false); });
    }
    if (this.closed || this.failed) return;
    const subtitles = inBinding(command.subtitles?.binding, command.binding) ? command.subtitles : null;
    applySubtitles(subtitles, this.source, error => this.publish('subtitleStatus', {error}));
    this.applyAudio(inBinding(command.audio?.binding, command.binding) ? command.audio : null);
    const held = this.heldAway();
    if (this.wantPaused || held || command.rate !== 1 || !!pending || !!this.seek) { if (!this.player.paused) this.player.pause(); }
    else if (this.player.paused && !this.over) void this.player.play().catch(() => this.fail({uncertain: true}, 'play'));
  }

  /**
   * The player's audio choice. The server's renditions are known to the player by the names in
   * the playlist; which of them this stream has is told once (`audioMapped`), then what is
   * playing (`audioObserved`), and each change asked for is answered (`audioApplied` or
   * `audioFailed`).
   */
  private retireAudioRequest(): void {
    const request = this.audioRequest;
    this.audioRequest = undefined; // revoke the original owner before abort callbacks can run
    request?.selection?.cancel();
  }

  private sampleAudio(force = false): void {
    const audio = this.audioCommand, hls = this.hls;
    if (!audio || !hls || !accepted || !inBinding(accepted.audio?.binding, this.binding)
      || JSON.stringify([accepted.audio?.binding, accepted.audio?.epoch]) !== this.audioKey) return;
    const observed = [...this.audioMap].find(([, index]) => index === hls.audioIndex())?.[0] ?? null;
    const stamp = {audioBinding: audio.binding, audioEpoch: audio.epoch};
    if (force || this.audioObserved !== observed) {
      this.audioObserved = observed;
      this.publish('audioObserved', {...stamp, audioSequence: ++this.audioSequence, renditionId: observed});
    }
    const request = this.audioRequest;
    if (request?.kind === 'select' && request.mediaAccepted && !request.applied && request.current() && observed === request.renditionId) {
      request.applied = true;
      // The current controller admits Applied only after the matching Observed fact.
      this.publish('audioApplied', {...stamp, audioRevision: request.revision, renditionId: observed});
    }
  }

  private startAudio(audio: AudioCommand, index: number, kind: 'select' | 'fallback', revision: number, renditionId: string): void {
    this.retireAudioRequest();
    const hls = this.hls;
    if (!hls || this.failed || this.closed) return;
    const key = this.audioKey, binding = Object.freeze({...this.binding});
    audio = Object.freeze({...audio, binding: Object.freeze({...audio.binding}), renditions: Object.freeze(audio.renditions.map(value => Object.freeze({...value}))), pending: audio.pending ? Object.freeze({...audio.pending}) : null});
    const sourceCurrent = () => !this.closed && !this.failed && engine === this && this.hls === hls && lease === binding.lease && !!accepted && same(accepted.binding, binding);
    const request = {key, kind, revision, renditionId, mediaAccepted: false, applied: false, selection: undefined as AudioSelection | undefined,
      current: () => sourceCurrent() && this.audioRequest === request && this.audioKey === key
        && JSON.stringify([accepted?.audio?.binding, accepted?.audio?.epoch]) === key
        && (kind === 'select' ? accepted?.audio?.pending?.revision === revision && accepted.audio.pending.renditionId === renditionId
          : !accepted?.audio?.pending || accepted.audio.pending.revision <= revision)};
    this.audioRequest = request;
    request.selection = hls.selectAudio(index, {current: request.current, sourceCurrent, willMutate: () => {
      if (!request.current()) return;
      this.audioObserved = null;
      this.publish('audioObserved', {audioBinding: audio.binding, audioEpoch: audio.epoch, audioSequence: ++this.audioSequence, renditionId: null});
    }});
    void request.selection.done.then(() => {
      if (!request.current()) return;
      request.mediaAccepted = true;
      this.sampleAudio(true);
      if (kind === 'fallback' && this.audioRequest === request) this.retireAudioRequest();
    }, error => {
      if (!request.current()) return;
      this.retireAudioRequest();
      if (!sourceCurrent() || this.audioKey !== key) return;
      if (kind === 'select' && error instanceof AudioRenditionUnavailable) {
        this.publish('audioUnavailable', {audioBinding: audio.binding, audioEpoch: audio.epoch, renditionId});
        // Only a known default in this exact all-or-none map may start a NEW recovery operation.
        if (!sourceCurrent() || this.audioKey !== key || JSON.stringify([accepted?.audio?.binding, accepted?.audio?.epoch]) !== key) return;
        const nextPending = accepted?.audio?.pending;
        if (this.audioRequest || nextPending && (nextPending.revision !== revision || nextPending.renditionId !== renditionId)) return;
        const fallback = this.audioMap.get(audio.defaultRenditionId);
        if (fallback !== undefined && audio.defaultRenditionId !== renditionId) this.startAudio(audio, fallback, 'fallback', revision, audio.defaultRenditionId);
        else this.sampleAudio(true); // no usable default is not proof that the working source stopped
      } else {
        if (kind === 'select') this.publish('audioFailed', {audioBinding: audio.binding, audioEpoch: audio.epoch, audioRevision: revision});
        this.sampleAudio(true); // preparation failure leaves only the actually committed source observable
      }
    });
  }

  private applyAudio(audio: AudioCommand | null | undefined): void {
    const hls = this.hls;
    if (!audio || !hls || !Array.isArray(audio.renditions)) {
      this.retireAudioRequest();
      this.audioKey = ''; this.audioCommand = undefined; this.audioMap.clear(); this.audioObserved = undefined; this.audioMappingValid = undefined;
      return;
    }
    const key = JSON.stringify([audio.binding, audio.epoch]), pending = audio.pending, prior = this.audioRequest;
    if (prior && (prior.key !== key || (prior.kind === 'select' ? !pending || pending.revision !== prior.revision || pending.renditionId !== prior.renditionId
      : !!pending && pending.revision > prior.revision))) this.retireAudioRequest();
    if (this.failed || this.closed || !this.filled) return; // initial HLS IO keeps its generation until ready
    const stamp = {audioBinding: audio.binding, audioEpoch: audio.epoch};
    const offered = hls.audio(), mapped = new Map<string, number>(), used = new Set<number>();
    let valid = audio.renditions.length > 0 && audio.renditions.length <= 16 && audio.renditions.length === offered.length;
    for (const rendition of audio.renditions) {
      if (!rendition || typeof rendition.id !== 'string' || !rendition.id || mapped.has(rendition.id) || typeof rendition.name !== 'string' || !rendition.name) {valid = false; break;}
      const matches = offered.map((candidate, index) => candidate.name === rendition.name ? index : -1).filter(index => index >= 0);
      if (matches.length !== 1 || used.has(matches[0])) {valid = false; break;}
      mapped.set(rendition.id, matches[0]); used.add(matches[0]);
    }
    if (!mapped.has(audio.defaultRenditionId)) valid = false;
    if (!valid) {this.retireAudioRequest(); mapped.clear(); if (this.failed || this.closed) return;}
    const remapped = key !== this.audioKey || valid !== this.audioMappingValid || JSON.stringify([...mapped]) !== JSON.stringify([...this.audioMap]);
    const changed = key !== this.audioKey;
    this.audioCommand = audio; this.audioMap = mapped; this.audioMappingValid = valid;
    if (changed) {
      this.retireAudioRequest();
      if (this.failed || this.closed) return;
      this.audioKey = key; this.audioRevision = 0; this.audioSequence = 0; this.audioObserved = undefined;
    }
    if (remapped) this.publish('audioMapped', {...stamp, renditionIds: [...mapped.keys()], ...(!valid ? {audioMappingFailure: 'unsupported_metadata'} : {})});
    this.sampleAudio();
    if (!pending || pending.revision <= this.audioRevision) return;
    this.audioRevision = pending.revision;
    const index = mapped.get(pending.renditionId);
    if (index === undefined) {this.publish('audioFailed', {...stamp, audioRevision: pending.revision}); return;}
    this.startAudio(audio, index, 'select', pending.revision, pending.renditionId);
  }

  /** Whether what is playing has a picture (music and audiobooks do not). */
  private pictured(): boolean {
    if (this.hls) return this.hls.hasPicture();
    try { return Number((this.player as {videoWidth?: number}).videoWidth) > 0; } catch { return true; }
  }

  /**
   * Leaving the app: a video cannot go on being drawn while the app is not on screen, so it stays
   * paused until the app is back, whatever is wanted, and the engine says so once when the app
   * wanted it playing: the shared player then shows Play, and one press continues. Music and
   * audiobooks have no picture and play on.
   */
  private heldAway(): boolean {
    if (!away || !this.pictured()) { this.toldAway = false; return false; }
    if (!this.wantPaused && !this.toldAway) { this.toldAway = true; this.publish('systemPaused', {paused: true, reason: 'background'}); }
    return true;
  }

  /** The app left the screen or came back: what is wanted is applied again in that light. */
  refresh(): void {
    if (this.ready && !this.closed && !this.failed && accepted && engine === this) this.apply(accepted);
  }

  private sought(applied: boolean): void {
    const seek = this.seek;
    if (!seek || this.closed) return;
    clearTimeout(seek.timer);
    this.seek = undefined;
    this.hls?.settled();
    applied = applied && Number.isFinite(this.position()) && Math.abs(this.position() - seek.target) <= 0.5;
    this.publish(applied ? 'seekApplied' : 'seekFailed', {seekRevision: seek.revision, error: applied ? '' : SEEK_UNAVAILABLE});
  }

  /**
   * The media cannot continue. The fact says which of four things it was, in the contract's
   * order: the server answered with a refusal (`httpStatus`, `deliveryError`, and `networkError`
   * for an answer waiting may change); the connection gave no answer (`networkError`); the engine
   * refused the media (`engineCode`); or something else that is not the media's fault
   * (`uncertain`). The shared player decides what to do from these, never from the words.
   */
  private fail(failure: Failure, detail: string): void {
    if (this.closed || this.failed) return;
    this.failed = true;
    this.retireAudioRequest();
    this.hls?.close(); clearSubtitles();
    if (this.ticker) clearInterval(this.ticker); this.ticker = undefined;
    if (this.captions) clearInterval(this.captions); this.captions = undefined;
    if (this.seek) clearTimeout(this.seek.timer); this.seek = undefined;
    if (this.restoring) clearTimeout(this.restoring.timer); this.restoring = undefined;
    try { this.player.pause(); } catch { /* never started */ }
    const status = failure.status !== undefined && failure.status >= 400 && failure.status <= 599 ? failure.status : 0;
    const network = status ? status === 408 || status === 429 || status >= 500 : failure.network === true;
    const engineCode = status || network ? '' : failure.engineCode ?? '';
    this.publish('error', {
      paused: true, networkError: network, deliveryError: status > 0, httpStatus: status, hasPlayed: this.played, uncertain: !status && !network && !engineCode && failure.uncertain !== false,
      engineCode, errorDetail: detail.slice(0, 200), error: failure.message ?? (network ? NETWORK_ERROR : MEDIA_ERROR),
    });
  }

  /** The stream stopped (`hls.ts`): what the server answered, a lost connection, or bytes the
   * media element would not take. */
  private failSource(error: SourceError): void {
    this.fail({
      network: error.network, status: error.status,
      ...(error.refused ? {engineCode: error.refused === 'decode' ? 'decode_error' as const : 'source_not_supported' as const} : {}),
    }, error.message);
  }

  /** The element's clock for a position in the item (they differ for a stream: see `hls.ts`). */
  private elementTime(position: number): number { return this.hls ? this.hls.elementTime(position) : position; }

  /** Where playback is: seconds into the item, or on the channel's timeline. NaN while a
   * channel's place is not known yet. */
  position(): number {
    if (!this.opened || (this.hls && !this.filled)) return Number.isFinite(this.start) ? this.start : this.originMs === undefined ? 0 : NaN;
    const position = this.hls ? this.hls.position() : Number(this.player.currentTime);
    return Number.isFinite(position) ? position : this.originMs === undefined ? 0 : NaN;
  }

  private publish(type: string, extra: Readonly<Record<string, unknown>> = {}): void {
    if (this.closed || engine !== this || !lease || !accepted || ((this.failed || this.restoring) && type !== 'error')) return;
    const paused = this.opened ? this.player.paused : true;
    const moving = this.ready && !paused && !this.waiting && !this.over && !this.failed;
    const place = this.position();
    // A channel's place is not known until its first segment has landed: nothing is said of it.
    if (!Number.isFinite(place) && (type === 'progress' || type === 'ended')) return;
    const position = Number.isFinite(place) ? place : 0;
    if (type !== 'progress' || factSequence % 4 === 0) trace(`fact ${type} at ${position.toFixed(2)} paused=${paused} moving=${moving} seek=${!!this.seek} ${JSON.stringify(extra).slice(0, 160)}`);
    playbackEvents.emit('PorticoPlaybackFact', {
      instanceId, type, intentId: this.binding.intentId, position, paused: paused && !this.seek, error: '',
      effectiveRate: moving ? 1 : 0, supportedRates: this.ready ? [1] : [], rateReady: this.ready, rateWaiting: this.waiting && !paused,
      rateError: this.ready && accepted.rate !== 1 ? RATE_UNAVAILABLE : '', monotonicSeconds: Date.now() / 1000,
      ...extra,
      itemInstanceId: this.id, binding: this.binding, factSequence: ++factSequence, lastCommandRevision: commandRevision,
      seeking: !!this.seek || !!accepted.pendingSeek,
    });
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.retireAudioRequest();
    this.transportKeys?.retire(); this.transportKeys = undefined;
    if (this.ticker) clearInterval(this.ticker);
    if (this.captions) clearInterval(this.captions);
    clearSubtitles();
    if (this.seek) clearTimeout(this.seek.timer);
    if (this.restoring) clearTimeout(this.restoring.timer);
    this.restoring = undefined;
    this.hls?.close();
    if (!this.opened) return;
    for (const [type, listener] of this.listeners) this.player.removeEventListener(type, listener);
    try { this.player.pause(); } catch { /* not playing */ }
    try { if (this.attached) this.player.clearSurfaceHandle(this.attached); } catch { /* the view went first */ }
    void this.player.deinitialize().catch(() => {});
  }
}

function acquire(name: string): Promise<{lease: string; instanceId: string; engine: 'native'}> {
  if (typeof name !== 'string' || name.length < 1 || name.length > 128 || (lease && owner !== name)) return Promise.reject(failure('owner_busy', 'Another player owns playback.'));
  if (!lease) { owner = name; lease = uuid(); commandRevision = 0; factSequence = 0; accepted = undefined; lastSeek = undefined; kept = undefined; }
  return Promise.resolve({lease, instanceId, engine: 'native'});
}

/** The Apple controller's checks, in its order and with its words. */
function refusal(command: Command): string | null {
  const binding = command?.binding;
  if (!binding || typeof binding !== 'object') return 'Invalid playback binding.';
  if (binding.lease !== lease || !lease) return 'Playback lease is no longer active.';
  if (typeof command.paused !== 'boolean') return 'Invalid paused flag.';
  if (typeof command.source !== 'string' || typeof command.rate !== 'number' || !Number.isFinite(command.rate)) return 'Invalid playback command.';
  if (!whole(binding.intentId, 1) || !whole(binding.generation, 1)) return 'Invalid playback binding.';
  if (!whole(command.commandRevision, 1)) return 'Invalid playback revision.';
  if (command.commandRevision < commandRevision) return 'Stale playback command.';
  if (command.commandRevision === commandRevision) return same(accepted, command) ? null : 'Conflicting playback command.';
  if (typeof binding.sessionId !== 'string' || typeof binding.itemId !== 'string') return 'Invalid playback binding.';
  if (command.timelineOriginMs !== undefined && command.timelineOriginMs !== null && !whole(command.timelineOriginMs, 1)) return 'Invalid channel clock.';
  if (command.connectionState !== undefined && (!['ready', 'verifying'].includes(command.connectionState) || !whole(command.routeRevision, 0))) return 'Invalid route transition.';
  if (accepted?.routeRevision !== undefined && (command.routeRevision === undefined || command.routeRevision < accepted.routeRevision)) return 'Stale route transition.';
  if (accepted) {
    const prior = accepted.binding;
    if (binding.intentId < prior.intentId || (binding.intentId === prior.intentId && (!same(binding, prior) || !sameSource(command, accepted)) && !replannedInPlace(prior, binding))) return 'Stale playback binding.';
  }
  const pending = command.pendingSeek;
  if (pending !== undefined && pending !== null) {
    if (typeof pending !== 'object' || typeof pending.revision !== 'number' || !Number.isFinite(pending.revision) || pending.revision < 0 || typeof pending.positionSeconds !== 'number' || !Number.isFinite(pending.positionSeconds) || (command.timelineOriginMs == null && pending.positionSeconds < 0)) return 'Invalid seek command.';
    if (!whole(pending.revision, 1)) return 'Invalid seek revision.';
    if (accepted && same(binding, accepted.binding) && lastSeek) {
      if (pending.revision < lastSeek.revision) return 'Stale seek command.';
      if (pending.revision === lastSeek.revision && (!accepted.pendingSeek || pending.positionSeconds !== lastSeek.positionSeconds)) return 'Conflicting or retired seek command.';
    }
  }
  return null;
}

/**
 * A newer presentation of the same playback: the same lease, intent, session and title, a later
 * generation. The server planned the session again in place (a quality, version, audio or
 * subtitle change), so the command is the current one and its source is opened.
 */
function replannedInPlace(prior: Command['binding'], next: Command['binding']): boolean {
  return whole(next.generation, 1) && whole(prior.generation, 1) && next.generation > prior.generation && next.intentId === prior.intentId &&
    next.lease === prior.lease && typeof next.sessionId === 'string' && next.sessionId === prior.sessionId && typeof next.itemId === 'string' && next.itemId === prior.itemId;
}

/** Only a newer verified route may move an unchanged binding to the same media path/query. */
function sameSource(next: Command, previous: Command): boolean {
  if (next.source === previous.source) return true;
  if ((next.routeRevision ?? 0) <= (previous.routeRevision ?? 0)) return false;
  try {
    const a = new URL(next.source), b = new URL(previous.source);
    return ['http:', 'https:'].includes(a.protocol) && ['http:', 'https:'].includes(b.protocol) && a.pathname === b.pathname && a.search === b.search;
  } catch { return false; }
}

function command(value: Command): Promise<boolean> {
  const problem = refusal(value);
  trace(`command r${value?.commandRevision} paused=${value?.paused} seek=${JSON.stringify(value?.pendingSeek ?? null)} subtitles=${value?.subtitles?.mode ?? 'none'} audio=${value?.audio ? `${value.audio.renditions?.length} renditions, pending ${JSON.stringify(value.audio.pending)}` : 'none'}${problem ? ' refused: ' + problem : ''}`, !!problem);
  if (problem) return Promise.reject(failure('command_rejected', problem));
  if (value.commandRevision === commandRevision) return Promise.resolve(true);
  if (!accepted || !same(accepted.binding, value.binding)) lastSeek = undefined;
  if (value.pendingSeek) lastSeek = {...value.pendingSeek};
  commandRevision = value.commandRevision;
  accepted = value;
  if (kept && !same(kept.binding, value.binding)) kept = undefined;
  if (value.connectionState === 'verifying') {
    // The route to the server is in doubt. The place reached is kept, the media is closed, and
    // nothing is said until a command says the route is ready: the media then opens there.
    if (engine && same(engine.binding, value.binding)) {
      const position = engine.position();
      if (Number.isFinite(position)) kept = {binding: value.binding, position};
    }
    engine?.close();
    engine = undefined;
    return Promise.resolve(true);
  }
  if (!engine || !same(engine.binding, value.binding) || engine.source !== value.source) {
    // The same binding with another address is another route to the same item: it carries on.
    const carried = value.pendingSeek ? NaN : engine && same(engine.binding, value.binding) ? engine.position() : kept ? kept.position : NaN;
    kept = undefined;
    engine?.close();
    const origin = whole(value.timelineOriginMs, 1) ? value.timelineOriginMs : undefined;
    engine = value.source ? new Engine(value.binding, value.source, carried, origin) : undefined;
  }
  engine?.apply(value);
  return Promise.resolve(true);
}
function release(held: string): Promise<boolean> {
  if (held && held === lease) {
    engine?.close();
    engine = undefined;
    lease = undefined;
    owner = undefined;
    accepted = undefined;
    lastSeek = undefined;
    kept = undefined;
  }
  return Promise.resolve(true);
}

const unavailable = () => Promise.reject(failure('listening_unavailable', 'This device has no separate listening owner.'));

// Leaving the app and coming back (the contract's "System behaviour every engine keeps"). Only
// the app going to the background counts: something of the system's drawn over it does not.
try {
  AppState.addEventListener('change', state => {
    if (state !== 'background' && state !== 'active') return;
    const now = state === 'background';
    if (now === away) return;
    away = now;
    engine?.refresh();
  });
} catch { /* without the app's state a video simply plays on, as before */ }

/**
 * What this engine does and does not do, as every engine's module says it (the contract's
 * constants). The player screen reads these: it offers Picture in Picture and AirPlay only where
 * the engine in use has them, and asks the server to burn in any subtitle kind not listed.
 * (`musicSessions` is about a separate listening owner, which this device does not have: music
 * and audiobooks play here as ordinary sessions.)
 */
const features = Object.freeze({
  pictureInPicture: false,
  airPlayVideo: false,
  liveChannels: true,
  musicSessions: false,
  audioTrackSwitching: false,
  subtitleRendering: Object.freeze(['text']),
  chapters: false,
});

export const PorticoPlaybackModule = withEvents({
  engine: 'native',
  available: true,
  features,
  acquire,
  command,
  release,
  adoptListeningSession: unavailable,
  listeningSessionUpdated: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  purgeAudioCache: (): Promise<void> => Promise.resolve(),
  listeningAdvance: unavailable,
  listeningRoute: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  listeningAction: unavailable,
  listeningSnapshot: (..._args: unknown[]): Promise<null> => Promise.resolve(null),
  acknowledgeListening: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  listeningPolicy: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  listeningDeadlines: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  listeningCredentials: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  retireListening: (..._args: unknown[]): Promise<void> => Promise.resolve(),
  listeningMetadata: (..._args: unknown[]): void => {},
  listeningSkipIntervals: (_back: number, _forward: number): void => {},
}, playbackEvents);

/**
 * The surface the picture is drawn on. As on Apple, the view's lifetime and the lease's are
 * separate: the engine plays without a surface, and takes whichever one is mounted.
 */
/** One actual native surface mount; its callbacks retain this immutable lease and owner. */
function LeaseSurface({heldLease}: {heldLease: string}): React.ReactElement {
  const owner = useRef<object>({}).current;
  const live = useRef(true);
  const detach = useCallback(() => {
    if (surface?.owner !== owner) return;
    surface = null; engine?.attach();
  }, [owner]);
  useLayoutEffect(() => {
    live.current = true;
    return () => {live.current = false; detach();};
  }, [detach]);
  const created = useCallback((handle: string) => {
    if (!live.current || !heldLease || heldLease !== lease) return;
    surface = {owner, lease: heldLease, handle}; engine?.attach();
  }, [heldLease, owner]);
  return React.createElement(KeplerVideoSurfaceView, {style: StyleSheet.absoluteFill, onSurfaceViewCreated: created, onSurfaceViewDestroyed: detach});
}

export function PorticoPlayer({lease: viewLease, surfaceActive = true, pictureInPictureRequest: _request, pictureInPictureBinding: _pictureBinding, children: _children, onLayout, ...view}: ViewProps & {lease: string; surfaceActive?: boolean; pictureInPictureRequest?: number; pictureInPictureBinding?: unknown}): React.ReactElement {
  const app = useKeplerAppStateManager();
  component ??= app.getComponentInstance();
  const composition = useContext(VegaSurfaceComposition);
  const active = surfaceActive && (composition === undefined || composition && StyleSheet.flatten(view.style)?.display !== 'none');
  const [size, setSize] = useState({width: 0, height: 0});
  const laidOut = useCallback((event: LayoutChangeEvent) => { const {width, height} = event.nativeEvent.layout; setSize({width, height}); onLayout?.(event); }, [onLayout]);
  return React.createElement(View, {...view, onLayout: laidOut},
    active && viewLease ? React.createElement(LeaseSurface, {key: viewLease, heldLease: viewLease}) : null,
    active ? React.createElement(CaptionLabel, size) : null);
}
