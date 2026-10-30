import {localPlaybackScope,type LocalPlaybackScope} from '../../../packages/player-core/src/index.ts';
import type {LocalSession} from '../../../packages/client-core/src/index.ts';

/**
 * The download scope a launch may use without the server.
 *
 * Derived only from the session the app already persisted and validated when it
 * was issued: the same viewer authority, server, account, profile and session
 * family the store was bound to online. Nothing is minted here, no anonymous or
 * local-only scope is invented, and an unreadable or expired authority yields
 * undefined so the offline surface simply does not appear.
 *
 * The horizon is the session's own authorization horizon (falling back to the
 * access token's expiry). Downloads are not a way to keep playing past the
 * authority that admitted them; the server's own refresh path stays the only
 * way to extend it.
 */
export function offlineDownloadScope(
  session: Pick<LocalSession,'viewer'|'sessionFamilyId'|'expiresAt'|'authorizationHorizon'>|null|undefined,
  now: number,
): LocalPlaybackScope|undefined {
  if (!session || !Number.isFinite(now)) return undefined;
  const authority = session.viewer.authority;
  if (authority !== 'local' && authority !== 'hosted') return undefined;
  let scope: LocalPlaybackScope;
  try {
    scope = localPlaybackScope({
      serverId: session.viewer.serverId,
      authority,
      accountId: session.viewer.accountId,
      profileId: session.viewer.profileId,
      sessionFamilyId: session.sessionFamilyId,
    });
  } catch {
    return undefined;
  }
  const horizon = Date.parse(session.authorizationHorizon || session.expiresAt);
  if (!Number.isFinite(horizon) || horizon <= now) return undefined;
  return scope;
}
