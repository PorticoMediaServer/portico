import type {PlaybackFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackPresentation} from './types.ts';

export type MediaSessionActionName = 'play' | 'pause' | 'seekbackward' | 'seekforward' | 'seekto' | 'previoustrack' | 'nexttrack' | 'stop';

const ACTIONS: readonly MediaSessionActionName[] = ['play', 'pause', 'seekbackward', 'seekforward', 'seekto', 'previoustrack', 'nexttrack', 'stop'];

export type MediaSessionLike = {
  metadata: MediaMetadata | null;
  playbackState: MediaSessionPlaybackState;
  setActionHandler(action: MediaSessionActionName, handler: MediaSessionActionHandler | null): void;
  setPositionState?(state?: MediaPositionState): void;
};

export type MediaSessionActions = Readonly<{
  play(fence: PlaybackFence): void | Promise<void>;
  pause(fence: PlaybackFence): void | Promise<void>;
  replay(fence: PlaybackFence): void | Promise<void>;
  seek(fence: PlaybackFence, seconds: number): void | Promise<void>;
  skip(fence: PlaybackFence, deltaSeconds: number): void | Promise<void>;
  previous(fence: PlaybackFence): void | Promise<void>;
  next(fence: PlaybackFence): void | Promise<void>;
  stop(fence: PlaybackFence): void | Promise<void>;
}>;

export type RuntimeMediaSessionOptions = Readonly<{
  actions: MediaSessionActions;
  resolveArtwork?: (path: string) => string;
  session?: MediaSessionLike | null;
  createMetadata?: (init: MediaMetadataInit) => MediaMetadata;
  now?: () => number;
  seekStepSeconds?: number;
}>;

function defaultSession(): MediaSessionLike | null {
  if (typeof navigator === 'undefined' || !('mediaSession' in navigator) || !navigator.mediaSession) return null;
  return navigator.mediaSession as unknown as MediaSessionLike;
}

function defaultMetadata(init: MediaMetadataInit): MediaMetadata {
  if (typeof MediaMetadata === 'function') return new MediaMetadata(init);
  return init as unknown as MediaMetadata;
}

function describeMetadata(presentation: WebPlaybackPresentation, resolveArtwork?: (path: string) => string): Readonly<{key: string; init: MediaMetadataInit}> {
  const subject = presentation.subject;
  let title: string, artist = '', album = '';
  if (subject.kind === 'channel') {
    title = subject.programmeTitle || subject.channelName;
    artist = subject.programmeTitle ? subject.channelName : '';
  } else {
    const item = subject.item;
    title = item.title;
    artist = item.show?.title ?? item.artist ?? item.author ?? '';
    album = item.album ?? '';
  }
  const rawArt = presentation.posterUrl ?? presentation.backdropUrl;
  let artworkUrl = '';
  if (rawArt) {
    try { artworkUrl = resolveArtwork ? resolveArtwork(rawArt) : rawArt; }
    catch { artworkUrl = ''; }
  }
  const artwork: MediaImage[] = artworkUrl ? [{src: artworkUrl}] : [];
  const key = JSON.stringify([title, artist, album, artworkUrl]);
  return {key, init: {title, artist, album, artwork}};
}

/** Projects the runtime's current presentation into `navigator.mediaSession` for OS media keys,
 * headset controls, and lock-screen metadata, clearing all handlers on stop/retire/dispose. */
export class RuntimeMediaSession {
  private readonly options: RuntimeMediaSessionOptions;
  private metadataKey?: string;
  private activeFence?: PlaybackFence;
  private activeControls?: readonly WebPlaybackPresentation['controls'][number][];
  private activeFinished = false;
  private boundHandlers = false;
  private lastPosition?: Readonly<{duration: number; rate: number; position: number; playing: boolean; at: number}>;

  constructor(options: RuntimeMediaSessionOptions) {
    this.options = options;
  }

  private session(): MediaSessionLike | null {
    return this.options.session !== undefined ? this.options.session : defaultSession();
  }

  private setHandler(session: MediaSessionLike, action: MediaSessionActionName, handler: MediaSessionActionHandler | null): void {
    try { session.setActionHandler(action, handler); }
    catch { /* Browser does not support this MediaSessionAction. */ }
  }

  private run(task: () => void | Promise<void>): void {
    try { void Promise.resolve(task()).catch(() => {}); }
    catch { /* Stale fence or unavailable command must not surface as unhandled. */ }
  }

  sync(presentation: WebPlaybackPresentation | undefined): void {
    const session = this.session();
    if (!session) return;
    if (!presentation) { this.clear(); return; }

    const {key, init} = describeMetadata(presentation, this.options.resolveArtwork);
    if (this.metadataKey !== key) {
      this.metadataKey = key;
      try { session.metadata = (this.options.createMetadata ?? defaultMetadata)(init); }
      catch { /* Ignore invalid metadata construction in restricted environments. */ }
    }

    const playbackState: MediaSessionPlaybackState = presentation.playing ? 'playing' : 'paused';
    try { session.playbackState = playbackState; } catch { /* readonly in rare mocks */ }

    this.activeFence = presentation.fence;
    this.activeControls = presentation.controls;
    this.activeFinished = presentation.finished;
    this.syncHandlers(session, presentation.controls);
    this.syncPosition(session, presentation);
  }

