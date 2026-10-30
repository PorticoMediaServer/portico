import {routePath} from '../../contracts/src/route-paths.ts';
import {decodeGetHardwareFactsResponse, decodeTestAdminTranscodeHardwareResponse} from '../../contracts/src/decoders.ts';
import {parseTranscodeCapacity, type TranscodeCapacity} from './telemetry.ts';

/** The install this server runs on, and the one plain remedy its owner can act
 * on when no graphics card is usable (HARDWARE HW-7). The wording is the
 * server's; a screen shows it verbatim rather than keeping a second copy. */
export type HardwarePlatform = Readonly<{kind: string; label: string; container: boolean; remedy?: string}>;
/** One row of the frame's V1 target matrix: what this install is meant to reach. */
export type HardwareTarget = Readonly<{id: string; label: string; decode: readonly string[]; encode: readonly string[]; toneMapping: readonly string[]; notes: readonly string[]}>;
/** One row of the formats grid, in the frame's one order. */
export type HardwareFormat = Readonly<{id: string; label: string}>;
/** A capability the frame places after V1. */
export type HardwareDeferred = Readonly<{id: string; label: string; detail: string}>;

export type HardwareInventory = Readonly<{
  platform: HardwarePlatform; targets: readonly HardwareTarget[]; formats: readonly HardwareFormat[]; later: readonly HardwareDeferred[];
}>;

export type HardwareApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;

const obj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const text = (v: unknown, max = 512): v is string => typeof v === 'string' && v.length <= max;
const textList = (v: unknown): readonly string[] => Array.isArray(v) ? Object.freeze(v.filter((item): item is string => text(item, 200))) : Object.freeze([]);

/** The platform, the matrix and the grid's order. A row this app cannot read is
 * left out; the page still draws the rest, and the measured facts stay exact. */
export function parseHardwareInventory(value: unknown): HardwareInventory {
  const decoded = decodeGetHardwareFactsResponse(value);
  const platform = decoded.platform;
  const targets: HardwareTarget[] = [];
  for (const target of decoded.targets.slice(0, 12)) {
    if (!text(target.id, 40) || !target.id || !text(target.label, 120)) continue;
    targets.push(Object.freeze({id: target.id, label: target.label, decode: textList(target.decode), encode: textList(target.encode),
      toneMapping: textList(target.toneMapping), notes: textList(target.notes)}));
  }
  const formats: HardwareFormat[] = [];
  for (const format of decoded.formats.slice(0, 16)) if (text(format.id, 32) && format.id && text(format.label, 80)) formats.push(Object.freeze({id: format.id, label: format.label}));
  const later: HardwareDeferred[] = [];
  for (const item of decoded.later.slice(0, 8)) if (text(item.id, 40) && item.id && text(item.label, 120) && text(item.detail, 300)) later.push(Object.freeze({id: item.id, label: item.label, detail: item.detail}));
  return Object.freeze({platform: Object.freeze({kind: platform.kind, label: platform.label, container: platform.container,
    ...(text(platform.remedy, 300) && platform.remedy ? {remedy: platform.remedy} : {})}), targets: Object.freeze(targets), formats: Object.freeze(formats), later: Object.freeze(later)});
}

/** This install's platform, remedies and target matrix. Static for the life of
 * the server, so a page reads it once. */
export async function readHardwareInventory(api: HardwareApi, signal?: AbortSignal): Promise<HardwareInventory> {
  return parseHardwareInventory(await api.request<unknown>(routePath('get_hardware_facts'), 'GET', undefined, signal));
}

/** Probe every adapter and backend again now, and answer with the refreshed
 * capacity document the panel draws from. The server bounds the work itself. */
export async function testHardware(api: HardwareApi, signal?: AbortSignal): Promise<TranscodeCapacity> {
  const decoded = decodeTestAdminTranscodeHardwareResponse(await api.request<unknown>(routePath('test_admin_transcode_hardware'), 'POST', {}, signal));
  return parseTranscodeCapacity(decoded.data);
}

/** The measured hardware half of one server's capacity document. */
export async function readTranscodeCapacity(api: HardwareApi, signal?: AbortSignal): Promise<TranscodeCapacity> {
  const decoded = await api.request<unknown>(routePath('get_admin_transcode_capacity'), 'GET', undefined, signal);
  return parseTranscodeCapacity(obj(decoded) ? (decoded as {data?: unknown}).data : decoded);
}
