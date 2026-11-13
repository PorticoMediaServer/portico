import type {ReadingClient} from '@portico/client-core';
import type {ViewerQueries} from '@core/query/viewer-queries.ts';
import {readTitleIdentity} from '../../platform/title-identity';
import {readItemEligibility} from '../read/navigation';

/** The kinds a reader opens: a book's text and a comic's pages. An audiobook is listened to. */
const readKinds: ReadonlySet<string> = new Set(['ebook', 'comic']);

/** Only a completed, current reading lookup may choose the ordinary detail
 * branch. Errors and superseded/aborted reads never classify an audiobook.
 * A title that is not a book or a comic is never asked how to read it: its
 * kind comes from the title's own page, which the route has read already. */
export async function resolveItemRead(client: ReadingClient, queries: ViewerQueries, itemId: string, signal: AbortSignal, isCurrent: () => boolean) {
  const assert = () => {signal.throwIfAborted();if (!isCurrent()) throw new DOMException('Item authority changed', 'AbortError');};
  assert();
  // When the title's page cannot be read here, the reading lookup decides, as before.
  const kind = await readTitleIdentity(queries, itemId, signal).then(identity => identity.kind, () => undefined);
  assert();
  if (kind !== undefined && !readKinds.has(kind)) return null;
  const eligibility = await readItemEligibility(client, itemId, signal);
  assert();
  return eligibility;
}
