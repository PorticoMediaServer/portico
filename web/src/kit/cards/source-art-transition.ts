import {easings, reducedMotion} from '@design';

/** Motion 3.3's one temporary shared name, per claim. Each capture gets its own
 * suffix, so two cards (or a reused card element) can never be mistaken for each
 * other by a late release or an old queued callback: every cleanup compares the
 * exact name it minted with the element's current computed name. */
export const SOURCE_ART_PREFIX = 'card-source-art-';
export const SOURCE_ART_MORPH_MS = 420;

let sequence = 0;

export type SourceArtFacts = Readonly<{
  attached: boolean; width: number; height: number; top: number; left: number;
  intersectsViewport: boolean; visible: boolean; named: boolean; reduce: boolean;
}>;

/** A real, attached, laid-out, on-screen artwork with no computed name of its own
 * and motion allowed. Geometry must be finite; a positive rect that sits off the
 * viewport is not eligible. */
export function sourceArtEligible(facts: SourceArtFacts): boolean {
  return facts.attached && !facts.named && !facts.reduce && facts.visible && facts.intersectsViewport &&
    Number.isFinite(facts.width) && Number.isFinite(facts.height) &&
    Number.isFinite(facts.top) && Number.isFinite(facts.left) && facts.width > 0 && facts.height > 0;
}

/** The reverse only targets a source that is still attached, laid out and on screen. */
export function sourceArtReverseEligible(facts: Omit<SourceArtFacts, 'named' | 'reduce'>): boolean {
  return facts.attached && facts.visible && facts.intersectsViewport &&
    Number.isFinite(facts.width) && Number.isFinite(facts.height) &&
    Number.isFinite(facts.top) && Number.isFinite(facts.left) && facts.width > 0 && facts.height > 0;
}

/** Poster→backdrop runs 420ms out; a reduced system or app preference is 160ms opacity only. */
export function sourceArtMorph(reduce: boolean): Readonly<{duration: number; easing: string}> {
  return reduce
    ? {duration: reducedMotion.durationMs, easing: reducedMotion.easing}
    : {duration: SOURCE_ART_MORPH_MS, easing: easings.out};
}

/** Only the claim whose exact minted name is still on the element may clear it. */
export function sourceArtOwnsName(claimName: string, current: string | undefined | null): boolean {
  return current === claimName;
}

export type SourceArtClaim = Readonly<{name: string; art: HTMLElement}>;

function nextName(): string {
  sequence += 1;
  return `${SOURCE_ART_PREFIX}${sequence}`;
}

function reducePreferred(): boolean {
  if (typeof document === 'undefined') return true;
  const root = document.documentElement?.dataset.reduceMotion === 'true';
  const body = document.body?.dataset.reduceMotion === 'true';
  return root || body || (typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches);
}

function cssVisible(element: HTMLElement): boolean {
  if (typeof getComputedStyle !== 'function') return true;
  const css = getComputedStyle(element);
  return css.visibility !== 'hidden' && css.display !== 'none' && Number(css.opacity) !== 0;
}

/** Real viewport intersection from the laid-out rect. An unknown, non-finite or
 * non-positive viewport is never evidence of visibility, so it refuses. */
export function viewportIntersects(bounds: Readonly<{right: number; bottom: number; left: number; top: number}>, view: Readonly<{width: unknown; height: unknown}>): boolean {
  const {width, height} = view;
  if (typeof width !== 'number' || typeof height !== 'number') return false;
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return false;
  return bounds.right > 0 && bounds.bottom > 0 && bounds.left < width && bounds.top < height;
}

function onScreen(element: HTMLElement, bounds: DOMRect): boolean {
  const view = element.ownerDocument?.defaultView;
  return viewportIntersects(bounds, {width: view?.innerWidth, height: view?.innerHeight});
}

/** Captures the one name for an admitted navigation. The route owner calls this at
 * the original activation, after its own admission check, and calls
 * `releaseSourceArt` with the same claim when that navigation is cancelled or the
 * route retires. No timer stands in for route cleanup. */
export function captureSourceArt(art: HTMLElement | null | undefined): SourceArtClaim | undefined {
  if (!art || typeof document === 'undefined' || typeof getComputedStyle !== 'function') return undefined;
  const bounds = art.getBoundingClientRect();
  const computed = getComputedStyle(art).viewTransitionName;
  const eligible = sourceArtEligible({
    attached: art.isConnected, width: bounds.width, height: bounds.height, top: bounds.top, left: bounds.left,
    intersectsViewport: onScreen(art, bounds), visible: cssVisible(art), named: !!computed && computed !== 'none', reduce: reducePreferred(),
  });
  if (!eligible) return undefined;
  const name = nextName();
  art.style.viewTransitionName = name;
  return Object.freeze({name, art});
}

/** Clears only this claim's own inline name. It works on a detached or reused node
 * too: the owned value must come off, while a newer claim's different name stays. */
export function releaseSourceArt(claim: SourceArtClaim | undefined): void {
  if (!claim) return;
  const inline = claim.art.style?.viewTransitionName;
  if (sourceArtOwnsName(claim.name, inline)) claim.art.style.viewTransitionName = '';
}
