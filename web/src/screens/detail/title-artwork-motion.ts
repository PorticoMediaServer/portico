import {useCallback, useLayoutEffect, useRef} from 'react';
import {easings, motion, reducedMotion} from '@design';
import {commitTitleArtwork, isTitleArtworkIntent} from '../../platform/card-title-motion';
import {SOURCE_ART_PREFIX, type SourceArtClaim} from '../../kit/cards/source-art-transition';

/** The destination half of motion 3.3. The one claim (and its exact minted name)
 * comes from the original navigation owner; this module never mints a name or
 * keeps a render-time registry of its own. */
export const TITLE_ARTWORK_MORPH_MS = 420;
export const TITLE_COPY_RISE_PX = 10;
export const TITLE_COPY_GROUPS_MS = 60;

export function titleArtworkMorph(reduce: boolean): Readonly<{duration: number; easing: string}> {
  return reduce ? {duration: reducedMotion.durationMs, easing: reducedMotion.easing} : {duration: TITLE_ARTWORK_MORPH_MS, easing: easings.out};
}

/** Title words rise 10 px over standard 180, one 60 ms group after another; reduce
 * is the 160 ms opacity policy with no rise and no stagger. */
export function titleCopyTiming(reduce: boolean): Readonly<{risePx: number; duration: number; staggerMs: number; easing: string}> {
  return reduce
    ? {risePx: 0, duration: reducedMotion.durationMs, staggerMs: 0, easing: reducedMotion.easing}
    : {risePx: TITLE_COPY_RISE_PX, duration: motion.standard, staggerMs: TITLE_COPY_GROUPS_MS, easing: easings.out};
}

function reducePreferred(): boolean {
  if (typeof document === 'undefined') return true;
  return document.documentElement?.dataset.reduceMotion === 'true' ||
    document.body?.dataset.reduceMotion === 'true' ||
    (typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches);
}

/** The passive layout signal starts false; every paint ORs it with this live read. */
export function titleMotionReduced(signal: boolean): boolean {
  return signal || reducePreferred();
}

/** A wrapper box is not proof of artwork: the destination must carry a current,
 * decoded image, be attached and laid out, and carry no other writer's name. */
function realArtwork(art: HTMLElement): boolean {
  const image = art.querySelector<HTMLImageElement>('[data-foreground-art] [data-artwork-status="ready"] > img');
  if (!image) return false;
  const source = image.currentSrc || image.getAttribute('src') || '';
  return source.length > 0 && image.complete && image.naturalWidth > 0;
}

function computedName(art: HTMLElement): string {
  if (typeof getComputedStyle !== 'function') return '';
  const value = getComputedStyle(art).viewTransitionName;
  return value === 'none' ? '' : value;
}

/** Applies the owner's claim to the real destination artwork once. Another
 * writer's computed name is refused, and reduce keeps no geometry name. */
export function applyTitleArtworkClaim(art: HTMLElement | null | undefined, claim: SourceArtClaim | undefined, reduce = titleMotionReduced(false)): () => void {
  if (!art || !claim || !claim.name.startsWith(SOURCE_ART_PREFIX) || reduce) return () => {};
  const bounds = art.getBoundingClientRect();
  const current = computedName(art);
  if (current && current !== claim.name) return () => {};
  if (!art.isConnected || !Number.isFinite(bounds.width) || !Number.isFinite(bounds.height) || !(bounds.width > 0) || !(bounds.height > 0) || !realArtwork(art)) return () => {};
  art.style.viewTransitionName = claim.name;
  return () => {if (art.style.viewTransitionName === claim.name) art.style.viewTransitionName = '';};
}

export function useTitleArtworkClaim(node: HTMLElement | null, claim: SourceArtClaim | undefined, titleKey: string, reduced: boolean): (drawnNode: HTMLElement) => void {
  const committed = useRef<{node: HTMLElement | null; claim: typeof claim; titleKey: string; reduced: boolean} | undefined>(undefined);
  useLayoutEffect(() => {
    const held = {node, claim, titleKey, reduced};committed.current = held;
    if (!node || claim && isTitleArtworkIntent(claim)) return;
    return applyTitleArtworkClaim(node, claim, titleMotionReduced(reduced));
  }, [node, claim, titleKey, reduced]);
  useLayoutEffect(() => () => {committed.current = undefined;}, []);
  // Artwork captures this callback with its original resource/decode lease. Only
  // the committed node for this exact title/claim may accept that delivery.
  return useCallback((drawnNode: HTMLElement) => {
    const held = committed.current;
    if (!claim || !held?.node || held.node !== drawnNode || held.claim !== claim || held.titleKey !== titleKey || titleMotionReduced(held.reduced)) return;
    commitTitleArtwork(claim, held.node, () => applyTitleArtworkClaim(held.node, claim, false));
  }, [claim, titleKey]);
}

