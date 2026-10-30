import type {HttpLocalApi} from '@core/index.ts';
import {PersonalSavedService} from '@core/personal-saved.ts';
import {randomId} from '@core/random-id.ts';
import {resetRecommendations} from '@core/recommendation-feedback.ts';
import {clearSearchHistory} from '@core/search.ts';

/** The three drawn Clear data rows, each with its checked producer. */
export const privacyActionRowIds = ['privacy.clearHistory', 'privacy.clearSearches', 'privacy.resetRecommendations'] as const;
export type PrivacyActionRowId = typeof privacyActionRowIds[number];
const privacyActionRows: ReadonlySet<string> = new Set(privacyActionRowIds);

export function isPrivacyAction(rowId: string): rowId is PrivacyActionRowId {
  return privacyActionRows.has(rowId);
}

/** True only for the three Clear data rows; undefined leaves the row to the shared controller. */
export function privacyActionClaim(rowId: string): true | undefined {
  return privacyActionRows.has(rowId) ? true : undefined;
}

export type PrivacyActionApi = Pick<HttpLocalApi, 'request'>;
export type PrivacyActionOptions = Readonly<{
  api: PrivacyActionApi;
  /** The signed-in server's id; every receipt is checked against it. */
  serverId: string;
  /** The same authority/account/profile scope Saved uses. */
  viewerId: string;
  signal: AbortSignal;
}>;

/** Clear viewing history through the checked personal-history route.
 * The service reads the current history revision first, because the server
 * fences the action on it; the same receipt check Saved uses applies here.
 * The settings session's abort retires the service immediately, so a read or
 * mutation cannot outlive the page that offered it. */
export async function clearViewingHistory(options: PrivacyActionOptions): Promise<void> {
  const service = new PersonalSavedService(options.api, {serverId: options.serverId, viewerId: options.viewerId}, async () => randomId());
  if (options.signal.aborted) {service.dispose(); throw new DOMException('privacy_action_cancelled', 'AbortError');}
  const abort = () => service.dispose();
  options.signal.addEventListener('abort', abort, {once: true});
  try {
    await service.select({view: 'history'});
    if (options.signal.aborted) throw new DOMException('privacy_action_cancelled', 'AbortError');
    const read = service.getSnapshot();
    if (read.error || !read.viewerFence) throw read.error ?? new Error('privacy_history_unreadable');
    if (!await service.mutate({action: 'clear-history'})) {
      if (options.signal.aborted) throw new DOMException('privacy_action_cancelled', 'AbortError');
      const error = service.getSnapshot().mutationError;
      throw error ?? new Error('privacy_history_not_cleared');
    }
    if (options.signal.aborted) throw new DOMException('privacy_action_cancelled', 'AbortError');
  } finally {
    options.signal.removeEventListener('abort', abort);
    service.dispose();
  }
}

/** Forget the recent searches the server kept for this profile. */
export async function clearRecentSearches(options: PrivacyActionOptions): Promise<void> {
  await clearSearchHistory(options.api, options.signal);
}

/** Forget the profile's recommendation taste and Not interested marks.
 * The operation ID is the idempotency key the retry-safe route expects. */
export async function resetProfileRecommendations(options: PrivacyActionOptions): Promise<void> {
  await resetRecommendations(options.api, {serverId: options.serverId, operationId: randomId(), signal: options.signal});
}

export async function runPrivacyAction(rowId: PrivacyActionRowId, options: PrivacyActionOptions): Promise<void> {
  if (rowId === 'privacy.clearHistory') return clearViewingHistory(options);
  if (rowId === 'privacy.clearSearches') return clearRecentSearches(options);
  return resetProfileRecommendations(options);
}

/** The checked personal-history producer reports failures as {code,message,conflict},
 * without an HTTP status. Treat both shapes as the same auth refusal; the route's
 * normalized 401/403 codes are unauthorized and not_permitted. */
export function privacyAuthFailure(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const value = error as {status?: unknown; code?: unknown};
  return value.status === 401 || value.status === 403 || value.code === 'unauthorized' || value.code === 'forbidden' || value.code === 'not_permitted';
}
