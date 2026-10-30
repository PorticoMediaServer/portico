import {createContext, useContext, useEffect, useState} from 'react';
import {ArtworkScheduler, artworkRenditionFor, type ArtworkScreenStats} from '../../../../packages/client-core/src/artwork/index';
import {beginArtworkArrivalScreen} from './artwork-arrival';

/**
 * PERF-03 / APL-SYS-11: one queue for every card image on Apple, driven by client-core's
 * `ArtworkScheduler`.
 *
 * React Native's image pipeline does the network, decode and caching (it needs the
 * Authorization header, which a prefetch can't carry), so the scheduler decides *when* an image
 * may start and the `<Image>` itself is the load:
 *
 * - `load(key)` is called by the scheduler when the image's turn comes (visual order: row × 1000
 *   + column, a started row finishes before the next, the hero first). The `<Artwork>` waiting
 *   for that key then mounts its `<Image>`.
 * - `onLoad`/`onError` of that `<Image>` settle the attempt. Failures retry with the scheduler's
 *   backoff, and slow attempts time out without holding a slot.
 * - A card leaving the screen aborts its interest, so off-screen rows never compete with
 *   visible ones.
 *
 * Only a handful of images decode at once. React Native allows 4 network loads and 2 decodes
 * at a time, first come first served, so the scheduler's order is what the viewer sees.
 */
type Attempt = {n: number; settle: (ok: boolean, error?: unknown) => void};
const attempts = new Map<string, Attempt>();
const listeners = new Map<string, Set<() => void>>();
/** Attempts made per image; the first goes to the plain URL, retries add a cache-buster. */
const tries = new Map<string, number>();
/** Images that have decoded this session: shown at once on remount, never faded again. */
export const shown = new Set<string>();
const SHOWN_LIMIT = 4000;
function remember(key: string) {
  shown.add(key);
  // Oldest first out: a long session of browsing must not grow this for ever.
  if (shown.size > SHOWN_LIMIT) shown.delete(shown.values().next().value!);
}
/**
 * Images that had loaded and then failed when shown again (the system dropped their bytes and
 * the server could not be reached to fetch them anew). The scheduler believes these are done, so
 * they are retried here: the count of tries so far, by image.
 */
const reloads = new Map<string, number>();
/** Waits before each new try; after the last one the card keeps its placeholder until it is shown afresh. */
export const RELOAD_BACKOFF_MS = [1500, 6000, 20000] as const;

function notify(key: string) {
  const set = listeners.get(key);
  if (set) for (const l of [...set]) l();
}

const scheduler = new ArtworkScheduler<true>({
  load: (key, signal) =>
    new Promise<true>((resolve, reject) => {
      const n = tries.get(key) ?? 0;
      tries.set(key, n + 1);
      if (tries.size > 2000) tries.delete(tries.keys().next().value!);
      const attempt: Attempt = {
        n,
        settle: (ok, error) => {
          if (ok) remember(key);
          if (attempts.get(key) === attempt) attempts.delete(key);
          notify(key);
          if (ok) resolve(true);
          else reject(error ?? new Error('Image failed to load.'));
        },
      };
      attempts.set(key, attempt);
      notify(key);
      signal.addEventListener('abort', () => {
        const e = new Error('Aborted');
        e.name = 'AbortError';
        attempt.settle(false, e);
      }, {once: true});
    }),
  // React Native reports no status for a failed image; a busy home server recovers, so retry.
  classify: error => ({retryable: !(error && typeof error === 'object' && (error as {name?: unknown}).name === 'AbortError')}),
  concurrency: 6,
  priorityExtra: 2,
  maxEntries: 800,
  timeoutMs: 12000,
  onScreenSettled: stats => {
    if (__DEV__) console.log(`[artwork] ${describe(stats)}`);
  },
});

function describe(s: ArtworkScreenStats): string {
  return `${s.label}: first image ${s.firstImageMs ?? '—'} ms, all visible ${s.allVisibleMs ?? '—'} ms, ${s.requested} requested, ${s.cached} cached, ${s.failed} failed`;
}

/** Call on navigation, before the new screen's cards ask for artwork. */
export function beginArtworkScreen(label: string) {
  beginArtworkArrivalScreen();
  scheduler.beginScreen(label);
  timing = {label, startedAt: Date.now(), loads: 0};
}

/**
 * Dev A/B switch for the before/after timings: `globalThis.__porticoArtworkDirect = true` (from
 * the debugger) loads every image at once at full size, the way it worked before the scheduler.
 */
const direct = () => __DEV__ && (globalThis as {__porticoArtworkDirect?: boolean}).__porticoArtworkDirect === true;

/** Screen timing that works in both modes: first image and last image of a burst, logged in dev. */
let timing: {label: string; startedAt: number; loads: number; first?: number; last?: number; timer?: ReturnType<typeof setTimeout>} = {label: 'launch', startedAt: Date.now(), loads: 0};
function recordLoad() {
  const now = Date.now() - timing.startedAt;
  timing.loads++;
  timing.first ??= now;
  timing.last = now;
  clearTimeout(timing.timer);
  const t = timing;
  if (__DEV__) t.timer = setTimeout(() => console.log(`[artwork${direct() ? ' direct' : ''}] ${t.label}: first ${t.first} ms, last ${t.last} ms, ${t.loads} images`), 3000);
}

