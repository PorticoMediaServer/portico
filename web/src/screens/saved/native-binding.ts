import type {HttpLocalApi} from '@core/index.ts';
import type {SavedRuntime} from './runtime';
import type {SavedDownloadsPort} from './downloads-port';
import type {SaveReadyPreparation} from './ready-downloads';

export type SavedDownloadsBinderInput = Readonly<{
  runtime: SavedRuntime; api: HttpLocalApi; isCurrent: () => boolean; signal: AbortSignal;
}>;
export type SavedDownloadsBinding = Readonly<{
  localDownloads: SavedDownloadsPort;
  /** Resolves on the native queue's acknowledgement, never on fabricated file completion. */
  saveReadyPreparation: SaveReadyPreparation;
  /** Owns the native port/coordinator lease; never deletes persisted downloads. */
  dispose(): void;
}>;
export type SavedDownloadsBinder = (input: SavedDownloadsBinderInput) => Promise<SavedDownloadsBinding>;
let installed: SavedDownloadsBinder | undefined;
const listeners = new Set<() => void>();
/** Inert platform installation. Existing captured leases live until their
 * viewer retires; app teardown retires those viewers before removing the platform. */
export function installSavedDownloadsBinder(binder: SavedDownloadsBinder): () => void {
  if (installed) throw new Error('saved_downloads_binder_already_installed');
  installed = binder;for (const listener of [...listeners]) listener();
  return () => {if (installed === binder) {installed = undefined;for (const listener of [...listeners]) listener();}};
}
export const savedDownloadsBinder = () => installed;
export function subscribeSavedDownloadsBinder(listener: () => void): () => void {
  listeners.add(listener);return () => {listeners.delete(listener);};
}
