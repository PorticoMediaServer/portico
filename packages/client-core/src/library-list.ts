import {firstOfEach, keepReadable} from './tolerant.ts';
import {unreadableServerResponse} from './server-messages.ts';
import type {Library} from './index.ts';

/**
 * The viewer's libraries (`GET /v1/libraries`). The answer has to carry a list. A library without
 * an id or a name is left out alone and a repeated one keeps its first appearance, so one bad row
 * never empties the rail. A kind this app does not know is kept as it is and shown as a plain
 * library; a missing one reads as no kind. Anything else the server sends with a row is carried
 * through untouched.
 */
/** Who the viewer is and what they may see, as the directory answers it (client data step 5):
 *  the viewer fence moves when a library is added or removed, the privacy fence only when the
 *  viewer or what they may see changes. The shell keys itself on the privacy fence. */
export type LibraryDirectoryFences = Readonly<{viewerFence: string; privacyFence: string}>;
const fence = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256 && !/[\x00-\x1f\x7f]/.test(value);

export function parseLibraryList(raw: unknown): {items: Library[]; fences?: LibraryDirectoryFences} {
  const items = (raw as {items?: unknown} | null)?.items;
  if (!raw || typeof raw !== 'object' || !Array.isArray(items)) throw new Error(unreadableServerResponse);
  const readable = keepReadable('libraries', 'items', items, row => {
    const r = row as Record<string, unknown> | null;
    if (!r || typeof r !== 'object' || Array.isArray(r) || typeof r.id !== 'string' || !r.id || typeof r.name !== 'string' || !r.name) return undefined;
    return (typeof r.kind === 'string' ? r : {...r, kind: ''}) as Library;
  });
  const r = raw as {viewerFence?: unknown; privacyFence?: unknown};
  const fences = fence(r.viewerFence) && fence(r.privacyFence) ? Object.freeze({viewerFence: r.viewerFence, privacyFence: r.privacyFence}) : undefined;
  return {items: firstOfEach('libraries', 'items', readable, row => row.id), ...(fences ? {fences} : {})};
}
