/**
 * How far a control is from the focused one in the direction of a key: the measure the focus
 * engine (`focus.tsx`) uses to choose where an arrow goes, as the native engines do. The edge
 * closest along the key's axis wins, and sideways distance counts double against a candidate, so
 * Down from a card in a grid keeps its column and Right in a row stays in the row.
 */
export type Direction = 'up' | 'down' | 'left' | 'right';

export type Rect = {left: number; top: number; right: number; bottom: number};
/** How far `candidate` is from `from` in `direction`, or Infinity when it is not that way. */
export function distance(from: Rect, candidate: Rect, direction: Direction): number {
  const slack = 1;
  let along: number, across: number;
  if (direction === 'left' || direction === 'right') {
    along = direction === 'right' ? candidate.left - from.right : from.left - candidate.right;
    if (along < -slack) {
      // Overlapping on the axis still counts when the centre lies that way.
      const centre = (candidate.left + candidate.right) / 2, fromCentre = (from.left + from.right) / 2;
      if (direction === 'right' ? centre <= fromCentre + slack : centre >= fromCentre - slack) return Infinity;
      along = 0;
    }
    across = candidate.bottom < from.top ? from.top - candidate.bottom : candidate.top > from.bottom ? candidate.top - from.bottom : 0;
  } else {
    along = direction === 'down' ? candidate.top - from.bottom : from.top - candidate.bottom;
    if (along < -slack) {
      const centre = (candidate.top + candidate.bottom) / 2, fromCentre = (from.top + from.bottom) / 2;
      if (direction === 'down' ? centre <= fromCentre + slack : centre >= fromCentre - slack) return Infinity;
      along = 0;
    }
    across = candidate.right < from.left ? from.left - candidate.right : candidate.left > from.right ? candidate.left - from.right : 0;
  }
  return Math.max(0, along) + across * 2;
}
