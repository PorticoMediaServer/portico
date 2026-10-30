/**
 * `writer_capacity` is the server's answer when its database writer refused a change before it
 * began: the queue was full, or the write met a lock it should never meet. Nothing was saved, and
 * the server asks for the same request again after `Retry-After` (1–30 s). A change the person made
 * is therefore sent again, not dropped: the two central request paths (`HttpLocalApi.request` and
 * playback v1's `call`) wait and resend it here, within a bounded total wait, and only then report
 * the (still retryable, `busy`) failure. Cancelling the request ends the wait at once.
 */
export const WRITER_CAPACITY = 'writer_capacity';
/**
 * `database_busy` is the same promise for any request, reads included: the server refused it before
 * it began because a write-ahead-log reset briefly held new reads. It is resent on the same terms.
 */
export const DATABASE_BUSY = 'database_busy';
/** The refusals that guarantee nothing ran and ask for the same request again. */
export function resendableRefusal(code: string): boolean {
  return code === WRITER_CAPACITY || code === DATABASE_BUSY;
}
/** The longest the server may ask for, and the floor it never goes under. */
export const WRITER_RETRY_MIN_MS = 1_000;
export const WRITER_RETRY_MAX_MS = 30_000;
/** Total waiting one request may spend on writer refusals before the failure is reported. */
export const WRITER_RETRY_BUDGET_MS = 60_000;

/** The wait before resending: the server's Retry-After, held to 1–30 s. */
export function writerRetryDelayMs(retryAfterMs: number | undefined): number {
  const asked = typeof retryAfterMs === 'number' && Number.isFinite(retryAfterMs) ? retryAfterMs : WRITER_RETRY_MIN_MS;
  return Math.min(Math.max(asked, WRITER_RETRY_MIN_MS), WRITER_RETRY_MAX_MS);
}

function cancelled(): Error {
  return new DOMException('The request was cancelled.', 'AbortError');
}

/** Waits `ms`, or rejects as soon as `signal` aborts. */
export function writerRetryWait(ms: number, signal?: AbortSignal, timers: Pick<typeof globalThis, 'setTimeout' | 'clearTimeout'> = globalThis): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) { reject(cancelled()); return; }
    const onAbort = () => { timers.clearTimeout(timer); reject(cancelled()); };
    const timer = timers.setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve(); }, ms);
    signal?.addEventListener('abort', onAbort, {once: true});
  });
}

/**
 * Runs `attempt`, and while it fails with `writer_capacity` waits the server's Retry-After and runs
 * it again, until the total wait would pass the budget. `refusal` reads the error: undefined for
 * any other failure, which is thrown unchanged.
 */
export async function retryWriterCapacity<T>(
  attempt: () => Promise<T>,
  refusal: (error: unknown) => {retryAfterMs?: number} | undefined,
  signal?: AbortSignal,
  wait: (ms: number, signal?: AbortSignal) => Promise<void> = writerRetryWait,
  budgetMs = WRITER_RETRY_BUDGET_MS,
): Promise<T> {
  let waited = 0;
  for (;;) {
    try {
      return await attempt();
    } catch (error) {
      const refused = refusal(error);
      if (!refused) throw error;
      const delay = writerRetryDelayMs(refused.retryAfterMs);
      if (waited + delay > budgetMs) throw error;
      await wait(delay, signal);
      waited += delay;
    }
  }
}
