import type {MetricName, TelemetryPoint, TelemetryReading} from '@core/telemetry.ts';
import {defaultI18n} from '@i18n';
import {LOAD_TILES, loadValue} from '@core/server-admin/dashboard.ts';

const warmingUp = 'Waiting for a second reading before a rate can be reported.';
type History = 'samples' | 'gap';
export type HealthTile = Readonly<{
  metric: MetricName; label: (typeof LOAD_TILES)[number]['label']; value: string;
  points: readonly TelemetryPoint[]; history: History; limited: boolean; details: readonly string[];
}>;
const known = (point: TelemetryPoint) => (point.status === 'available' || point.status === 'limited')
  && point.detail !== warmingUp && Number.isFinite(point.v) && point.v >= 0;
const details = (items: readonly (string | undefined)[]) => [...new Set(items.filter((value): value is string => !!value))];

/** Unknown returned points invalidate the whole history; measured current zero remains visible.
 * The parser may discard malformed raw points or details before this boundary;
 * this check cannot establish completeness of the original raw series. */
export function healthTiles(reading?: TelemetryReading): readonly HealthTile[] {
  return LOAD_TILES.flatMap<HealthTile>(tile => {
    const metric = reading?.status[tile.metric];
    if (!metric || metric.status === 'unavailable' || !Number.isFinite(metric.value) || metric.value < 0 || metric.detail === warmingUp) return [];
    const points = reading?.series[tile.metric] ?? [], safe = points.every(known);
    return [{metric: tile.metric, label: tile.label, value: loadValue(tile, metric),
      points: safe ? points : [], history: safe ? 'samples' : 'gap', limited: metric.status === 'limited',
      details: details([metric.status === 'limited' ? metric.detail : undefined])}];
  });
}

export type HealthPlot = Readonly<{line: string; area: string; endX: number; endY: number}>;
/** Measured nonnegative observations only: actual interval on x, zero baseline and 10% headroom. */
export function healthPlot(points: readonly TelemetryPoint[], from: number, until: number): HealthPlot | undefined {
  if (!Number.isFinite(from) || !Number.isFinite(until) || until <= from || points.length < 2) return undefined;
  if (points.some((p, i) => !Number.isFinite(p.t) || !known(p) || p.t < from || p.t > until || (i > 0 && p.t <= points[i - 1].t))) return undefined;
  const peak = Math.max(...points.map(p => p.v)), top = peak * 1.1;
  const coords = points.map(p => ({x: (p.t - from) / (until - from) * 100, y: top === 0 ? 44 : 44 - p.v / top * 40}));
  const line = coords.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(' ');
  const first = coords[0], last = coords[coords.length - 1];
  return {line, area: `${line} L${last.x.toFixed(2)},46 L${first.x.toFixed(2)},46 Z`, endX: last.x, endY: last.y};
}

export type HealthDetail = Readonly<{
  line: string; area: string; endX: number; endY: number; top: number;
  low: number; average: number; high: number; highAt: number;
}>;
/** One validated observation set feeds both the chart and all three statistics. */
export function healthDetail(points: readonly TelemetryPoint[], from: number, until: number, percent: boolean): HealthDetail | undefined {
  if (!Number.isFinite(from) || !Number.isFinite(until) || until <= from || points.length < 2) return undefined;
  if (points.some((p, i) => !Number.isFinite(p.t) || !known(p) || (percent && p.v > 100)
    || p.t < from || p.t > until || (i > 0 && p.t <= points[i - 1].t))) return undefined;
  const high = Math.max(...points.map(p => p.v)), low = Math.min(...points.map(p => p.v));
  const top = percent ? 100 : high;
  const coords = points.map(p => ({x: (p.t - from) / (until - from) * 100, y: top === 0 ? 100 : 100 - p.v / top * 100}));
  const line = coords.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(' ');
  const first = coords[0], last = coords[coords.length - 1];
  return {line, area: `${line} L${last.x.toFixed(2)},100 L${first.x.toFixed(2)},100 Z`, endX: last.x, endY: last.y,
    top, low, high, average: points.reduce((sum, p) => sum + p.v, 0) / points.length, highAt: points.find(p => p.v === high)!.t};
}
