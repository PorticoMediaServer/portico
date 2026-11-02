/** How many whole title lines a generated cover can draw (FX-P3). The copy box's height, less its
 *  padding, the rule above the title and the author below it, holds this many lines; the title is
 *  clamped there, so it ends on a whole line with an ellipsis instead of being cut by the cover's
 *  edge. Never more than the authored five, and always one. */
export const COVER_MAX_LINES = 5;
export type CoverFitInput = Readonly<{copyHeight: number; paddingTop: number; paddingBottom: number; rule: number; author: number; lineHeight: number}>;
export function coverTitleLines({copyHeight, paddingTop, paddingBottom, rule, author, lineHeight}: CoverFitInput): number {
  if (!(lineHeight > 0) || !(copyHeight > 0)) return COVER_MAX_LINES;
  // Keep a little air between the last title line and the author.
  const gap = author > 0 ? lineHeight * .35 : 0;
  const room = copyHeight - paddingTop - paddingBottom - rule - author - gap;
  return Math.max(1, Math.min(COVER_MAX_LINES, Math.floor(room / lineHeight + .02)));
}
/** The parts of a copy box that depend only on its size and authored scale, not on its text. */
type CoverFrameMetrics = Readonly<{paddingTop: number; paddingBottom: number; rule: number; lineHeight: number}>;
const px = (value: string) => Number.parseFloat(value) || 0;
/** A grid of generated covers shares a handful of sizes, so the computed-style reads are made
 *  once per size and scale, not once per cover (FX-P3 follow-up). Bounded; oldest out first. */
const frames = new Map<string, CoverFrameMetrics>();
const FRAME_CACHE_LIMIT = 64;
function frameMetrics(view: Window, copy: HTMLElement, title: HTMLElement, key: string): CoverFrameMetrics {
  const known = frames.get(key);
  if (known) return known;
  const box = view.getComputedStyle(copy), titleStyle = view.getComputedStyle(title), before = view.getComputedStyle(copy, '::before');
  const metrics: CoverFrameMetrics = {paddingTop: px(box.paddingTop), paddingBottom: px(box.paddingBottom),
    rule: before.display === 'none' || before.content === 'none' ? 0 : px(before.height) + px(before.marginBottom),
    lineHeight: px(titleStyle.lineHeight) || px(titleStyle.fontSize) * 1.03};
  if (frames.size >= FRAME_CACHE_LIMIT) frames.delete(frames.keys().next().value!);
  frames.set(key, metrics);
  return metrics;
}
/** Reads the drawn copy box and returns its line count; the DOM side of `coverTitleLines`.
 *  `scale` names everything besides the size that changes the box's styles (font scale, title
 *  class, selection clearance), so two covers of one size and scale share one style read. */
export function measureCoverTitleLines(copy: HTMLElement, scale = ''): number {
  const view = copy.ownerDocument.defaultView;
  const title = copy.querySelector('b'), author = copy.querySelector('small');
  if (!view || !title) return COVER_MAX_LINES;
  const width = copy.clientWidth, height = copy.clientHeight;
  // The selection clearance pads differently under 600 px (artwork.module.css), so the window class is part of the key.
  const metrics = frameMetrics(view, copy, title, `${width}x${height}|${scale}|${view.innerWidth < 600}`);
  // The author's height depends on its own text (one line or two), so it is read per cover.
  const authorHeight = author && author.offsetParent !== null ? author.getBoundingClientRect().height : 0;
  return coverTitleLines({copyHeight: height, ...metrics, author: authorHeight});
}

type SizeWatch = {observer: ResizeObserver; callbacks: Map<Element, () => void>};
const watches = new WeakMap<Window, SizeWatch>();
/** One ResizeObserver per window for every generated cover, not one per cover. */
export function observeCoverSize(element: Element, callback: () => void): () => void {
  const view = element.ownerDocument.defaultView;
  if (!view?.ResizeObserver) return () => {};
  let watch = watches.get(view);
  if (!watch) {
    const callbacks = new Map<Element, () => void>();
    watch = {callbacks, observer: new view.ResizeObserver(entries => {for (const entry of entries) callbacks.get(entry.target)?.();})};
    watches.set(view, watch);
  }
  const {observer, callbacks} = watch;
  callbacks.set(element, callback);
  observer.observe(element);
  return () => {
    if (callbacks.get(element) !== callback) return;
    callbacks.delete(element);
    observer.unobserve(element);
  };
}
