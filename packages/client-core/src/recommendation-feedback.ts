import {routePath} from "../../contracts/src/route-paths.ts";
/**
 * Recommendation feedback (Recommendations plan P5): "Not interested" on a recommendation card and
 * "Reset recommendations" in Privacy.
 *
 * Not interested is a personal-state field on the personal batch route
 * (`PUT /v1/items/personal-state:batch {items: [{itemId, notInterested}]}`): the title leaves every
 * recommendation and similar titles rank a little lower. `false` undoes it. It is a statement about
 * a title, so a show, album or book card sends its own id (the one row kind the batch accepts for a
 * container); a film or an episode sends the item's.
 *
 * Reset recommendations (`POST /v1/me/recommendations:reset`) forgets the profile's learned taste
 * and every Not interested mark; history, ratings and lists stay. The operation ID is the
 * idempotency key: a retry with the same ID returns the first receipt and resets nothing further.
 */
import {PERSONAL_BATCH_PATH, parsePersonalBatchReceipt, personalBatchBody} from './personal-saved.ts';
import {unreadableServerResponse} from './server-messages.ts';

type Api = {acknowledgeUserMutation?(intentId:string):Promise<void>;request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};

export const RESET_RECOMMENDATIONS_PATH = routePath("post_me_recommendations_reset");

const operation = /^[A-Za-z0-9_-]{1,128}$/;
const containerKinds = new Set(['show', 'album', 'book']);

/** The id a card's Not interested names: a show, album or book by its own id, else the item. */
export function notInterestedTarget(entry: {id: string; kind?: string; navigation?: {entityId?: string}}): string {
  return entry.kind && containerKinds.has(entry.kind) ? entry.navigation?.entityId || entry.id : entry.id;
}

/**
 * Whether a row's cards are recommendations (and so offer Not interested): Home rows of the
 * `recommendation` kind, a library's Discover recommendation rows, and every row of a title's
 * recommendations. Continue Watching, the Watchlist, Favorites and Recently added never are.
 */
export function isRecommendationRow(row: {id: string; kind?: string}): boolean {
  if (row.kind !== undefined) return row.kind === 'recommendation';
  return row.id === 'recommended' || row.id === 'trending_now' || row.id.startsWith('for_you:');
}

export type NotInterestedInput = Readonly<{
  itemId: string;
  notInterested: boolean;
  /** The signed-in server's id: the receipt must come from it. */
  serverId: string;
  /** A new operation ID per request (`^[A-Za-z0-9_-]{1,128}$`; a UUID fits). */
  operationId: string;
  signal?: AbortSignal;
}>;

/** Marks (or unmarks) one title Not interested. Rejects with the row's error code when refused. */
export async function setNotInterested(api: Api, input: NotInterestedInput): Promise<{revision: number}> {
  if (!input.itemId) throw new Error('A title is required.');
  const body = personalBatchBody(input.operationId, [{itemId: input.itemId, notInterested: input.notInterested}]);
  const receipt = parsePersonalBatchReceipt(await api.request<unknown>(PERSONAL_BATCH_PATH, 'PUT', body, input.signal), input.serverId, [input.itemId]);
  const row = receipt.results[0]!;
  if (!row.ok) throw Object.assign(new Error(row.message || 'The change was not accepted.'), {code: row.code});
  await api.acknowledgeUserMutation?.(input.operationId);
  const revision = (row.personal as {revision?: unknown} | null)?.revision;
  return {revision: typeof revision === 'number' && Number.isSafeInteger(revision) && revision >= 0 ? revision : 0};
}

export type RecommendationsResetReceipt = Readonly<{operationId: string; clearedNotInterested: number}>;

/** Forgets what recommendations have learned for the signed-in profile. */
export async function resetRecommendations(api: Api, input: {serverId: string; operationId: string; signal?: AbortSignal}): Promise<RecommendationsResetReceipt> {
  if (!operation.test(input.operationId)) throw new Error('Invalid operation ID.');
  const raw = await api.request<unknown>(RESET_RECOMMENDATIONS_PATH, 'POST', {operationId: input.operationId}, input.signal);
  return parseRecommendationsReset(raw, input.serverId, input.operationId);
}

export function parseRecommendationsReset(raw: unknown, serverId: string, operationId: string): RecommendationsResetReceipt {
  const v = raw as {serverId?: unknown; operationId?: unknown; clearedNotInterested?: unknown} | null;
  // The receipt must be this server's answer to this operation. The count is only shown: one that cannot be read is 0.
  if (!v || typeof v !== 'object' || v.serverId !== serverId || v.operationId !== operationId) {
    throw Object.assign(new Error(unreadableServerResponse), {code: 'invalid_recommendations_reset'});
  }
  const cleared = v.clearedNotInterested;
  return Object.freeze({operationId, clearedNotInterested: typeof cleared === 'number' && Number.isSafeInteger(cleared) && cleared >= 0 ? cleared : 0});
}
