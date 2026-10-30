import {captureSourceArt, releaseSourceArt, sourceArtMorph, sourceArtReverseEligible, viewportIntersects, type SourceArtClaim} from '../kit/cards/source-art-transition';

export const TITLE_ART_WAIT_MS = 1200;
export type ArtworkLocation = Readonly<{key: string; href: string; pathname: string}>;
export type ArtworkNavigation = Readonly<{from?: ArtworkLocation; to: ArtworkLocation}>;
export type TitleArtworkIntent = Readonly<{
  scope: string; entry: object; itemId: string; source: ArtworkLocation;
  admitted(): boolean; principalCurrent(): boolean; sourceCurrent(): boolean;
  location(): ArtworkLocation; subscribe(listener: (event: ArtworkNavigation) => void): () => void;
  browserOwns(): boolean; navigate(manual: boolean): Promise<void>;
}>;
type Original = {
  live: boolean; visual: boolean; dispatched: boolean; intent: TitleArtworkIntent; claim: SourceArtClaim;
  document: Document; sourceImage: HTMLImageElement; sourceSrc: string; targetImage?: HTMLImageElement; targetSrc?: string; destination?: ArtworkLocation; target?: HTMLElement; clearTarget?: () => void;
  transition?: ViewTransition; cancelWait?: () => void; reverse?: () => void; finishWait(): void; off: (() => void)[];
};
function decodedImage(node: HTMLElement, foreground = false): HTMLImageElement | undefined {
  const image = node.querySelector<HTMLImageElement>(`${foreground ? '[data-foreground-art] ' : ''}[data-artwork-status="ready"] > img`);
  // The status belongs to Artwork's original resource/decode lease. The exact
  // mounted image/source is retained; ambient or complete guesses cannot admit.
  return image?.isConnected && image.getAttribute('src') ? image : undefined;
}
const owners = new WeakMap<Document, Original>();
const claims = new WeakMap<SourceArtClaim, Original>();
const same = (a: ArtworkLocation | undefined, b: ArtworkLocation) => !!a && a.key.length > 0 && a.key === b.key && a.href === b.href && a.pathname === b.pathname;
const frozen = (value: ArtworkLocation): ArtworkLocation => Object.freeze({key: value.key, href: value.href, pathname: value.pathname});
function reduced(document: Document): boolean {
  try {return document.documentElement.dataset.reduceMotion === 'true' || document.body?.dataset.reduceMotion === 'true'
    || document.defaultView?.matchMedia('(prefers-reduced-motion: reduce)').matches !== false;} catch {return true;}
}
function owns(held: Original): boolean {return held.live && owners.get(held.document) === held;}
function principal(held: Original): boolean {
  if (!owns(held)) return false;
  let accepted = false;try {accepted = held.intent.principalCurrent();} catch { /* Original scope refused. */ }
  return owns(held) && accepted;
}
function destinationCurrent(held: Original): boolean {
  if (!principal(held) || !held.destination) return false;
  const location = held.intent.location();
  return owns(held) && same(held.destination, location);
}
function quiet(cleanup: (() => void) | undefined): void {try {cleanup?.();} catch { /* Visual retirement must release its other original resources. */ }}
function keepVisual(held: Original, cleanup: () => void): void {
  if (held.visual && owns(held)) held.off.push(cleanup);else quiet(cleanup);
}
function endVisual(held: Original, skip: boolean): void {
  if (!held.visual) return;
  held.visual = false;
  const cleanup = held.off.splice(0), clear = held.clearTarget;
  held.clearTarget = undefined;
  held.finishWait();quiet(() => releaseSourceArt(held.claim));quiet(clear);
  for (const off of cleanup) quiet(off);
  if (skip) {try {held.transition?.skipTransition();} catch { /* Already settled. */ }}
}
function retire(held: Original): void {
  if (!held.live) return;
  held.live = false;if (owners.get(held.document) === held) owners.delete(held.document);
  held.reverse?.();endVisual(held, true);
}
export function retireTitleArtworkScope(document: Document, scope: string): void {
  const held = owners.get(document);if (held?.intent.scope === scope) retire(held);
}

/** The original effect has already admitted its entry. A physical activation is
 * optional evidence; fallback always uses the very same ordinary navigate. */
