import {setItemState} from './items.ts';
import type {ServerRequester} from './query/server-api.ts';
/**
 * Remove from Continue Watching: `PUT /v1/items/{id}/state {continueDismissed: true}` takes the title off this
 * profile's Continue Watching without marking it watched or touching its resume point; `false` puts it back (Undo).
 * The next playback of the title makes it eligible again. Of two edits to it, the later one wins.
 */
export type ContinueDismissalInput = Readonly<{
  itemId: string;
  dismissed: boolean;
  /** Kept for callers from the old server; the rebuilt server keeps no revisions. */
  revision?: number;
  operationId?: () => string | Promise<string>;
  signal?: AbortSignal;
}>;

/** The personal revision in a detail or a personal-state receipt (`{personal: {revision}}`), from the old server. */
export function personalRevision(raw: unknown): number | undefined {
  const r = (raw as {personal?: {revision?: unknown}} | null)?.personal?.revision;
  return typeof r === 'number' && Number.isSafeInteger(r) && r >= 0 ? r : undefined;
}

/** Dismisses (or restores) one title on this profile's Continue Watching. The revision is always 1: the later edit wins. */
export async function setContinueDismissed(api: ServerRequester, input: ContinueDismissalInput): Promise<{revision: number}> {
  if (!input.itemId) throw new Error('A title is required.');
  await setItemState(api, input.itemId, {continueDismissed: input.dismissed}, input.signal);
  return {revision: 1};
}
