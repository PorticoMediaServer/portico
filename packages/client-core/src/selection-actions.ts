import {routePath} from '../../contracts/src/route-paths.ts';
import {canonicalMusicQualityLadder, canonicalQualityLadder} from '../../contracts/src/types.ts';
import {jobRequestBody, type JobSelector} from './bulk-jobs.ts';
import {parseBrowseNode} from './browse.ts';
import {validSelectionBasis} from './selection-basis.ts';
import {selectionQuerySelector} from './selection-model/targets.ts';
import type {QuerySelectionTarget} from './selection-model/types.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {parseDownloadPage, type DownloadPage} from './downloads.ts';

export interface SelectionActionsApi {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}
export type SelectionAction = 'delete' | 'download';
export type SelectionActionQuery = Extract<JobSelector, {query: unknown}>['query'] & Readonly<{selectionBasis: string}>;
export type SelectionActionOptions = Readonly<{action: 'delete'; options: Readonly<{deleteFiles: boolean}>}>
  | Readonly<{action: 'download'; options: Readonly<{deviceId: string; quality: string}>}>;
export type SelectionActionInput = SelectionActionOptions & Readonly<{
  selector: Readonly<{query: SelectionActionQuery}>; expected: Readonly<{catalogRevision: string}>;
}>;
export type SelectionActionPreparing = Readonly<{kind: 'selection-action-preparing'; preparationId: string; retryAfterMs: number}>;
export type SelectionActionPreview = Readonly<{
  kind: 'selection-action-preview'; preparationId: string; action: SelectionAction; basis: string;
  total: number; files: number; bytes: number; bytesKnown: boolean; confirmation: string;
  confirmationKind: 'title' | 'count'; expiresAt: string;
  sample: readonly Readonly<{itemId: string; title: string; kind: string}>[];
}>;
export type SelectionActionPreparation = SelectionActionPreparing | SelectionActionPreview;
export type SelectionActionState = 'capturing' | 'sealed' | 'running' | 'cancelling' | 'complete' | 'partial' | 'failed' | 'cancelled';
export type SelectionActionJob = Readonly<{
  operationId: string; action: SelectionAction; state: SelectionActionState; total: number; admitted: number;
  done: number; failed: number; bytesDeleted: number; filesDeleted: number; errorCode?: string; retryAfterMs: number;
}>;
export type SelectionActionConfirmation = Readonly<{operationId: string; preparationId: string; confirmation: string}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const id = (v: unknown, max = 160): v is string => typeof v === 'string' && v.length <= max && /^[A-Za-z0-9_-]+$/.test(v);
const text = (v: unknown, max = 1024): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const action = (v: unknown): v is SelectionAction => v === 'delete' || v === 'download';
const exact = (v: Record<string, unknown>, keys: readonly string[]) => Object.keys(v).every(k => keys.includes(k));
const leafPivots = new Map([['movies', 'movie'], ['episodes', 'episode'], ['songs', 'song'], ['parts', 'audiobook_file']]);
function invalid(): never {throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_selection_action'});}
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child); Object.freeze(value);}
  return value;
}

/** Synchronous, exact validation. No selector rewrite, loaded-row conversion or ID enumeration. */
export function selectionActionInput(raw: unknown): SelectionActionInput {
  if (!object(raw) || !exact(raw, ['action', 'selector', 'expected', 'options']) || !action(raw.action)
    || !object(raw.expected) || !exact(raw.expected, ['catalogRevision']) || typeof raw.expected.catalogRevision !== 'string' || !/^browse_[a-f0-9]{64}$/.test(raw.expected.catalogRevision)
    || !object(raw.options)) invalid();
  // Reuse the existing strict selector grammar, then require this producer's full witness and leaf pivot.
  const checked = jobRequestBody({operationId: 'selection-validation', command: 'refresh', selector: raw.selector as JobSelector, args: {}}).selector;
  if (!('query' in checked) || !id(checked.query.libraryId) || !leafPivots.has(checked.query.pivot) || !validSelectionBasis(checked.query.selectionBasis)) invalid();
  if (checked.query.sort && checked.query.sort.length > 16) invalid();
  const filter = checked.query.filter === undefined || checked.query.filter === null ? checked.query.filter : parseBrowseNode(checked.query.filter);
  const query = {...checked.query, ...(filter === undefined ? {} : {filter}), selectionBasis: checked.query.selectionBasis};
  let options: SelectionActionOptions;
  if (raw.action === 'delete') {
    if (!exact(raw.options, ['deleteFiles']) || typeof raw.options.deleteFiles !== 'boolean') invalid();
    options = {action: 'delete', options: {deleteFiles: raw.options.deleteFiles}};
  } else {
    const quality = raw.options.quality;
    if (!exact(raw.options, ['deviceId', 'quality']) || !id(raw.options.deviceId) || typeof raw.options.quality !== 'string'
      || ![...canonicalQualityLadder, ...canonicalMusicQualityLadder].some(q => q.id === quality)) invalid();
    options = {action: 'download', options: {deviceId: raw.options.deviceId, quality: raw.options.quality}};
  }
  return freeze({...options, selector: {query}, expected: {catalogRevision: raw.expected.catalogRevision}});
}

