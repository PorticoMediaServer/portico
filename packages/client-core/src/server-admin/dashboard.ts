/**
 * The Dashboard's figures, worked out once for every client: the health sentence's facts, the
 * load tiles and their values, and the summary of what was watched.
 */
import type {Measurement} from '../console.ts';
import type {Metric, MetricName} from '../telemetry.ts';
import type {MessageId} from '../../../i18n/src/index.ts';
import {formatBytes} from '../presentation/content.ts';

type Fact = Measurement['facts'][string];
const measured = (f?: Fact) => !!f && (f.state === 'available' || f.state === 'measured' || f.state === 'ok' || f.state === 'limited') && f.value !== null && f.value !== undefined;
const number = (f?: Fact) => (measured(f) && typeof f!.value === 'number' ? (f!.value as number) : undefined);

export type HealthFacts = Readonly<{/** The database reports a problem: the server needs attention. */ degraded: boolean; uptimeSeconds?: number; freeBytes?: number; databaseBytes?: number}>;

/** What the Health line says, from the `health` and `storage` measurements. A figure the server did not measure is absent. */
export function healthFacts(health?: Measurement, storage?: Measurement): HealthFacts {
  const facts = health?.facts ?? {};
  return {
    degraded: !!facts.database && !measured(facts.database),
    uptimeSeconds: number(facts.uptime),
    freeBytes: number(storage?.facts.stateVolumeAvailable),
    databaseBytes: number(storage?.facts.databaseBytes),
  };
}

export type LoadTile = Readonly<{metric: MetricName; label: MessageId; unit: 'percent' | 'rate'}>;

/** The load tiles, in the order they are shown. */
export const LOAD_TILES: readonly LoadTile[] = [
  {metric: 'cpu', label: 'web.telemetry.metricCpu', unit: 'percent'}, {metric: 'memory', label: 'web.telemetry.metricMemory', unit: 'percent'},
  {metric: 'netOut', label: 'web.telemetry.metricNetOut', unit: 'rate'}, {metric: 'diskRead', label: 'web.telemetry.metricDiskRead', unit: 'rate'},
  {metric: 'gpuUsage', label: 'web.telemetry.metricGpu', unit: 'percent'}, {metric: 'gpuEncoder', label: 'web.telemetry.metricEncoder', unit: 'percent'},
];

/** A tile's current value in words; a metric this server cannot measure is a dash, never zero. */
export function loadValue(tile: LoadTile, status?: Metric): string {
  if (!status || status.status === 'unavailable') return '—';
  return tile.unit === 'percent' ? `${Math.round(status.value)}%` : `${formatBytes(status.value)}/s`;
}

/** The load tiles a server can fill: one it cannot measure is left out, never drawn empty (hide what is absent). */
export function measuredLoadTiles(status: Readonly<Record<string, {status: string} | undefined>>): readonly LoadTile[] {
  return LOAD_TILES.filter(tile => status[tile.metric] && status[tile.metric]!.status !== 'unavailable');
}
