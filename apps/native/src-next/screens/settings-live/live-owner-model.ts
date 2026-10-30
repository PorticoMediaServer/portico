/**
 * Live TV & Channels owner documents, read exactly as the server names them.
 * A field this client cannot read is refused rather than guessed; the server's
 * own keys are the contract, so extra fields are ignored and never invented.
 */
import {liveSourceAcquisition, parseLiveSourceDocument, parseLiveDefaultsDocument, parseChannelMapPage as parseCoreChannelMapPage, type ChannelMapEntry as CoreChannelMapEntry, type ChannelMapPage as CoreChannelMapPage, type LiveSourceAcquisition} from '../../../../../packages/client-core/src/administration';

const object = (v: unknown): v is Record<string, any> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 4096): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const count = (v: unknown, max = Number.MAX_SAFE_INTEGER): v is number => Number.isSafeInteger(v) && Number(v) >= 0 && Number(v) <= max;
const flag = (v: unknown): v is boolean => typeof v === 'boolean';
const oneOf = <T extends string>(v: unknown, values: readonly T[]): v is T => typeof v === 'string' && (values as readonly string[]).includes(v);
const strings = (v: unknown, max: number): v is string[] => Array.isArray(v) && v.length <= max && v.every(item => text(item, 512));
function invalid(): never { throw new Error('invalid_response'); }

/** The server's administration envelope: the list carries its fields at the top,
 * a typed owner result carries them under `result`. Both name this server. */
export function adminResult(v: unknown, serverId: string, wrap = false): Record<string, any> {
  if (!object(v) || typeof v.serverId !== 'string' || v.serverId !== serverId) invalid();
  const body = wrap ? v.result : v;
  if (!object(body)) invalid();
  return body;
}

export type LiveRefresh = Readonly<{sourceId: string; state: 'healthy' | 'degraded' | 'credentials-required' | 'refreshing'; errorCode: string; nextRefresh: string; failures: number}>;
export type ImportDiagnostics = Readonly<{skippedChannels: number; skippedProgrammes: number; adjustedProgrammes: number; examples: readonly string[]}>;
export type LiveSource = Readonly<{
  id: string; name: string; revision: number; state: string; tunerCount: number; tunerCountMode: string;
  generation: string; publishedAt: string; channels: number; programmes: number;
  deliveryValidation: string; capacityKnown: boolean; planningEstimate: number; diagnostics: ImportDiagnostics;
  /** The server's own update mode. Absent means unknown, never inferred from a refresh row. */
  acquisition?: LiveSourceAcquisition;
}>;
export type LiveSources = Readonly<{sources: readonly LiveSource[]; refresh: readonly LiveRefresh[]}>;

const diagnostics = (v: unknown): ImportDiagnostics => {
  if (!object(v)) return {skippedChannels: 0, skippedProgrammes: 0, adjustedProgrammes: 0, examples: []};
  return {
    skippedChannels: count(v.skippedChannels) ? v.skippedChannels : 0,
    skippedProgrammes: count(v.skippedProgrammes) ? v.skippedProgrammes : 0,
    adjustedProgrammes: count(v.adjustedProgrammes) ? v.adjustedProgrammes : 0,
    examples: strings(v.examples, 10) ? v.examples : [],
  };
};
const source = (v: unknown): LiveSource => {
  if (!object(v) || !text(v.id, 64) || !v.id || !text(v.name, 512) || !count(v.revision) || !text(v.state, 64)) invalid();
  const acquisition = liveSourceAcquisition(v.acquisition);
  return Object.freeze({
    id: v.id, name: v.name, revision: v.revision, state: v.state, ...(acquisition ? {acquisition} : {}),
    tunerCount: count(v.tunerCount, 256) ? v.tunerCount : 0,
    tunerCountMode: text(v.tunerCountMode, 64) ? v.tunerCountMode : '',
    generation: text(v.generation, 128) ? v.generation : '',
    publishedAt: text(v.publishedAt, 64) ? v.publishedAt : '',
    channels: count(v.channels, 100000) ? v.channels : 0,
    programmes: count(v.programmes, 10000000) ? v.programmes : 0,
    deliveryValidation: text(v.deliveryValidation, 128) ? v.deliveryValidation : '',
    capacityKnown: flag(v.capacityKnown) ? v.capacityKnown : false,
    planningEstimate: count(v.planningEstimate, 256) ? v.planningEstimate : 0,
    diagnostics: diagnostics(v.diagnostics),
  });
};
export type UploadPreview = Readonly<{channels: number; programmes: number; names: readonly string[]}>;
/** The direct live-source preview (an uploaded playlist/guide), not the remote preview. */
export function parseUploadPreview(v: unknown, serverId: string): UploadPreview {
  const body = adminResult(v, serverId);
  if (!object(body.preview)) invalid();
  const p = body.preview;
  if (!count(p.channels, 100000) || !count(p.programmes, 10000000) || !Array.isArray(p.names) || p.names.length > 100 || p.names.some(name => !text(name, 256))) invalid();
  return Object.freeze({channels: p.channels, programmes: p.programmes, names: Object.freeze([...p.names])});
}
/** The direct save receipt is the published source row. */
export function parseLiveSourceReceipt(v: unknown, serverId: string): LiveSource {
  const body = adminResult(v, serverId);
  return source(body.source);
}
export function parseLiveSources(v: unknown, serverId: string): LiveSources {
  const body = adminResult(v, serverId);
  if (!Array.isArray(body.sources) || !Array.isArray(body.refresh)) invalid();
  return Object.freeze({
    sources: Object.freeze(body.sources.map(source)),
    refresh: Object.freeze(body.refresh.map(raw => {
      if (!object(raw) || !text(raw.sourceId, 64) || !oneOf(raw.state, ['healthy', 'degraded', 'credentials-required', 'refreshing'] as const)) invalid();
      return Object.freeze({sourceId: raw.sourceId, state: raw.state, errorCode: text(raw.errorCode, 128) ? raw.errorCode : '', nextRefresh: text(raw.nextRefresh, 64) ? raw.nextRefresh : '', failures: count(raw.failures) ? raw.failures : 0});
    })),
  });
}

