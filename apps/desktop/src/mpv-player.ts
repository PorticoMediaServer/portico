import {withMpvEngine, type CapabilityDocument} from '../../../packages/client-core/src/client-profile.ts';
import type {PlayerEvent, PlayerObservation, PlayerSource, PlayerState} from '../../../packages/client-core/src/playback-v1/port.ts';
import {repeat, type Repeating} from '../../../packages/client-core/src/timer.ts';
import type {PlaybackMediaPort} from '../../../web/src/platform/playback/web-playback.ts';

export type Invoke = <T>(command: string, args?: Record<string, unknown>) => Promise<T>;
type Reading = Readonly<{state: PlayerState | 'idle'; positionMs: number; durationMs: number; rate: number; volume: number; muted: boolean}>;

/** The page's own player plus mpv (src-tauri/src/mpv.rs). Desktop CPUs decode 4K in software;
 * mpv draws HDR and every subtitle kind and picks a file's own tracks. Passthrough waits for a
 * setting, since the app can't tell when sound goes to a receiver. */
export function withDesktopMpv(document: CapabilityDocument): CapabilityDocument {
  return withMpvEngine({...document, client: {...document.client, form: 'desktop', app: 'portico-desktop'}}, {softwareMaxHeight: 2160});
}

/**
 * The second player (web `EngineSwitch`): mpv in its own window, followed by reading its state
 * twice a second while a presentation is loaded. Closing mpv's window reads as paused; Play opens
 * it again where it was.
 */
export class MpvPlayer implements PlaybackMediaPort {
  private source?: PlayerSource;
  private reading: PlayerObservation = {generation: 0, state: 'idle', positionMs: 0, rate: 1};
  private following?: Repeating;
  private seekingTo?: number;
  /** The file's own tracks still to pick for the loaded presentation, once mpv has opened it. */
  private pendingTracks?: PlayerSource['fileTracks'];
  private readonly listeners = new Set<(event: PlayerEvent) => void>();
  loaded = 0;
  settling = false;

  constructor(private readonly invoke: Invoke, private readonly resolveUrl: (path: string) => string) {}

  private emit(event: PlayerEvent) { for (const listener of [...this.listeners]) listener(event); }
  private fail(error: unknown) {
    this.reading = {...this.reading, state: 'error'};
    this.emit({type: 'error', observation: this.reading, code: 'engine_failure', detail: String(error)});
  }
  private send(action: string, value = 0) { void this.invoke('mpv_command', {action, value}).catch(error => this.fail(error)); }

  load(source: PlayerSource): void {
    this.pendingTracks = source.fileTracks && Object.keys(source.fileTracks).length ? source.fileTracks : undefined;
    this.source = source;
    this.reading = {generation: source.generation, state: 'loading', positionMs: source.startPositionMs, rate: 1};
    this.settling = true;
    void this.invoke('mpv_load', {url: this.resolveUrl(source.url), startMs: source.startPositionMs, autoplay: source.autoplay}).then(() => {
      if (this.source !== source) return;
      this.loaded = source.generation;
      this.settling = false;
      // A reading half a second after the last one answered, never two at once.
      this.following ??= repeat(() => this.poll(), {everyMs: 500, immediate: false, backoff: {baseMs: 500, maxMs: 2000, jitter: 'none'}});
    }, error => { if (this.source === source) this.fail(error); });
  }

  private async poll() {
    const source = this.source;
    if (!source) return;
    const now = await this.invoke<Reading>('mpv_observe').catch(() => undefined);
    if (!now || this.source !== source) return;
    // A closed window is paused where it was; everything else is mpv's own reading.
    const state: PlayerState = now.state === 'idle' ? 'paused' : now.state;
    const positionMs = now.state === 'idle' ? this.reading.positionMs : now.positionMs;
    const changed = state !== this.reading.state;
    this.reading = {generation: source.generation, state, positionMs, rate: now.rate, volume: now.volume / 100, muted: now.muted};
    if (this.pendingTracks && now.state !== 'loading' && now.state !== 'idle') {
      const tracks = this.pendingTracks; this.pendingTracks = undefined;
      void this.invoke('mpv_tracks', {audio: tracks.audio ?? null, subtitle: tracks.subtitle ?? null}).catch(error => this.fail(error));
    }
    if (this.seekingTo !== undefined && Math.abs(positionMs - this.seekingTo) < 1500) {
      this.seekingTo = undefined;
      this.emit({type: 'seeked', observation: this.reading});
    }
    this.emit({type: changed ? 'state' : 'time', observation: this.reading});
  }

  play(): void {
    // After its window was closed, mpv opens again at the place it stopped.
    if (this.source && this.reading.state === 'paused' && this.loaded) {
      void this.invoke<Reading>('mpv_observe').then(now => {
        if (now.state === 'idle' && this.source) this.load({...this.source, startPositionMs: this.reading.positionMs, autoplay: true});
        else this.send('play');
      }, error => this.fail(error));
      return;
    }
    this.send('play');
  }
  pause(): void { this.send('pause'); }
  seek(positionMs: number): void { this.seekingTo = positionMs; this.send('seek', positionMs); }
  setRate(rate: number): void { this.send('rate', rate); }
  selectSidecarSubtitle(trackId: string | null): void {
    const url = trackId === null ? null : this.source?.subtitles.find(subtitle => subtitle.trackId === trackId)?.url;
    if (url !== undefined) void this.invoke('mpv_subtitle', {url: url && this.resolveUrl(url)}).catch(error => this.fail(error));
  }
  observe(): PlayerObservation { return this.reading; }
  onEvent(listener: (event: PlayerEvent) => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }
  onNotice(): () => void { return () => {}; }
  unload(): void {
    this.source = undefined;
    this.loaded = 0;
    this.following?.stop();
    this.following = undefined;
    this.send('stop');
  }
  suspend(): void {}
  resume(): void {}
  dispose(): void { this.unload(); this.listeners.clear(); }
}
