import {sameFence} from '../../../../packages/player-core/src/index.ts';
import type {WebPlaybackAction, WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../platform/playback';

export type WatchIntent = Pick<WebPlaybackAction, 'kind' | 'seconds' | 'volume' | 'direction'>;
export type WatchCommands = Pick<WebShellPlaybackRuntime, 'getSnapshot' | 'onAction'>;

/** Only the current engine can offer frame stepping; a browser seek is never a substitute. */
export function watchStepIntent(runtime: Pick<WebShellPlaybackRuntime, 'getSnapshot' | 'speedChoices' | 'canStepFrame'>,
  shown: WebPlaybackRuntimeSnapshot, key: string): WatchIntent | null {
  if (!['<', '>', ',', '.'].includes(key)) return null;
  const live = runtime.getSnapshot(), fence = shown.fence, presentation = shown.presentation;
  if (!fence || !live.fence || shown.disposed || live.disposed || shown.authority !== live.authority || !sameFence(fence, live.fence)
    || !presentation?.expanded || presentation.kind !== 'video' || presentation.finished || !sameFence(presentation.fence, fence)
    || live.dock.remote?.phase === 'playing') return null;
  if (key === '<' || key === '>') return runtime.speedChoices(fence).length ? {kind: 'step-speed', direction: key === '<' ? -1 : 1} : null;
  return !presentation.playing && runtime.canStepFrame(fence) ? {kind: 'step-frame', direction: key === ',' ? -1 : 1} : null;
}

/** A retained render or an old keyboard closure cannot command a new playback. */
export async function dispatchWatch(runtime: WatchCommands, shown: WebPlaybackRuntimeSnapshot, intent: WatchIntent): Promise<void> {
  const current = runtime.getSnapshot();
  if (shown.disposed || current.disposed || current.dock.remote?.phase === 'playing' || !shown.fence || !current.fence || !sameFence(shown.fence, current.fence)
    || shown.authority !== current.authority) throw new Error('stale_watch_command');
  await runtime.onAction({...intent, fence: shown.fence});
}

export function watchSeekBounds(snapshot: WebPlaybackRuntimeSnapshot): Readonly<{start: number; end: number}> | null {
  const presentation = snapshot.presentation;
  if (!presentation || !presentation.controls.includes('seek')) return null;
  const start = presentation.seekWindow?.startSeconds ?? 0, end = presentation.seekWindow?.endSeconds ?? presentation.durationSeconds;
  return Number.isFinite(start) && Number.isFinite(end) && start >= 0 && end > start ? {start, end} : null;
}
export function watchSeek(snapshot: WebPlaybackRuntimeSnapshot, position: number): number | null {
  const bounds = watchSeekBounds(snapshot);
  return bounds && Number.isFinite(position) ? Math.min(bounds.end, Math.max(bounds.start, position)) : null;
}
export function watchClock(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '';
  const whole = Math.floor(seconds), hours = Math.floor(whole / 3600), minutes = Math.floor(whole / 60) % 60, rest = whole % 60;
  return hours ? `${hours}:${String(minutes).padStart(2, '0')}:${String(rest).padStart(2, '0')}` : `${Math.floor(whole / 60)}:${String(rest).padStart(2, '0')}`;
}
export function watchIsPortrait(width: number, height: number): boolean {
  return width < 1024 && height > width;
}
/** Shortcut capture stays out of text fields, sliders, menus and other modal surfaces. */
export function watchShortcutTarget(target: EventTarget | null): boolean {
  return typeof Element !== 'undefined' && target instanceof Element && !!target.closest('input,textarea,select,[contenteditable="true"],[role="slider"],[role="menu"],[role="dialog"]');
}
