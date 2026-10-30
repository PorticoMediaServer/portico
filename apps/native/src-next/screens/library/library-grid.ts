/** The tablet browse grid is measured from the pane it actually sits in.
 *
 * tablet.html #4 docks Filters and the grid beside it reads three columns at
 * about 145 pt; #5 draws the upright grid as four columns of 152 pt cards. The
 * drawn values are a 152 pt card, 16 pt between columns, 26 pt between rows, a
 * 32 pt gutter when expanded and 28 pt on medium, and 52 pt kept clear on the
 * right while the A–Z rail is drawn.
 *
 * Columns then follow the real pane, never the window: a docked panel, a
 * collapsed sidebar or a fold changes the pane and the same cards reflow.
 * Three columns is the floor, because no tablet drawing goes narrower.
 */
export const libraryTabletGrid = Object.freeze({
  cardWidth: 152, gap: 16, rowGap: 26, railRight: 52, minColumns: 3,
  gutter: Object.freeze({medium: 28, expanded: 32}),
} as const);

export type LibraryGridGeometry = Readonly<{columns: number; itemWidth: number; gap: number; rowGap: number; gutter: number; rightGutter: number}>;

/** `paneWidth` is the laid-out width of the grid's own column, after the rail
 * and any docked pane have taken theirs. `rail` is true only while the A–Z rail
 * is actually drawn, so its 52 pt stay clear only then. An unmeasured or
 * nonsense pane falls back to the three-column floor instead of a bad width. */
export function libraryTabletGridGeometry(paneWidth: number, options: Readonly<{expanded: boolean; rail: boolean}>): LibraryGridGeometry {
  const {cardWidth, gap, rowGap, minColumns} = libraryTabletGrid;
  const gutter = options.expanded ? libraryTabletGrid.gutter.expanded : libraryTabletGrid.gutter.medium;
  const rightGutter = options.rail ? libraryTabletGrid.railRight : gutter;
  const pane = Number.isFinite(paneWidth) ? Math.max(0, paneWidth) : 0;
  const room = Math.max(0, pane - gutter - rightGutter);
  const columns = Math.max(minColumns, Math.floor((room + gap) / (cardWidth + gap)));
  const itemWidth = Math.max(1, Math.floor((room - (columns - 1) * gap) / columns));
  return {columns, itemWidth, gap, rowGap, gutter, rightGutter};
}
