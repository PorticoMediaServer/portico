import type {AdmissionSlot} from './visual-admission';

/** One original mounted image, with a new token for every real decode attempt.
 * This owns no image resource, request, cache or admission timer. */
export type ArtworkDecodeElement = Pick<HTMLImageElement, 'decode' | 'isConnected'>;
export type ArtworkDecode<I> = Readonly<{image: I; element: ArtworkDecodeElement}>;
export type ArtworkDecodeCallbacks<I> = Readonly<{
  isCurrent: (decode: ArtworkDecode<I>) => boolean;
  receive: (decode: ArtworkDecode<I>, failed: boolean) => void;
}>;

export function createArtworkDecodeLifetime<I>(options: ArtworkDecodeCallbacks<I>) {
  let callbacks: ArtworkDecodeCallbacks<I> | undefined = options;
  let latest: ArtworkDecode<I> | undefined;
  const current = (decode: ArtworkDecode<I>) => callbacks !== undefined && latest === decode
    && decode.element.isConnected && callbacks.isCurrent(decode);
  const receive = (decode: ArtworkDecode<I>, failed: boolean) => {
    if (current(decode)) callbacks?.receive(decode, failed);
  };
  return {
    decode(image: I, element: ArtworkDecodeElement) {
      if (!callbacks) return;
      const decode = Object.freeze({image, element}); latest = decode;
      if (!current(decode)) return;
      try {void element.decode().then(() => receive(decode, false), () => receive(decode, true));}
      catch {receive(decode, true);}
    },
    fail(image: I, element: ArtworkDecodeElement) {
      if (!callbacks) return;
      const decode = Object.freeze({image, element}); latest = decode;
      receive(decode, true);
    },
    retire() {callbacks = undefined; latest = undefined;},
  };
}

/** Paint-only state for one original request's visible membership. Loading to
 * first image keeps its pending predecessor; a new decode generation replaces
 * the old registration before retiring it, so siblings cannot borrow its turn. */
export function createArtworkVisualLifetime<I, O extends object>(mount: () => AdmissionSlot) {
  let mountSlot: (() => AdmissionSlot) | undefined = mount;
  let visibility: 'unknown' | 'visible' | 'hidden' | 'unavailable' = 'unknown';
  let slot: AdmissionSlot | undefined, image: I | undefined, generation: O | undefined, admitted = false, failed = false;
  let decoded: {owner: O; cached: boolean; current: () => boolean; paint: () => void} | undefined;
  const register = () => {
    if (mountSlot && !slot && !admitted && !failed && visibility === 'visible') slot = mountSlot();
  };
  const offer = () => {
    const original = decoded;
    if (!mountSlot || admitted || failed || !original || generation !== original.owner || !original.current()) return;
    const paint = () => {
      if (!mountSlot || decoded !== original || generation !== original.owner || !original.current() || failed || admitted
        || !original.cached && visibility !== 'visible' && visibility !== 'unavailable') return;
      admitted = true; slot = undefined; original.paint();
    };
    if (original.cached) {slot?.retire(); slot = undefined; paint();}
    else if (visibility === 'unavailable') paint();
    else if (visibility === 'visible') slot?.ready(paint);
  };
  return {
    begin(next: I, owner: O) {
      if (!mountSlot) return;
      if (generation && (image !== next || generation !== owner)) {
        const previous = slot; slot = undefined; decoded = undefined; admitted = false; failed = false;
        image = next; generation = owner; register(); previous?.retire();
      } else {image = next; generation = owner;}
    },
    change(value: boolean | undefined) {
      if (!mountSlot) return;
      visibility = value === undefined ? 'unavailable' : value ? 'visible' : 'hidden';
      if (visibility === 'visible') register(); else {slot?.retire(); slot = undefined;}
    },
    flush: offer,
    decoded(owner: O, cached: boolean, current: () => boolean, paint: () => void) {
      if (mountSlot && !failed && generation === owner && decoded?.owner !== owner) {decoded = {owner, cached, current, paint}; offer();}
    },
    fail() {failed = true; decoded = undefined; slot?.retire(); slot = undefined;},
    retire() {mountSlot = undefined; decoded = undefined; generation = undefined; image = undefined; slot?.retire(); slot = undefined;},
  };
}

/** The visual admission callback publishes on this exact original image node,
 * rather than asking React for another commit before the native fade can start.
 * Status/callback publication remains owned by the separate real decode record.
 */
export type ArtworkPaintElement = Pick<HTMLImageElement, 'isConnected' | 'setAttribute' | 'removeAttribute'>;
const paintOwners = new WeakMap<ArtworkPaintElement, object>();
export function createArtworkPaintLifetime(element: ArtworkPaintElement, current: () => boolean) {
  const owner = {};
  let target: ArtworkPaintElement | undefined = element, accepts: (() => boolean) | undefined = current;
  paintOwners.set(element, owner);
  element.removeAttribute('data-artwork-admitted');
  return {
    reveal() {
      const original = target;
      if (original?.isConnected && accepts?.() && target === original && paintOwners.get(original) === owner) original.setAttribute('data-artwork-admitted', '');
    },
    retire() {
      const original = target; target = undefined; accepts = undefined;
      if (original && paintOwners.get(original) === owner) {
        paintOwners.delete(original); original.removeAttribute('data-artwork-admitted');
      }
    },
  };
}
