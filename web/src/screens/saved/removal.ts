import type {SavedReceipt} from '@core/saved.ts';

export type PlaylistRemovalUndo = Readonly<{token: string; until: number; title: string}>;

/** A prior receipt or another occurrence's token cannot undo this removal. */
export function playlistRemovalUndo(receipt: SavedReceipt | null, expected: Readonly<{serverId: string; viewerFence: string; playlistId: string; entryId: string}>, title: string, now = Date.now()): PlaylistRemovalUndo | undefined {
  if (!receipt || receipt.deleted || receipt.serverId !== expected.serverId || receipt.viewerFence !== expected.viewerFence || receipt.playlistId !== expected.playlistId || receipt.entryId !== expected.entryId || !receipt.undoToken || !receipt.undoUntil) return undefined;
  const until = Date.parse(receipt.undoUntil);
  return Number.isFinite(until) && until > now ? Object.freeze({token: receipt.undoToken, until, title}) : undefined;
}
