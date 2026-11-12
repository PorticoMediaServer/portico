import type {BrowseFacetPage} from '../browse.ts';
import {callServer, type ServerRequester} from './server-api.ts';

/** A search reads at most this many names before it says its count is only a lower bound, so a
 * keystroke in a library of tens of thousands of authors never reads all of them. */
const searchedAtMost = 2000;
const page = 200;

type Choice = {value: string; label: string; count: number; seriesId?: string};
type Answer = Readonly<{choices: Choice[]; read: number; nextCursor?: string; total?: number}>;

/** One page of a library's authors or series, as choices. */
async function choices(api: ServerRequester, libraryId: string, field: 'author' | 'series', cursor: string | undefined, total: boolean, signal?: AbortSignal): Promise<Answer> {
  const query = {library: libraryId, limit: page, cursor, ...(total ? {total: true} : {})};
  if (field === 'author') {
    const answer = await callServer(api, 'GET /v1/people', {query: {...query, role: 'author'}, signal});
    const rows = Array.isArray(answer?.items) ? answer.items : [];
    return {read: rows.length, nextCursor: answer?.nextCursor, total: answer?.total,
      choices: rows.flatMap(row => typeof row?.id === 'string' && row.id && typeof row.name === 'string' && row.name ? [{value: row.id, label: row.name, count: row.titleCount ?? 0}] : [])};
  }
  const answer = await callServer(api, 'GET /v1/items', {query: {...query, kind: 'series', sort: 'title'}, signal});
  const rows = Array.isArray(answer?.items) ? answer.items : [];
  return {read: rows.length, nextCursor: answer?.nextCursor, total: answer?.total,
    choices: rows.flatMap(row => typeof row?.id === 'string' && row.id && typeof row.title === 'string' && row.title
      ? [{value: row.id, label: row.title, count: row.counts?.children ?? 0, seriesId: row.id}] : [])};
}

/**
 * A Read library's author or series choices on the rebuilt server: its authors from the people
 * list (`GET /v1/people?library=&role=author`, by name, each with how many of its titles the
 * profile may see), its series from the item list (`kind=series`, by title, each with its
 * volumes). A choice's value is what the list's filter takes: a person's id, a series' id.
 */
export async function readRebuiltReadingFacet(api: ServerRequester,
  options: Readonly<{libraryId: string; field: 'author' | 'series'; q?: string; limit: number; offset?: number}>, signal?: AbortSignal): Promise<BrowseFacetPage> {
  const offset = options.offset ?? 0, wanted = (options.q ?? '').trim().toLocaleLowerCase(), need = offset + options.limit;
  const values: Choice[] = [];
  let cursor: string | undefined, total: number | undefined, read = 0;
  do {
    const answer = await choices(api, options.libraryId, options.field, cursor, total === undefined && !wanted, signal);
    if (total === undefined && typeof answer.total === 'number') total = answer.total;
    values.push(...answer.choices.filter(choice => !wanted || choice.label.toLocaleLowerCase().includes(wanted)));
    cursor = answer.nextCursor || undefined; read += answer.read;
  } while (cursor && values.length < need && (!wanted || read < searchedAtMost));
  return Object.freeze({field: options.field, total: wanted || total === undefined ? values.length : total,
    values: Object.freeze(values.slice(offset, need)), ...(wanted && cursor ? {totalIsLowerBound: true} : {})}) as BrowseFacetPage;
}
