import type {PlaybackApi, PlaybackSession} from '../../../packages/client-core/src/index';
import type {OfflineExtras} from './offline-downloads';

export type LocalMedia = Readonly<{
  /** The download entry, for the offline journal. */
  entryId: string;
  itemId: string;
  /** Names this play in the offline journal: one id for as long as the file stays open. */
  playId: string;
  /** What came with the download: its description, chapters, skip markers and audio tracks, and
   * its subtitle tracks as files on this device. */
  extras?: OfflineExtras;
  /** `file://` URL of the downloaded file. */
  fileUrl: string;
  durationSeconds: number;
  startSeconds: number;
}>;
export type LocalProgress = Readonly<{entryId: string; itemId: string; playId: string; positionSeconds: number; durationSeconds: number; state: 'playing' | 'paused' | 'ended'; ended: boolean}>;

export const LOCAL_SESSION_PREFIX = 'local:';
export const isLocalSession = (id: string | undefined): boolean => !!id && id.startsWith(LOCAL_SESSION_PREFIX);
export const isLocalSource = (url: string | undefined): boolean => !!url && url.startsWith('file://');

/**
 * OS-07: a downloaded title plays in Portico's own player. `PlaybackService.playLocal` takes the
 * session this makes (a file inside the app's Downloads folder, which the native controller
 * accepts) instead of asking the server for one. The service captures this wrapper's explicit
 * localPlayback owner for its progress and stop: no queue, server session or network. Base API
 * calls retain server custody regardless of how their IDs are spelled.
 */
export class LocalPlayback {
  private sessions = new Map<string, LocalMedia>();
  private seq = 0;
  private readonly sink: (progress: LocalProgress) => void;
  constructor(sink: (progress: LocalProgress) => void) { this.sink = sink; }

  /** A maker of local sessions for `media` (a fresh id each call: a retry is a new session). */
  session(media: LocalMedia): () => PlaybackSession {
    if (!isLocalSource(media.fileUrl)) throw new Error('A local title needs a file on this device.');
    return () => {
      const id = `${LOCAL_SESSION_PREFIX}${media.entryId}:${++this.seq}`;
      this.sessions.set(id, media);
      while (this.sessions.size > 16) this.sessions.delete(this.sessions.keys().next().value!);
      return {id, generation: 1, streamUrl: media.fileUrl, mode: 'local', duration: media.durationSeconds, resumeSeconds: media.startSeconds};
    };
  }

  wrap(inner: PlaybackApi): PlaybackApi & {readonly localPlayback: Pick<PlaybackApi, 'stopPlayback' | 'progressPlayback'>} {
    const local = (id: string) => this.sessions.get(id);
    const progressPlayback: PlaybackApi['progressPlayback'] = (id, payload) => {
      const media = local(id);
      if (!media) return Promise.resolve();
      this.sink({entryId: media.entryId, itemId: media.itemId, playId: media.playId, positionSeconds: Math.max(0, payload.positionSeconds), durationSeconds: media.durationSeconds, state: payload.state, ended: payload.state === 'ended'});
      return Promise.resolve();
    };
    // Engine ownership closes the file. Keep its bounded mapping for the
    // service's already-captured final journal checkpoint; never call a server.
    const stopPlayback: PlaybackApi['stopPlayback'] = () => Promise.resolve();
    const wrapped: PlaybackApi = {
      createPlayback: (itemId, requestId, signal) => inner.createPlayback(itemId, requestId, signal),
      progressPlayback: (id, payload) => inner.progressPlayback(id, payload),
      stopPlayback: (id, signal) => inner.stopPlayback(id, signal),
      ...(inner.reportPlayerFailure ? {reportPlayerFailure: (id: string, code: Parameters<NonNullable<PlaybackApi['reportPlayerFailure']>>[1], detail: string, positionSeconds: number) => inner.reportPlayerFailure!(id, code, detail, positionSeconds)} : {}),
      ...(inner.markerSkipped ? {markerSkipped: (id: string, skipped: Parameters<NonNullable<PlaybackApi['markerSkipped']>>[1]) => inner.markerSkipped!(id, skipped)} : {}),
      ...(inner.onServerEnded ? {onServerEnded: (listener: Parameters<NonNullable<PlaybackApi['onServerEnded']>>[0]) => inner.onServerEnded!(listener)} : {}),
    };
    return Object.assign(wrapped, {localPlayback: Object.freeze({progressPlayback, stopPlayback})});
  }
}