export function artworkStats() {
  return scheduler.stats();
}

/** Where a card sits on screen, for visual-order loading. Provided per row by the screens. */
export type ArtworkPlacement = {row: number; group?: string; column?: number; priority?: boolean};
export const ArtworkPlacementContext = createContext<ArtworkPlacement | null>(null);

/**
 * Slot-aware URL: the server made a small and a large file of every picture when it arrived, so
 * a box asks for the one whose actual pixels cover it at this density, crop and zoom. One picture
 * at one slot is one URL everywhere, and a described URL names the slot's exact bytes, which the
 * platform caches for good. A URL that already names a representation (`size=`) is left alone.
 */
export function sizedUri(uri: string, pointWidth: number, pointHeight: number, scale: number, fit: 'cover' | 'contain' = 'cover', zoom = 1): string {
  if (direct() || /[?&]size=/.test(uri)) return uri;
  return artworkRenditionFor(uri, {width: pointWidth, height: pointHeight, dpr: scale, fit, zoom}).url;
}

/**
 * The state of one image: whether it has loaded before (show at once), whether its turn has come
 * (mount the `<Image>`), which attempt it is on, and how to report the result.
 */
export function useArtworkSlot(key: string, placement: ArtworkPlacement | null, priority: boolean) {
  const [, force] = useState(0);
  const reload = key ? reloads.get(key) : undefined;
  const recovering = reload !== undefined;
  const cached = !!key && !recovering && (shown.has(key) || scheduler.peek(key) === true);
  const [gaveUp, setGaveUp] = useState(false);
  // The try this card may make now, while an image that had loaded is being fetched again.
  const [armed, setArmed] = useState(0);
  useEffect(() => {
    if (!key) return;
    const set = listeners.get(key) ?? new Set();
    const listener = () => force(v => v + 1);
    set.add(listener);
    listeners.set(key, set);
    return () => {
      set.delete(listener);
      if (!set.size) listeners.delete(key);
    };
  }, [key]);
  const order = placement ? placement.row * 1000 + (placement.column ?? 0) : undefined;
  useEffect(() => {
    if (!key || reloads.has(key) || shown.has(key) || scheduler.peek(key) === true) return;
    setGaveUp(false);
    const controller = new AbortController();
    scheduler
      .load(key, {signal: controller.signal, priority: priority || placement?.priority ? 'high' : 'normal', order, group: placement?.group})
      .then(() => force(v => v + 1), error => {
        if (!controller.signal.aborted && !(error && (error as {name?: string}).name === 'AbortError')) setGaveUp(true);
      });
    return () => controller.abort();
    // Order changes are applied with reorder below; the request itself is keyed by the image.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  useEffect(() => {
    if (key && order !== undefined) scheduler.reorder(key, order, placement?.group);
  }, [key, order, placement?.group]);
  // An image that had loaded and now fails is tried again after a wait, a few times.
  useEffect(() => {
    if (!key || reload === undefined) return;
    if (reload > RELOAD_BACKOFF_MS.length) {
      setGaveUp(true);
      // Out of tries for now. The next time this image is put on screen it starts afresh.
      return () => { if ((reloads.get(key) ?? 0) > RELOAD_BACKOFF_MS.length) reloads.delete(key); };
    }
    setGaveUp(false);
    const timer = setTimeout(() => setArmed(reload), RELOAD_BACKOFF_MS[reload - 1]);
    return () => clearTimeout(timer);
  }, [key, reload]);
  const attempt = key ? attempts.get(key) : undefined;
  const bypass = direct();
  return {
    cached,
    /** It had loaded before and failed when shown again: the placeholder is back while it is fetched anew. */
    recovering,
    /** Mount the image now: it loaded before, or its turn has come. */
    active: recovering ? armed === reload && reload <= RELOAD_BACKOFF_MS.length : cached || !!attempt || bypass,
    attempt: recovering ? 1000 + reload : attempt?.n ?? 0,
    gaveUp,
    loaded: () => {
      recordLoad();
      if (bypass) remember(key);
      const pending = attempts.get(key);
      if (pending) { pending.settle(true); return; }
      if (reloads.delete(key)) { remember(key); notify(key); }
    },
    failed: (error?: unknown) => {
      const pending = attempts.get(key);
      if (pending) { pending.settle(false, error); return; }
      // No attempt of the scheduler's is waiting on this image: it was shown from memory of an
      // earlier load. Its placeholder comes back and it is fetched again.
      if (!key || bypass) return;
      shown.delete(key);
      reloads.set(key, (reloads.get(key) ?? 0) + 1);
      if (reloads.size > 500) reloads.delete(reloads.keys().next().value!);
      notify(key);
    },
  };
}

export function useArtworkPlacement() {
  return useContext(ArtworkPlacementContext);
}
