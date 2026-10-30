import type {VideoSnapshotExitImage} from '../video-snapshot';
import type {NativePlaybackVideoExitImage} from './types';
import {playbackFence, type PlaybackFence} from '../../../../../packages/player-core/src';

const valid = (read: () => boolean) => {try {return read() === true;} catch {return false;}};
/** One original page's passive file, never a playback/source/command lease. */
export async function retainNativeVideoExitImage(fence: PlaybackFence, original: Readonly<{
  sourceCurrent(): boolean; paintCurrent(): boolean;
  subscribe(listener: () => void): () => void;
  retain(): Promise<VideoSnapshotExitImage | undefined>;
}>, retiring: Set<() => Promise<void>>): Promise<NativePlaybackVideoExitImage | undefined> {
  if (!valid(original.sourceCurrent) || !valid(original.paintCurrent)) return;
  const capturedFence = playbackFence(fence);
  const life = new AbortController(), listeners = new Set<() => void>();
  let image: VideoSnapshotExitImage | undefined, off: (() => void) | undefined, disposal: Promise<void> | undefined, imageRelease: Promise<void> | undefined;
  const releaseImage = (): Promise<void> => image ? imageRelease ??= Promise.resolve().then(() => image!.release()) : Promise.resolve();
  const release = (): Promise<void> => {
    if (!disposal) disposal = Promise.resolve().then(releaseImage).finally(() => {retiring.delete(release);});
    if (!life.signal.aborted) {
      life.abort(); try {off?.();} catch { /* File release remains owned even if an unsubscribe fails. */ } off = undefined;
      image?.signal.removeEventListener('abort', invalidate);
      for (const listener of [...listeners]) {try {listener();} catch { /* Passive notification cannot regain permission. */ }}
      listeners.clear();
    }
    return disposal;
  };
  // A declined passive offer must not wait for an unresponsive native cleanup.
  const refuse = (): undefined => {void releaseImage().catch(() => {});void release().catch(() => {});return undefined;};
  const paintCurrent = (): boolean => {
    if (!life.signal.aborted && !image?.signal.aborted && valid(original.paintCurrent) && !life.signal.aborted) return true;
    void release().catch(() => {}); return false;
  };
  const invalidate = () => {if (!paintCurrent()) void release().catch(() => {});};
  try {
    off = original.subscribe(invalidate);
    if (life.signal.aborted) {off();off = undefined;}
    if (!paintCurrent() || !valid(original.sourceCurrent)) {return refuse();}
    image = await original.retain();
    // No post-retirement claim even if native granted immediately before replacement.
    if (!image || !paintCurrent() || !valid(original.sourceCurrent)) {return refuse();}
    image.signal.addEventListener('abort', invalidate, {once: true});
    if (!paintCurrent()) {return refuse();}
    retiring.add(release);
    return Object.freeze({fence: capturedFence, image: image.image, signal: life.signal, paintCurrent,
      subscribe(listener: () => void) {if (paintCurrent()) listeners.add(listener);return () => {listeners.delete(listener);};}, release});
  } catch {return refuse();}
}
