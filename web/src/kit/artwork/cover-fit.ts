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
/** Reads the drawn copy box and returns its line count; the DOM side of `coverTitleLines`. */
export function measureCoverTitleLines(copy: HTMLElement): number {
  const view = copy.ownerDocument.defaultView;
  const title = copy.querySelector('b'), author = copy.querySelector('small');
  if (!view || !title) return COVER_MAX_LINES;
  const box = view.getComputedStyle(copy), titleStyle = view.getComputedStyle(title), before = view.getComputedStyle(copy, '::before');
  const px = (value: string) => Number.parseFloat(value) || 0;
  const lineHeight = px(titleStyle.lineHeight) || px(titleStyle.fontSize) * 1.03;
  const rule = before.display === 'none' || before.content === 'none' ? 0 : px(before.height) + px(before.marginBottom);
  const authorHeight = author && view.getComputedStyle(author).display !== 'none' ? author.getBoundingClientRect().height : 0;
  return coverTitleLines({copyHeight: copy.clientHeight, paddingTop: px(box.paddingTop), paddingBottom: px(box.paddingBottom), rule, author: authorHeight, lineHeight});
}
