// Test-only historical model; no production consumer mounts this module.
/** FX-075 clearance observations: the numeric processor behind the toast, dock and
 * tab-bar report.
 *
 * Everything here is same-coordinate-space geometry the caller already measured in
 * one window: the three frames, the safe inset, the explicit scale and the window
 * identity. It reports the signed vertical edge gap between the toast's bottom edge
 * and an obstruction's top edge, and separately whether the two rectangles actually
 * intersect on both axes. It never infers a dock from playback, never substitutes a
 * declared height for a measurement, and never carries a toast body, action or
 * private identifier. */

export type ClearanceUnits = 'pt' | 'dp' | 'px';
export type ClearanceRect = Readonly<{x: number; y: number; width: number; height: number}>;

export type ClearanceSnapshot = Readonly<{
  /** Version of this observation shape. */
  schema: 1;
  units: ClearanceUnits;
  /** Explicit renderer scale for the window this snapshot was taken in. */
  scale: number;
  window: Readonly<{id: string; width: number; height: number}>;
  /** Safe-area bottom inset in the same units and space as the rectangles. */
  safeBottom: number;
  /** The drawn toast frame, when this window actually drew one. */
  toast?: ClearanceRect;
  /** The drawn dock frame, when one was actually on screen. */
  dock?: ClearanceRect;
  /** The drawn tab bar frame, when one was actually on screen. */
  tabBar?: ClearanceRect;
  /** Declared component heights are provenance only: they are never treated as a
   * physical measurement of anything on screen. */
  declared?: Readonly<{dockHeight?: number; tabBarHeight?: number}>;
}>;

export type ClearanceReading = Readonly<{
  /** Signed vertical edge gap in the snapshot's own units: obstruction top minus
   * toast bottom. Negative means the obstruction starts above the toast's bottom
   * edge; it is not by itself a physical overlap. */
  verticalGap: number;
  /** True only when both rectangles intersect positively on the horizontal AND the
   * vertical axis. A horizontally disjoint obstruction never intersects. */
  intersects: boolean;
}>;

export type ClearanceObservation = Readonly<{
  schema: 1;
  /** The caller's actual window identity, kept privately for same-window comparison.
   * It is never printed: receipts replace it with their own ordinal labels. */
  windowIdentity: string;
  units: ClearanceUnits;
  scale: number;
  /** The safe-area bottom inset this observation was read with, in the same units. */
  safeBottom: number;
  /** True only when at least one obstruction produced a real measured reading. */
  observed: boolean;
  source: 'measured' | 'declared-only' | 'unobserved';
  /** A role this window did not draw stays absent: unknown, never false evidence. */
  readings: Readonly<{dock?: ClearanceReading; tabBar?: ClearanceReading}>;
  reason?: 'missing-measurement' | 'invalid-geometry';
}>;

const finite = (value: number | undefined): value is number => value !== undefined && Number.isFinite(value);
const validRect = (rect: ClearanceRect): boolean => finite(rect.x) && finite(rect.y) && finite(rect.width)
  && finite(rect.height) && rect.width > 0 && rect.height > 0;

const read = (toast: ClearanceRect, obstruction: ClearanceRect): ClearanceReading => {
  const width = Math.min(toast.x + toast.width, obstruction.x + obstruction.width) - Math.max(toast.x, obstruction.x);
  const height = Math.min(toast.y + toast.height, obstruction.y + obstruction.height) - Math.max(toast.y, obstruction.y);
  return Object.freeze({verticalGap: obstruction.y - (toast.y + toast.height), intersects: width > 0 && height > 0});
};

const unobserved = (snapshot: ClearanceSnapshot, reason: NonNullable<ClearanceObservation['reason']>): ClearanceObservation => Object.freeze({
  schema: 1, windowIdentity: snapshot.window.id, units: snapshot.units, scale: snapshot.scale, safeBottom: snapshot.safeBottom,
  observed: false, source: 'unobserved', readings: Object.freeze({}), reason,
});

/** The numeric reading for one window's snapshot. A supplied but impossible rectangle
 * is invalid geometry, not a missing measurement; a role the window never drew stays
 * unknown rather than an empty overlap flag. */
