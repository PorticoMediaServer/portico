import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath, routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Viewer suggestions and per-item related rows. Reasons, ranking and row shape are server-authored. */
import {validateContentEntry, type ContentEntry} from './library-content.ts';
import {validateHomeRow, type HomeApi, type HomeRevision, type HomeRow} from './home.ts';
import {firstOfEach, keepReadable, noteSkipped} from './tolerant.ts';

export type Suggestion = Readonly<{entry: ContentEntry; reason: string; source: string; score: number}>;
export type Suggestions = Readonly<{items: readonly Suggestion[]; total: number; revision: HomeRevision; generatedAt: string}>;
export type ItemRecommendations = Readonly<{itemId: string; rows: readonly HomeRow[]; revision: HomeRevision}>;

const object = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const text = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length > 0 && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const whole = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;

function invalid(): never {
  throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_recommendations', retryable: false});
}

function revision(value: unknown): HomeRevision {
  if (!object(value) || !whole(value.catalog) || !whole(value.viewer)) invalid();
  return Object.freeze({catalog: value.catalog, viewer: value.viewer});
}

/** Text a suggestion is drawn with: absent reads as empty, and text over its bound refuses the suggestion. */
function words(value: unknown, max: number): string {
  if (value === undefined || value === null || value === '') return '';
  if (!text(value, max)) invalid();
  return value;
}

/**
 * Suggestions. The revision and the bound of 100 are exact. A suggestion whose entry cannot be read
 * is left out alone, a repeated entry keeps its first appearance, a missing reason or source is
 * empty, and a score outside 0–1 reads as 0.
 */
export function validateSuggestions(value: unknown): Suggestions {
  if (!object(value) || !Array.isArray(value.items) || value.items.length > 100) invalid();
  const items = firstOfEach('recommendations', 'suggestions', keepReadable('recommendations', 'suggestions', value.items, raw => {
    if (!object(raw)) invalid();
    const score = typeof raw.score === 'number' && Number.isFinite(raw.score) && raw.score > 0 && raw.score <= 1 ? raw.score : 0;
    return Object.freeze({entry: validateContentEntry(raw.entry), reason: words(raw.reason, 1024), source: words(raw.source, 128), score});
  }), item => item.entry.id);
  // The total is at least the suggestions kept.
  return Object.freeze({items: Object.freeze(items), total: Math.max(whole(value.total) ? value.total : 0, items.length), revision: revision(value.revision), generatedAt: words(value.generatedAt, 128)});
}

/**
 * A title's rows: a row that cannot be read is left out alone, a repeated row keeps its first
 * appearance, an entry that is the title itself is left out, and a row with nothing left is not shown.
 */
function rowsFor(list: readonly unknown[], self: string): HomeRow[] {
  return firstOfEach('recommendations', 'rows', keepReadable('recommendations', 'rows', list, raw => {
    const row = validateHomeRow(raw);
    const entries = row.entries.filter(entry => entry.id !== self);
    if (!entries.length) return undefined;
    if (entries.length === row.entries.length) return row;
    noteSkipped('recommendations', 'entries', row.entries.length - entries.length);
    return Object.freeze({...row, entries: Object.freeze(entries)});
  }), row => row.id);
}

export function validateItemRecommendations(value: unknown, itemId: string): ItemRecommendations {
  if (!object(value) || value.itemId !== itemId || !Array.isArray(value.rows) || value.rows.length > 12) invalid();
  return Object.freeze({itemId, rows: Object.freeze(rowsFor(value.rows, itemId)), revision: revision(value.revision)});
}

/**
 * A title page's recommendation rows (Recommendations P4): every row the endpoint returned, in
 * the server's order (by `priority`): the engine's rows first (More like X, Starring, From its
 * creator, Viewers also watched), then the director, the genre and person rows and the
 * collections. Nothing is filtered or reordered here; an empty row has nothing to show. Name each
 * with its `titleText`.
 */
export function titleRecommendationRows(recommendations: Readonly<{rows: readonly HomeRow[]}> | null | undefined): readonly HomeRow[] {
  return (recommendations?.rows ?? []).filter(row => row.entries.length > 0);
}

/**
 * A movie's "More like this" row from its recommendations: the library-local
 * similar-titles row (relation `more_like`, formerly `because_you_watched`) under the given
 * heading, else the server's first row under its own title; undefined when
 * there is nothing to show.
 */
export function moreLikeThisRow(recommendations: ItemRecommendations | null | undefined, heading: string): {id: string; title: string; entries: readonly ContentEntry[]} | undefined {
  const rows = recommendations?.rows ?? [];
  const similar = rows.find(row => row.relation === 'more_like' || row.relation === 'because_you_watched');
  const row = similar ?? rows[0];
  if (!row || !row.entries.length) return undefined;
  return {id: row.id, title: similar ? heading : row.title, entries: row.entries};
}

export type ShowRecommendations = Readonly<{showId: string; rows: readonly HomeRow[]; revision: HomeRevision}>;

/** A show page's rows (`GET /v1/shows/{id}/recommendations`): More like the show, From its creator… */
export function validateShowRecommendations(value: unknown, showId: string): ShowRecommendations {
  if (!object(value) || value.showId !== showId || !Array.isArray(value.rows) || value.rows.length > 12) invalid();
  return Object.freeze({showId, rows: Object.freeze(rowsFor(value.rows, showId)), revision: revision(value.revision)});
}

export async function fetchShowRecommendations(api: HomeApi, showId: string, limit?: number, signal?: AbortSignal): Promise<ShowRecommendations> {
  const path = encodedRoutePath("get_shows_id_recommendations", {"id": encodeURIComponent(showId)}) + (limit === undefined ? '' : '?limit=' + limit);
  return validateShowRecommendations(await api.request<unknown>(path, 'GET', undefined, signal), showId);
}

export async function fetchSuggestions(api: HomeApi, limit?: number, signal?: AbortSignal): Promise<Suggestions> {
  return validateSuggestions(await api.request<RouteResponse<"get_suggestions">>(routePath("get_suggestions") + (limit === undefined ? '' : '?limit=' + limit), 'GET', undefined, signal));
}

export async function fetchItemRecommendations(api: HomeApi, itemId: string, limit?: number, signal?: AbortSignal): Promise<ItemRecommendations> {
  const path = encodedRoutePath("get_items_id_recommendations", {"id": encodeURIComponent(itemId)}) + (limit === undefined ? '' : '?limit=' + limit);
  return validateItemRecommendations(await api.request<unknown>(path, 'GET', undefined, signal), itemId);
}
