import {routePath} from '../../../contracts/src/route-paths.ts';
import {isJobTerminal, parseJob, type Job, type JobFailure} from '../bulk-jobs.ts';
import type {HomeApi} from '../home.ts';
import {validateContentEntry, type ContentEntry} from '../library-content.ts';
import {selectionExecutionError} from './execution.ts';

export type HomeDestinationReceiptRequest = Readonly<{kind: 'playlist' | 'collection'; destinationId: string; jobIds: readonly string[]; cursor?: string}>;
export type HomeDestinationReceipt = Readonly<{
  kind: 'playlist' | 'collection'; destinationId: string; jobIds: readonly string[]; jobs: readonly Job[];
  /** Distinct actual expanded members across this entire genuine job set. */
  added: number; alreadyPresent: number; failed: number; unknown: number;
  complete: boolean; pending: boolean; unresolvedJobIds: readonly string[];
  failures: readonly JobFailure[]; nextCursor: string;
  /** Actual authorized expanded leaves for this failure window, same order. */
  failureEntries: readonly ContentEntry[];
}>;
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const count = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const id = (v: unknown): v is string => typeof v === 'string' && !!v && v.length <= 256 && !/[\x00-\x1f\x7f]/.test(v);
function bad(): never {throw selectionExecutionError('invalid-destination-receipt');}
export function parseHomeDestinationReceipt(raw: unknown, request: HomeDestinationReceiptRequest, serverId: string, viewerFence: string): HomeDestinationReceipt {
  if (!record(raw) || raw.serverId !== serverId || raw.viewerFence !== viewerFence || raw.kind !== request.kind || raw.destinationId !== request.destinationId || !Array.isArray(raw.jobIds) || raw.jobIds.length !== request.jobIds.length || raw.jobIds.some((value, index) => value !== request.jobIds[index]) || !Array.isArray(raw.jobs) || raw.jobs.length !== request.jobIds.length || !count(raw.added) || !count(raw.alreadyPresent) || !count(raw.failed) || !count(raw.unknown) || typeof raw.complete !== 'boolean' || typeof raw.pending !== 'boolean' || !Array.isArray(raw.unresolvedJobIds) || raw.unresolvedJobIds.some(value => !id(value) || !request.jobIds.includes(value)) || !Array.isArray(raw.failures) || raw.failures.length > 100 || raw.failures.length > raw.failed || typeof raw.nextCursor !== 'string' || raw.nextCursor.length > 8192) bad();
  const jobs = raw.jobs.map(parseJob);
  if (jobs.some((job, index) => job.jobId !== request.jobIds[index] || job.command !== request.kind + '-add') || new Set(raw.unresolvedJobIds).size !== raw.unresolvedJobIds.length) bad();
  const pending = jobs.some(job => !isJobTerminal(job.state));
  if (raw.pending !== pending || raw.complete !== (!pending && raw.unknown === 0 && raw.unresolvedJobIds.length === 0)) bad();
  const failures = raw.failures.map(value => {if (!record(value) || !id(value.itemId) || !id(value.code)) bad(); return Object.freeze({itemId: value.itemId, code: value.code});});
  if (!Array.isArray(raw.failureEntries) || raw.failureEntries.length !== failures.length) bad();
  const failureEntries = raw.failureEntries.map(validateContentEntry);
  if (failureEntries.some((entry, index) => entry.id !== failures[index].itemId)) bad();
  if (new Set(failures.map(value => value.itemId)).size !== failures.length || raw.nextCursor && failures.length !== 100) bad();
  return Object.freeze({kind: request.kind, destinationId: request.destinationId, jobIds: Object.freeze([...request.jobIds]), jobs: Object.freeze(jobs), added: raw.added, alreadyPresent: raw.alreadyPresent, failed: raw.failed, unknown: raw.unknown, complete: raw.complete, pending: raw.pending, unresolvedJobIds: Object.freeze(raw.unresolvedJobIds as string[]), failures: Object.freeze(failures), failureEntries: Object.freeze(failureEntries), nextCursor: raw.nextCursor});
}
export async function fetchHomeDestinationReceipt(api: HomeApi, request: HomeDestinationReceiptRequest, serverId: string, viewerFence: string, signal?: AbortSignal): Promise<HomeDestinationReceipt> {
  if (!id(request.destinationId) || !request.jobIds.length || request.jobIds.length > 200 || !request.jobIds.every(id) || new Set(request.jobIds).size !== request.jobIds.length) throw selectionExecutionError('invalid-destination-jobset');
  return parseHomeDestinationReceipt(await api.request(routePath('post_home_destination_receipt'), 'POST', request, signal), request, serverId, viewerFence);
}
