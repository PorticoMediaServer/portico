import type {LocalSession} from '../../../../../packages/client-core/src/index';
import {compareSessionAuthority} from '../../../../../packages/client-core/src/session-authority';

/** Whether passive pixels painted for the sign-in `held` may stay up under `live`: the same
 *  sign-in, or its routine refresh (same family, newer token), on the same device and
 *  installation, in the same role. A different sign-in, an older token or a role change ends them. */
export function paintAuthorityHolds(held: LocalSession | undefined, live: LocalSession | undefined): boolean {
  if (!held || !live) return false;
  const change = compareSessionAuthority(held, live);
  return (change === 'same' || change === 'rotated') && live.viewer.role === held.viewer.role
    && live.deviceId === held.deviceId && live.installationId === held.installationId;
}
