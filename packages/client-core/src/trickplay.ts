import {unreadableServerResponse} from './server-messages.ts';
/** Scrub previews: a version's frames on JPEG sprite sheets, as the players draw them.
 *
 * The server owns the geometry and the sheet addresses (`GET /v1/items/{id}/scrub-previews`);
 * this module only rejects answers that do not describe a consistent grid. It never derives a
 * sheet path from anything but the parsed answer. */

export type TrickplaySet = Readonly<{
  id: string;
  sourceId: string;
  sourceRevision: string;
  width: number;
  height: number;
  tileWidth: number;
  tileHeight: number;
  columns: number;
  rows: number;
  intervalSeconds: number;
  durationSeconds: number;
  sourceOffsetSeconds: number;
  tileCount: number;
  frameCount: number;
  stale: boolean;
  revision: string;
  tilesUrl: string;
  thumbnailsUrl: string;
  /** A download's sheets, as files on the device, by tile; they stand in for `tilesUrl`. */
  tileUrls?: readonly string[];
}>;
export type TrickplayTarget = Readonly<{libraryId: string; itemId: string}>;
/** One resolved preview frame: which tile to draw and the crop inside it. */
export type TrickplayFrame = Readonly<{
  index: number;
  tileIndex: number;
  tileUrl: string;
  x: number;
  y: number;
  width: number;
  height: number;
  startSeconds: number;
  endSeconds: number;
}>;

const object = (x: unknown): x is Record<string, unknown> => !!x && typeof x === 'object' && !Array.isArray(x);
const text = (x: unknown, max = 512): x is string => typeof x === 'string' && x.length > 0 && x.length <= max && !/[\x00-\x1f\x7f]/.test(x);
const count = (x: unknown, max: number): x is number => typeof x === 'number' && Number.isSafeInteger(x) && x >= 1 && x <= max;
const positive = (x: unknown): x is number => typeof x === 'number' && Number.isFinite(x) && x > 0;
function assertTrickplay(condition: unknown): asserts condition {
  if (!condition) throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_trickplay'});
}

/** Whether the server makes scrub previews (`scrub-previews` in `GET /v1/system`'s capabilities). Until it
 * says so the players never ask, so a server without them costs no failing read. */
export function serverOffersScrubPreviews(system: unknown): boolean {
  const capabilities = object(system) ? system.capabilities : undefined;
  return Array.isArray(capabilities) && capabilities.includes('scrub-previews');
}

const scrubPath = (itemId: string) => '/v1/items/' + encodeURIComponent(itemId) + '/scrub-previews';

/** One version's previews as the set the players draw: `null` when the server has none for it
 * (`available: false`), and the player keeps its plain scrubber. Sheets are read with the viewer's
 * sign-in, at the addresses the answer names. */
export function parseScrubPreviews(raw: unknown, itemId: string, versionId: string): TrickplaySet | null {
  assertTrickplay(object(raw) && raw.version === versionId && typeof raw.available === 'boolean');
  if (!raw.available) return null;
  // The grid is 10 by 10 unless the answer names its rows.
  const rows = raw.rows ?? raw.columns;
  assertTrickplay(count(raw.frameWidth, 4096) && count(raw.frameHeight, 4096) && count(raw.columns, 64) && count(rows, 64) && count(raw.frameCount, 65536));
  assertTrickplay(positive(raw.intervalMs) && positive(raw.durationMs) && Array.isArray(raw.sheets));
  const tileCount = Math.ceil(raw.frameCount / (raw.columns * rows)), base = scrubPath(itemId) + '?';
  assertTrickplay(raw.sheets.length === tileCount);
  const tileUrls = raw.sheets.map((url, sheet) => {
    assertTrickplay(text(url, 1024) && url.startsWith(base));
    const query = new URLSearchParams(url.slice(base.length));
    assertTrickplay([...query.keys()].length === 2 && query.get('version') === versionId && query.get('sheet') === String(sheet));
    return base + new URLSearchParams({version: versionId, sheet: String(sheet)});
  });
  return Object.freeze({id: versionId, sourceId: versionId, sourceRevision: versionId, width: raw.columns * raw.frameWidth, height: rows * raw.frameHeight,
    tileWidth: raw.frameWidth, tileHeight: raw.frameHeight, columns: raw.columns, rows, intervalSeconds: raw.intervalMs / 1000, durationSeconds: raw.durationMs / 1000,
    sourceOffsetSeconds: 0, tileCount, frameCount: raw.frameCount, stale: false, revision: versionId, tilesUrl: '', thumbnailsUrl: '', tileUrls: Object.freeze(tileUrls)});
}

