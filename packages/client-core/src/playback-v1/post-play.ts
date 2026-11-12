import {preferenceValue, type PreferenceSnapshot} from '../preferences.ts';
import type {QueueEntry} from './types.ts';

/** What happens when a title ends, from the profile's Playback settings. The player decides it;
 * the server's queue says only what comes next. */
export type PostPlaySettings = Readonly<{
  /** Autoplay next: the next title starts by itself after the countdown. */
  autoplay: boolean;
  countdownSeconds: 0 | 5 | 10 | 15;
  startDuringCredits: boolean;
  /** Stop at season end: the last episode of a season does not move on to the next by itself. */
  stopAtSeasonEnd: boolean;
}>;

export const defaultPostPlay: PostPlaySettings = Object.freeze({autoplay: true, countdownSeconds: 10, startDuringCredits: false, stopAtSeasonEnd: false});

/** The profile's post-play settings; a setting the server does not have keeps its default. */
export function postPlaySettings(snapshot: PreferenceSnapshot): PostPlaySettings {
  const read = <T,>(key: string, fallback: T, valid: (v: unknown) => boolean): T => {
    try { const v = preferenceValue(snapshot, key); return valid(v) ? v as T : fallback; } catch { return fallback; }
  };
  const countdownMs = read<number>('playback.upNextCountdownMs', 10_000, v => v === 0 || v === 5000 || v === 10_000 || v === 15_000);
  return Object.freeze({autoplay: read('playback.autoplayNext', true, v => typeof v === 'boolean'), countdownSeconds: countdownMs / 1000 as PostPlaySettings['countdownSeconds'],
    startDuringCredits: read('playback.startNextDuringCredits', false, v => typeof v === 'boolean'),
    stopAtSeasonEnd: read('playback.stopAtSeasonEnd', false, v => typeof v === 'boolean')});
}

/** Whether `next` begins another season or another show than the episode `current`. */
export function endsSeason(current: QueueEntry | undefined, next: QueueEntry | undefined): boolean {
  if (current?.kind !== 'episode' || !next) return false;
  return next.kind !== 'episode' || next.showId !== current.showId || next.episode?.seasonNumber !== current.episode?.seasonNumber;
}

/** What the player does when the current entry ends, before the next one: whether it moves on by
 * itself, after how long, and whether it may start during the credits. */
export function postPlayFor(settings: PostPlaySettings, current: QueueEntry | undefined, next: QueueEntry | undefined): Readonly<{autoplay: boolean; countdownSeconds: PostPlaySettings['countdownSeconds']; startDuringCredits: boolean}> {
  const autoplay = settings.autoplay && !(settings.stopAtSeasonEnd && endsSeason(current, next));
  return Object.freeze({autoplay, countdownSeconds: settings.countdownSeconds, startDuringCredits: autoplay && settings.startDuringCredits});
}
