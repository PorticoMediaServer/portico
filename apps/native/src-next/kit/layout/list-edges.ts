export type ListEdges = Readonly<{top: boolean; bottom: boolean}>;
export const noListEdges: ListEdges = Object.freeze({top: false, bottom: false});
/** Which ends of a scrolled list have more beyond them, for `ListEdgeFade`. Only a list that really continues fades. */
export function listEdges(offset: number, contentHeight: number, viewportHeight: number): ListEdges {
  if (viewportHeight <= 0 || contentHeight <= viewportHeight + 1) return noListEdges;
  return {top: offset > 1, bottom: offset + viewportHeight < contentHeight - 1};
}
