/**
 * What a selection operation actually achieved, and whether its destructive prompt may run.
 *
 * Both clients show receipts and a delete prompt from the same controller facts, so these stay
 * pure: formatting, wording and the mock-up's presentation remain the renderer's. The delete gate
 * is a presentation precondition only. Execution authority stays with the controller that holds
 * the ticket, the preview revision and the exact selected identities.
 */
import {deletionAllowed, type DeletePreview} from './administration.ts';
import type {BulkReceipt, BulkResult} from './metadata-bulk.ts';

export type BulkReceiptView = Readonly<{results: readonly BulkResult[]; updated: number; failed: number}>;

/** The counters a picker observation publishes; `complete` says the observation has ended. */
export type PickerObservation = Readonly<{
  inserted?: number;
  failed?: number;
  unknown?: number;
  pending?: boolean;
  failures: readonly unknown[];
  complete: boolean;
}>;

/** Receipts remain with the controller; only each target's latest real outcome is displayed.
 * The latest receipt per `(kind, id)` wins, and the same id in another kind is another target. */
export function bulkReceiptView(receipts: readonly BulkReceipt[]): BulkReceiptView {
  const latest = new Map<string, BulkResult>();
  for (const receipt of receipts) for (const row of receipt.results) latest.set(JSON.stringify([row.kind, row.id]), row);
  const results = [...latest.values()];
  const updated = results.filter(row => row.ok).length;
  return {results, updated, failed: results.length - updated};
}

/** Complete observations may still contain failures; missing counters prove no success. */
export function pickerResultSucceeded(result: PickerObservation): boolean {
  return result.complete && result.inserted !== undefined && result.failed === 0 && (result.unknown === undefined || result.unknown === 0) && result.pending !== true && result.failures.length === 0;
}

/** The mock-up's ceiling: a larger destructive selection is refused, never split into parts. */
export const DELETE_SELECTION_LIMIT = 200;

/** Why the prompt may not run. Every other refusal is explained by its own presentation. */
export type DeleteGateReason = 'owner' | 'limit' | 'loading' | 'preview' | 'libraries' | 'blocked' | 'confirmation' | 'pending';
export type DeleteGate = Readonly<{allowed: boolean; reason?: DeleteGateReason}>;

const ALLOWED: DeleteGate = Object.freeze({allowed: true});
const denied = (reason: DeleteGateReason): DeleteGate => Object.freeze({allowed: false, reason});

/** The supplied preview remains authoritative: no title, count or confirmation is recomputed,
 * and an exact typed confirmation is required before anything destructive can be offered. */
export function deleteSelectionGate(input: Readonly<{owner: boolean; selectionCount: number; preview: DeletePreview | null; confirmation: string; loading?: boolean; pending?: boolean}>): DeleteGate {
  const {preview} = input, count = input.selectionCount;
  if (!input.owner) return denied('owner');
  if (!Number.isSafeInteger(count) || count < 1 || count > DELETE_SELECTION_LIMIT) return denied('limit');
  if (input.loading) return denied('loading');
  if (!preview || preview.targets.length !== count || new Set(preview.targets.map(target => target.itemId)).size !== count || preview.confirmation.length === 0) return denied('preview');
  if (new Set(preview.targets.map(target => target.libraryId)).size !== 1) return denied('libraries');
  if (!deletionAllowed(preview)) return denied('blocked');
  if (input.pending) return denied('pending');
  if (input.confirmation !== preview.confirmation) return denied('confirmation');
  return ALLOWED;
}