/** Offers and dispatch share this guard; unsupported semantic refinements are never silently dropped. */
export function selectionActionTargetInput<C>(target: QuerySelectionTarget<C>, options: SelectionActionOptions): SelectionActionInput {
  const checked = selectionQuerySelector(target);
  if (!checked.ok || !('query' in checked.selector)) invalid();
  const query = checked.selector.query;
  const unavailable = (target.summary as {unavailableActions?: Readonly<Record<string, unknown>>}).unavailableActions;
  if (!count(target.total) || !target.total || target.summary.count !== target.total
    || !target.summary.every[options.action] || target.summary.library.kind !== 'single'
    || target.summary.library.libraryId !== query.libraryId || target.summary.types.length !== 1
    || target.summary.types[0].kind !== leafPivots.get(query.pivot) || target.summary.types[0].count !== target.total
    || unavailable?.[options.action] !== undefined) invalid();
  return selectionActionInput({...options, selector: checked.selector, expected: checked.expected});
}

export function parseSelectionActionPreparation(raw: unknown): SelectionActionPreparation {
  if (!object(raw) || !id(raw.preparationId, 128)) invalid();
  if (raw.kind === 'selection-action-preparing') {
    if (!exact(raw, ['kind', 'preparationId', 'retryAfterMs']) || !count(raw.retryAfterMs)) invalid();
    return Object.freeze({kind: raw.kind, preparationId: raw.preparationId, retryAfterMs: raw.retryAfterMs});
  }
  if (raw.kind !== 'selection-action-preview' || !exact(raw, ['kind', 'preparationId', 'action', 'basis', 'total', 'files', 'bytes', 'bytesKnown', 'confirmation', 'confirmationKind', 'expiresAt', 'sample'])
    || !action(raw.action) || !validSelectionBasis(raw.basis) || !count(raw.total) || !raw.total || !count(raw.files) || !count(raw.bytes)
    || typeof raw.bytesKnown !== 'boolean' || raw.action === 'delete' && !raw.bytesKnown
    || !text(raw.confirmation) || !raw.confirmation || raw.confirmationKind !== (raw.total === 1 ? 'title' : 'count')
    || raw.total > 1 && raw.confirmation !== String(raw.total)
    || !text(raw.expiresAt, 64) || !Number.isFinite(Date.parse(raw.expiresAt)) || !Array.isArray(raw.sample) || raw.sample.length > 20 || raw.sample.length > raw.total) invalid();
  const sample = raw.sample.map(entry => {
    if (!object(entry) || !exact(entry, ['itemId', 'title', 'kind']) || !id(entry.itemId) || !text(entry.title) || !text(entry.kind, 64) || ![...leafPivots.values()].includes(entry.kind)) invalid();
    return Object.freeze({itemId: entry.itemId, title: entry.title, kind: entry.kind});
  });
  if (new Set(sample.map(entry => entry.itemId)).size !== sample.length) invalid();
  return Object.freeze({kind: raw.kind, preparationId: raw.preparationId, action: raw.action, basis: raw.basis, total: raw.total,
    files: raw.files, bytes: raw.bytes, bytesKnown: raw.bytesKnown, confirmation: raw.confirmation,
    confirmationKind: raw.confirmationKind as 'title' | 'count', expiresAt: raw.expiresAt, sample: Object.freeze(sample)});
}

export const isSelectionActionTerminal = (state: SelectionActionState): boolean => ['complete', 'partial', 'failed', 'cancelled'].includes(state);
export function parseSelectionActionJob(raw: unknown): SelectionActionJob {
  if (!object(raw) || !exact(raw, ['operationId', 'action', 'state', 'total', 'admitted', 'done', 'failed', 'bytesDeleted', 'filesDeleted', 'errorCode', 'retryAfterMs'])
    || !id(raw.operationId) || !action(raw.action) || !['capturing', 'sealed', 'running', 'cancelling', 'complete', 'partial', 'failed', 'cancelled'].includes(String(raw.state))) invalid();
  for (const key of ['total', 'admitted', 'done', 'failed', 'bytesDeleted', 'filesDeleted', 'retryAfterMs']) if (!count(raw[key])) invalid();
  const v = raw as unknown as SelectionActionJob;
  if (!v.total || v.admitted > v.total || v.done > v.total - v.failed || v.done + v.failed > v.admitted
    || v.state === 'complete' && (v.done !== v.total || v.failed !== 0 || v.admitted !== v.total)
    || ['sealed', 'running'].includes(v.state) && v.admitted !== v.total
    || ['capturing', 'sealed'].includes(v.state) && (v.done !== 0 || v.failed !== 0 || v.bytesDeleted !== 0 || v.filesDeleted !== 0)
    || raw.errorCode !== undefined && (!text(raw.errorCode, 160) || !raw.errorCode)) invalid();
  return Object.freeze({...v});
}

