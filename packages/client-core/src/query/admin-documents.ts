/**
 * Owner and administrator documents in the viewer's cache (CLIENT-DATA-REVIEW §6 row 6). The role is
 * part of the cache's scope, so nothing read as an owner is ever shown to a member. A document read
 * here is shared by every owner that asks for it and shown at once when its screen is opened again,
 * for as long as nothing it was built from has moved and it is not older than `maxAgeMs`.
 */
import type {QueryKey} from '@tanstack/query-core';
import type {ViewerQueries} from './viewer-queries.ts';
import type {ViewerQueryOptions} from './react.ts';
import {until} from './until.ts';

/** Owner documents carry figures the server sends no hint for (load, uptime), so a held one is shown at once for this long only. */
export const adminDocumentMaxAgeMs = 30_000;

/** The cached document under `key`, when nothing it was built from has moved and it is not too old. */
export function heldDocument<T>(queries: ViewerQueries, key: QueryKey, maxAgeMs: number = adminDocumentMaxAgeMs, now: number = Date.now()): T | undefined {
  const state = queries.client.getQueryState<T>(key);
  if (!state || state.data === undefined || state.isInvalidated) return undefined;
  if (queries.documentFor(key)?.getState().stale === true) return undefined;
  return now - state.dataUpdatedAt <= maxAgeMs ? state.data : undefined;
}

/**
 * Read a document through the viewer's cache. `fresh`: something moved that a held copy may not
 * include (a hint, a write here, a poll), so the server is asked; otherwise a current held copy answers.
 */
export function readDocument<T>(queries: ViewerQueries, options: ViewerQueryOptions<T>, read: {fresh: boolean; signal?: AbortSignal; maxAgeMs?: number}): Promise<T> {
  if (!read.fresh) {
    const held = heldDocument<T>(queries, options.queryKey as QueryKey, read.maxAgeMs);
    if (held !== undefined) return Promise.resolve(held);
  }
  return until(queries.client.fetchQuery({...options, staleTime: 0}), read.signal);
}
