/** Rows to load when a guide/list body scrolls with the page. */
export function guideVisibleRows(bodyTop: number, viewportHeight: number, rowHeight: number, total: number) {
  if (!(rowHeight > 0) || total < 1) return {first: 0, last: 0};
  const first = Math.min(total - 1, Math.max(0, Math.floor(-bodyTop / rowHeight) - 1));
  const last = Math.min(total - 1, Math.max(first, Math.ceil((viewportHeight - bodyTop) / rowHeight) + 2));
  return {first, last};
}

/** One-line channel, programme, facts, progress and next-airing compact stack. */
export function guideListRowHeight(phone: boolean): number { return phone ? 128 : 92; }