function checkedId(value: string, max = 160) {if (!id(value, max)) invalid(); return value;}
function preparationMatches(value: SelectionActionPreparation, preparationId: string) {if (value.preparationId !== preparationId) invalid(); return value;}
export async function prepareSelectionAction(api: SelectionActionsApi, input: SelectionActionInput, signal?: AbortSignal): Promise<SelectionActionPreparation> {
  const body = selectionActionInput(input);
  const result = parseSelectionActionPreparation(await api.request<unknown>(routePath('create_selection_action_preparation', {id: body.selector.query.libraryId}), 'POST', body, signal));
  if (result.kind === 'selection-action-preview' && (result.action !== body.action || result.basis !== body.selector.query.selectionBasis)) invalid();
  return result;
}
export async function getSelectionActionPreparation(api: SelectionActionsApi, libraryId: string, preparationId: string, signal?: AbortSignal): Promise<SelectionActionPreparation> {
  return preparationMatches(parseSelectionActionPreparation(await api.request<unknown>(routePath('get_selection_action_preparation', {id: checkedId(libraryId), preparationId: checkedId(preparationId, 128)}), 'GET', undefined, signal)), preparationId);
}
export async function releaseSelectionActionPreparation(api: SelectionActionsApi, libraryId: string, preparationId: string, signal?: AbortSignal): Promise<void> {
  await api.request(routePath('delete_selection_action_preparation', {id: checkedId(libraryId), preparationId: checkedId(preparationId, 128)}), 'DELETE', undefined, signal);
}
export async function createSelectionAction(api: SelectionActionsApi, libraryId: string, input: SelectionActionConfirmation, signal?: AbortSignal): Promise<SelectionActionJob> {
  if (!object(input) || !exact(input, ['operationId', 'preparationId', 'confirmation']) || !id(input.operationId) || !id(input.preparationId, 128) || !text(input.confirmation) || !input.confirmation) invalid();
  const body = Object.freeze({operationId: input.operationId, preparationId: input.preparationId, confirmation: input.confirmation});
  const result = parseSelectionActionJob(await api.request<unknown>(routePath('create_selection_action', {id: checkedId(libraryId)}), 'POST', body, signal));
  if (result.operationId !== body.operationId) invalid();
  return result;
}
export async function getSelectionAction(api: SelectionActionsApi, operationId: string, signal?: AbortSignal): Promise<SelectionActionJob> {
  const result = parseSelectionActionJob(await api.request<unknown>(routePath('get_selection_action', {operationId: checkedId(operationId)}), 'GET', undefined, signal));
  if (result.operationId !== operationId) invalid();
  return result;
}
export async function cancelSelectionAction(api: SelectionActionsApi, operationId: string, signal?: AbortSignal): Promise<SelectionActionJob> {
  const result = parseSelectionActionJob(await api.request<unknown>(routePath('cancel_selection_action', {operationId: checkedId(operationId)}), 'DELETE', undefined, signal));
  if (result.operationId !== operationId) invalid();
  return result;
}

/** Exact admitted preparations, including reused claims. Closed proves admission ended, independently of short job history. */
export type SelectionActionPreparationPage = DownloadPage & Readonly<{closed: boolean}>;
export async function listSelectionActionPreparations(api: SelectionActionsApi, operationId: string, deviceId: string,
  options: Readonly<{cursor?: string; limit?: number; signal?: AbortSignal}> = {}): Promise<SelectionActionPreparationPage> {
  const limit = options.limit ?? 20;
  if (!Number.isInteger(limit) || limit < 1 || limit > 100 || options.cursor !== undefined && !text(options.cursor, 4096)) invalid();
  const query = new URLSearchParams({operationId: checkedId(operationId), deviceId: checkedId(deviceId), limit: String(limit)});
  if (options.cursor) query.set('cursor', options.cursor);
  const raw = await api.request<unknown>(routePath('list_download_preparations') + '?' + query, 'GET', undefined, options.signal);
  if (!object(raw) || typeof raw.closed !== 'boolean' || !Array.isArray(raw.items) || raw.items.length > limit) invalid();
  const page = parseDownloadPage(raw);
  // General discovery tolerates one unreadable row. Exact confirmed transfers must never silently lose one.
  if (page.items.length !== raw.items.length) invalid();
  return Object.freeze({...page, closed: raw.closed});
}