export function navigateTitleArtwork(art: HTMLElement | undefined, input: TitleArtworkIntent): Promise<void> {
  const intent = Object.freeze({...input, source: frozen(input.source)}), document = art?.ownerDocument;
  const permitted = () => {
    const admitted = intent.admitted();
    const principal = intent.principalCurrent();
    return admitted && principal && intent.admitted() && same(intent.source, intent.location());
  };
  const ordinary = () => permitted() ? intent.navigate(false) : Promise.reject(new DOMException('Artwork opening retired', 'AbortError'));
  if (!art || !document || !intent.source.key || !intent.scope || !intent.itemId) return ordinary();
  // Recheck after an external predicate before either capture or dispatch.
  if (!intent.principalCurrent() || !intent.admitted() || !same(intent.source, intent.location())) return Promise.reject(new DOMException('Artwork opening retired', 'AbortError'));
  if (!intent.sourceCurrent() || intent.browserOwns() || reduced(document) || typeof document.startViewTransition !== 'function') return ordinary();
  const sourceImage = decodedImage(art);
  if (!sourceImage) return ordinary();
  const sourceSrc = sourceImage.getAttribute('src')!;
  let claim: SourceArtClaim | undefined;
  try {claim = captureSourceArt(art);} catch { /* Unknown visual facts use ordinary navigation. */ }
  if (!claim) return ordinary();
  if (!permitted()) {releaseSourceArt(claim);return Promise.reject(new DOMException('Artwork opening retired', 'AbortError'));}
  const previous = owners.get(document);if (previous) retire(previous);
  if (owners.has(document) || !permitted()) {releaseSourceArt(claim);return Promise.reject(new DOMException('Artwork opening superseded', 'AbortError'));}
  let finishWait!: () => void;
  const ready = new Promise<void>(resolve => {finishWait = resolve;});
  const held: Original = {live: true, visual: true, dispatched: false, intent, claim, document, sourceImage, sourceSrc, finishWait, off: []};
  owners.set(document, held);claims.set(claim, held);
  let navigation: Promise<void> | undefined;
  const dispatch = () => {
    if (held.dispatched) return navigation ?? Promise.resolve();
    held.dispatched = true;
    if (!principal(held) || !intent.admitted() || !owns(held) || !same(intent.source, intent.location())) {
      retire(held);navigation = Promise.reject(new DOMException('Artwork opening retired', 'AbortError'));
    } else {
      // A lost physical source cancels paint, never the admitted navigation.
      if (!intent.sourceCurrent()) endVisual(held, true);
      if (!owns(held) || !intent.admitted() || !owns(held) || !same(intent.source, intent.location())) {retire(held);return navigation = Promise.reject(new DOMException('Artwork opening retired', 'AbortError'));}
      // Withdraw the old name before any retained source and target coexist.
      releaseSourceArt(claim);
      try {navigation = intent.navigate(true);} catch (error) {navigation = Promise.reject(error);}
    }
    return navigation;
  };
  let off: () => void;
  try {off = intent.subscribe(event => {
    if (!owns(held)) return;
    if (!held.dispatched || !held.visual || !principal(held)) {retire(held);return;}
    if (!held.destination && same(event.from, intent.source) && event.to.key && event.to.key !== intent.source.key
      && event.to.pathname === `/item/${encodeURIComponent(intent.itemId)}`) {
      held.destination = frozen(event.to);return;
    }
    // A redirect, second entry (even same href), or reentrant navigation cannot
    // lend this intent a replacement destination.
    if (!same(held.destination, event.to)) retire(held);
  });} catch {retire(held);return ordinary();}
  keepVisual(held, off);
  if (!owns(held)) {releaseSourceArt(claim);return ordinary();}
  try {
  const media = document.defaultView?.matchMedia('(prefers-reduced-motion: reduce)');
  const policy = () => {if (owns(held) && (reduced(document) || intent.browserOwns())) endVisual(held, true);};
  media?.addEventListener('change', policy);keepVisual(held, () => media?.removeEventListener('change', policy));
  const Observer = document.defaultView?.MutationObserver;
  if (Observer) {const observer = new Observer(policy);observer.observe(document.documentElement, {attributes: true, subtree: true, attributeFilter: ['data-reduce-motion']});keepVisual(held, () => observer.disconnect());}
  const timer = setTimeout(() => endVisual(held, true), TITLE_ART_WAIT_MS);held.cancelWait = () => clearTimeout(timer);keepVisual(held, held.cancelWait);
  } catch {retire(held);return ordinary();}
  if (!held.visual || !owns(held)) {retire(held);return ordinary();}
  let resolveNavigation!: () => void, rejectNavigation!: (error: unknown) => void;
  const result = new Promise<void>((resolve, reject) => {resolveNavigation = resolve;rejectNavigation = reject;});
  const update = async () => {
    try {
      await dispatch();
      if (!held.destination || !destinationCurrent(held)) {endVisual(held, true);resolveNavigation();return;}
      resolveNavigation();
      if (held.visual) await ready;
      if (held.visual && !destinationCurrent(held)) endVisual(held, true);
    } catch (error) {retire(held);rejectNavigation(error);}
  };
  try {
    const transition = document.startViewTransition(update);held.transition = transition;
    if (!held.visual) transition.skipTransition();
    void transition.ready.then(() => {
      if (!held.visual || !destinationCurrent(held)) return;
      const pseudo = `::view-transition-group(${claim.name})`;
      for (const animation of document.documentElement.getAnimations({subtree: true})) {
        const effect = animation.effect as KeyframeEffect | null;
        if (effect?.pseudoElement === pseudo) effect.updateTiming(sourceArtMorph(false));
      }
    }, () => {});
    void transition.updateCallbackDone.catch(() => {if (!held.dispatched) void update();});
    void transition.finished.then(() => endVisual(held, false), () => endVisual(held, false));
  } catch {endVisual(held, true);void update();}
  return result;
}

