import type {Item, Facets} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {readingItem} from './reading-lists-rebuilt.ts';
import {ReadingError} from './reading.ts';
import type {ReadingPeriodicalType, ReadingPeriodicalGroup, ReadingPeriodicalIssue, ReadingPeriodicalGroupsPage, ReadingPeriodicalIssuesPage} from './reading.ts';

type Options = Readonly<{cursor?: string; limit?: number; issueDate?: string; year?: string; month?: string; order?: 'oldest' | 'newest'}>;
const date = (value: string | undefined) => !!value && /^\d{4}-\d{2}(-\d{2})?$/.test(value) && !value.startsWith('0000') && Number.isFinite(Date.parse(value.length === 7 ? value + '-01' : value)) && new Date(value.length === 7 ? value + '-01' : value).toISOString().startsWith(value);
function issue(wire: Item, kind: ReadingPeriodicalType, group?: Readonly<{id: string; title: string}>): ReadingPeriodicalIssue | undefined {
 const item = readingItem(wire);
 if (!item || !date(wire.releaseDate) || kind === 'newspaper' && wire.releaseDate!.length !== 10) return undefined;
 return {item: {...item, publicationType: {value: kind, source: 'file'}, ...(group ? {series: {id: group.id, name: group.title, source: 'file'}} : {})}, issueDate: wire.releaseDate!};
}
export async function rebuiltPeriodicalGroups(api: ServerRequester, library: string, kind: ReadingPeriodicalType, options: Options, signal?: AbortSignal): Promise<ReadingPeriodicalGroupsPage> {
 const page = await callServer(api, 'GET /v1/items', {query: {library, kind: 'series', publication: kind, sort: 'title', cursor: options.cursor, limit: options.limit ?? 24}, signal});
 const groups = await Promise.all((page.items ?? []).map(async group => {
  const page = await callServer(api, 'GET /v1/items', {query: {library, series: group.id, kind: 'book,comic,issue', publication: kind, sort: 'release', order: 'desc', limit: 1, total: true}, signal});
  const latest = page.items?.[0] && issue(page.items[0], kind, group);
  return latest ? {id: group.id, name: group.title, issueCount: page.total ?? 1, latest} : undefined;
 }));
 return {libraryId: library, publicationType: kind, groups: groups.filter((v): v is ReadingPeriodicalGroup => !!v), ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})};
}
export async function rebuiltPeriodicalIssues(api: ServerRequester, library: string, series: string, kind: ReadingPeriodicalType, options: Options, signal?: AbortSignal) {
 if (options.year && !/^\d{4}$/.test(options.year) || options.month && !/^\d{4}-\d{2}$/.test(options.month) || options.issueDate && !date(options.issueDate)) throw new Error('invalid_request');
 const prefix = options.month ?? options.year;
 const released = options.issueDate ?? (prefix ? `${prefix}..${prefix}` : undefined);
 const query = {library, series, publication: kind, kind: 'book,comic,issue'};
 const [page, years, days, group] = await Promise.all([
  callServer(api, 'GET /v1/items', {query: {...query, released, sort: 'release', order: options.order === 'oldest' ? 'asc' : 'desc', cursor: options.cursor, limit: options.limit ?? 50}, signal}),
  callServer(api, 'GET /v1/facets', {query: {...query, facet: 'issueYear'}, signal}),
  options.month ? callServer(api, 'GET /v1/facets', {query: {...query, released: `${options.month}-01..${options.month}-31`, facet: 'issueDay'}, signal}) : Promise.resolve({facet: 'issueDay', values: []} as Facets),
  callServer(api, 'GET /v1/items/{id}', {id: series, signal}),
 ]);
 if (!Array.isArray(page.items)) throw new ReadingError('invalid_document');
 for (let i = 1; i < page.items.length; i++) {
  const before = page.items[i-1].releaseDate ?? '', after = page.items[i].releaseDate ?? '';
  if (options.order === 'oldest' ? before > after : before < after) throw new ReadingError('invalid_document');
 }
 const items = (page.items ?? []).flatMap(wire => {const found = issue(wire, kind, group); return found ? [found] : [];});
 const yearChoices = (years.values ?? []).map(v => ({value: v.value, count: v.count}));
 const dayChoices = (days.values ?? []).map(v => ({value: v.value, count: v.count})).sort((a,b) => a.value.localeCompare(b.value));
 const out: ReadingPeriodicalIssuesPage = {libraryId: library, seriesId: series, publicationType: kind, items,
  years: yearChoices.map(v => ({year: v.value, issueCount: v.count})), days: dayChoices.map(v => ({date: v.value, issueCount: v.count})), ...options, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})};
 return {...out, yearChoices, dayChoices, calendarMonth: options.month};
}
