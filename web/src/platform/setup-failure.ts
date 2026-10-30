/** Web presentation metadata, never a substitute producer error or authority.
 * Commands close over one original setup continuation; they carry no public
 * setup proof and cannot select another server or claim. */
export type SetupFailureStage = 'browser-start' | 'initialize' | 'claim';
export class SetupStageFailure extends Error {
  readonly code?: string;
  readonly status?: number;
  readonly retryAfterSeconds?: number;
  constructor(readonly stage: SetupFailureStage, readonly original: unknown,
    readonly retry?: () => Promise<void>, readonly useLocal?: () => Promise<void>) {
    super(original instanceof Error ? original.message : 'Setup could not continue.', {cause: original});
    const value = original as {code?: unknown; status?: unknown; retryAfterSeconds?: unknown} | undefined;
    this.code = typeof value?.code === 'string' ? value.code : undefined;
    this.status = typeof value?.status === 'number' ? value.status : undefined;
    this.retryAfterSeconds = typeof value?.retryAfterSeconds === 'number' ? value.retryAfterSeconds : undefined;
  }
}
/** The server (403) or this page refused because setup was not started from the server's own
 * network (PLEX-SETUP-NOTES S7). Nothing here can be retried from the same place. */
export const notLocalSourceFailure = (error: unknown): boolean =>
  (error as {code?: unknown} | null)?.code === 'setup_source_not_local';
export const addressSetupFailure = (error: unknown): error is SetupStageFailure =>
  error instanceof SetupStageFailure && error.stage === 'browser-start' && (error.status === 401 || error.status === 403) && !notLocalSourceFailure(error);
export const unavailableSetupFailure = (error: unknown): error is SetupStageFailure =>
  error instanceof SetupStageFailure && error.stage === 'claim' && error.code === 'claim_unavailable' && error.status === 503;
/** The server's 409 `setup_in_progress`: another browser's setup attempt is still live. */
export const setupInProgressFailure = (error: unknown): boolean =>
  (error as {code?: unknown} | null)?.code === 'setup_in_progress';
