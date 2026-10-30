import {followedByScene} from './segment-skip.ts';
/**
 * When to offer the next title before this one has ended: while its closing credits run.
 *
 * The server's credits marker is the trigger, so the offer appears when the story is over, not a
 * fixed number of seconds before the file ends. A marker in the first half of a title is not a
 * closing one (opening credits). An episode without a marker gets the offer for its last thirty
 * seconds. Nothing is offered when there is no next title, when the viewer turned credits prompts
 * off, or when the server wants to ask whether anyone is still watching. The web and native players
 * both use these rules.
 */
export type ClosingMarker = Readonly<{id: string; kind: string; startSeconds: number; endSeconds: number; automaticSafe?: boolean}>;
export type UpNextOffer = Readonly<{
  /** Stable for as long as the same offer stands; a dismissal is remembered against it. */
  id: string;
  /** The credits marker behind the offer, when there is one. */
  marker?: ClosingMarker;
}>;

export function upNextOffer(input: Readonly<{
  markers?: readonly ClosingMarker[] | null; positionSeconds: number; durationSeconds: number; kind?: string;
  mode: 'ask' | 'auto' | 'off'; nextAvailable: boolean; passoutDue: boolean;
}>): UpNextOffer | null {
  const {positionSeconds: position, durationSeconds: duration} = input;
  if (!input.nextAvailable || input.passoutDue || input.mode === 'off' || !(duration > 0) || !(position >= 0)) return null;
  const closing = (input.markers ?? []).filter(m => (m.kind === 'credits' || m.kind === 'outro') && m.startSeconds >= duration / 2 && m.endSeconds > m.startSeconds);
  if (closing.length) {
    const marker = closing.find(m => position >= m.startSeconds && position < m.endSeconds - 0.5);
    // Credits the viewer asked to have skipped unattended are skipped by the skip prompt; there is nothing to offer.
    if (!marker || (input.mode === 'auto' && marker.automaticSafe)) return null;
    return {id: 'marker:' + marker.id, marker};
  }
  if (input.kind === 'episode' && duration >= 600 && position >= duration - 30 && position < duration - 1) return {id: 'tail'};
  return null;
}

/**
 * The countdown on the Up next card, when the next title will start without anyone pressing a
 * thing. Only when the viewer has turned on "Start the next episode during the credits" (off by
 * default), with autoplay on and credits the server is sure of (`automaticSafe`), does the card
 * count the viewer's post-play countdown and start the next title when it reaches zero, as Plex
 * does. Otherwise nothing starts early: the count is the rest of this title, at its speed, plus the
 * countdown the end card will run. Credits that end before the title does (a mid- or post-credits
 * scene follows) count to the end card too, so the scene is never skipped by a countdown. With
 * autoplay off there is no count.
 */
export type UpNextCountdown = Readonly<{automatic: boolean; seconds: number}>;
export function upNextCountdown(input: Readonly<{
  offer: UpNextOffer; positionSeconds: number; durationSeconds: number; rate: number;
  autoplay: boolean; countdownSeconds: number; elapsedSeconds: number;
  /** The viewer's `playback.startNextDuringCredits`; absent means off. */
  startDuringCredits?: boolean;
}>): UpNextCountdown | null {
  if (!input.autoplay) return null;
  const countdown = Math.max(0, input.countdownSeconds);
  // Credits followed by a scene of this title never start the next one early: the scene would be lost.
  const marker = input.offer.marker;
  if (input.startDuringCredits === true && marker?.automaticSafe && !followedByScene(marker, input.durationSeconds)) return {automatic: true, seconds: Math.max(0, Math.ceil(countdown - input.elapsedSeconds))};
  const left = Math.max(0, input.durationSeconds - input.positionSeconds) / Math.max(0.25, input.rate || 1);
  return {automatic: false, seconds: Math.ceil(left + countdown)};
}
