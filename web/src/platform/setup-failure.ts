/** Web presentation metadata, never a substitute producer error or authority.
 * Commands close over one original setup continuation; they carry no public
 * setup proof and cannot select another server or claim. */
export type SetupFailureStage = 'browser-start' | 'initialize' | 'claim';
export class SetupStageFailure extends Error {
  readonly code?: string;
  readonly status?: number;
  constructor(readonly stage: SetupFailureStage, readonly original: unknown,
    readonly retry?: () => Promise<void>, readonly useLocal?: () => Promise<void>) {
    super(original instanceof Error ? original.message : 'Setup could not continue.', {cause: original});
    const value = original as {code?: unknown; status?: unknown} | undefined;
    this.code = typeof value?.code === 'string' ? value.code : undefined;
    this.status = typeof value?.status === 'number' ? value.status : undefined;
  }
}
export const addressSetupFailure = (error: unknown): error is SetupStageFailure =>
  error instanceof SetupStageFailure && error.stage === 'browser-start' && (error.status === 401 || error.status === 403);
export const unavailableSetupFailure = (error: unknown): error is SetupStageFailure =>
  error instanceof SetupStageFailure && error.stage === 'claim' && error.code === 'claim_unavailable' && error.status === 503;
