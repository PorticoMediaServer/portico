/**
 * The Dashboard's figures, worked out once for every client: the health sentence's facts, the
 * load tiles and their values, and the summary of what was watched.
 */
import type {Measurement} from '../console.ts';
import type {Metric, MetricName} from '../telemetry.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

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

export type LoadTile = Readonly<{metric: MetricName; label: MessageId; unit: 'percent' | 'mbps' | 'count'}>;

/** The load tiles, in the order they are shown: what console rule 12 names and the rebuilt server measures. */
export const LOAD_TILES: readonly LoadTile[] = [
  {metric: 'homeMbps', label: 'next.dashboard.bandwidthHome', unit: 'mbps'}, {metric: 'awayMbps', label: 'next.dashboard.bandwidthAway', unit: 'mbps'},
  {metric: 'cpu', label: 'next.dashboard.processor', unit: 'percent'}, {metric: 'memory', label: 'next.dashboard.memory', unit: 'percent'},
  {metric: 'conversions', label: 'next.dashboard.conversions', unit: 'count'},
];

/** A tile's current value in words; a metric this server cannot measure is a dash, never zero. */
export function loadValue(tile: LoadTile, status?: Metric): string {
  if (!status || status.status === 'unavailable') return '—';
  return tile.unit === 'percent' ? `${Math.round(status.value)}%` : tile.unit === 'mbps' ? `${status.value.toFixed(1)} Mbps` : String(Math.round(status.value));
}

/** Rule 12: how many conversions run on hardware and how many on the processor, from the load route's split; absent
 *  when the server sends no split. The Conversions tile's number is their total. */
export function conversionSplit(status: Readonly<Partial<Record<MetricName, Metric>>>): Readonly<{hardware: number; processor: number}> | undefined {
  const hardware = status.hardwareConversions, processor = status.processorConversions;
  if (!hardware || !processor || hardware.status === 'unavailable' || processor.status === 'unavailable') return undefined;
  return {hardware: Math.round(hardware.value), processor: Math.round(processor.value)};
}

/** The load tiles a server can fill: one it cannot measure is left out, never drawn empty (hide what is absent). */
export function measuredLoadTiles(status: Readonly<Record<string, {status: string} | undefined>>): readonly LoadTile[] {
  return LOAD_TILES.filter(tile => status[tile.metric] && status[tile.metric]!.status !== 'unavailable');
}
