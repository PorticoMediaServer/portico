import {playbackIdentity, type TextEnvironment} from '@core/presentation/kit-model/index.ts';
import type {WebPlaybackRuntimeSnapshot} from '../platform/playback';
import type {DockNoticeData, DockPresentation, DockStarting} from '../kit/dock';
import type {ShellArtwork} from './artwork';
import {playbackFence} from '../../../packages/player-core/src/index.ts';

export const fenceKey = (fence: NonNullable<WebPlaybackRuntimeSnapshot['fence']>) => JSON.stringify(playbackFence(fence));
/** A pending receiver choice preserves the current local picture. Only confirmed remote play takes it over. */
export function localVideoSurfaceActive(snapshot: WebPlaybackRuntimeSnapshot | undefined): boolean {
  const presentation = snapshot?.presentation, local = snapshot?.dock.local;
  return !!snapshot && !snapshot.disposed && !!presentation && presentation.kind === 'video' && !!local
    && snapshot.dock.remote?.phase !== 'playing' && fenceKey(local.fence) === fenceKey(presentation.fence);
}
/** A notice offers only the bookmark still owned by this exact runtime. */
export function retainedPlaybackNotice<Runtime>(notice: Readonly<{runtime: Runtime; value: DockNoticeData}> | null, runtime: Runtime | undefined,
  dock: WebPlaybackRuntimeSnapshot['dock'] | undefined, presentations: readonly DockPresentation[]): DockNoticeData | null {
  if (!notice || runtime === undefined || notice.runtime !== runtime || !dock) return null;
  const bookmark = notice.value.kind === 'paused-audio' ? dock.parkedAudio : dock.resumeVideo;
  const key = fenceKey(notice.value.playback.fence);
  return bookmark && fenceKey(bookmark.fence) === key && presentations.some(value => fenceKey(value.fence) === key) ? notice.value : null;
}

/** O-159: the starting dock from the pressed card's own facts, drawn like the real dock's
 * identity and art; the bar adds the pending ring and Stop only. */
export function startingDock(starting: NonNullable<WebPlaybackRuntimeSnapshot['starting']>, environment: TextEnvironment, artwork: ShellArtwork): DockStarting {
  const subject = starting.display.subject, kind = subject.kind === 'music' || subject.kind === 'audiobook' ? subject.kind : 'video', audio = kind !== 'video';
  return {key: starting.key, kind, identity: playbackIdentity(subject, 'dock', environment), stop: starting.stop, ...(starting.waiting ? {waiting: starting.waiting} : {}),
    artwork: artwork.foreground(starting.display.posterUrl, (audio ? 64 : 98) * (globalThis.devicePixelRatio || 1), audio ? 1 : 16 / 9, {group: 'playback', priority: 'high'})};
}
export function playbackPresentation(snapshot: WebPlaybackRuntimeSnapshot, environment: TextEnvironment, artwork: ShellArtwork, skips: {back: number; forward: number}): DockPresentation | undefined {
  const current = snapshot.presentation;
  if (!current) return undefined;
  const audio = current.kind !== 'video';
  return {fence: current.fence, identity: playbackIdentity(current.subject, 'dock', environment), durationSeconds: current.durationSeconds,
    controls: current.controls, finished: current.finished, rate: current.rate, volume: current.volume, muted: current.muted,
    skipBackSeconds: skips.back, skipForwardSeconds: skips.forward,
    artwork: artwork.foreground(current.posterUrl ?? current.backdropUrl, (audio ? 64 : 98) * (globalThis.devicePixelRatio || 1), audio ? 1 : 16 / 9, {group: 'playback', priority: 'high'}),
    ambient: artwork.ambient(current.backdropUrl ?? current.posterUrl, {group: 'playback', priority: 'high'}),
  };
}
/** Keep only actual active/bookmarked identities. A private presentation cannot survive a source teardown. */
export class PlaybackPresentations {
  private readonly values = new Map<string, DockPresentation>();
  /** Bind private display facts to the runtime whose teardown clears them. */
  constructor(private readonly authority?: Readonly<{getSnapshot: () => WebPlaybackRuntimeSnapshot}>) {}
  update(snapshot: WebPlaybackRuntimeSnapshot, current: DockPresentation | undefined): readonly DockPresentation[] {
    if (this.authority && this.authority.getSnapshot() !== snapshot) { this.clear(); return Object.freeze([]); }
    if (current) this.values.set(fenceKey(current.fence), current);
    const state = snapshot.dock;
    const retained = new Set([state.local?.fence, state.parkedAudio?.fence, state.resumeVideo?.fence, state.remote?.playback.fence].flatMap(fence => fence ? [fenceKey(fence)] : []));
    for (const key of this.values.keys()) if (!retained.has(key)) this.values.delete(key);
    return Object.freeze([...this.values.values()]);
  }
  clear() { this.values.clear(); }
}
export function hasDock(snapshot: WebPlaybackRuntimeSnapshot): boolean {
  return !!snapshot.dock.remote || !!snapshot.dock.local && !snapshot.dock.local.expanded;
}
