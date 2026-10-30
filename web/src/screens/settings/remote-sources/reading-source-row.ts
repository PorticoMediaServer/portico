import type {RemoteSource, RcloneReceipt, SourceReadSettings} from '@core/remote-sources.ts';

/** A configuration's exact ordinary readback, never a same-id successor. The
 * caller still owns its original form, API and receipt lifetime. */
export function configuredReadingSource(rows: readonly RemoteSource[], receipt: Pick<RcloneReceipt, 'sourceId' | 'generation'>) {
  return rows.find(row => row.id === receipt.sourceId && row.generation === receipt.generation);
}

/** Called only after the original SDK command resolved with its accepted fresh
 * profile. A causal child must match that exact raw generation; this is neither
 * a latest-source rebase nor permission to promote a historical receipt. */
export function acceptedReadingSource(rows: readonly RemoteSource[], profile: SourceReadSettings) {
  return rows.find(row => row.id === profile.sourceId && String(row.generation) === profile.generation);
}
