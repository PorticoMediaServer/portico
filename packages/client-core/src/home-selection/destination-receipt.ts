import type {Job, JobFailure} from '../bulk-jobs.ts';
import type {ContentEntry} from '../library-content.ts';

export type HomeDestinationReceiptRequest = Readonly<{kind: 'playlist' | 'collection'; destinationId: string; jobIds: readonly string[]; cursor?: string}>;
/** What an addition to a playlist or collection did, as the add-to sheets draw it. `destinations.ts` builds it from the
 * rebuilt server's answers (the job fields stay empty). */
export type HomeDestinationReceipt = Readonly<{
  kind: 'playlist' | 'collection'; destinationId: string; jobIds: readonly string[]; jobs: readonly Job[];
  /** Distinct actual expanded members across this entire genuine job set. */
  added: number; alreadyPresent: number; failed: number; unknown: number;
  complete: boolean; pending: boolean; unresolvedJobIds: readonly string[];
  cancelled?: boolean;
  failures: readonly JobFailure[]; nextCursor: string;
  /** Actual authorized expanded leaves for this failure window, same order. */
  failureEntries: readonly ContentEntry[];
}>;
