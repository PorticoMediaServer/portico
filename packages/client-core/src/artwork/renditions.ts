/**
 * Artwork slots: the server makes two files of every picture when it arrives,
 * a small one (800 px on its long edge) and a large one (the owner's chosen
 * 1280, 1920 or 2560). Nothing is resized on request, so a client picks one.
 *
 * A page carries each picture as
 *   …/art/{role}?v={source}&sd={w}x{h}.{bytes}.{r}&ld={w}x{h}.{bytes}.{r}
 * describing both slots' actual size. The client asks for one slot by name,
 * with that slot's token r:
 *   …/art/{role}?v={source}&size=small|large&r={r}
 * which names exact bytes and is cached for good.
 */

/** The small slot's long edge, for URLs that carry no descriptor. */
export const ARTWORK_SMALL_EDGE = 800;

/** A slot may be this much smaller than the painted pixels (10%). */
export const ARTWORK_SLOT_TOLERANCE = 1.1;

export type ArtworkSlotName = 'small' | 'large';

export type ArtworkSlot = Readonly<{width: number; height: number; bytes: number; token: string}>;

export type ArtworkDescriptor = Readonly<{small: ArtworkSlot; large: ArtworkSlot}>;

export type ArtworkBox = Readonly<{
  /** Painted box, in CSS pixels or points. */
  width: number;
  height: number;
  /** Device pixel ratio or screen scale. */
  dpr: number;
  /** cover crops to fill the box; contain fits inside it. Default cover. */
  fit?: 'cover' | 'contain';
  /** Largest authored zoom the box applies (Ken Burns, hover). Default 1. */
  zoom?: number;
}>;

export type ArtworkRendition = Readonly<{
  url: string;
  slot: ArtworkSlotName;
  /** The chosen slot's actual size, when the page described it. */
  width?: number;
  height?: number;
  bytes?: number;
}>;

const slotPattern = /^([1-9]\d{0,4})x([1-9]\d{0,4})\.(\d{1,9})\.([0-9a-f]{16})$/;
const descriptorKeys = new Set(['sd', 'ld', 'w', 'size', 'r']);

function parseSlot(value: string | null): ArtworkSlot | undefined {
  const match = value ? slotPattern.exec(value) : null;
  if (!match) return undefined;
  return {width: Number(match[1]), height: Number(match[2]), bytes: Number(match[3]), token: match[4]!};
}

/** The slot descriptor a page URL carries, if it carries a complete one. */
export function artworkDescriptor(url: string): ArtworkDescriptor | undefined {
  const query = url.split('?')[1];
  if (!query) return undefined;
  const params = new URLSearchParams(query);
  const small = parseSlot(params.get('sd')), large = parseSlot(params.get('ld'));
  return small && large ? {small, large} : undefined;
}

/**
 * How many times the slot's pixels must be stretched to paint the box:
 * at most the tolerance means the slot is sharp enough.
 */
function stretch(slot: Readonly<{width: number; height: number}>, box: ArtworkBox): number {
  const dpr = box.dpr > 0 && Number.isFinite(box.dpr) ? box.dpr : 1;
  const zoom = box.zoom && box.zoom > 1 && Number.isFinite(box.zoom) ? box.zoom : 1;
  const across = box.width / slot.width, down = box.height / slot.height;
  return dpr * zoom * (box.fit === 'contain' ? Math.min(across, down) : Math.max(across, down));
}

/**
 * The slot to paint a picture into a box: small when its actual pixels cover
 * the box at this density, crop and zoom, otherwise large. Without a
 * descriptor the small slot's nominal edge stands in for its size.
 */
export function artworkSlotFor(descriptor: ArtworkDescriptor | undefined, box: ArtworkBox): ArtworkSlotName {
  if (!(box.width > 0) || !(box.height > 0)) return 'small';
  if (descriptor) return stretch(descriptor.small, box) <= ARTWORK_SLOT_TOLERANCE ? 'small' : 'large';
  const dpr = box.dpr > 0 && Number.isFinite(box.dpr) ? box.dpr : 1;
  const zoom = box.zoom && box.zoom > 1 && Number.isFinite(box.zoom) ? box.zoom : 1;
  return Math.max(box.width, box.height) * dpr * zoom <= ARTWORK_SMALL_EDGE * ARTWORK_SLOT_TOLERANCE ? 'small' : 'large';
}

/**
 * The URL to request for a page artwork URL painted into box. The descriptor
 * and any legacy width are dropped, so one picture at one slot has one URL and
 * one cache entry everywhere it appears. A URL without a source version keeps
 * no token and is revalidated by the server.
 */
export function artworkRenditionFor(url: string, box: ArtworkBox): ArtworkRendition {
  const [path, query = ''] = url.split('?');
  const params = new URLSearchParams(query);
  const descriptor = artworkDescriptor(url);
  const slot = artworkSlotFor(descriptor, box);
  for (const key of descriptorKeys) params.delete(key);
  params.set('size', slot);
  const chosen = descriptor?.[slot];
  if (chosen && params.has('v')) params.set('r', chosen.token);
  params.sort();
  const out = path + '?' + params.toString();
  return chosen ? {url: out, slot, width: chosen.width, height: chosen.height, bytes: chosen.bytes} : {url: out, slot};
}
