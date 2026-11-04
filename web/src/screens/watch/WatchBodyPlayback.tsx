import {useMemo, useSyncExternalStore, type ReactNode} from 'react';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
import {WatchBodyPlaybackContext, type WatchBodyPlayback} from '../detail/watch-body-playback';
import {fenceKey} from '../../shell/playback-model';

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
  // A streamed playback's fence is a fresh object on every sync: key the value on what it names, so
  // the title body redraws when its words change, not on every tick. Toggle re-reads the live fence
  // at the press and acts only while it is still this playback.
  const key = fence ? fenceKey(fence) : undefined;
  const value = useMemo<WatchBodyPlayback>(() => ({playing, enabled, ...(subject ? {subject} : {}), ...(leftSeconds === undefined ? {} : {leftSeconds}),
    toggle: () => {
      const live = runtime.getSnapshot().fence;
      if (enabled && key !== undefined && live && fenceKey(live) === key) void runtime.toggle(live).catch(reportError);
    }}), [playing, enabled, subject, leftSeconds, key, runtime, reportError]);
  return <WatchBodyPlaybackContext.Provider value={value}>{children}</WatchBodyPlaybackContext.Provider>;
}
