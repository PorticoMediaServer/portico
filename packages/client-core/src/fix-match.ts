import type {MatchCandidate, MatchResult, UnmatchedEntry, UnmatchedEntryPage} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

/**
 * Fix match on the rebuilt server (definition area 4, rules 12 to 17 and 13a): a library's
 * Unmatched items, the matches a title could have, choosing one, Keep as is, and Look it up
 * again. Every lookup is the server's; an app never asks a provider itself (rule 7).
 */
export type {MatchCandidate, MatchResult, UnmatchedEntry, UnmatchedEntryPage};
export type EpisodeOrder = 'aired' | 'dvd' | 'absolute';
export const EPISODE_ORDERS: readonly EpisodeOrder[] = ['aired', 'dvd', 'absolute'];

/** One page of a library's Unmatched items, 25 at a time as the mock-up loads them. */
export function readUnmatched(api: ServerRequester, libraryId: string, cursor = '', signal?: AbortSignal): Promise<UnmatchedEntryPage> {
  return callServer(api, 'GET /v1/admin/libraries/{id}/unmatched', {id: libraryId, query: {limit: 25, cursor: cursor || undefined}, signal});
}

/** What to look a title up by: its own name and year (nothing given), a typed title, or an id or address. */
export type MatchLookup = Readonly<{query?: string; year?: number; ref?: string}>;

/** The titles a person can choose from, best first. Each call asks the provider. */
export async function findMatches(api: ServerRequester, itemId: string, lookup: MatchLookup = {}, signal?: AbortSignal): Promise<readonly MatchCandidate[]> {
  const answer = await callServer(api, 'GET /v1/admin/items/{id}/matches', {id: itemId, query: {q: lookup.query?.trim() || undefined, year: lookup.year, ref: lookup.ref?.trim() || undefined}, signal});
  return answer.items ?? [];
}

/** A typed id as the server reads it: an address or IMDb id as typed, a plain number as the named provider's. */
export function providerRef(typed: string, provider: string): string | undefined {
  const value = typed.trim();
  if (!value || value.length > 2048 || /[\x00-\x1f\s]/.test(value)) return undefined;
  return /^\d+$/.test(value) ? `${provider}:${value}` : value;
}

/** Uses a match. A show's episode order is kept with it through every refresh (rule 15). */
export function chooseMatch(api: ServerRequester, itemId: string, candidate: Pick<MatchCandidate, 'provider' | 'id'>, episodeOrder?: EpisodeOrder, signal?: AbortSignal): Promise<MatchResult> {
  return callServer(api, 'PUT /v1/admin/items/{id}/match', {id: itemId, body: {provider: candidate.provider, id: candidate.id, ...(episodeOrder ? {episodeOrder} : {})}, signal});
}

/** Keep as is: an unmatched title leaves Unmatched items (rule 13); a matched one loses its match (rule 13a). */
export function keepAsIs(api: ServerRequester, itemId: string, signal?: AbortSignal): Promise<MatchResult> {
  return callServer(api, 'PUT /v1/admin/items/{id}/match', {id: itemId, body: {keepAsIs: true}, signal});
}

/** Look it up again: a matched title is asked about again, an unmatched one is looked up from its name and year. */
export async function lookUpAgain(api: ServerRequester, itemId: string, signal?: AbortSignal): Promise<boolean> {
  return (await callServer(api, 'POST /v1/admin/items/{id}/metadata/refresh', {id: itemId, signal})).matched;
}