export type FilterRule = Readonly<{mode: 'include' | 'exclude'; values: readonly string[]}>;
export type LiveDefaults = Readonly<{
  timeshiftMinutes?: 10 | 30 | 60 | 120;
  importLimits: Readonly<{bytes: number; channels: number; programmes: number}>;
  streamBufferMs: number; retryWindowMs: number; userAgent: string; guideDays: number; logoImport: boolean; discoveryEnabled: boolean;
}>;
export type LiveSourceSettings = Readonly<{
  enabled?: boolean; viewerAccess?: 'owner-only' | 'server-members'; remote?: import('../../../../../packages/client-core/src/linear-api').PersistedRemoteConfiguration;
  enabledGroups: readonly string[]; enabledChannels: readonly string[]; kind: string;
  streamBufferMs: number | null; retryWindowMs: number | null; userAgent: string;
  filters: Readonly<{categories: FilterRule; countries: FilterRule; keywords: FilterRule}>;
  logoImport: Readonly<{enabled: boolean; overwriteExisting: boolean}>;
  channelNumbering: Readonly<{mode: 'source' | 'sequential'; startAt: number; step: number}>;
  guideSourceId: string;
  importLimits: Readonly<{bytes: number; channels: number; programmes: number}> | null;
}>;
export type LiveSourceDocument = Readonly<{
  sourceId: string; sourceName: string; revision: number; digest: string;
  settings: LiveSourceSettings; effective: LiveDefaults; availableKinds: readonly string[]; tunerCount: number;
  /** Same server fact as the list; absent means unknown. */
  acquisition?: LiveSourceAcquisition;
}>;

/** Keep the server envelope check here; all document validation belongs to client-core. */
export function parseSourceDocument(v: unknown, serverId: string): LiveSourceDocument {
  return parseLiveSourceDocument(adminResult(v, serverId, true));
}
export function parseLiveDefaults(v: unknown, serverId: string): Readonly<{revision: number; settings: LiveDefaults}> {
  return parseLiveDefaultsDocument(adminResult(v, serverId, true));
}

export type ChannelMapEntry = CoreChannelMapEntry;
export type ChannelMapPage = CoreChannelMapPage;
export type ChannelMapEdit = Readonly<{number: string; guideChannelId: string; hidden: boolean}>;
export function parseChannelMapPage(v: unknown, serverId: string): ChannelMapPage {
  return parseCoreChannelMapPage(adminResult(v, serverId, true));
}

export type TunerDevice = Readonly<{deviceId: string; model: string; friendlyName: string; address: string; baseUrl: string; tunerCount: number; configured: boolean}>;
export type TunerDiscovery = Readonly<{devices: readonly TunerDevice[]; discoveryEnabled: boolean; message: string}>;
export function parseTunerDiscovery(v: unknown, serverId: string): TunerDiscovery {
  const body = adminResult(v, serverId, true);
  if (!Array.isArray(body.devices) || !flag(body.discoveryEnabled)) invalid();
  return Object.freeze({discoveryEnabled: body.discoveryEnabled, message: text(body.message, 512) ? body.message : '', devices: Object.freeze(body.devices.map(raw => {
    if (!object(raw) || !text(raw.address, 256) || !text(raw.baseUrl ?? '', 512) || !count(raw.tunerCount, 256) || !flag(raw.configured)) invalid();
    return Object.freeze({deviceId: text(raw.deviceId, 256) ? raw.deviceId : '', model: text(raw.model, 256) ? raw.model : '', friendlyName: text(raw.friendlyName, 256) ? raw.friendlyName : '', address: raw.address, baseUrl: raw.baseUrl ?? '', tunerCount: raw.tunerCount, configured: raw.configured});
  }))});
}

export type LogoImportOutcome = Readonly<{requested: number; imported: number; skipped: number}>;
export function parseLogoImport(v: unknown, serverId: string): LogoImportOutcome {
  const body = adminResult(v, serverId, true);
  if (!count(body.requested, 100000) || !count(body.imported, 100000) || !count(body.skipped, 100000)) invalid();
  return Object.freeze({requested: body.requested, imported: body.imported, skipped: body.skipped});
}

/** The fields an owner types for one source, held in whole seconds and MB before they are sent. */
export const secondsOf = (ms: number | null): string => ms === null ? '' : String(ms / 1000);
export const millisOf = (value: string): number | null => value.trim() === '' ? null : Math.max(0, Math.round(Number(value) || 0)) * 1000;
export const megabytes = 1048576;
export const listOf = (value: string): string[] => value.split(',').map(part => part.trim()).filter(Boolean);
