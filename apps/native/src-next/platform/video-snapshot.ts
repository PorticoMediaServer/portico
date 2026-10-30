import {NativeEventEmitter, NativeModules, Platform} from 'react-native';

export type VideoSnapshotBinding = Readonly<{intentId: number; sessionId: string; generation: number; itemId: string}>;
export type VideoSnapshotSource = Readonly<{
  reactTag: number; engine: 'native' | 'mpv'; lease: string; binding: VideoSnapshotBinding;
}>;
export type VideoSnapshotExitImage = Readonly<{
  image: Readonly<{uri: string; width: number; height: number}>;
  signal: AbortSignal;
  release: () => Promise<void>;
}>;
export type VideoSnapshot = Readonly<{
  source: VideoSnapshotSource;
  image: Readonly<{uri: string; width: number; height: number}>;
  /** One file-only transfer, admitted by native while this exact settled source is live.
   * Missing native support or a retirement race declines; never captures again. */
  retainForExit?: () => Promise<VideoSnapshotExitImage | undefined>;
  /** Release after the drag ends, on source/authority replacement, or on unmount. */
  release: () => Promise<void>;
}>;
export type VideoSnapshotCapture = Readonly<{
  capture: (source: VideoSnapshotSource, options?: {signal?: AbortSignal}) => Promise<VideoSnapshot>;
  cancel: () => void;
  dispose: () => void;
}>;
export class VideoSnapshotError extends Error {
  readonly code: 'snapshot_unavailable' | 'snapshot_busy' | 'snapshot_stale' | 'snapshot_cancelled' | 'snapshot_timeout';
  constructor(code: VideoSnapshotError['code']) { super(code); this.name = 'VideoSnapshotError'; this.code = code; }
}
type Bridge = {
  capture: (id: string, source: VideoSnapshotSource) => Promise<{id: string; uri: string; width: number; height: number}>;
  release: (id: string) => Promise<boolean>;
  retainForExit?: (id: string, source: VideoSnapshotSource) => Promise<boolean>;
  addListener?: (event: string) => void;
  removeListeners?: (count: number) => void;
};
let sequence = 0;
const validText = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 512;
function validSource(source: VideoSnapshotSource): boolean {
  return Number.isSafeInteger(source.reactTag) && source.reactTag > 0 && ['native', 'mpv'].includes(source.engine) && validText(source.lease) &&
    !!source.binding && Number.isSafeInteger(source.binding.intentId) && source.binding.intentId > 0 &&
    Number.isSafeInteger(source.binding.generation) && source.binding.generation > 0 && validText(source.binding.sessionId) && validText(source.binding.itemId);
}

