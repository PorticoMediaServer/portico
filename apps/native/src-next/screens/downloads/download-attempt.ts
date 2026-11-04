/** One download sheet's submit attempts (#736's contract). A submit is identified by its operation id, and the
 * server replays an exact body under that id and refuses a changed one (422 `idempotency_key_reused`). So:
 *
 * - retrying the same choice (same quality, same episodes policy) after a failure resends the identical body under
 *   the same operation id, and gets the first answer if the server already admitted it;
 * - a changed choice is a new intent and gets a new operation id;
 * - an answer that settles the intent (accepted, or the server asking for a quality: `download_quality_required`)
 *   retires the id, so the next choice is a new intent too.
 *
 * `choice` is the body's identity as a string: everything the person chose that the body carries. */
export function createDownloadAttempts(newOperationId: () => string) {
  let held: Readonly<{choice: string; operationId: string}> | undefined;
  return Object.freeze({
    operationFor(choice: string): string {
      if (held?.choice !== choice) held = Object.freeze({choice, operationId: newOperationId()});
      return held.operationId;
    },
    settle(): void {held = undefined;},
  });
}
export type DownloadAttempts = ReturnType<typeof createDownloadAttempts>;

/** The server's answer when the saved download quality is Ask each time and the body carried no quality. */
export const isDownloadQualityRequired = (failure: unknown): boolean => (failure as {code?: unknown} | undefined)?.code === 'download_quality_required';
