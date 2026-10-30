import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {wireDurations} from './wire-units.ts';
/** Trickplay set descriptors and their delivery URLs.
 *
 * The server owns the geometry, the staleness decision and the published URLs;
 * this module only rejects payloads that do not describe a consistent sprite
 * sheet. It never derives a tile path from anything but the parsed set. */

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
export type TrickplayScope = Readonly<{
  serverId: string;
  libraryId: string;
  itemId: string;
  viewerFence: string;
  sessionId: string;
  sessionGeneration: number;
}>;
export type TrickplayView = Readonly<{scope: TrickplayScope; sets: readonly TrickplaySet[]}>;
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
const opaque = (x: unknown): x is string => typeof x === 'string' && /^[a-f0-9]{64}$/.test(x);
const count = (x: unknown, max: number): x is number => typeof x === 'number' && Number.isSafeInteger(x) && x >= 1 && x <= max;
const positive = (x: unknown): x is number => typeof x === 'number' && Number.isFinite(x) && x > 0;
function assertTrickplay(condition: unknown): asserts condition {
  if (!condition) throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_trickplay'});
}

function parseTrickplaySet(wire: unknown, target: TrickplayTarget): TrickplaySet {
  // The wire carries milliseconds; the set is indexed in seconds like the player's clock.
  let raw: unknown;
  try { raw = wireDurations(wire, {intervalSeconds: 'intervalMs', durationSeconds: 'durationMs', sourceOffsetSeconds: 'sourceOffsetMs'}); } catch { raw = undefined; }
  assertTrickplay(object(raw));
  assertTrickplay(opaque(raw.id) && text(raw.sourceId, 256) && text(raw.sourceRevision, 256) && opaque(raw.revision));
  assertTrickplay(count(raw.tileWidth, 4096) && count(raw.tileHeight, 4096) && count(raw.columns, 64) && count(raw.rows, 64));
  assertTrickplay(count(raw.width, 16384) && count(raw.height, 16384));
  // Sheet geometry must be exactly the grid it claims, or every crop drifts.
  assertTrickplay(raw.width === raw.columns * raw.tileWidth && raw.height === raw.rows * raw.tileHeight);
  assertTrickplay(count(raw.frameCount, 65536) && count(raw.tileCount, 65536));
  assertTrickplay(raw.tileCount === Math.ceil(raw.frameCount / (raw.columns * raw.rows)));
  assertTrickplay(positive(raw.intervalSeconds) && positive(raw.durationSeconds));
  // Frames are captured over the whole source; a trimmed item starts later in it.
  assertTrickplay(typeof raw.sourceOffsetSeconds === 'number' && Number.isFinite(raw.sourceOffsetSeconds) && raw.sourceOffsetSeconds >= 0);
  assertTrickplay(typeof raw.stale === 'boolean');
  const tilesUrl = encodedRoutePath("get_items_id", {"id": target.itemId}) + '/trickplay/' + raw.id + '/tiles';
  const thumbnailsUrl = encodedRoutePath("get_items_id_trickplay_set_thumbnails_vtt", {"id": target.itemId, "set": raw.id});
  // Delivery URLs are server authored; accept only the ones this item can serve.
  assertTrickplay(raw.tilesUrl === tilesUrl && raw.thumbnailsUrl === thumbnailsUrl);
  return Object.freeze({
    id: raw.id,
    sourceId: raw.sourceId,
    sourceRevision: raw.sourceRevision,
    width: raw.width,
    height: raw.height,
    tileWidth: raw.tileWidth,
    tileHeight: raw.tileHeight,
    columns: raw.columns,
    rows: raw.rows,
    intervalSeconds: raw.intervalSeconds,
    durationSeconds: raw.durationSeconds,
    sourceOffsetSeconds: raw.sourceOffsetSeconds,
    tileCount: raw.tileCount,
    frameCount: raw.frameCount,
    stale: raw.stale,
    revision: raw.revision,
    tilesUrl,
    thumbnailsUrl,
  });
}

export function parseTrickplay(raw: unknown, serverId: string, target: TrickplayTarget): TrickplayView {
  assertTrickplay(object(raw) && object(raw.scope) && Array.isArray(raw.sets) && raw.sets.length <= 32);
  const scope = raw.scope;
  assertTrickplay(scope.serverId === serverId && scope.libraryId === target.libraryId && scope.itemId === target.itemId);
  assertTrickplay(text(scope.viewerFence, 256) && scope.sessionId === '' && scope.sessionGeneration === 0);
  const sets = raw.sets.map(v => parseTrickplaySet(v, target));
  assertTrickplay(new Set(sets.map(v => v.id)).size === sets.length);
  return Object.freeze({
    scope: Object.freeze({
      serverId,
      libraryId: target.libraryId,
      itemId: target.itemId,
      viewerFence: scope.viewerFence,
      sessionId: '',
      sessionGeneration: 0,
    }),
    sets: Object.freeze(sets),
  });
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

/** The WebVTT thumbnails track for a set. */
export function trickplayThumbnailsUrl(set: TrickplaySet): string {
  return set.thumbnailsUrl;
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

/** Prefer a current set over a stale one, then the finest interval. */
export function preferredTrickplaySet(view: TrickplayView, sourceId?: string): TrickplaySet | null {
  const usable = view.sets.filter(v => sourceId === undefined || v.sourceId === sourceId);
  if (!usable.length) return null;
  const ranked = [...usable].sort(
    (a, b) => Number(a.stale) - Number(b.stale) || a.intervalSeconds - b.intervalSeconds || b.tileWidth - a.tileWidth,
  );
  return ranked[0];
}
