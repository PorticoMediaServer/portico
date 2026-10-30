import type {ReadingClient} from '@portico/client-core';
import {readItemEligibility} from '../read/navigation';

/** Only a completed, current reading lookup may choose the ordinary detail
 * branch. Errors and superseded/aborted reads never classify an audiobook. */
export async function resolveItemRead(client: ReadingClient, itemId: string, signal: AbortSignal, isCurrent: () => boolean) {
  const assert = () => {signal.throwIfAborted();if (!isCurrent()) throw new DOMException('Item authority changed', 'AbortError');};
  assert();
  const eligibility = await readItemEligibility(client, itemId, signal);
  assert();
  return eligibility;
}
