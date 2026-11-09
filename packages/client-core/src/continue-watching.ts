import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
/**
 * X-12: Remove from Continue Watching, on the server's own dismissal (api/home.md, "Continue
 * Watching dismissal"): `PUT /v1/items/{id}/personal-state {continueDismissed: true}` takes the
 * title off this profile's Continue Watching without marking it watched or touching its resume
 * point; `false` puts it back (Undo). The next playback of the title makes it eligible again.
 *
 * The mutation carries the item's personal revision. A caller that has it (an open title's detail)
 * passes it; otherwise the current revision is read from the title's detail. A conflict
 * preserves that authored operation and revision for review; it never rebases automatically.
 */
type Api = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

export type ContinueDismissalInput = Readonly<{
  itemId: string;
  dismissed: boolean;
  /** The item's current personal revision, when the caller already holds it. */
  revision?: number;
  /** One operation ID for this logical submission and its retries. */
  operationId: () => string | Promise<string>;
  signal?: AbortSignal;
}>;

const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value);
const identifier = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 256 && !/[\x00-\x1f\x7f]/.test(value);
function invalid(): never {throw Object.assign(new Error('The personal-state receipt could not be read.'), {code: 'invalid_detail'});}
const operation = /^[A-Za-z0-9_-]{1,128}$/;
const revisionValue = (v: unknown): v is number => typeof v === 'number' && Number.isSafeInteger(v) && v >= 0;
const itemPath = (itemId: string) => encodedRoutePath("get_items_id", {"id": encodeURIComponent(itemId)});

/** The personal revision in a detail or a personal-state receipt (`{personal: {revision}}`). */
export function personalRevision(raw: unknown): number | undefined {
  const r = (raw as {personal?: {revision?: unknown}} | null)?.personal?.revision;
  return revisionValue(r) ? r : undefined;
}

async function currentRevision(api: Api, itemId: string, signal?: AbortSignal): Promise<number> {
  const r = personalRevision(await api.request<unknown>(itemPath(itemId) + '/detail', 'GET', undefined, signal)); // query-read: kept (write precondition: the revision the PUT pins, read fresh)
  if (r === undefined) throw Object.assign(new Error('The title’s personal state could not be read.'), {code: 'invalid_detail'});
  return r;
}

/**
 * Dismisses (or restores) one title on this profile's Continue Watching. Resolves with the
 * personal revision after the change, which an Undo passes back.
 */
export async function setContinueDismissed(api: Api, input: ContinueDismissalInput): Promise<{revision: number}> {
  const {itemId, dismissed, revision: authoredRevision, operationId: createOperationId, signal} = input;
  if (!itemId) throw new Error('A title is required.');
  const revision = revisionValue(authoredRevision) ? authoredRevision : await currentRevision(api, itemId, signal);
  const operationId = String(await createOperationId());
  if (!operation.test(operationId)) throw new Error('Invalid operation ID.');
  const raw = await api.request<unknown>(itemPath(itemId) + '/personal-state', 'PUT', {operationId, expectedRevision: revision, continueDismissed: dismissed}, signal);
  if (!object(raw) || raw.operationId !== operationId || raw.itemId !== itemId || !identifier(raw.serverId) || !identifier(raw.libraryId) || !identifier(raw.viewerFence)) invalid();
  const confirmed = personalRevision(raw.current === undefined ? raw : {personal: raw.current});
  if (confirmed === undefined || confirmed < revision) invalid();
  return {revision: confirmed};
}
