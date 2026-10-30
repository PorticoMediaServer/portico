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
export function parseLibraryList(raw: unknown): {items: Library[]} {
  const items = (raw as {items?: unknown} | null)?.items;
  if (!raw || typeof raw !== 'object' || !Array.isArray(items)) throw new Error(unreadableServerResponse);
  const readable = keepReadable('libraries', 'items', items, row => {
    const r = row as Record<string, unknown> | null;
    if (!r || typeof r !== 'object' || Array.isArray(r) || typeof r.id !== 'string' || !r.id || typeof r.name !== 'string' || !r.name) return undefined;
    return (typeof r.kind === 'string' ? r : {...r, kind: ''}) as Library;
  });
  return {items: firstOfEach('libraries', 'items', readable, row => row.id)};
}
