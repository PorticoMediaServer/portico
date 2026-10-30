import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
/**
 * Titles a catalogue rebuild set aside so that everything else could finish (owner only): what
 * each is, why, how often it has been tried and when it last failed. The server tries them again
 * by itself. One row that cannot be read is left out alone.
 */
import type {CatalogueFailure} from './freshness.ts';
import {keepReadable} from './tolerant.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {wireInstants} from './wire-units.ts';

export type CatalogueFailures = Readonly<{failures: readonly CatalogueFailure[]; /** The server holds more than the latest it lists. */ more: boolean}>;
const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x08\x0b\x0c\x0e-\x1f]/.test(v);
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;

export function parseCatalogueFailures(raw: unknown): CatalogueFailures {
  if (!obj(raw) || !Array.isArray(raw.failures) || raw.failures.length > 200) throw new Error(unreadableServerResponse);
  const failures = keepReadable('catalogue-failures', 'failures', raw.failures, (value): CatalogueFailure => {
    const v = wireInstants(value, ['failedAt']);
    if (!obj(v) || !count(v.domain) || !text(v.reason, 2048) || !count(v.attempts) || !count(v.failedAt) || (v.entityId !== undefined && !text(v.entityId, 256)) || (v.title !== undefined && !text(v.title, 2048))) throw new Error(unreadableServerResponse);
    return Object.freeze({domain: v.domain, reason: v.reason, attempts: v.attempts, failedAt: v.failedAt, ...(v.entityId ? {entityId: v.entityId as string} : {}), ...(v.title ? {title: v.title as string} : {})});
  });
  return Object.freeze({failures: Object.freeze(failures), more: raw.more === true});
}

export async function readCatalogueFailures(api: Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>, signal?: AbortSignal): Promise<CatalogueFailures> {
  return parseCatalogueFailures(await api.request<RouteResponse<"get_catalogue_failures">>(routePath("get_catalogue_failures"), 'GET', undefined, signal));
}