  private syncHandlers(session: MediaSessionLike, controls: readonly WebPlaybackPresentation['controls'][number][]): void {
    const step = this.options.seekStepSeconds ?? 10;
    const {actions} = this.options;
    const canPlay = controls.includes('play') || controls.includes('replay');
    const canPause = controls.includes('pause');
    const canSeek = controls.includes('seek');
    const canPrevious = controls.includes('previous');
    const canNext = controls.includes('next');

    this.boundHandlers = true;
    this.setHandler(session, 'play', canPlay ? () => {
      const fence = this.activeFence;
      if (!fence) return;
      this.run(() => this.activeFinished || this.activeControls?.includes('replay') ? actions.replay(fence) : actions.play(fence));
    } : null);
    this.setHandler(session, 'pause', canPause ? () => {
      const fence = this.activeFence;
      if (fence) this.run(() => actions.pause(fence));
    } : null);
    this.setHandler(session, 'seekbackward', canSeek ? details => {
      const fence = this.activeFence;
      if (!fence) return;
      const offset = typeof details?.seekOffset === 'number' && Number.isFinite(details.seekOffset) && details.seekOffset > 0 ? details.seekOffset : step;
      this.run(() => actions.skip(fence, -offset));
    } : null);
    this.setHandler(session, 'seekforward', canSeek ? details => {
      const fence = this.activeFence;
      if (!fence) return;
      const offset = typeof details?.seekOffset === 'number' && Number.isFinite(details.seekOffset) && details.seekOffset > 0 ? details.seekOffset : step;
      this.run(() => actions.skip(fence, offset));
    } : null);
    this.setHandler(session, 'seekto', canSeek ? details => {
      const fence = this.activeFence;
      const seekTime = details?.seekTime;
      if (!fence || typeof seekTime !== 'number' || !Number.isFinite(seekTime)) return;
      this.run(() => actions.seek(fence, Math.max(0, seekTime)));
    } : null);
    this.setHandler(session, 'previoustrack', canPrevious ? () => {
      const fence = this.activeFence;
      if (fence) this.run(() => actions.previous(fence));
    } : null);
    this.setHandler(session, 'nexttrack', canNext ? () => {
      const fence = this.activeFence;
      if (fence) this.run(() => actions.next(fence));
    } : null);
    this.setHandler(session, 'stop', () => {
      const fence = this.activeFence;
      if (fence) this.run(() => actions.stop(fence));
    });
  }

  private syncPosition(session: MediaSessionLike, presentation: WebPlaybackPresentation): void {
    if (typeof session.setPositionState !== 'function') return;
    const duration = presentation.durationSeconds;
    const rawPosition = presentation.positionSeconds;
    if (presentation.subject.kind === 'channel' || !Number.isFinite(duration) || duration <= 0 || !Number.isFinite(rawPosition) || rawPosition < 0) {
      if (this.lastPosition) {
        this.lastPosition = undefined;
        try { session.setPositionState(); } catch { /* unsupported */ }
      }
      return;
    }
    const position = Math.min(duration, Math.max(0, rawPosition));
    const rate = Number.isFinite(presentation.rate) && presentation.rate > 0 ? presentation.rate : 1;
    const now = (this.options.now ?? Date.now)();
    const prev = this.lastPosition;
    if (prev && prev.duration === duration && prev.rate === rate && prev.playing === presentation.playing) {
      const predicted = prev.playing ? Math.min(duration, prev.position + ((now - prev.at) / 1000) * prev.rate) : prev.position;
      if (Math.abs(predicted - position) < 0.75) return;
    }
    this.lastPosition = Object.freeze({duration, rate, position, playing: presentation.playing, at: now});
    try { session.setPositionState({duration, playbackRate: rate, position}); }
    catch { /* RangeError or unsupported */ }
  }

  clear(): void {
    const session = this.session();
    const hadState = this.metadataKey !== undefined || this.boundHandlers || this.lastPosition !== undefined || this.activeFence !== undefined;
    this.metadataKey = undefined;
    this.activeFence = undefined;
    this.activeControls = undefined;
    this.activeFinished = false;
    this.lastPosition = undefined;
    if (!session || !hadState) return;
    this.boundHandlers = false;
    try { session.metadata = null; } catch { /* ignore */ }
    try { session.playbackState = 'none'; } catch { /* ignore */ }
    try { session.setPositionState?.(); } catch { /* ignore */ }
    for (const action of ACTIONS) this.setHandler(session, action, null);
  }
}
