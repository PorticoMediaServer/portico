import type {NativePlaybackStart} from '../../platform/playback/types';

/** A row/card owner supplies actual navigation and membership checks. Playback
 * output changes are deliberately not an input to this creation lifetime. */
export function nativeHomeEntryLifetime(current: () => boolean,
  subscriptions: readonly ((listener: () => void) => () => void)[], parent: AbortSignal) {
  const abort = new AbortController(), off: (() => void)[] = [];
  const stop = () => abort.abort();
  const isCurrent = () => !abort.signal.aborted && !parent.aborted && current();
  const check = () => {if (!isCurrent()) stop();};
  parent.addEventListener('abort', stop, {once: true});
  for (const subscribe of subscriptions) off.push(subscribe(check));
  check();
  const lifetime: NonNullable<NativePlaybackStart['lifetime']> = {signal: abort.signal, isCurrent};
  return {lifetime, dispose: () => {
    parent.removeEventListener('abort', stop); for (const unsubscribe of off.splice(0)) unsubscribe();
  }};
}

/** E-tv-36: the lifetime of a Play already dispatched from a Home or See All card. The card checks
 * that it is still current before dispatch; after that the start belongs to the player (the
 * starting dock is drawn at once), exactly as a title-page Play. A Home re-read (Live rows refresh
 * the document), a navigation snapshot or a selection change must not abort it. Only the shell's
 * authority (sign-in, profile, server) or the owner's own disposal ends it. */
export function nativeDispatchedPlayLifetime(current: () => boolean,
  subscribeAuthority: (listener: () => void) => () => void, parent: AbortSignal) {
  return nativeHomeEntryLifetime(current, [subscribeAuthority], parent);
}

/** A failed dispatched Play is always said, unless it was retired (Stop on the starting dock, or
 * the authority changed) or the owner is gone. Never a silent nothing. */
export function reportDispatchedPlayFailure(error: unknown, ownerCurrent: () => boolean, report: (error: unknown) => void) {
  if (!ownerCurrent() || (error as {name?: unknown} | null)?.name === 'AbortError') return;
  report(error);
}
