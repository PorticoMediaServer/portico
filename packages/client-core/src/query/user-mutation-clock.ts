import {routePath} from '../../../contracts/src/route-paths.ts';
import type {DownloadsApi} from '../downloads-service.ts';
/** Native listening's clock uses the captured app transport; no shared cached clock or extra request. */
export async function readUserMutationClockOffset(api: DownloadsApi): Promise<number> {
  const policy = await api.request<{serverTimeMs: number}>(routePath('get_user_mutation_policy'));
  if (!Number.isSafeInteger(policy.serverTimeMs)) throw new Error('mutation_clock');
  return policy.serverTimeMs - Date.now();
}
