import {defaultI18n} from '@i18n';
import {parseRemoteSourcePreview, type RemoteSourcePreview} from '@core/linear-api.ts';
import type {OwnerLiveSource, SourceDocument} from './source-controller';
const bad = (): never => {throw new Error('live_owner_response_invalid');};
export const record = (v: unknown): Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : bad();
export const text = (v: unknown, max = 256): string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v) ? v : bad();
const id = (v: unknown) => typeof v === 'string' && /^[a-f0-9]{48}$/.test(v) ? v : bad();
const count = (v: unknown, max = Number.MAX_SAFE_INTEGER) => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 && v <= max ? v : bad();
const flag = (v: unknown) => typeof v === 'boolean' ? v : bad();
const instant = (v: unknown, empty = false) => v === '' && empty ? '' : typeof v === 'string' && v.length <= 35 && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(v) && Number.isFinite(Date.parse(v)) ? v : bad();
const list = (v: unknown, max: number) => Array.isArray(v) && v.length <= max ? v : bad();
const choice = <T extends string>(v: unknown, choices: readonly T[]): T => typeof v === 'string' && choices.includes(v as T) ? v as T : bad();
export function envelope(raw: unknown, serverId: string) {const v = record(raw);if (v.serverId !== serverId || v.protocolVersion !== '1.0') bad();return v;}
function diagnostics(raw: unknown) {
  const v = record(raw);
  return Object.freeze({skippedChannels: count(v.skippedChannels, 100000000), skippedProgrammes: count(v.skippedProgrammes, 100000000), adjustedProgrammes: count(v.adjustedProgrammes, 100000000), examples: Object.freeze(list(v.examples ?? [], 10).map(x => text(x)).filter(key => key.startsWith('live.import.') && defaultI18n.has(key)))});
}
export function source(raw: unknown): OwnerLiveSource {
  const v = record(raw), revision = count(v.revision);if (revision < 1) bad();
  // The server states how it keeps this source current. Absent (an older
  // server) stays unknown; it is never inferred from a refresh row.
  const acquisition = v.acquisition === 'upload' || v.acquisition === 'remote' ? v.acquisition : undefined;
  return Object.freeze({id: id(v.id), name: text(v.name, 120), revision, state: choice(v.state, ['active', 'disabled', 'failed', 'pending', 'new'] as const), ...(acquisition ? {acquisition} : {}),
    tunerCount: count(v.tunerCount, 256), tunerCountMode: choice(v.tunerCountMode, ['', 'defaulted', 'configured', 'discovered', 'unlimited'] as const), generation: id(v.generation),
    publishedAt: instant(v.publishedAt, true), channels: count(v.channels, 100000), programmes: count(v.programmes, 10000000), ...(v.diagnostics === undefined ? {} : {diagnostics: diagnostics(v.diagnostics)})});
}
export function sourceDocument(raw: unknown, serverId: string): SourceDocument {
  const v = envelope(raw, serverId), sources = list(v.sources, 10000).map(source), ids = new Set(sources.map(x => x.id));if (ids.size !== sources.length) bad();
  const refresh = list(v.refresh, 10000).map(raw => {const r = record(raw), sourceId = id(r.sourceId);if (!ids.has(sourceId)) bad();return Object.freeze({sourceId,
    state: choice(r.state, ['healthy', 'degraded', 'credentials-required', 'refreshing'] as const), errorCode: typeof r.errorCode === 'string' && /^[a-z0-9_]{0,128}$/.test(r.errorCode) ? r.errorCode : bad(), nextRefresh: instant(r.nextRefresh, true), failures: count(r.failures)});});
  return Object.freeze({sources: Object.freeze(sources), refresh: Object.freeze(refresh)});
}
export function uploadPreview(raw: unknown, serverId: string) {
  const v = record(envelope(raw, serverId).preview);
  return Object.freeze({channels: count(v.channels, 100000), programmes: count(v.programmes, 10000000), names: Object.freeze(list(v.names ?? [], 100).map(x => text(x)))});
}
export function remotePreview(raw: unknown, serverId: string): RemoteSourcePreview {
  envelope(raw, serverId);const v = parseRemoteSourcePreview(raw, serverId);
  return Object.freeze({previewId: id(v.previewId), expiresAt: instant(v.expiresAt), discoveredCapacity: count(v.discoveredCapacity, 256), capacityMode: v.capacityMode,
    preview: Object.freeze({...uploadPreview({protocolVersion: '1.0', serverId, preview: v.preview}, serverId), availableStart: instant(v.preview.availableStart, true), availableEnd: instant(v.preview.availableEnd, true), diagnostics: diagnostics(v.preview.diagnostics)}),
    mappingKeys: Object.freeze(v.mappingKeys.map(k => Object.freeze({channelKey: text(k.channelKey), name: text(k.name), guideKey: text(k.guideKey)})))}) as RemoteSourcePreview;
}
export function sourceReceipt(raw: unknown, serverId: string, sourceId: string) {const v = source(envelope(raw, serverId).source);if (v.id !== sourceId) bad();return v;}
export function actionReceipt(raw: unknown, serverId: string, action: 'refresh' | 'toggle' | 'remove') {if (envelope(raw, serverId)[action === 'toggle' ? 'saved' : action === 'refresh' ? 'scheduled' : 'removed'] !== true) bad();}
/** Only an origin, never a URL with credentials, path, query or fragment. */
export function origin(raw: unknown): string {
  const value = text(raw, 512);let url: URL;try {url = new URL(value);} catch {return bad();}
  if (!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash || url.pathname !== '/') bad();
  return url.origin;
}
export function confirmRoots(raw: unknown): readonly string[] {const roots = list(raw, 8).map(origin);if (!roots.length) bad();return Object.freeze([...new Set(roots)]);}
export function discovery(raw: unknown, serverId: string) {
  const v = record(envelope(raw, serverId).result);
  return Object.freeze({discoveryEnabled: flag(v.discoveryEnabled), devices: Object.freeze(list(v.devices, 256).map(raw => {
    const r = record(raw), baseUrl = origin(r.baseUrl);return Object.freeze({deviceId: text(r.deviceId, 128), model: text(r.model, 128), friendlyName: text(r.friendlyName),
      address: new URL(baseUrl).hostname, baseUrl, tunerCount: count(r.tunerCount, 256), configured: flag(r.configured)});
  }))});
}
