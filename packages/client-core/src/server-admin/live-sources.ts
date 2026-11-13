import type {LiveSource} from '../../../contracts/src/next/api.ts';
import type {LibraryContentApi} from '../library-content.ts';
import {callServer, serverPath} from '../query/server-api.ts';
import {unreadableServerResponse} from '../server-messages.ts';

export type OwnerSource = LiveSource & Readonly<{acquisition: 'upload' | 'remote'}>;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const invalid = (): never => {throw new Error(unreadableServerResponse);};
function sourceReport(raw: unknown): import('../../../contracts/src/next/api.ts').LiveSourceReport {
 if (!object(raw)) return invalid();
 for (const field of ['channels','programmes','skippedChannels','skippedProgrammes','channelsWithGuide'] as const) if (typeof raw[field] !== 'number' || !Number.isSafeInteger(raw[field]) || raw[field] < 0) return invalid();
 for (const field of ['examples','groups']) if (raw[field] !== undefined && (!Array.isArray(raw[field]) || !(raw[field] as unknown[]).every(value => typeof value === 'string'))) return invalid();
 return Object.freeze({channels: raw.channels, programmes: raw.programmes, skippedChannels: raw.skippedChannels, skippedProgrammes: raw.skippedProgrammes, channelsWithGuide: raw.channelsWithGuide,
  ...(raw.groups ? {groups: raw.groups} : {}), ...(raw.examples ? {examples: raw.examples} : {}), ...(raw.guideThrough ? {guideThrough: raw.guideThrough} : {})}) as import('../../../contracts/src/next/api.ts').LiveSourceReport;
}
export function ownerSource(value: unknown): OwnerSource {
 if (!object(value) || typeof value.id !== 'string' || !value.id || typeof value.name !== 'string' || typeof value.kind !== 'string' || typeof value.state !== 'string' || typeof value.revision !== 'string' || !/^[1-9][0-9]*$/.test(value.revision) || typeof value.paused !== 'boolean' || typeof value.tunerCount !== 'number' || !Number.isSafeInteger(value.tunerCount) || !object(value.config) || typeof value.hasSignIn !== 'boolean') return invalid();
 const config = Object.fromEntries(Object.entries(value.config).filter(([name]) => ['playlistUrl','uploaded','guideUrl','guideUploaded','serverUrl','tunerUrl','refreshHours','groups','channels','numbering','firstNumber','guideSource'].includes(name)));
 const upload = (config.uploaded || config.guideUploaded) && !config.playlistUrl && !config.guideUrl && !config.serverUrl && !config.tunerUrl;
 return Object.freeze({id: value.id, name: value.name, kind: value.kind, state: value.state, revision: value.revision, paused: value.paused, tunerCount: value.tunerCount, audience: value.audience, hasSignIn: value.hasSignIn,
  config: Object.freeze(config), ...(value.report ? {report: sourceReport(value.report)} : {}), ...(value.lastError ? {lastError: value.lastError} : {}), ...(value.refreshedAt ? {refreshedAt: value.refreshedAt} : {}), acquisition: upload ? 'upload' : 'remote'}) as OwnerSource;
}
export async function readOwnerSources(api: LibraryContentApi, signal?: AbortSignal): Promise<{sources: readonly OwnerSource[]}> {
 const answer = await callServer(api, 'GET /v1/admin/live/sources', {signal});
 if (!Array.isArray(answer.items)) return invalid();
 return {sources: Object.freeze(answer.items.map(ownerSource))};
}
export async function changeOwnerSource(api: LibraryContentApi, source: OwnerSource, action: 'refresh' | 'toggle' | 'remove', signal?: AbortSignal): Promise<void> {
 if (action === 'remove') {await callServer(api, 'DELETE /v1/admin/live/sources/{id}', {id: source.id, signal});return;}
 if (action === 'refresh') {ownerSource(await callServer(api, 'POST /v1/admin/live/sources/{id}/refresh', {id: source.id, signal}));return;}
 if (!api.requestRaw) return invalid();
 const answer = await api.requestRaw(serverPath('PATCH /v1/admin/live/sources/{id}', {id: source.id}), 'PATCH', {body: {paused: !source.paused}, headers: {'If-Match': `"${source.revision}"`}, signal, singleAttempt: true});
 if (answer.status >= 400) {
  const error = object(answer.body) && object(answer.body.error) ? answer.body.error : {};
  throw Object.assign(new Error(typeof error.message === 'string' ? error.message : unreadableServerResponse), {status: answer.status, code: error.code});
 }
 if (ownerSource(answer.body).id !== source.id) return invalid();
}

export type OwnerSourcePreview = Readonly<{report: import('../../../contracts/src/next/api.ts').LiveSourceReport; names: readonly string[]; tuners: number}>;
export type SourceSave = import('../../../contracts/src/next/api.ts').LiveSourceSave;
export const LIVE_SOURCE_UPLOAD_BYTES = 128 * 1024 * 1024;
function sized(body: SourceSave) {
 if (new TextEncoder().encode(JSON.stringify(body)).length > LIVE_SOURCE_UPLOAD_BYTES) throw new Error('The source request must be no larger than 128 MiB.');
 return body;
}
/** A preview exposes names/counts, never stream locators or the submitted sign-in. */
export async function previewOwnerSource(api: LibraryContentApi, body: SourceSave, signal?: AbortSignal): Promise<OwnerSourcePreview> {
 const answer = await callServer(api, 'POST /v1/admin/live/sources/preview', {body: sized(body), signal});
 if (!Array.isArray(answer.channels) || !Number.isSafeInteger(answer.tuners)) return invalid();
 const report = sourceReport(answer.report);
 return Object.freeze({report, names: Object.freeze(answer.channels.map(channel => typeof channel.Name === 'string' ? channel.Name : invalid())), tuners: answer.tuners});
}
export async function createOwnerSource(api: LibraryContentApi, body: SourceSave, operationId: string, signal?: AbortSignal): Promise<OwnerSource> {
 if (!api.requestRaw) return invalid();
 const answer = await api.requestRaw(serverPath('POST /v1/admin/live/sources'), 'POST', {body: sized(body), headers: {'Idempotency-Key': operationId}, signal, singleAttempt: true});
 if (answer.status >= 400) {
  const error = object(answer.body) && object(answer.body.error) ? answer.body.error : {};
  throw Object.assign(new Error(typeof error.message === 'string' ? error.message : unreadableServerResponse), {status: answer.status, code: error.code});
 }
 return ownerSource(answer.body);
}
export type {DvrTuners as OwnerTunerView} from '../../../contracts/src/next/api.ts';
