/**
 * A Playback Protocol v1 session in the shape `PlaybackService` shows to the player screens.
 */
import type {PlaybackSession} from '../index.ts';
import type {Session} from './types.ts';

/** The service's session for a v1 session: stream mode is HLS, direct stays direct. */
export function toPlaybackSession(session: Session, durationSeconds: number, resumeSeconds?: number): PlaybackSession {
  return {
    id: session.id,
    generation: session.presentation.generation,
    streamUrl: session.presentation.url,
    mode: session.presentation.mode === 'stream' ? 'hls' : 'direct',
    duration: Math.max(0, durationSeconds),
    resumeSeconds: resumeSeconds ?? session.presentation.startPositionMs / 1000,
    protocol: 'v1',
    // §18.1 version 2: the device decodes the file itself.
    ...(session.presentation.audio ? {audio: session.presentation.audio} : {}),
  };
}
