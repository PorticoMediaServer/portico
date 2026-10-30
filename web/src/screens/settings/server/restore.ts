import type {MessageId} from '@i18n';
import type {RestoreRefusalCode} from '@core/administration.ts';

/** The one honest answer for a protected restore the server refuses to open (v2 Root §1). */
export type ProtectedRestoreResult = 'unopened' | 'failed';

/**
 * The v2 envelope authenticates the header and the encrypted records together, so a
 * wrong derived key and damaged authenticated bytes fail the same check. The refusal
 * code proves only that this backup cannot be opened now; it never proves which input
 * is wrong. Every other refusal keeps its own path.
 */
export function protectedRestoreResult(error: unknown): ProtectedRestoreResult {
  return (error as {code?: unknown})?.code === 'restore_integrity_failed' ? 'unopened' : 'failed';
}

export const restoreRefusalMessage: Readonly<Record<RestoreRefusalCode, MessageId>> = Object.freeze({
  restore_lock_unavailable: 'web.maintenance.restoreLockUnavailable',
  restore_source_invalid: 'web.maintenance.restoreSourceInvalid',
  restore_pre_release: 'web.maintenance.restorePreRelease',
  restore_integrity_failed: 'web.maintenance.restoreIntegrityFailed',
  restore_not_portico: 'web.maintenance.restoreNotPortico',
  insufficient_disk: 'web.maintenance.restoreNoSpace',
});

export const restoreRecoveryMessage = Object.freeze({
  repaired: 'web.maintenance.restoreRepaired',
  recovered: 'web.maintenance.restoreRecovered',
  partial: 'web.maintenance.restorePartial',
} as const satisfies Record<string, MessageId>);
