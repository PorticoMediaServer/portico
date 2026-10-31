/**
 * Cleanup never throws. A release (dispose, close, retire, revoke) that fails is logged and the
 * code carries on: it must not hide the error that sent us into cleanup, and it must not stop the
 * releases after it (docs/ENGINEERING-RULES.md).
 */
export type CleanupLog = (label: string, error: unknown) => void;

let log: CleanupLog = (label, error) => console.warn(`Cleanup failed: ${label}`, error);

/** Default logs with console.warn. A platform may route it to its own log. */
export function setCleanupLog(next: CleanupLog): void {
  log = next;
}

function report(label: string, error: unknown): void {
  try {log(label, error);} catch {/* a failing log must not throw from cleanup either */}
}

const thenable = (value: unknown): value is PromiseLike<unknown> =>
  !!value && (typeof value === 'object' || typeof value === 'function') && typeof (value as {then?: unknown}).then === 'function';

/** Runs release; a throw, or a rejection of the promise it returns, is logged and never propagated. */
export function releaseQuietly(label: string, release: () => unknown): void {
  try {
    const result = release();
    if (thenable(result)) result.then(undefined, error => report(label, error));
  } catch (error) {report(label, error);}
}

/** Awaits release; never rejects. */
export async function releaseQuietlyAsync(label: string, release: () => unknown): Promise<void> {
  try {await release();} catch (error) {report(label, error);}
}

/** Runs every release in order, even when an earlier one throws. */
export function releaseAll(label: string, releases: readonly (() => unknown)[]): void {
  releases.forEach((release, index) => releaseQuietly(`${label}[${index}]`, release));
}

/** For catch blocks: release quietly, then rethrow the ORIGINAL error. */
export async function releaseThenRethrow(original: unknown, label: string, release: () => unknown): Promise<never> {
  await releaseQuietlyAsync(label, release);
  throw original;
}
