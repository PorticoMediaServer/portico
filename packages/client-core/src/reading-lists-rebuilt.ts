/**
 * A Read library's lists from the rebuilt server, in the shapes the reading screens already read
 * (`ReadingItem`, `ReadingContinueItem`). The rebuilt server has no reading list routes: every
 * list is the item list or a landing or Home row, and this file turns those answers into the
 * reader's rows.
 *
 * - The library's titles: `GET /v1/items?library=&kind=book,comic,issue`, by title.
 * - Recently added: the same, by date added.
 * - Continue reading: the library's landing row `continue`, or Home's `continue_reading`.
 * - A series' volumes: `GET /v1/items?series=`, in volume order: a book series and a comic series alike.
 *
 * A row names no version: the book's page and the reader ask the book route, which picks the
 * server's choice. A row's format is known only from a place made in it.
 */
import type {Home, Item as WireItem, ItemPage} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import type {ReadingContinueItem, ReadingItem} from './reading.ts';

/** Every readable title in a library: books, single comics and comic issues. */
const readable = 'book,comic,issue';
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 4096): v is string => typeof v === 'string' && v.length > 0 && v.length <= max;

/** The cover the server keeps for a title, under its own picture address. */
const cover = (item: WireItem): string | undefined => {
  const value = item.images?.poster ?? item.images?.cover;
  return typeof value === 'string' && /^\/v1\/images\/[A-Za-z0-9_-]{1,128}$/.test(value) ? value : undefined;
};

/** A place the reader saved: the reader's own notation with how far through and the file it was made in. */
type Place = Readonly<{format: string; progression: number; cfi?: string; section?: string; pageIndex?: number; fingerprint?: string}>;
function place(raw: unknown): Place | undefined {
  if (!text(raw, 4096)) return undefined;
  let value: unknown;
  try { value = JSON.parse(raw); } catch { return undefined; }
  if (!object(value) || !text(value.format, 16) || typeof value.progression !== 'number' || !(value.progression >= 0 && value.progression < 1)) return undefined;
  return Object.freeze({format: value.format, progression: value.progression,
    ...(text(value.cfi) ? {cfi: value.cfi} : {}), ...(text(value.section, 1024) ? {section: value.section} : {}),
    ...(Number.isSafeInteger(value.pageIndex) && (value.pageIndex as number) >= 0 ? {pageIndex: value.pageIndex as number} : {}),
    ...(text(value.fingerprint, 64) ? {fingerprint: value.fingerprint} : {})});
}

/** One title as the reading screens draw it. Anything that is not a readable title is left out. */
export function readingItem(item: WireItem, format = ''): ReadingItem | undefined {
  if (!object(item) || !text(item.id, 128) || !text(item.title) || !text(item.libraryId, 128) || !['book', 'comic', 'issue'].includes(item.kind)) return undefined;
  const coverUrl = cover(item);
  const publicationType = (item as WireItem & {publicationType?: string}).publicationType;
  return Object.freeze({id: item.id, libraryId: item.libraryId, title: item.title, format, sourceId: item.id, inventoryAvailable: true,
    ...(coverUrl ? {coverUrl} : {}),
    ...(publicationType ? {publicationType: {value: publicationType, source: 'file'}} : {}),
    readingMark: Object.freeze({itemId: item.id, read: item.state?.watched === true, revision: 0})});
}

const items = (raw: readonly WireItem[] | undefined) => (Array.isArray(raw) ? raw : []).flatMap(item => {const row = readingItem(item); return row ? [row] : [];});

/** A page of a library's titles, or of its most recently added. */
export async function rebuiltReadingPage(api: ServerRequester, options: Readonly<{libraryId?: string; cursor?: string; limit: number; sort?: 'title' | 'added'}>, signal?: AbortSignal):
  Promise<Readonly<{items: ReadingItem[]; nextCursor?: string}>> {
  const page = await callServer(api, 'GET /v1/items', {signal, query: {library: options.libraryId, kind: readable, limit: options.limit, cursor: options.cursor,
    ...(options.sort === 'added' ? {sort: 'added', order: 'desc'} : {})}}) as ItemPage;
  return {items: items(page?.items), ...(text(page?.nextCursor, 4096) ? {nextCursor: page.nextCursor} : {})};
}

/** A series' volumes, in volume order. */
export async function rebuiltReadingSeries(api: ServerRequester, seriesId: string, options: Readonly<{cursor?: string; limit: number}>, signal?: AbortSignal):
  Promise<Readonly<{items: ReadingItem[]; nextCursor?: string}>> {
  const page = await callServer(api, 'GET /v1/items', {signal, query: {series: seriesId, sort: 'order', limit: options.limit, cursor: options.cursor}}) as ItemPage;
  return {items: items(page?.items), ...(text(page?.nextCursor, 4096) ? {nextCursor: page.nextCursor} : {})};
}

/** The titles in progress, most recent first: a library's own Continue reading, or Home's
 * Continue your books. Each carries its place; one with no readable place is left out. */
export async function rebuiltReadingContinue(api: ServerRequester, libraryId: string | undefined, signal?: AbortSignal): Promise<ReadingContinueItem[]> {
  const home = (libraryId
    ? await callServer(api, 'GET /v1/libraries/{id}/landing', {id: libraryId, signal})
    : await callServer(api, 'GET /v1/home', {signal})) as Home;
  const row = (Array.isArray(home?.rows) ? home.rows : []).find(entry => entry?.id === (libraryId ? 'continue' : 'continue_reading'));
  const out: ReadingContinueItem[] = [];
  for (const wire of Array.isArray(row?.items) ? row.items : []) {
    const saved = place(wire?.state?.position), item = saved && readingItem(wire, saved.format);
    if (!saved || !item) continue;
    const location = Object.freeze({format: saved.format, progression: saved.progression,
      ...(saved.cfi !== undefined ? {cfi: saved.cfi} : {}), ...(saved.section !== undefined ? {section: saved.section} : {}),
      ...(saved.pageIndex !== undefined ? {pageIndex: saved.pageIndex} : {})});
    out.push(Object.freeze({item, progress: Object.freeze({location, sourceId: item.sourceId, sourceRevision: saved.fingerprint ?? '', revision: 1,
      observedAt: Date.parse(wire.state?.lastPlayedAt ?? '') || 1, stale: false})}) as ReadingContinueItem);
  }
  return out;
}
