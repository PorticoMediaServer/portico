import {routePath} from '../../contracts/src/route-paths.ts';
import type {SavedApi} from './saved.ts';
/** Clear the captured history revision once, under the caller's existing durable intent ID.
 * The history screen keeps its lifetime guard and reloads only its still-current filter. */
export async function clearPersonalHistory(api: SavedApi, operationId: string, expectedRevision: number, signal: AbortSignal): Promise<void> {
  await api.request(routePath('post_personal_history_actions'), 'POST', {operationId, expectedRevision, action: 'clear-history'}, signal);
}