/** Called in the new route's layout setup, never to adopt a render-time slot. */
export function admittedTitleArtwork(document: Document, scope: string, itemId: string, location: ArtworkLocation): SourceArtClaim | undefined {
  const held = owners.get(document);
  if (!held?.visual || held.intent.scope !== scope || held.intent.itemId !== itemId || !same(held.destination, location) || !destinationCurrent(held)) return;
  return held.visual && owns(held) ? held.claim : undefined;
}
/** The foreground Artwork's original decoded lease calls this with its committed
 * wrapper. Its source/lease checks happen before this purely visual handoff. */
export function commitTitleArtwork(claim: SourceArtClaim, node: HTMLElement, install: () => () => void): boolean {
  const held = claims.get(claim);
  if (!held?.visual || node.ownerDocument !== held.document || !destinationCurrent(held) || reduced(held.document) || held.intent.browserOwns() || !owns(held)) return false;
  const image = decodedImage(node, true);if (!image) return false;
  if (held.target === node && node.style.viewTransitionName === claim.name) return true;
  const oldCleanup = held.clearTarget;held.clearTarget = undefined;oldCleanup?.();
  if (!held.visual || !destinationCurrent(held) || !owns(held)) return false;
  releaseSourceArt(claim);
  const cleanup = install();
  if (!held.visual || !destinationCurrent(held) || node.style.viewTransitionName !== claim.name) {cleanup();return false;}
  held.target = node;held.targetImage = image;held.targetSrc = image.getAttribute('src')!;held.clearTarget = cleanup;held.cancelWait?.();held.finishWait();return true;
}
export function isTitleArtworkIntent(claim: SourceArtClaim): boolean {return claims.has(claim);}
export function releaseTitleArtwork(claim: SourceArtClaim): void {
  const held = claims.get(claim);if (!held) return;
  // The original router Back intentionally retires its title DOM before the
  // rendered event inside that same update callback. Its bounded visual receipt
  // uses the original principal and target entry, never the retired route guard.
  const reverse = held.reverse;
  if (reverse && principal(held) && held.reverse === reverse) {endVisual(held, true);return;}
  retire(held);
}

function originalPhysicalArtwork(held: Original): boolean {
  const art = held.claim.art;
  if (art.ownerDocument !== held.document || !held.intent.sourceCurrent() || !owns(held) || decodedImage(art) !== held.sourceImage || held.sourceImage.getAttribute('src') !== held.sourceSrc) return false;
  const bounds = art.getBoundingClientRect(), css = art.ownerDocument.defaultView?.getComputedStyle(art), view = art.ownerDocument.defaultView;
  return owns(held) && sourceArtReverseEligible({attached: art.isConnected, width: bounds.width, height: bounds.height, top: bounds.top, left: bounds.left,
    visible: !!css && css.visibility !== 'hidden' && css.display !== 'none' && Number(css.opacity) !== 0,
    intersectsViewport: viewportIntersects(bounds, {width: view?.innerWidth, height: view?.innerHeight})});
}
/** Reverse only to the actual original physical node. A remounted or recycled
 * card has no such receipt and uses the ordinary Back path. */
export function originalArtworkReverseAvailable(claim: SourceArtClaim): boolean {
  const held = claims.get(claim), art = claim.art;
  if (!held || !destinationCurrent(held) || reduced(held.document) || !held.intent.sourceCurrent() || !owns(held)) return false;
  return originalPhysicalArtwork(held);
}

/** Back stays the router's own traversal and native update callback. The only
 * synchronous commit work is an original-node name transfer, never navigation,
 * I/O, decode waiting or another View Transition. */
