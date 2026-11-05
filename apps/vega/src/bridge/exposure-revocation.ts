import {checkResponse, type Exposure} from './credential-boundary';
import {revokeExposedSessions as revoke} from '../../../../packages/client-core/src/query/exposure-revocation';
export function revokeExposedSessions(exposure: Exposure): Promise<boolean> {
  return revoke(exposure, checkResponse, () => new XMLHttpRequest());
}
