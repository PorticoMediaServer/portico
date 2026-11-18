import {unreadableServerResponse} from './server-messages.ts';
import type {ContentEntry} from './library-content.ts';
import {validateHomeRow, type HomeApi, type HomeRevision, type HomeRow} from './home.ts';
import {firstOfEach, keepReadable, noteSkipped} from './tolerant.ts';
import {entriesFromItems, readRelatedItems} from './items.ts';
import type {Related} from '../../contracts/src/next/api.ts';

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
  return (recommendations?.rows ?? []).filter(row => row.entries.length > 0 || !!row.nextCursor);
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

/** A show's page reads the same related groups as a film's: shows are titles on the server. */
export async function fetchShowRecommendations(api: HomeApi, showId: string, limit?: number, signal?: AbortSignal): Promise<ShowRecommendations> {
  const rows = relatedRows(await readRelatedItems(api, showId, signal, {limit}), showId, limit);
  return Object.freeze({showId, rows, revision: unversioned});
}

/** A title's More like this, cast, creator and genre rows from the server's related groups. */
export async function fetchItemRecommendations(api: HomeApi, itemId: string, limit?: number, signal?: AbortSignal): Promise<ItemRecommendations> {
  return itemRecommendationsFromRelated(await readRelatedItems(api, itemId, signal, {limit}), itemId, limit);
}

/** The server's answer carries no revision; nothing compares one for these rows. */
const unversioned: HomeRevision = Object.freeze({catalog: 0, viewer: 0});

/** Map the title-page groups in server order. Other ties (extras, collection members) have their own page sections. */
function relatedRows(value: Related, self: string, limit?: number): readonly HomeRow[] {
  if (!object(value) || !Array.isArray(value.groups)) invalid();
  const rows: HomeRow[] = [];
  value.groups.forEach((group, priority) => {
    if (!object(group) || !['more_like_this', 'cast', 'creator', 'genre'].includes(group.relation)) return;
    try {
      if (!Array.isArray(group.items)) invalid();
      let id: string, title: string, relation: string;
      let titleText: HomeRow['titleText'];
      if (group.relation === 'more_like_this') {
        id = 'more_like_this'; title = 'More like this'; relation = 'more_like';
      } else if (group.relation === 'cast' || group.relation === 'creator') {
        if (!object(group.person) || !text(group.person.id) || !text(group.person.name)) invalid();
        id = group.relation + ':' + group.person.id;
        relation = group.relation === 'cast' ? 'starring' : 'creator';
        title = (group.relation === 'cast' ? 'Starring ' : 'From ') + group.person.name;
        titleText = {code: group.relation === 'cast' ? 'home.row.starring' : 'home.row.fromCreator', params: {name: group.person.name}, fallback: title};
      } else if (group.relation === 'genre') {
        if (!object(group.tag) || !text(group.tag.value) || !text(group.tag.label)) invalid();
        id = 'genre:' + group.tag.value; title = group.tag.label; relation = 'genre';
      } else return;
      let entries = firstOfEach('recommendations', 'entries', entriesFromItems(group.items).filter(entry => entry.id !== self), entry => entry.id);
      if (group.relation !== 'more_like_this' && limit !== undefined && limit >= 0) entries = entries.slice(0, limit);
      const nextCursor = group.relation === 'more_like_this' && typeof group.nextCursor === 'string' ? group.nextCursor : '';
      if (!entries.length && !nextCursor) return;
      rows.push(Object.freeze({id, title, ...(titleText ? {titleText} : {}), kind: group.relation, relation, artworkShape: 'poster' as const,
        endpoint: '', privacySensitivity: '', policyState: '', priority, required: false, hideable: false, reorderable: false, critical: false, cursorCapable: !!nextCursor,
        entries: Object.freeze(entries), start: 0, limit: entries.length, hasMore: !!nextCursor, nextCursor}));
    } catch {
      if (group.relation !== 'more_like_this') noteSkipped('recommendations', 'rows');
    }
  });
  return Object.freeze(firstOfEach('recommendations', 'rows', rows, row => row.id));
}

export function itemRecommendationsFromRelated(value: Related, itemId: string, limit?: number): ItemRecommendations {
  return Object.freeze({itemId, rows: relatedRows(value, itemId, limit), revision: unversioned});
}
