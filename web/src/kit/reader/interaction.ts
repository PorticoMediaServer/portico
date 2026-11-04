export type ReadDirection = 'ltr' | 'rtl' | 'vertical';
export type ReadTurn = 'next' | 'previous';

/** RD-9: the direction a comic is read in, its in-book choice or else its file's; any other book
 * follows its file. Vertical stays vertical, never a left-to-right paged comic. */
export function readingDirection(comic: boolean, chosen: 'file' | 'ltr' | 'rtl' | 'vertical', file: string | undefined): ReadDirection {
  return comic && chosen !== 'file' ? chosen : file === 'rtl' ? 'rtl' : 'ltr';
}

/** Desktop-read 6, phone-read 5, motion 5.1. Physical arrows mirror in RTL. */
export function readKeyTurn(key: string, direction: ReadDirection, shift = false): ReadTurn | undefined {
  // Keep the browser/renderer text-selection shortcuts; Shift+Space is reading back.
  if (shift && key !== ' ') return undefined;
  if (key === 'ArrowLeft') return direction === 'rtl' ? 'next' : 'previous';
  if (key === 'ArrowRight') return direction === 'rtl' ? 'previous' : 'next';
  // A vertical strip also turns with the up and down arrows (RD-9).
  if (direction === 'vertical' && key === 'ArrowDown') return 'next';
  if (direction === 'vertical' && key === 'ArrowUp') return 'previous';
  if (key === 'PageDown' || key === ' ' && !shift) return 'next';
  if (key === 'PageUp' || key === ' ' && shift) return 'previous';
  return undefined;
}

/** Position is viewport-relative, not a page number or a fabricated resume. */
export function readTapTurn(fraction: number, direction: ReadDirection): ReadTurn | 'chrome' | undefined {
  if (!Number.isFinite(fraction) || fraction < 0 || fraction > 1) return undefined;
  if (fraction >= 1 / 3 && fraction <= 2 / 3) return 'chrome';
  return fraction < 1 / 3 ? direction === 'rtl' ? 'next' : 'previous' : direction === 'rtl' ? 'previous' : 'next';
}

export function observedProgress(value: number | undefined): number | undefined {
  return value !== undefined && Number.isFinite(value) && value >= 0 && value <= 1 ? value : undefined;
}

/** Focused controls keep their own keyboard and pointer semantics. */
export function readerInteractiveTarget(target: EventTarget | null): boolean {
  return target instanceof Element && !!target.closest('button,a,input,textarea,select,[contenteditable]:not([contenteditable="false"]),[role="slider"],[role="tab"],[data-reader-panel]');
}

/** desktop-read 6, 9 and 10: each reader's toolbar in the order its frame draws it. EPUB: Contents,
 * Search, Bookmark, Aa. PDF: the zoom group (−, the zoom, +), then Pages, Two pages, Contents,
 * Search and Bookmark. Comic: the fit menu, Two pages, direction, Contents and Bookmark. Then More
 * and full screen. Any other action keeps its place ahead of More. */
const toolbarOrders = {
  epub: ['contents', 'search', 'bookmark', 'display'],
  pdf: ['zoom-out', 'zoom', 'zoom-in', 'pages', 'layout', 'contents', 'search', 'bookmark'],
  comic: ['zoom', 'layout', 'direction', 'contents', 'bookmark'],
} as const;
export function readerToolbarOrder<T extends Readonly<{id: string; separatorBefore?: boolean}>>(actions: readonly T[], kind: 'epub' | 'pdf' | 'comic' = 'epub'): T[] {
  const order: readonly string[] = toolbarOrders[kind], tail = ['more', 'fullscreen'];
  const rank = (id: string) => tail.includes(id) ? order.length + 1 + tail.indexOf(id) : order.includes(id) ? order.indexOf(id) : order.length;
  const sorted = actions.map((action, index) => ({action, index})).sort((a, b) => rank(a.action.id) - rank(b.action.id) || a.index - b.index).map(entry => entry.action);
  // PDF: a rule between the zoom group and the pages tools, as frame 10 draws.
  return kind === 'pdf' && sorted.some(action => action.id === 'zoom') ? sorted.map(action => action.id === 'pages' && !action.separatorBefore ? {...action, separatorBefore: true} : action) : sorted;
}
