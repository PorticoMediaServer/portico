import type {NativePlaybackStart} from './types';

/** A card's retained resume position is display data. Only a deliberate seek,
 * handoff or Start over supplies a position to the server's new start. */
export function nativeStartSeconds(input: Pick<NativePlaybackStart, 'target' | 'startIntent'>): number | undefined {
  switch (input.startIntent ?? 'resume') {
    case 'resume': return undefined;
    case 'start-over': return 0;
    case 'explicit': {
      const seconds = input.target.startSeconds;
      if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0 || seconds > 1e9) throw new Error('Invalid playback target.');
      return seconds;
    }
    default: throw new Error('Invalid playback start intent.');
  }
}
