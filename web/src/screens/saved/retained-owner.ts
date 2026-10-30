import type {HttpLocalApi} from '@core/index.ts';
import {createSavedRuntime} from './runtime';
import {sameSavedDownloadsScope, type SavedDownloadsScope, type SavedDownloadsPort} from './downloads-port';
import type {SaveReadyPreparation} from './ready-downloads';
import {savedDownloadsBinder, subscribeSavedDownloadsBinder, type SavedDownloadsBinding} from './native-binding';

type NativeState = Readonly<{localDownloads?: SavedDownloadsPort; saveReadyPreparation?: SaveReadyPreparation}>;
const empty: NativeState = Object.freeze({});
/** Route outlets borrow this owner. Only the protected captured viewer retires
 * its service, READY observer and optional native lease. Construction is inert. */
export function createRetainedSavedOwner(input: Readonly<{
  api: HttpLocalApi; scope: SavedDownloadsScope; deviceId?: string; isCurrent: () => boolean; reportError(error: unknown): void;
}>) {
  const life = new AbortController(), listeners = new Set<() => void>();
  const current = () => !life.signal.aborted && input.isCurrent();
  const runtime = createSavedRuntime({...input, isCurrent: current});
  let state = empty, native: SavedDownloadsBinding | undefined, pending = false, started = false;
  let offPlatform: (() => void) | undefined, offLocal: (() => void) | undefined;
  const publish = (value: NativeState) => {state = value;for (const listener of [...listeners]) listener();};
  const installObserver = () => {
    if (current() && native && state.saveReadyPreparation && native.localDownloads.getSnapshot().available)
      runtime.installReadyDownloads(state.saveReadyPreparation, error => {if (current()) input.reportError(error);});
  };
  const bind = async () => {
    const factory = savedDownloadsBinder();
    if (!factory || pending || native || !current()) return;
    pending = true;
    try {
      const value = await factory({runtime, api: input.api, isCurrent: current, signal: life.signal});
      if (!current()) {value.dispose();return;}
      if (!sameSavedDownloadsScope(runtime.scope, value.localDownloads.scope)) {value.dispose();throw new Error('saved_download_scope_mismatch');}
      native = value;
      const saveReadyPreparation: SaveReadyPreparation = async (preparation, signal) => {
        const active = AbortSignal.any([life.signal, signal]);active.throwIfAborted();
        if (!current()) throw new DOMException('Saved authority changed', 'AbortError');
        await value.saveReadyPreparation(preparation, active);
        active.throwIfAborted();if (!current()) throw new DOMException('Saved authority changed', 'AbortError');
      };
      publish(Object.freeze({localDownloads: value.localDownloads, saveReadyPreparation}));
      offLocal = value.localDownloads.subscribe(installObserver);installObserver();
    } catch (error) {if (current()) input.reportError(error);}
    finally {pending = false;}
  };
  return {
    runtime,
    getSnapshot: () => current() ? state : empty,
    subscribe(listener: () => void) {listeners.add(listener);return () => {listeners.delete(listener);};},
    start() {
      if (started || !current()) return;started = true;
      offPlatform = subscribeSavedDownloadsBinder(() => {void bind();});void bind();
    },
    dispose() {
      if (life.signal.aborted) return;
      life.abort();offPlatform?.();offLocal?.();
      // Cancel the one observer before releasing its actual native coordinator.
      runtime.dispose();native?.dispose();native = undefined;publish(empty);listeners.clear();
    },
  };
}
export type RetainedSavedOwner = ReturnType<typeof createRetainedSavedOwner>;
