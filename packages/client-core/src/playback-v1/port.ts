import type {ObservedPlayback} from './observed-playback.ts';

/**
 * The MediaPlayer port (Plan — Client Playback Migration §2): what a platform player must do for
 * the platform-neutral session core. Web implements it over `<video>` + hls.js, Apple over the
 * native AVPlayer bridge. The core never touches a DOM or native type.
 *
 * Every observation carries the presentation `generation` it came from, so events from an older
 * URL (a late `timeupdate` after a track switch) can be dropped (spec §2, §6; invariant 3).
 */

import type {LocalPlayerSource, LocalPlayerLoadReceipt, LocalPlayerLoadContext} from './local-port.ts';
export type {LocalPlayerSource, LocalPlayerLoadReceipt, LocalPlayerLoadContext} from './local-port.ts';

export type PlayerState = 'idle' | 'loading' | 'playing' | 'paused' | 'buffering' | 'ended' | 'error';

/** A sidecar subtitle delivered as its own capability URL (spec §5.5). */
export type SidecarSubtitle = Readonly<{trackId: string; format: string; url: string}>;

/** One presentation to load (spec §5.1 `presentation`). */
export type PlayerSource = Readonly<{
  url: string;
  generation: number;
  mode: 'direct' | 'stream';
  startPositionMs: number;
  subtitles: readonly SidecarSubtitle[];
  /** Start playing once loaded, or stay paused at the start position. */
  autoplay: boolean;
  /** The session and item this presentation belongs to (identity for native engines' bindings; web ignores them). */
  sessionId?: string;
  itemId?: string;
  /** What the stream needs from the connection, when the server said: engines size their buffers by it. */
  bitrateKbps?: number;
  /**
   * A live stream: positions are seconds since this wall-clock instant and are mapped through the
   * stream's programme dates, never through the element's own clock.
   */
  liveOriginMs?: number;
}>;

/** A point-in-time reading of the player, for timeline reports (spec §6). */
export type PlayerObservation = Readonly<{
  /** Actual renderer output from this generation, independent of source/capability facts. */
  observedPlayback?: ObservedPlayback;
  generation: number;
  state: PlayerState;
  positionMs: number;
  partIndex?: number;
  rate: number;
  bufferedMs?: number;
  bandwidthKbps?: number;
  droppedFrames?: number;
  volume?: number;
  muted?: boolean;
  /** The audio track playing and the subtitle shown (`null` = off); reported in the timeline (spec §17). */
  audioTrackId?: string;
  subtitleTrackId?: string | null;
  /** Playing on an external route (AirPlay, HDMI) or in Picture in Picture. */
  externalRoute?: 'airplay' | 'hdmi' | 'pip';
  /**
   * Whether the chosen sidecar subtitle is visible where the picture is. False when an overlay
   * the app draws can't follow the picture (AirPlay, PiP): the core then asks the server for
   * in-manifest subtitles (`delivery: 'embeddedClient'`), which the system renders everywhere.
   */
  subtitlesCarried?: boolean;
}>;

export type PlayerEvent =
  | Readonly<{type: 'state'; observation: PlayerObservation}>
  | Readonly<{type: 'time'; observation: PlayerObservation}>
  /** A seek the viewer (or a command) asked for has finished. */
  | Readonly<{type: 'seeked'; observation: PlayerObservation}>
  | Readonly<{type: 'error'; observation: PlayerObservation; code: string; detail?: string}>;

export interface MediaPlayer {
  /** Absent in browsers. A local source never enters the URL-required load path.
   * On retirement the adapter cleans only its exact accepted private load/journal. */
  loadLocal?(source: LocalPlayerSource, context: LocalPlayerLoadContext): Promise<LocalPlayerLoadReceipt>;
  load(source: PlayerSource): void;
  play(): void;
  pause(): void;
  seek(positionMs: number): void;
  setRate(rate: number): void;
  /** `null` turns sidecar subtitles off. Embedded tracks are chosen by the server (a new generation). */
  selectSidecarSubtitle(trackId: string | null): void;
  /** The current reading, tagged with the generation now loaded. */
  observe(): PlayerObservation;
  onEvent(listener: (event: PlayerEvent) => void): () => void;
  /** Release decoders and network; the port is not used again. */
  dispose(): void;
}