function painted(element: HTMLElement): Readonly<{opacity: number; transform: string}> {
  const css = typeof getComputedStyle === 'function' ? getComputedStyle(element) : undefined;
  const opacity = css ? Number.parseFloat(css.opacity) : 1;
  return {opacity: Number.isFinite(opacity) ? opacity : 1, transform: css?.transform ?? 'none'};
}

/** One authored entrance per committed title presentation, owned by the actual
 * copy node. Settled entries stay settled; only active owned animations retarget
 * when motion changes. */
export function useTitleCopyEntrance(node: HTMLElement | null, titleKey: string, reduced: boolean): void {
  const active = useRef(new Map<HTMLElement, Animation>());
  const played = useRef(new WeakSet<HTMLElement>());
  const effective = titleMotionReduced(reduced);
  const start = (frames: readonly Readonly<{element: HTMLElement; from: Readonly<{opacity: number; transform: string}>; fresh: boolean}>[], timing: ReturnType<typeof titleCopyTiming>) => {
    for (const [index, frame] of frames.entries()) {
      if (!frame.element.animate) continue;
      // Only a genuinely fresh child gets the initial rise; an owned frame keeps
      // its sampled transform verbatim, including none.
      const transform = timing.risePx > 0 && frame.fresh && frame.from.transform === 'none' ? `translateY(${timing.risePx}px)` : frame.from.transform;
      const from = timing.risePx > 0 ? {opacity: frame.from.opacity, transform} : {opacity: frame.from.opacity};
      const to = timing.risePx > 0 ? {opacity: 1, transform: 'none'} : {opacity: 1};
      const animation = frame.element.animate([from, to], {
        duration: timing.duration, delay: timing.risePx > 0 ? timing.staggerMs * index : 0, easing: timing.easing, fill: 'backwards'
      });
      active.current.set(frame.element, animation);
      void animation.finished.then(() => {
        played.current.add(frame.element);
        if (active.current.get(frame.element) === animation) active.current.delete(frame.element);
      }, () => {});
    }
  };
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({effective});
  useLayoutEffect(() => {setupPorts1.current = {effective};});
  useLayoutEffect(() => {
    const {effective} = setupPorts1.current;
    if (!node) return;
    const children = Array.from(node.children) as HTMLElement[];
    for (const [element, animation] of active.current) {
      if (!children.includes(element)) {animation.cancel(); active.current.delete(element);}
    }
    const interrupted = new Map<HTMLElement, Readonly<{opacity: number; transform: string}>>();
    for (const element of active.current.keys()) interrupted.set(element, painted(element));
    for (const animation of active.current.values()) animation.cancel();
    active.current.clear();
    start(children.map(element => ({element, fresh: !interrupted.has(element), from: interrupted.get(element) ?? {opacity: 0, transform: 'none'}})),
      titleCopyTiming(effective));
  }, [node, titleKey]);
  const lastEffective = useRef(effective);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts2 = useRef({node});
  useLayoutEffect(() => {setupPorts2.current = {node};});
  useLayoutEffect(() => {
    const {node} = setupPorts2.current;
    if (lastEffective.current === effective) return;
    lastEffective.current = effective;
    if (!node) return;
    const children = Array.from(node.children) as HTMLElement[];
    for (const [element, animation] of active.current) {
      if (!children.includes(element)) {animation.cancel(); active.current.delete(element);}
    }
    const owned = Array.from(active.current.keys());
    if (!owned.length) return; // settled entries keep their played identity
    const frames = owned.map(element => ({element, fresh: false, from: painted(element)}));
    for (const animation of active.current.values()) animation.cancel();
    active.current.clear();
    start(frames, titleCopyTiming(effective));
  }, [effective]);
  useLayoutEffect(() => () => {
    for (const animation of active.current.values()) animation.cancel();
    active.current.clear();
  }, [node]);
}