export function backTitleArtwork(document: Document, scope: string, back: () => void,
  subscribeRendered: (listener: (to: ArtworkLocation) => void) => () => void): void {
  let performed = false, failedCleanup: (() => void) | undefined;
  const ordinary = () => {if (performed) return;performed = true;back();};
  try {
  const held = owners.get(document), hero = held?.target;
  if (!held || held.intent.scope !== scope || !hero?.isConnected || hero.ownerDocument !== held.document || !destinationCurrent(held) || reduced(document)
    || held.intent.browserOwns() || typeof document.startViewTransition !== 'function' || decodedImage(hero, true) !== held.targetImage || held.targetImage?.getAttribute('src') !== held.targetSrc) {ordinary();return;}
  const view = document.defaultView, computed = view?.getComputedStyle(hero).viewTransitionName;
  if (computed && computed !== 'none' && computed !== held.claim.name) {ordinary();return;}
  held.reverse?.();
  let live = true, frame: number | undefined, timer: ReturnType<typeof setTimeout> | undefined;
  let off: (() => void) | undefined, offPolicy: (() => void) | undefined;
  const animations = new Set<Animation>(), resources: (() => void)[] = [];
  const keep = (off: () => void) => {if (ownsAttempt()) resources.push(off);else quiet(off);};
  const ownsAttempt = () => live && owns(held) && held.reverse === cleanup;
  const clearName = (node: HTMLElement) => {if (node.style.viewTransitionName === held.claim.name) node.style.viewTransitionName = '';};
  const cleanup = () => {
    if (!live) return;live = false;
    if (held.reverse === cleanup) held.reverse = undefined;
    for (const release of resources.splice(0)) quiet(release);if (timer !== undefined) clearTimeout(timer);
    if (frame !== undefined) view?.cancelAnimationFrame(frame);
    quiet(() => clearName(hero));quiet(() => clearName(held.claim.art));
    for (const animation of animations) animation.cancel();animations.clear();
  };
  failedCleanup = cleanup;held.reverse = cleanup;
  const valid = () => ownsAttempt() && principal(held) && ownsAttempt() && !reduced(document) && !held.intent.browserOwns() && ownsAttempt();
  let animationFrames = 0;
  const animate = () => {
    frame = undefined;animationFrames++;
    if (!valid() || !same(held.intent.source, held.intent.location())) {cleanup();return;}
    const pseudo = `::view-transition-group(${held.claim.name})`;
    for (const animation of document.documentElement.getAnimations({subtree: true})) {
      const effect = animation.effect as KeyframeEffect | null;
      if (effect?.pseudoElement !== pseudo) continue;
      effect.updateTiming(sourceArtMorph(false));animations.add(animation);
      void animation.finished.then(cleanup, cleanup);
    }
    if (!animations.size && animationFrames < 3 && ownsAttempt()) frame = view?.requestAnimationFrame(animate);
  };
  off = subscribeRendered(to => {
    if (!valid()) {cleanup();return;}
    const now = held.intent.location();
    if (!same(held.intent.source, to) || !same(to, now) || !originalPhysicalArtwork(held) || !valid()) {cleanup();return;}
    // onRendered is emitted inside the installed router's native update callback.
    // Clear the captured hero first even if its DOM survived the commit.
    clearName(hero);
    const art = held.claim.art, name = view?.getComputedStyle(art).viewTransitionName;
    if (name && name !== 'none' && name !== held.claim.name || !valid() || !same(to, held.intent.location())) {cleanup();return;}
    art.style.viewTransitionName = held.claim.name;
    off?.();off = undefined;
    frame = view?.requestAnimationFrame(animate);
  });
  const originalOff = off;let stopped = false;off = () => {if (stopped) return;stopped = true;originalOff?.();};keep(off);
  if (!ownsAttempt()) {ordinary();return;}
  const media = view?.matchMedia('(prefers-reduced-motion: reduce)');
  const policy = () => {if (!valid()) cleanup();};
  media?.addEventListener('change', policy);
  const Observer = view?.MutationObserver, observer = Observer ? new Observer(policy) : undefined;
  observer?.observe(document.documentElement, {attributes: true, subtree: true, attributeFilter: ['data-reduce-motion']});
  offPolicy = () => {media?.removeEventListener('change', policy);observer?.disconnect();};keep(offPolicy);
  timer = setTimeout(cleanup, TITLE_ART_WAIT_MS);
  if (!valid() || !destinationCurrent(held)) {cleanup();ordinary();return;}
  hero.style.viewTransitionName = held.claim.name;
  ordinary();
  } catch (error) {quiet(failedCleanup);if (performed) throw error;ordinary();}
}
