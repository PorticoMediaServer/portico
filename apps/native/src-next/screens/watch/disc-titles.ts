import type {DetailApi} from '../../../../../packages/client-core/src/detail';

/** The measured disc-title choices the server exposes for one physical version.
 * Only public facts: the asset's own version id and opaque selection ids. */
export type DiscTitleKind = 'bluray' | 'dvd';
export type DiscPackaging = 'iso' | 'folder';
export type DiscTitleOption = Readonly<{id: string; number: number; durationMs: number; chapterCount: number; main: boolean; ready: boolean; revision?: string}>;
export type DiscTitles = Readonly<{versionId: string; revision: string; kind: DiscTitleKind; packaging: DiscPackaging; ready: boolean; titles: readonly DiscTitleOption[]}>;
export type DiscTitleWrite = Readonly<{itemId: string; versionId: string; revision: string; titleId: string}>;
export type DiscTitleLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;

/** Bound from disc.MaxTitles on the server, which refuses more than this. */
export const MAX_DISC_TITLES = 4096;
/** The disc protocol's own title-length bounds: one minute to seven days. */
const MIN_TITLE_MS = 60_000, MAX_TITLE_MS = 7 * 24 * 60 * 60 * 1000;
/** The server publishes sha256 hex for both a title's opaque id and the revision. */
const HEX64 = /^[0-9a-f]{64}$/u;

const fail = (): never => {throw new Error('invalid_disc_titles');};
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
/** Only the named public fields are read; anything else the server sends is ignored. */
const closed = (value: unknown): Record<string, unknown> => record(value) ? value : fail();
const text = (value: unknown, max: number): string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\u0000-\u001f\u007f]/u.test(value) ? value : fail();
const integer = (value: unknown, min: number, max: number): number => typeof value === 'number' && Number.isSafeInteger(value) && value >= min && value <= max ? value : fail();
const asString = (value: unknown, max = 256): string => text(value, max);
const asHex = (value: unknown): string => typeof value === 'string' && HEX64.test(value) ? value : fail();
const asBoolean = (value: unknown): boolean => typeof value === 'boolean' ? value : fail();
const asList = (value: unknown): readonly unknown[] => Array.isArray(value) ? value : fail();

/** The GET /v1/items/{id}/disc-titles body, projected onto the server's public
 * facts. Unknown extension fields are ignored, not rejected. */
export function parseDiscTitles(raw: unknown, expectedVersionId: string): DiscTitles {
  const body = closed(raw);
  const versionId = asString(body.versionId), revision = asHex(body.revision);
  if (versionId !== expectedVersionId) fail();
  const kind: DiscTitleKind = body.kind === 'bluray' || body.kind === 'dvd' ? body.kind : fail();
  const packaging: DiscPackaging = body.packaging === 'iso' || body.packaging === 'folder' ? body.packaging : fail();
  const ready = asBoolean(body.ready), rawTitles = asList(body.titles);
  if (rawTitles.length > MAX_DISC_TITLES) fail();
  const seen = new Set<string>(), seenNumber = new Set<number>();
  const titles = rawTitles.map((value): DiscTitleOption => {
    const title = closed(value);
    const id = asHex(title.id);
    // The published number is the inventory ordinal, never a playlist number.
    const number = integer(title.number, 1, MAX_DISC_TITLES);
    if (seen.has(id) || seenNumber.has(number)) fail();
    seen.add(id); seenNumber.add(number);
    const main = asBoolean(title.main), rowReady = asBoolean(title.ready);
    const rowRevision = title.revision === undefined ? undefined : asHex(title.revision);
    return Object.freeze({id, number, durationMs: integer(title.durationMs, MIN_TITLE_MS, MAX_TITLE_MS),
      chapterCount: integer(title.chapterCount, 0, 65535), main, ready: rowReady,
      ...(rowRevision === undefined ? {} : {revision: rowRevision})});
  });
  // The server publishes exactly one chosen title; known zero chapters stay zero.
  if (titles.filter(title => title.main).length > 1) fail();
  return Object.freeze({versionId, revision, kind, packaging, ready, titles: Object.freeze(titles)});
}

const code = (error: unknown): string | undefined => {
  const value = (error as {code?: unknown} | null)?.code;
  return typeof value === 'string' ? value : undefined;
};
const current = (lifetime: DiscTitleLifetime) => !lifetime.signal.aborted && lifetime.isCurrent();

/** Owner read of the current choices for one physical version. The version is the
 * one the panel was opened with; a changed item or version never borrows it. */
export async function readDiscTitles(api: DetailApi, itemId: string, versionId: string, lifetime: DiscTitleLifetime): Promise<DiscTitles | undefined> {
  if (!current(lifetime)) return;
  const raw = await api.request<unknown>(`/v1/items/${encodeURIComponent(itemId)}/disc-titles?versionId=${encodeURIComponent(versionId)}`, undefined, undefined, lifetime.signal);
  if (!current(lifetime)) return;
  return parseDiscTitles(raw, versionId);
}

/** Device-scoped preparation of one alternate title. It analyzes that title without
 * changing the main title, the version's duration or personal state, and answers 202
 * with the document it re-read. A stale revision answers with the current document
 * instead, so the caller re-reads rather than writing over someone else's choice. */
export async function prepareDiscTitle(api: DetailApi, write: DiscTitleWrite, lifetime: DiscTitleLifetime): Promise<DiscTitles | undefined> {
  if (!current(lifetime)) return;
  const raw = await api.request<unknown>(`/v1/items/${encodeURIComponent(write.itemId)}/disc-titles/prepare`, 'POST',
    {versionId: write.versionId, revision: write.revision, titleId: write.titleId}, lifetime.signal);
  if (!current(lifetime)) return;
  return parseDiscTitles(raw, write.versionId);
}

/** Owner write of the main title. It sends the revision it read, so a changed
 * structure refuses instead of moving someone else's choice. The 202 body is the
 * current choices with the choice applied and analysis not yet ready. */
export async function chooseDiscMainTitle(api: DetailApi, write: DiscTitleWrite, lifetime: DiscTitleLifetime): Promise<DiscTitles | undefined> {
  if (!current(lifetime)) return;
  try {
    const raw = await api.request<unknown>(`/v1/items/${encodeURIComponent(write.itemId)}/main-title`, 'PUT',
      {versionId: write.versionId, revision: write.revision, titleId: write.titleId}, lifetime.signal);
    if (!current(lifetime)) return;
    return parseDiscTitles(raw, write.versionId);
  } catch (error) {
    if (code(error) === 'revision_mismatch' || (error as {status?: number} | null)?.status === 412) {
      throw Object.assign(new Error('disc_titles_stale'), {code: 'disc_titles_stale'});
    }
    throw error;
  }
}
