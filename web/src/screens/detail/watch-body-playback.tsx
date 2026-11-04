import {createContext, useContext} from 'react';
import {playbackIdentity, titleFactParts, type FactPart, type PlaybackSubject, type TextEnvironment} from '@core/presentation/kit-model';
import {serviceText} from '@core/presentation/service-text.ts';

/** phone-player 4 / desktop-player 7: on the watch page the title's white button follows the video
 * playing above it, Pause while it plays and Play when it's paused. The watch host supplies this
 * for the title it plays; an ordinary title page has none and keeps its own Play or Resume.
 * `subject` is what plays (a show's header names its episode); `leftSeconds` is how much of it is
 * left, in the steps its words change (seconds under a minute, else whole minutes), so the page
 * redraws when the text changes, not on every tick. */
export type WatchBodyPlayback = Readonly<{playing: boolean; enabled: boolean; toggle(): void; subject?: PlaybackSubject; leftSeconds?: number}>;
export const WatchBodyPlaybackContext = createContext<WatchBodyPlayback | undefined>(undefined);
export const useWatchBodyPlayback = () => useContext(WatchBodyPlaybackContext);

/** phone-player 4: the watch page's facts line keeps the title's year and genres, then says how
 * long is left of what plays ("37 min left"). */
export function watchHeaderFacts(parts: readonly FactPart[], leftSeconds: number | undefined, env: TextEnvironment): FactPart[] {
  const kept = parts.filter(part => part.id === 'year' || part.id === 'genres');
  const left = leftSeconds && leftSeconds > 0 ? titleFactParts({durationSeconds: leftSeconds}, env).find(part => part.id === 'duration')?.text : undefined;
  return left ? [...kept, {id: 'left', role: 'supplement', text: serviceText('duration.remaining', {duration: left})}] : kept;
}
/** phone-player 4: under a show's name, the episode playing ("S1 E1", then its title). */
export function watchEpisodeLine(subject: PlaybackSubject | undefined, env: TextEnvironment): readonly FactPart[] {
  return subject?.kind === 'video' && subject.item.kind === 'episode' ? playbackIdentity(subject, 'full', env).subtitle : [];
}