/** Caller owns the mounted ref and exact playback/authority fence. No URL or bearer crosses this API. */
export function createVideoSnapshotCapture(options: {isCurrent: (source: VideoSnapshotSource) => boolean}): VideoSnapshotCapture {
  let disposed = false;
  let pending: {cancel: () => void} | undefined;
  const retained = new Set<() => Promise<void>>();
  const transferred = new Set<() => Promise<void>>();
  const current = (source: VideoSnapshotSource) => { try { return !disposed && options.isCurrent(source); } catch { return false; } };
  const cancel = () => {
    pending?.cancel();
    for (const release of retained) void release().catch(() => {});
  };
  return {
    cancel,
    dispose: () => { disposed = true; cancel(); for (const release of transferred) void release().catch(() => {}); },
    async capture(input, {signal} = {}) {
      if (pending || retained.size || transferred.size) throw new VideoSnapshotError('snapshot_busy');
      if (!validSource(input)) throw new VideoSnapshotError('snapshot_unavailable');
      const source: VideoSnapshotSource = Object.freeze({reactTag: input.reactTag, engine: input.engine, lease: input.lease,
        binding: Object.freeze({intentId: input.binding.intentId, sessionId: input.binding.sessionId, generation: input.binding.generation, itemId: input.binding.itemId})});
      if (!current(source)) throw new VideoSnapshotError('snapshot_stale');
      if (signal?.aborted) throw new VideoSnapshotError('snapshot_cancelled');
      const bridge = NativeModules.PorticoVideoSnapshot as Bridge | undefined;
      if (!['android', 'ios'].includes(Platform.OS) || Platform.isTV || typeof bridge?.capture !== 'function' || typeof bridge?.release !== 'function') throw new VideoSnapshotError('snapshot_unavailable');
      const id = `frame-${Date.now().toString(36)}-${(++sequence).toString(36)}`;
      let cancelled = false, timer: ReturnType<typeof setTimeout> | undefined;
      let rejectCancel!: (error: VideoSnapshotError) => void;
      const releaseNative = async () => { await bridge.release(id); };
      const cancellation = new Promise<never>((_, reject) => { rejectCancel = reject; });
      const abort = (code: VideoSnapshotError['code'] = 'snapshot_cancelled') => {
        if (cancelled) return;
        cancelled = true;
        void releaseNative().catch(() => {});
        rejectCancel(new VideoSnapshotError(code));
      };
      const onAbort = () => abort();
      const flight = {cancel: onAbort};
      pending = flight;
      signal?.addEventListener('abort', onAbort, {once: true});
      timer = setTimeout(() => abort('snapshot_timeout'), 2200);
      try {
        const native = Promise.resolve(bridge.capture(id, source)).then(async result => {
          if (cancelled || !current(source)) { await releaseNative(); throw new VideoSnapshotError(cancelled ? 'snapshot_cancelled' : 'snapshot_stale'); }
          return result;
        });
        const result = await Promise.race([native, cancellation]);
        if (cancelled || !current(source)) throw new VideoSnapshotError('snapshot_stale');
        if (result.id !== id || !/^file:\/\//.test(result.uri) || !Number.isSafeInteger(result.width) || !Number.isSafeInteger(result.height) ||
          result.width <= 0 || result.height <= 0 || result.width > 1280 || result.height > 1280 || result.width * result.height > 921600) throw new VideoSnapshotError('snapshot_unavailable');
        const image = Object.freeze({uri: result.uri, width: result.width, height: result.height});
        let releasing: Promise<void> | undefined, claimed = false, claiming = false, cancelClaim: (() => void) | undefined;
        const release = () => {
          if (claimed) return Promise.resolve();
          releasing ??= Promise.resolve().then(releaseNative).finally(() => {retained.delete(release);});
          cancelClaim?.();return releasing;
        };
        retained.add(release);
        const retainForExit = async (): Promise<VideoSnapshotExitImage | undefined> => {
          if (claiming || claimed || releasing || !current(source) || signal?.aborted
            || typeof bridge.retainForExit !== 'function' || typeof bridge.addListener !== 'function' || typeof bridge.removeListeners !== 'function') return;
          claiming = true;
          const life = new AbortController();
          let deadline: ReturnType<typeof setTimeout> | undefined;
          let stop: {remove(): void} | undefined, disposal: Promise<void> | undefined;
          const dispose = () => {
            if (!disposal) disposal = (releasing ?? Promise.resolve().then(releaseNative)).finally(() => {transferred.delete(dispose); retained.delete(release);});
            if (!claimed) releasing = disposal;
            life.abort(); stop?.remove(); stop = undefined;
            return disposal;
          };
          cancelClaim = () => {void dispose().catch(() => {});};
          const cancellation = new Promise<false>(resolve => {life.signal.addEventListener('abort', () => resolve(false), {once: true});});
          try {
            // Register before native admission; expiry/background may beat its promise.
            stop = new NativeEventEmitter(bridge as never).addListener('PorticoVideoSnapshotRetired', value => {
              if (value?.id === id) void dispose().catch(() => {});
            });
            deadline = setTimeout(cancelClaim, 2200); // Same bounded native-operation wait as capture; never renews file TTL.
            const accepted = await Promise.race([bridge.retainForExit(id, source), cancellation]);
            if (accepted !== true || life.signal.aborted || releasing || signal?.aborted || !current(source)) {
              void dispose().catch(() => {}); return;
            }
            claimed = true; retained.delete(release); transferred.add(dispose);
            return Object.freeze({image, signal: life.signal, release: dispose});
          } catch {void dispose().catch(() => {}); return;}
          finally {claiming = false;cancelClaim = undefined;if (deadline !== undefined) clearTimeout(deadline);}
        };
        return Object.freeze({source, image, release, retainForExit});
      } catch (error) {
        // Cleanup is queued even when the UI/native bridge is unresponsive; it cannot delay cancellation.
        void releaseNative().catch(() => {});
        if (error instanceof VideoSnapshotError) throw error;
        throw new VideoSnapshotError('snapshot_unavailable');
      } finally {
        if (timer !== undefined) clearTimeout(timer);
        signal?.removeEventListener('abort', onAbort);
        if (pending === flight) pending = undefined;
      }
    },
  };
}
