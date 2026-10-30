/** The desktop's own completed downloads, offered on the unreachable launch page.
 *
 * Installed by the desktop host only when a validated persisted scope could bind
 * the native download store; the page shows the rows that play without a server
 * check-in and can open the folder. No player is mounted here and nothing calls
 * the server: in-app playback on this page is a separate, reviewed surface. */
import {useSyncExternalStore} from 'react';

export type OfflineDownloadRow = Readonly<{
  id: string;
  itemId: string;
  revision: number;
  quality: string;
  bytes: number;
  title: string;
  artist: string;
  kind: string;
  onServer: boolean;
}>;

export type OfflineDownloadsPort = Readonly<{
  subscribe: (listener: () => void) => () => void;
  snapshot: () => Readonly<{phase: 'loading' | 'ready' | 'unavailable'; items: readonly OfflineDownloadRow[]}>;
  openFolder: () => Promise<void>;
  /** Absent until the host's offline player surface is installed; the row then offers Play. */
  play?: (id: string) => Promise<void>;
  /** The page's one visible media surface. Null releases it and retires the local engine. */
  attachSurface?: (element: HTMLVideoElement | null) => void;
  /** What the launch page's local player is doing now. */
  playback?: () => Readonly<{phase: 'idle' | 'loading' | 'playing' | 'paused' | 'ended' | 'error'; itemId?: string}>;
  /** Retires the installed port and clears its snapshot (selection, family or horizon end). */
  retire?: () => void;
}>;

let installed: OfflineDownloadsPort | undefined;

/** Install (or clear) the launch-page port; returns its removal. */
export function installOfflineDownloads(next: OfflineDownloadsPort | undefined): () => void {
  installed = next;
  return () => { if (installed === next) installed = undefined; };
}

export function useOfflineDownloads(): OfflineDownloadsPort | undefined {
  return useSyncExternalStore(
    listener => installed ? installed.subscribe(listener) : () => {},
    () => installed,
    () => undefined,
  );
}

export const emptyOfflineDownloads = Object.freeze({phase: 'loading' as const, items: Object.freeze([]) as readonly OfflineDownloadRow[]});

/** Clear the installed port now; a sign-out or family end calls this synchronously. */
export function clearOfflineDownloads(): void {
  const port = installed;
  installed = undefined;
  try { port?.retire?.(); } catch { /* nothing left to retire */ }
}