export function readClearance(snapshot: ClearanceSnapshot): ClearanceObservation {
  if (!finite(snapshot.scale) || snapshot.scale <= 0 || !finite(snapshot.safeBottom)
    || !snapshot.window?.id || !finite(snapshot.window.width) || !finite(snapshot.window.height)
    || snapshot.window.width <= 0 || snapshot.window.height <= 0) {
    return unobserved(snapshot, 'invalid-geometry');
  }
  for (const supplied of [snapshot.toast, snapshot.dock, snapshot.tabBar]) {
    if (supplied !== undefined && !validRect(supplied)) return unobserved(snapshot, 'invalid-geometry');
  }
  const toast = snapshot.toast;
  if (!toast) return unobserved(snapshot, 'missing-measurement');
  const dock = snapshot.dock ? read(toast, snapshot.dock) : undefined;
  const tabBar = snapshot.tabBar ? read(toast, snapshot.tabBar) : undefined;
  const any = dock !== undefined || tabBar !== undefined;
  return Object.freeze({
    schema: 1, windowIdentity: snapshot.window.id, units: snapshot.units, scale: snapshot.scale, safeBottom: snapshot.safeBottom,
    observed: any, source: any ? 'measured' : 'declared-only',
    readings: Object.freeze({...(dock ? {dock} : {}), ...(tabBar ? {tabBar} : {})}),
    ...(any ? {} : {reason: 'missing-measurement' as const}),
  });
}

export type ClearanceResize = Readonly<{
  schema: 1;
  units: ClearanceUnits;
  scale: number;
  observed: boolean;
  /** Vertical-gap deltas between two readings of the same window in the same units
   * and scale; undefined when either side is unobserved or the metadata differs. */
  deltas?: Readonly<{dock?: number; tabBar?: number; safeBottom: number}>;
  reason?: 'replaced-window' | 'incompatible-units' | 'incompatible-scale' | 'unobserved';
}>;

/** A resize comparison needs two real observations of the same window, in the same
 * units and at the same renderer scale; anything else is refused explicitly. */
export function compareClearance(before: ClearanceObservation, after: ClearanceObservation): ClearanceResize {
  const base = {schema: 1 as const, units: after.units, scale: after.scale, observed: false};
  if (before.windowIdentity !== after.windowIdentity) return Object.freeze({...base, reason: 'replaced-window' as const});
  if (before.units !== after.units) return Object.freeze({...base, reason: 'incompatible-units' as const});
  if (before.scale !== after.scale) return Object.freeze({...base, reason: 'incompatible-scale' as const});
  if (!before.observed || !after.observed) return Object.freeze({...base, reason: 'unobserved' as const});
  const delta = (a?: ClearanceReading, b?: ClearanceReading) => a !== undefined && b !== undefined ? b.verticalGap - a.verticalGap : undefined;
  const dock = delta(before.readings.dock, after.readings.dock);
  const tabBar = delta(before.readings.tabBar, after.readings.tabBar);
  return Object.freeze({...base, observed: true, deltas: Object.freeze({
    ...(dock !== undefined ? {dock} : {}), ...(tabBar !== undefined ? {tabBar} : {}),
    safeBottom: after.safeBottom - before.safeBottom,
  })});
}

export type ClearanceReceipt = Readonly<{
  schema: 1;
  /** This report is source/observation only; it never claims FX-075 runtime or DONE. */
  claim: 'source-only';
  /** Every window keeps its own units and scale; a mixed report is labelled mixed. */
  units: ClearanceUnits | 'mixed' | 'missing';
  windows: readonly Readonly<{
    /** Receipt-local ordinal label. Raw caller window ids never enter the receipt. */
    label: string;
    units: ClearanceUnits;
    scale: number;
    observed: boolean;
    readings: Readonly<{dock?: ClearanceReading; tabBar?: ClearanceReading}>;
  }>[];
  observed: boolean;
  /** The only identity handling this receipt performs. */
  redaction: Readonly<{windowIdentity: 'ordinal-labels'}>;
}>;

/** A source-only receipt. Window identity is replaced by a receipt-local ordinal, and
 * each window keeps its own units and scale instead of borrowing the first one. */
export function clearanceReceipt(observations: readonly ClearanceObservation[]): ClearanceReceipt {
  const units = new Set(observations.map(observation => observation.units));
  // Ordinals are assigned here, at the output boundary, and reused for repeat
  // observations of the same actual window.
  const labels = new Map<string, string>();
  for (const observation of observations) {
    if (!labels.has(observation.windowIdentity)) labels.set(observation.windowIdentity, `window-${labels.size + 1}`);
  }
  return Object.freeze({
    schema: 1, claim: 'source-only',
    units: observations.length === 0 ? 'missing' : units.size === 1 ? observations[0]!.units : 'mixed',
    windows: Object.freeze(observations.map(observation => Object.freeze({
      label: labels.get(observation.windowIdentity)!,
      units: observation.units,
      scale: observation.scale,
      observed: observation.observed,
      readings: observation.readings,
    }))),
    observed: observations.some(observation => observation.observed),
    redaction: Object.freeze({windowIdentity: 'ordinal-labels' as const}),
  });
}
