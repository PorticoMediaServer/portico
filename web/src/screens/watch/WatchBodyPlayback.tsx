import {useMemo, useSyncExternalStore, type ReactNode} from 'react';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
import {WatchBodyPlaybackContext, type WatchBodyPlayback} from '../detail/watch-body-playback';

/** phone-player 4: the watch page's title follows the video playing above it. Pause and Play go to
 * this exact playback (its fence), never to whatever plays later. */
export function WatchBodyPlaybackProvider({runtime, reportError, children}: {runtime: WebShellPlaybackRuntime; reportError: (error: unknown) => void; children: ReactNode}) {
  const snapshot = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot, runtime.getSnapshot);
  const presentation = snapshot.presentation, fence = snapshot.fence, playing = !!presentation?.playing;
  const enabled = !snapshot.disposed && !!fence && !!presentation && presentation.controls.includes(playing ? 'pause' : 'play');
  const subject = presentation?.subject, length = presentation?.durationSeconds ?? 0;
  const left = presentation && length > 0 ? Math.max(0, length - presentation.positionSeconds) : undefined;
  // Quantised as the duration text reads (seconds under a minute, else whole minutes), so the page
  // redraws when the words change, not on every tick.
  const leftSeconds = left === undefined ? undefined : left < 60 ? Math.ceil(left) : Math.round(left / 60) * 60;
  const value = useMemo<WatchBodyPlayback>(() => ({playing, enabled, ...(subject ? {subject} : {}), ...(leftSeconds === undefined ? {} : {leftSeconds}),
    toggle: () => {if (enabled && fence) void runtime.toggle(fence).catch(reportError);}}), [playing, enabled, subject, leftSeconds, fence, runtime, reportError]);
  return <WatchBodyPlaybackContext.Provider value={value}>{children}</WatchBodyPlaybackContext.Provider>;
}