/** Reads one version's scrub previews. Ask only where `serverOffersScrubPreviews` says the server has them. */
export async function readScrubPreviews(request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>,
  itemId: string, versionId: string, signal?: AbortSignal): Promise<TrickplaySet | null> {
  return parseScrubPreviews(await request<unknown>(scrubPath(itemId) + '?' + new URLSearchParams({version: versionId}), 'GET', undefined, signal), itemId, versionId);
}

/** The sprite sheet holding a tile index. */
export function trickplayTileUrl(set: TrickplaySet, tileIndex: number): string {
  if (!Number.isSafeInteger(tileIndex) || tileIndex < 0 || tileIndex >= set.tileCount) {
    throw new Error('Trickplay tile is outside this set.');
  }
  if (set.tileUrls) {
    const local = set.tileUrls[tileIndex];
    if (!local) throw new Error('Trickplay tile is outside this set.');
    return local;
  }
  return set.tilesUrl + '/' + tileIndex + '.jpg';
}

/** Resolve a preview frame by its index within the set.
 *
 * Frame times are reported in item seconds: the source offset of a trimmed
 * association is already subtracted, and the range is clamped to the item. */
export function trickplayFrame(set: TrickplaySet, index: number): TrickplayFrame {
  if (!Number.isSafeInteger(index) || index < 0 || index >= set.frameCount) {
    throw new Error('Trickplay frame is outside this set.');
  }
  const perTile = set.columns * set.rows;
  const tileIndex = Math.floor(index / perTile);
  const within = index % perTile;
  const start = index * set.intervalSeconds - set.sourceOffsetSeconds;
  return Object.freeze({
    index,
    tileIndex,
    tileUrl: trickplayTileUrl(set, tileIndex),
    x: (within % set.columns) * set.tileWidth,
    y: Math.floor(within / set.columns) * set.tileHeight,
    width: set.tileWidth,
    height: set.tileHeight,
    startSeconds: Math.max(0, start),
    endSeconds: Math.min(set.durationSeconds, Math.max(0, start + set.intervalSeconds)),
  });
}

/** Resolve the preview frame covering a position, in item seconds. */
export function trickplayFrameAt(set: TrickplaySet, seconds: number): TrickplayFrame | null {
  if (!Number.isFinite(seconds) || seconds < 0 || seconds >= set.durationSeconds) return null;
  const index = Math.min(set.frameCount - 1, Math.floor((seconds + set.sourceOffsetSeconds) / set.intervalSeconds));
  if (index < 0) return null;
  return trickplayFrame(set, index);
}

/** A download's preview set (queue 6): the geometry its bundle carried and its sheets as files on
 * the device, in order. Null unless every sheet is here and the grid is the one it claims, so a
 * half-kept download leaves the strip dark rather than drawing the wrong pictures. */
export function downloadTrickplaySet(bundle: Readonly<{id: string; tileWidth: number; tileHeight: number; columns: number; rows: number; intervalSeconds: number; frameCount: number; durationSeconds: number; sourceOffsetSeconds: number; tiles: readonly unknown[]}> | undefined,
  tiles: readonly string[] | undefined): TrickplaySet | null {
  if (!bundle || !tiles?.length) return null;
  const whole = (x: number, max: number) => Number.isSafeInteger(x) && x >= 1 && x <= max;
  if (!whole(bundle.tileWidth, 4096) || !whole(bundle.tileHeight, 4096) || !whole(bundle.columns, 64) || !whole(bundle.rows, 64) || !whole(bundle.frameCount, 65536)
    || !positive(bundle.intervalSeconds) || !positive(bundle.durationSeconds) || !(Number.isFinite(bundle.sourceOffsetSeconds) && bundle.sourceOffsetSeconds >= 0)) return null;
  const tileCount = Math.ceil(bundle.frameCount / (bundle.columns * bundle.rows));
  if (tiles.length !== tileCount || bundle.tiles.length !== tileCount || tiles.some(tile => !text(tile, 4096))) return null;
  return Object.freeze({id: bundle.id, sourceId: 'download', sourceRevision: bundle.id, width: bundle.columns * bundle.tileWidth, height: bundle.rows * bundle.tileHeight,
    tileWidth: bundle.tileWidth, tileHeight: bundle.tileHeight, columns: bundle.columns, rows: bundle.rows, intervalSeconds: bundle.intervalSeconds,
    durationSeconds: bundle.durationSeconds, sourceOffsetSeconds: bundle.sourceOffsetSeconds, tileCount, frameCount: bundle.frameCount, stale: false,
    revision: bundle.id, tilesUrl: '', thumbnailsUrl: '', tileUrls: Object.freeze([...tiles])});
}

