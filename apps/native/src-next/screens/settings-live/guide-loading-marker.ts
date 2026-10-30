/** FX-072: the guide's programme loading marker keeps the exact cell footprint.
 * The quiet window and the static paint are LoadingPaint's (ready=false); this
 * model owns geometry only, so no per-cell clock, timer or visibility state is
 * introduced here. */
export function guideLoadingMarkerBox(cellWidth: number, rowHeight: number, compact: boolean) {
  return {
    width: Math.max(0, cellWidth),
    height: Math.max(0, rowHeight - (compact ? 6 : 0)),
    radius: compact ? 8 : 12,
    top: compact ? 3 : 0,
  } as const;
}
